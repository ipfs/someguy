package main

import (
	"context"
	"io"
	"math"
	"time"

	"github.com/libp2p/go-libp2p/core/event"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peerstore"
)

// By default, we keep recently connected peers for 48 hours, which leaves enough to probe
const RecentlyConnectedAddrTTL = time.Hour * 48
const ConnectedAddrTTL = math.MaxInt64

func manageAddrBook(ctx context.Context, addrBook peerstore.AddrBook, host host.Host) {
	sub, err := host.EventBus().Subscribe([]interface{}{
		&event.EvtPeerIdentificationCompleted{},
		&event.EvtPeerConnectednessChanged{},
	})
	if err != nil {
		logger.Errorf("failed to subscribe to peer identification events: %v", err)
		return
	}
	defer sub.Close()

	for {
		select {
		case <-ctx.Done():
			cabCloser, ok := addrBook.(io.Closer)
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
				if ev.SignedPeerRecord != nil {
					cab, ok := peerstore.GetCertifiedAddrBook(addrBook)
					if ok {
						ttl := RecentlyConnectedAddrTTL
						if host.Network().Connectedness(ev.Peer) == network.Connected {
							ttl = ConnectedAddrTTL
						}
						_, err := cab.ConsumePeerRecord(ev.SignedPeerRecord, ttl)
						if err != nil {
							logger.Warnf("failed to consume signed peer record: %v", err)
						}
					}
				}
			case event.EvtPeerConnectednessChanged:
				if ev.Connectedness != network.Connected {
					addrBook.UpdateAddrs(ev.Peer, ConnectedAddrTTL, RecentlyConnectedAddrTTL)
				}
			}
		}
	}
}
