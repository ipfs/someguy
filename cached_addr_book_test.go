package main

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p/core/event"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/p2p/host/eventbus"
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

func TestBackground(t *testing.T) {
	t.Skip("skipping until this test is less flaky")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Create a real event bus
	eventBus := eventbus.NewBus()

	emitter, err := eventBus.Emitter(new(event.EvtPeerIdentificationCompleted))
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
	}, time.Second*5, time.Millisecond*100, "peer was not added to cache")

	// Verify peer state
	pState, exists := cab.peerCache.Get(testPeer)
	assert.True(t, exists)
	assert.NotNil(t, pState)
}

func TestProbePeers(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

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
	assert.Equal(t, 1, pState.connectFailures)
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

func (mh *mockHost) EventBus() event.Bus {
	return mh.eventBus
}
