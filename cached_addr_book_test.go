package main

import (
	"context"
	"fmt"
	"io"
	"testing"
	"testing/synctest"
	"time"

	"github.com/ipfs/boxo/routing/http/types"
	"github.com/libp2p/go-libp2p/core/event"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/peerstore"
	"github.com/libp2p/go-libp2p/p2p/host/eventbus"
	"github.com/libp2p/go-libp2p/p2p/host/peerstore/pstoremem"
	ma "github.com/multiformats/go-multiaddr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCachedAddrBook(t *testing.T) {
	// Create a new cached address book
	cab, err := newCachedAddrBook(WithAllowPrivateIPs())
	require.NoError(t, err)
	require.NotNil(t, cab)
	require.NotNil(t, cab.peerCache)
	require.NotNil(t, cab.addrBook)
}

func TestGetCachedAddrsHostPeerstoreFallback(t *testing.T) {
	testPeer, err := peer.Decode("12D3KooWCZ67sU8oCvKd82Y6c9NgpqgoZYuZEUcg4upHCjK3n1aj")
	require.NoError(t, err)
	addr := ma.StringCast("/ip4/137.21.14.12/tcp/4001")

	t.Run("falls back to host peerstore when addrBook is empty", func(t *testing.T) {
		hostPeerstore := pstoremem.NewAddrBook()
		hostPeerstore.AddAddrs(testPeer, []ma.Multiaddr{addr}, peerstore.TempAddrTTL)

		cab, err := newCachedAddrBook(WithAllowPrivateIPs(), WithHostPeerstore(hostPeerstore))
		require.NoError(t, err)

		got := cab.GetCachedAddrs(testPeer)
		require.Len(t, got, 1)
		require.Equal(t, addr.String(), got[0].String())
	})

	t.Run("prefers addrBook over host peerstore", func(t *testing.T) {
		ownAddr := ma.StringCast("/ip4/1.2.3.4/tcp/4001")
		hostPeerstore := pstoremem.NewAddrBook()
		hostPeerstore.AddAddrs(testPeer, []ma.Multiaddr{addr}, peerstore.TempAddrTTL)

		cab, err := newCachedAddrBook(WithAllowPrivateIPs(), WithHostPeerstore(hostPeerstore))
		require.NoError(t, err)
		cab.addrBook.AddAddrs(testPeer, []ma.Multiaddr{ownAddr}, time.Hour)

		got := cab.GetCachedAddrs(testPeer)
		require.Len(t, got, 1)
		require.Equal(t, ownAddr.String(), got[0].String())
	})

	t.Run("returns nil when both are empty", func(t *testing.T) {
		cab, err := newCachedAddrBook(WithAllowPrivateIPs(), WithHostPeerstore(pstoremem.NewAddrBook()))
		require.NoError(t, err)
		require.Nil(t, cab.GetCachedAddrs(testPeer))
	})
}

func TestReplacePeerAddrsPrunesStaleAddrs(t *testing.T) {
	cab, err := newCachedAddrBook(WithAllowPrivateIPs())
	require.NoError(t, err)

	p := peer.ID("test-peer")

	// Seed an accumulated set, as if learned from provider records and gossip.
	stale := []ma.Multiaddr{
		ma.StringCast("/ip4/1.1.1.1/tcp/4001"),
		ma.StringCast("/ip4/2.2.2.2/udp/4001/quic-v1"),
	}
	cab.addrBook.AddAddrs(p, stale, time.Hour)
	require.Len(t, cab.addrBook.Addrs(p), 2)

	// A completed identify reports a different current set (no signed record),
	// plus one address held by a live connection.
	current := []ma.Multiaddr{ma.StringCast("/ip4/3.3.3.3/tcp/4001")}
	connAddr := ma.StringCast("/ip4/4.4.4.4/tcp/4001")

	cab.replacePeerAddrs(p, nil, current, []ma.Multiaddr{connAddr}, time.Hour)

	got := make([]string, 0)
	for _, a := range cab.addrBook.Addrs(p) {
		got = append(got, a.String())
	}

	// Stale addrs are gone; the current advertised addr and the live-connection
	// addr remain.
	require.ElementsMatch(t, []string{
		"/ip4/3.3.3.3/tcp/4001",
		"/ip4/4.4.4.4/tcp/4001",
	}, got)
}

