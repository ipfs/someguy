package main

import (
	"context"
	"io"
	"math"
	"sync"
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

const (
	// The TTL to keep recently connected peers for. Same as DefaultProviderAddrTTL in go-libp2p-kad-dht
	RecentlyConnectedAddrTTL = time.Hour * 24

	// Connected peers don't expire until they disconnect
	ConnectedAddrTTL = math.MaxInt64

	// How long to wait since last connection before probing a peer again
	PeerProbeThreshold = time.Hour

	// How often to run the probe peers function
	ProbeInterval = time.Minute * 5

	// How many concurrent probes to run at once
	MaxConcurrentProbes = 20

	// How many connect failures to tolerate before clearing a peer's addresses
	MaxConnectFailures = 3

	// How long to wait for a connect in a probe to complete
	ConnectTimeout = time.Second * 10
)

type peerState struct {
	lastConnTime    time.Time    // last time we successfully connected to this peer
	lastConnAddr    ma.Multiaddr // last address we connected to this peer on
	returnCount     int          // number of times we've returned this peer from the cache
	lastReturnTime  time.Time    // last time we returned this peer from the cache
	connectFailures int          // number of times we've failed to connect to this peer
}

type cachedAddrBook struct {
	addrBook        peerstore.AddrBook
	peers           map[peer.ID]*peerState
	mu              sync.RWMutex // Add mutex for thread safety
	isProbing       bool
	allowPrivateIPs bool // for testing
}

type AddrBookOption func(*cachedAddrBook) error

func WithAllowPrivateIPs() AddrBookOption {
	return func(cab *cachedAddrBook) error {
		cab.allowPrivateIPs = true
		return nil
	}
}

func newCachedAddrBook(opts ...AddrBookOption) (*cachedAddrBook, error) {
	cab := &cachedAddrBook{
		peers:    make(map[peer.ID]*peerState),
		addrBook: pstoremem.NewAddrBook(),
	}

	for _, opt := range opts {
		err := opt(cab)
		if err != nil {
			return nil, err
		}
	}
	return cab, nil
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
				pState, exists := cab.peers[ev.Peer]
				if !exists {
					pState = &peerState{}
					cab.peers[ev.Peer] = pState
				}
				pState.lastConnTime = time.Now()
				pState.lastConnAddr = ev.Conn.RemoteMultiaddr()

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
		// TODO: Add some cleanup logic to remove peers that haven't been returned from the cache in a while or have failed to connect too many times
	}
}

// Loops over all peers with addresses and probes them if they haven't been probed recently
func (cab *cachedAddrBook) probePeers(ctx context.Context, host host.Host) {
	cab.isProbing = true
	defer func() { cab.isProbing = false }()

	wg := sync.WaitGroup{}
	// semaphore channel to limit the number of concurrent probes
	semaphore := make(chan struct{}, MaxConcurrentProbes)

	for i, p := range cab.addrBook.PeersWithAddrs() {
		connectedness := host.Network().Connectedness(p)
		if connectedness == network.Connected || connectedness == network.Limited {
			continue // don't probe connected peers
		}

		peerState := cab.peers[p]

		if time.Since(peerState.lastConnTime) < PeerProbeThreshold {
			continue // don't probe peers below the probe threshold
		}
		if peerState.connectFailures > MaxConnectFailures {
			cab.addrBook.ClearAddrs(p) // clear the peer's addresses
			continue                   // don't probe this peer
		}

		addrs := cab.addrBook.Addrs(p)

		if !cab.allowPrivateIPs {
			addrs = ma.FilterAddrs(addrs, manet.IsPublicAddr)
		}

		if len(addrs) == 0 {
			continue // no addresses to probe
		}

		wg.Add(1)
		go func() {
			semaphore <- struct{}{}
			defer func() {
				<-semaphore // Release semaphore
				wg.Done()
			}()

			ctx, cancel := context.WithTimeout(ctx, ConnectTimeout)
			defer cancel()
			logger.Debugf("Probe %d: PeerID: %s, Addrs: %v", i+1, p, addrs)
			// if connect succeeds and identify runs, the background loop will take care of updating the peer state and cache
			err := host.Connect(ctx, peer.AddrInfo{
				ID: p,
				// TODO: Should we should probe the last connected address or all addresses?
				Addrs: addrs,
			})
			if err != nil {
				logger.Debugf("failed to connect to peer %s: %v", p, err)
				cab.mu.Lock() // Lock before accessing shared state
				cab.peers[p].connectFailures++
				cab.mu.Unlock()
			}
		}()
	}
	wg.Wait()
}

// Returns the cached addresses for a peer, incrementing the return count
func (cab *cachedAddrBook) GetCachedAddrs(p *peer.ID) []types.Multiaddr {
	cachedAddrs := cab.addrBook.Addrs(*p)

	if len(cachedAddrs) == 0 {
		return nil
	}

	cab.mu.Lock() // Lock before accessing shared state
	// Peer state already exists if it's in the addrbook so no need to check
	cab.peers[*p].returnCount++
	cab.peers[*p].lastReturnTime = time.Now()
	defer cab.mu.Unlock()

	var result []types.Multiaddr // convert to local Multiaddr type 🙃
	for _, addr := range cachedAddrs {
		result = append(result, types.Multiaddr{Multiaddr: addr})
	}
	return result
}
