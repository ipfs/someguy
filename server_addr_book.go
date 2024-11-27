package main

import (
	"context"
	"io"
	"math"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ipfs/boxo/routing/http/types"
	"github.com/libp2p/go-libp2p/core/event"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/peerstore"
	"github.com/libp2p/go-libp2p/p2p/host/peerstore/pstoremem"
	ma "github.com/multiformats/go-multiaddr"
	manet "github.com/multiformats/go-multiaddr/net"
)

// The TTL to keep recently connected peers for. This should be enough time to probe
const RecentlyConnectedAddrTTL = time.Hour * 24

// Connected peers don't expire until they disconnect
const ConnectedAddrTTL = math.MaxInt64

// How long to wait since last connection before probing a peer again
const PeerProbeThreshold = time.Hour

// How often to run the probe peers function
const ProbeInterval = time.Minute * 15

type peerState struct {
	lastConnTime    time.Time    // time we were connected to this peer
	lastConnAddr    ma.Multiaddr // last address we connected to this peer on
	returnCount     atomic.Int32 // number of times we've returned this peer
	connectFailures atomic.Int32 // number of times we've failed to connect to this peer
}

type cachedAddrBook struct {
	peers     map[peer.ID]*peerState // PeerID -> peer state
	addrBook  peerstore.AddrBook     // PeerID -> []Multiaddr with TTL expirations
	isProbing bool                   // Whether we are currently probing peers
}

func newCachedAddrBook() *cachedAddrBook {
	return &cachedAddrBook{
		peers:    make(map[peer.ID]*peerState),
		addrBook: pstoremem.NewAddrBook(),
	}
}

func (cab *cachedAddrBook) background(ctx context.Context, host host.Host) {
	sub, err := host.EventBus().Subscribe([]interface{}{
		&event.EvtPeerIdentificationCompleted{},
		&event.EvtPeerConnectednessChanged{},
	})
	if err != nil {
		logger.Errorf("failed to subscribe to peer identification events: %v", err)
		return
	}
	defer sub.Close()

	probeTicker := time.NewTicker(ProbeInterval)
	defer probeTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			cabCloser, ok := cab.addrBook.(io.Closer)
			if ok {
				errClose := cabCloser.Close()
				if errClose != nil {
					logger.Warnf("failed to close addr book: %v", errClose)
				}
			}
			return
		case ev := <-sub.Out():
			switch ev := ev.(type) {
			case event.EvtPeerIdentificationCompleted:
				// Update the peer state with the last connected address and time
				if _, exists := cab.peers[ev.Peer]; !exists {
					cab.peers[ev.Peer] = &peerState{
						lastConnTime:    time.Now(),
						lastConnAddr:    ev.Conn.RemoteMultiaddr(),
						returnCount:     atomic.Int32{},
						connectFailures: atomic.Int32{},
					}
				} else {
					cab.peers[ev.Peer].lastConnTime = time.Now()
					cab.peers[ev.Peer].lastConnAddr = ev.Conn.RemoteMultiaddr()
				}

				if ev.SignedPeerRecord != nil {
					logger.Debug("Caching signed peer record")
					cab, ok := peerstore.GetCertifiedAddrBook(cab.addrBook)
					if ok {
						ttl := RecentlyConnectedAddrTTL
						if host.Network().Connectedness(ev.Peer) == network.Connected || host.Network().Connectedness(ev.Peer) == network.Limited {
							ttl = ConnectedAddrTTL
						}
						_, err := cab.ConsumePeerRecord(ev.SignedPeerRecord, ttl)
						if err != nil {
							logger.Warnf("failed to consume signed peer record: %v", err)
						}
					}
				} else {
					logger.Debug("No signed peer record, caching listen addresses")
					// We don't have a signed peer record, so we use the listen addresses
					cab.addrBook.AddAddrs(ev.Peer, ev.ListenAddrs, ConnectedAddrTTL)
				}
			case event.EvtPeerConnectednessChanged:
				// If the peer is not connected or limited, we update the TTL
				if ev.Connectedness != network.Connected && ev.Connectedness != network.Limited {
					cab.addrBook.UpdateAddrs(ev.Peer, ConnectedAddrTTL, RecentlyConnectedAddrTTL)
				}
			}
		case <-probeTicker.C:
			if cab.isProbing {
				logger.Debug("Skipping peer probe, still running")
				continue
			}
			logger.Debug("Running peer probe")
			start := time.Now()
			cab.probePeers(ctx, host)
			elapsed := time.Since(start)
			logger.Debugf("Finished peer probe in %s", elapsed)
		}
	}
}

// Loops over all peers with addresses and probes them if they haven't been probed recently
func (cab *cachedAddrBook) probePeers(ctx context.Context, host host.Host) {
	cab.isProbing = true
	defer func() { cab.isProbing = false }()

	wg := sync.WaitGroup{}

	for i, p := range cab.addrBook.PeersWithAddrs() {
		logger.Debugf("Probe %d: PeerID: %s", i+1, p)
		if host.Network().Connectedness(p) == network.Connected || host.Network().Connectedness(p) == network.Limited {
			continue // don't probe connected peers
		}

		if time.Since(cab.peers[p].lastConnTime) < PeerProbeThreshold {
			continue // don't probe peers below the probe threshold
		}

		addrs := cab.addrBook.Addrs(p)

		if len(addrs) == 0 {
			continue // no addresses to probe
		}

		addrs = ma.FilterAddrs(addrs, manet.IsPublicAddr)
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(ctx, time.Second*10)
			defer cancel()
			// when connect succeeds and identify runs, the background loop will update the peer state and cache
			err := host.Connect(ctx, peer.AddrInfo{
				ID: p,
				// TODO: Should we should probe the last connected address or all addresses?
				Addrs: addrs,
			})
			if err != nil {
				logger.Warnf("failed to connect to peer %s: %v", p, err)
				cab.peers[p].connectFailures.Add(1)
				cab.addrBook.ClearAddrs(p)
			}
		}()
	}
	wg.Wait()
}

// Returns the cached addresses for a peer, incrementing the return count
func (cab *cachedAddrBook) getCachedAddrs(p *peer.ID) []types.Multiaddr {
	addrs := cab.addrBook.Addrs(*p)
	cab.peers[*p].returnCount.Add(1) // increment the return count

	var cachedAddrs []types.Multiaddr
	for _, addr := range addrs {
		cachedAddrs = append(cachedAddrs, types.Multiaddr{Multiaddr: addr})
	}
	return cachedAddrs
}