func TestReplacePeerAddrsKeepsLiveConnWhenAdvertisedSetEmpty(t *testing.T) {
	cab, err := newCachedAddrBook(WithAllowPrivateIPs())
	require.NoError(t, err)

	p := peer.ID("test-peer")
	connAddr := ma.StringCast("/ip4/4.4.4.4/tcp/4001")

	// Identify reported no usable listen addrs, but we hold a live connection.
	cab.replacePeerAddrs(p, nil, nil, []ma.Multiaddr{connAddr}, time.Hour)

	got := make([]string, 0)
	for _, a := range cab.addrBook.Addrs(p) {
		got = append(got, a.String())
	}
	require.Equal(t, []string{"/ip4/4.4.4.4/tcp/4001"}, got)
}

func TestReplacePeerAddrsEmptyInputKeepsExistingAddrs(t *testing.T) {
	cab, err := newCachedAddrBook(WithAllowPrivateIPs())
	require.NoError(t, err)

	p := peer.ID("test-peer")
	existing := ma.StringCast("/ip4/1.1.1.1/tcp/4001")
	cab.addrBook.AddAddrs(p, []ma.Multiaddr{existing}, time.Hour)

	// An identify with no signed record, no listen addrs, and no live
	// connection must not wipe the peer's existing cached addresses.
	cab.replacePeerAddrs(p, nil, nil, nil, time.Hour)

	got := make([]string, 0)
	for _, a := range cab.addrBook.Addrs(p) {
		got = append(got, a.String())
	}
	require.Equal(t, []string{"/ip4/1.1.1.1/tcp/4001"}, got)
}

func TestSplitRelayAddrs(t *testing.T) {
	direct1 := ma.StringCast("/ip4/1.2.3.4/tcp/4001")
	direct2 := ma.StringCast("/ip4/1.2.3.4/udp/4001/quic-v1")
	relay1 := ma.StringCast("/ip4/5.6.7.8/tcp/4001/p2p/12D3KooWCZ67sU8oCvKd82Y6c9NgpqgoZYuZEUcg4upHCjK3n1aj/p2p-circuit")
	relay2 := ma.StringCast("/dns4/relay.example/tcp/443/wss/p2p/12D3KooWCZ67sU8oCvKd82Y6c9NgpqgoZYuZEUcg4upHCjK3n1aj/p2p-circuit")

	direct, relay := splitRelayAddrs([]ma.Multiaddr{direct1, relay1, direct2, relay2})
	require.Equal(t, []ma.Multiaddr{direct1, direct2}, direct)
	require.Equal(t, []ma.Multiaddr{relay1, relay2}, relay)

	require.False(t, isRelayAddr(direct1))
	require.True(t, isRelayAddr(relay1))
}

// relayTTLPeer and the addrs reused by the TTL tests below.
var (
	ttlTestPeer       = peer.ID("test-peer")
	ttlTestDirectAddr = ma.StringCast("/ip4/3.3.3.3/tcp/4001")
	ttlTestRelayAddr  = ma.StringCast("/ip4/5.6.7.8/udp/4001/quic-v1/p2p/12D3KooWCZ67sU8oCvKd82Y6c9NgpqgoZYuZEUcg4upHCjK3n1aj/p2p-circuit")
)

