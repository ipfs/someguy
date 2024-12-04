package main

import (
	"context"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p"
	"github.com/libp2p/go-libp2p/core/event"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	ma "github.com/multiformats/go-multiaddr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCachedAddrBook(t *testing.T) {
	// Create a new cached address book
	cab, err := newCachedAddrBook(WithAllowPrivateIPs())
	require.NoError(t, err)
	require.NotNil(t, cab)
	require.NotNil(t, cab.peers)
	require.NotNil(t, cab.addrBook)
}

func TestGetCachedAddrs(t *testing.T) {
	cab, err := newCachedAddrBook(WithAllowPrivateIPs())
	require.NoError(t, err)

	// Create a test peer with new PeerID
	testPeer, err := peer.Decode("12D3KooWCZ67sU8oCvKd82Y6c9NgpqgoZYuZEUcg4upHCjK3n1aj")
	require.NoError(t, err)

	// Add test addresses
	addr1, _ := ma.NewMultiaddr("/ip4/127.0.0.1/tcp/1234")
	addr2, _ := ma.NewMultiaddr("/ip4/127.0.0.1/tcp/5678")
	cab.addrBook.AddAddrs(testPeer, []ma.Multiaddr{addr1, addr2}, time.Hour)

	// Initialize peer state
	cab.peers[testPeer] = &peerState{}

	// Test getting addresses
	addrs := cab.GetCachedAddrs(&testPeer)
	assert.Len(t, addrs, 2)

	// Verify return count and time were updated
	assert.Equal(t, 1, cab.peers[testPeer].returnCount)
}

func TestBackground(t *testing.T) {
	t.Skip("skipping until this test is less flaky")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Create a test libp2p host
	h, err := libp2p.New(libp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/0"))
	require.NoError(t, err)
	defer h.Close()

	em, err := h.EventBus().Emitter(&event.EvtPeerIdentificationCompleted{})
	require.NoError(t, err)
	defer em.Close()

	cab, err := newCachedAddrBook(WithAllowPrivateIPs())
	require.NoError(t, err)

	// Create a channel to signal when background processing is ready
	ready := make(chan struct{})

	// Start background process with ready signal
	go func() {
		// Signal ready before starting background process
		close(ready)
		cab.background(ctx, h)
	}()

	// Wait for background process to start
	<-ready

	// Create a test peer with new PeerID
	testPeer, err := peer.Decode("12D3KooWCZ67sU8oCvKd82Y6c9NgpqgoZYuZEUcg4upHCjK3n1aj")
	require.NoError(t, err)

	// Simulate peer identification event
	addr, _ := ma.NewMultiaddr("/ip4/127.0.0.1/tcp/1234")

	// Emit the event after setting up the waiter
	err = em.Emit(event.EvtPeerIdentificationCompleted{
		Peer: testPeer,
		Conn: &mockConnection{
			remoteAddr: addr,
		},
		ListenAddrs: []ma.Multiaddr{addr},
	})
	require.NoError(t, err)

	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			cab.mu.RLock()
			_, exists := cab.peers[testPeer]
			cab.mu.RUnlock()
			if exists {
				return
			}
			time.Sleep(30 * time.Millisecond)
		}
	}()

	// Wait for processing with timeout
	select {
	case <-done:
		// Success case - continue to verification
	case <-time.After(time.Second * 5):
		t.Fatal("timeout waiting for peer to be added to peer state")
	}

	// Verify peer was added
	cab.mu.RLock()
	peerState, exists := cab.peers[testPeer]
	assert.True(t, exists)
	assert.NotNil(t, peerState)
	cab.mu.RUnlock()
}

func TestProbePeers(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Create a test libp2p host
	h, err := libp2p.New()
	require.NoError(t, err)
	defer h.Close()

	cab, err := newCachedAddrBook(WithAllowPrivateIPs())
	require.NoError(t, err)

	// Add a test peer with some addresses
	testPeer, _ := peer.Decode("12D3KooWCZ67sU8oCvKd82Y6c9NgpqgoZYuZEUcg4upHCjK3n1aj")
	addr, _ := ma.NewMultiaddr("/ip4/127.0.0.1/tcp/1234")
	cab.addrBook.AddAddrs(testPeer, []ma.Multiaddr{addr}, time.Hour)

	// Initialize peer state with old connection time
	cab.peers[testPeer] = &peerState{
		lastConnTime: time.Now().Add(-2 * PeerProbeThreshold),
	}

	// Run probe
	cab.probePeers(ctx, h)

	// Verify connect failures increased (since connection will fail in test)
	assert.Equal(t, 1, cab.peers[testPeer].connectFailures)
}

// Mock connection for testing
type mockConnection struct {
	network.Conn
	remoteAddr ma.Multiaddr
}

func (mc *mockConnection) RemoteMultiaddr() ma.Multiaddr {
	return mc.remoteAddr
}