func TestCacheAddrsUsesShorterRelayTTL(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cab, err := newCachedAddrBook(
			WithAllowPrivateIPs(),
			WithRecentlyConnectedTTL(48*time.Hour),
			WithRelayAddrTTL(2*time.Hour),
		)
		require.NoError(t, err)
		defer cab.addrBook.(io.Closer).Close()

		cab.CacheAddrs(ttlTestPeer, []types.Multiaddr{
			{Multiaddr: ttlTestDirectAddr},
			{Multiaddr: ttlTestRelayAddr},
		})
		require.Len(t, cab.addrBook.Addrs(ttlTestPeer), 2)

		// Past the relay TTL but well within the direct TTL: the relay address
		// has aged out and only the direct address remains.
		time.Sleep(3 * time.Hour)
		got := cab.addrBook.Addrs(ttlTestPeer)
		require.Len(t, got, 1)
		require.Equal(t, ttlTestDirectAddr.String(), got[0].String())
	})
}

func TestReplacePeerAddrsCapsRelayTTL(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cab, err := newCachedAddrBook(
			WithAllowPrivateIPs(),
			WithRecentlyConnectedTTL(48*time.Hour),
			WithRelayAddrTTL(2*time.Hour),
		)
		require.NoError(t, err)
		defer cab.addrBook.(io.Closer).Close()

		// A completed identify (no signed record) reports a direct and a relay
		// address at the 48h ttl; the relay address must be capped to 2h.
		cab.replacePeerAddrs(ttlTestPeer, nil, []ma.Multiaddr{ttlTestDirectAddr, ttlTestRelayAddr}, nil, 48*time.Hour)
		require.Len(t, cab.addrBook.Addrs(ttlTestPeer), 2)

		time.Sleep(3 * time.Hour)
		got := cab.addrBook.Addrs(ttlTestPeer)
		require.Len(t, got, 1)
		require.Equal(t, ttlTestDirectAddr.String(), got[0].String())
	})
}

func TestBackground(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Create a real event bus
	eventBus := eventbus.NewBus()

	emitter, err := eventBus.Emitter(new(event.EvtPeerIdentificationCompleted), eventbus.Stateful)
	require.NoError(t, err)

	// Use a mock host with a real event bus
	mockHost := &mockHost{
		eventBus: eventBus,
	}

	cab, err := newCachedAddrBook(WithAllowPrivateIPs())
	require.NoError(t, err)

	ctx, cancel = context.WithTimeout(ctx, time.Second*5)
	defer cancel()
	go cab.background(ctx, mockHost)

	// Create a test peer
	testPeer, err := peer.Decode("12D3KooWCZ67sU8oCvKd82Y6c9NgpqgoZYuZEUcg4upHCjK3n1aj")
	require.NoError(t, err)

	// Create test address
	addr, err := ma.NewMultiaddr("/ip4/127.0.0.1/tcp/1234")
	require.NoError(t, err)

	// Emit a real peer identification event
	err = emitter.Emit(event.EvtPeerIdentificationCompleted{
		Peer: testPeer,
		Conn: &mockConnection{
			remoteAddr: addr,
		},
		ListenAddrs: []ma.Multiaddr{addr},
	})
	require.NoError(t, err)

	// Wait for the peer to be added to the cache
	require.Eventually(t, func() bool {
		_, exists := cab.peerCache.Get(testPeer)
		return exists
	}, time.Second*3, time.Millisecond*100, "peer was not added to cache")

	// Verify peer state
	pState, exists := cab.peerCache.Get(testPeer)
	assert.True(t, exists)
	assert.NotNil(t, pState)
}

func TestProbePeers(t *testing.T) {
	ctx := t.Context()

	// Create a test libp2p host
	mockHost := &mockHost{}

	cab, err := newCachedAddrBook(WithAllowPrivateIPs())
	require.NoError(t, err)

	// Add a test peer with some addresses
	testPeer, _ := peer.Decode("12D3KooWCZ67sU8oCvKd82Y6c9NgpqgoZYuZEUcg4upHCjK3n1aj")
	addr, _ := ma.NewMultiaddr("/ip4/127.0.0.1/tcp/1234")
	cab.addrBook.AddAddrs(testPeer, []ma.Multiaddr{addr}, time.Hour)

	// Initialize peer state with old connection time
	cab.peerCache.Add(testPeer, peerState{
		lastConnTime: time.Now().Add(-2 * PeerProbeThreshold),
	})

	// Run probe with mockHost instead of h
	cab.probePeers(ctx, mockHost)

	// Verify connect failures increased
	pState, exists := cab.peerCache.Get(testPeer)
	assert.True(t, exists)
	assert.Equal(t, pState.connectFailures, uint(1))
}

func TestShouldProbePeer(t *testing.T) {
	t.Parallel()

	cab, err := newCachedAddrBook()
	require.NoError(t, err)

	testPeer := peer.ID("test-peer")

	tests := []struct {
		name           string
		peerState      peerState
		expectedResult bool
	}{
		{
			name:           "peer not in cache",
			peerState:      peerState{},
			expectedResult: true,
		},
		{
			name: "no failures, within threshold",
			peerState: peerState{
				lastFailedConnTime: time.Now().Add(-30 * time.Minute),
				connectFailures:    0,
			},
			expectedResult: false,
		},
		{
			name: "no failures, beyond threshold",
			peerState: peerState{
				lastFailedConnTime: time.Now().Add(-2 * PeerProbeThreshold),
				connectFailures:    0,
			},
			expectedResult: true,
		},
		{
			name: "one failure, within backoff",
			peerState: peerState{
				lastFailedConnTime: time.Now().Add(-90 * time.Minute),
				connectFailures:    1,
			},
			expectedResult: true,
		},
		{
			name: "one failure, beyond backoff",
			peerState: peerState{
				lastFailedConnTime: time.Now().Add(-3 * PeerProbeThreshold),
				connectFailures:    1,
			},
			expectedResult: true,
		},
		{
			name: "two failures, within backoff",
			peerState: peerState{
				lastFailedConnTime: time.Now().Add(-90 * time.Minute),
				connectFailures:    2,
			},
			expectedResult: false,
		},
		{
			name: "two failures, beyond backoff",
			peerState: peerState{
				lastFailedConnTime: time.Now().Add(-3 * PeerProbeThreshold),
				connectFailures:    2,
			},
			expectedResult: true,
		},
		{
			name: "never failed connection",
			peerState: peerState{
				lastFailedConnTime: time.Time{}, // zero time
				connectFailures:    0,
			},
			expectedResult: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.peerState != (peerState{}) {
				cab.peerCache.Add(testPeer, tt.peerState)
			}
			result := cab.ShouldProbePeer(testPeer)
			assert.Equal(t, tt.expectedResult, result,
				"expected ShouldProbePeer to return %v for case: %s",
				tt.expectedResult, tt.name)
		})
	}
}

// Mock connection for testing
type mockConnection struct {
	network.Conn
	remoteAddr ma.Multiaddr
}

func (mc *mockConnection) RemoteMultiaddr() ma.Multiaddr {
	return mc.remoteAddr
}

type mockHost struct {
	host.Host
	eventBus event.Bus
}

func (mh *mockHost) Connect(ctx context.Context, pi peer.AddrInfo) error {
	// Simulate connection failure
	return fmt.Errorf("mock connection failure")
}

// Add Network method to mockHost
func (mh *mockHost) Network() network.Network {
	return &mockNetwork{}
}

// Add mockNetwork implementation
type mockNetwork struct {
	network.Network
}

func (mn *mockNetwork) Connectedness(p peer.ID) network.Connectedness {
	// Simulate not connected state
	return network.NotConnected
}

func (mn *mockNetwork) ConnsToPeer(p peer.ID) []network.Conn {
	// No live connections in tests
	return nil
}

func (mh *mockHost) EventBus() event.Bus {
	return mh.eventBus
}
