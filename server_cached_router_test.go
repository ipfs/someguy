package main

import (
	"context"
	"testing"
	"time"

	"github.com/ipfs/boxo/routing/http/types"
	"github.com/ipfs/boxo/routing/http/types/iter"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/routing"
	"github.com/multiformats/go-multiaddr"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

type mockResultIter[T any] struct {
	results []iter.Result[T]
	current int
	closed  bool
}

// Simple mock results iter that doesn't use channels
func newMockResultIter[T any](results []iter.Result[T]) *mockResultIter[T] {
	return &mockResultIter[T]{
		results: results,
		current: -1,
		closed:  false,
	}
}

func (m *mockResultIter[T]) Next() bool {
	if m.closed {
		return false
	}
	m.current++
	return m.current < len(m.results)
}

func (m *mockResultIter[T]) Val() iter.Result[T] {
	if m.current < 0 || m.current >= len(m.results) {
		panic("Val() called without calling Next() or after Next() returned false")
	}
	return m.results[m.current]
}

func (m *mockResultIter[T]) Close() error {
	m.closed = true
	return nil
}

func TestCachedRouter(t *testing.T) {
	t.Parallel()

	t.Run("FindProviders with cached addresses", func(t *testing.T) {
		ctx := context.Background()
		c := makeCID()
		pid := peer.ID("test-peer")

		// Create mock router
		mr := &mockRouter{}
		mockIter := newMockResultIter([]iter.Result[types.Record]{
			{Val: &types.PeerRecord{Schema: "peer", ID: &pid, Addrs: nil}},
		})
		mr.On("FindProviders", mock.Anything, c, 10).Return(mockIter, nil)

		// Create cached address book with test addresses
		cab, err := newCachedAddrBook()
		require.NoError(t, err)

		publicAddr := mustMultiaddr(t, "/ip4/137.21.14.12/tcp/4001")
		cab.addrBook.AddAddrs(pid, []multiaddr.Multiaddr{publicAddr.Multiaddr}, time.Hour)

		// Create cached router
		cr := NewCachedRouter(mr, cab)

		it, err := cr.FindProviders(ctx, c, 10)
		require.NoError(t, err)

		results, err := iter.ReadAllResults(it)
		require.NoError(t, err)
		require.Len(t, results, 1)

		// Verify cached addresses were added
		peerRecord := results[0].(*types.PeerRecord)
		require.Equal(t, pid, *peerRecord.ID)
		require.Len(t, peerRecord.Addrs, 1)
		require.Equal(t, publicAddr.String(), peerRecord.Addrs[0].String())
	})

	t.Run("FindPeers with cache hit", func(t *testing.T) {
		t.Skip("skipping until we decide if FindPeers should look up cache")
		ctx := context.Background()
		pid := peer.ID("test-peer")

		// Create mock router that returns error
		mr := &mockRouter{}
		mr.On("FindPeers", mock.Anything, pid, 10).Return(nil, routing.ErrNotFound)

		// Create cached address book with test addresses
		cab, err := newCachedAddrBook()
		require.NoError(t, err)

		publicAddr := mustMultiaddr(t, "/ip4/137.21.14.12/tcp/4001")
		cab.addrBook.AddAddrs(pid, []multiaddr.Multiaddr{publicAddr.Multiaddr}, time.Hour)

		// Create cached router
		cr := NewCachedRouter(mr, cab)

		it, err := cr.FindPeers(ctx, pid, 10)
		require.NoError(t, err)

		results, err := iter.ReadAllResults(it)
		require.NoError(t, err)
		require.Len(t, results, 1)

		// Verify cached addresses were returned
		require.Equal(t, pid, *results[0].ID)
		require.Len(t, results[0].Addrs, 1)
		require.Equal(t, publicAddr.String(), results[0].Addrs[0].String())
	})

	t.Run("FindPeers with cache miss", func(t *testing.T) {
		ctx := context.Background()
		pid := peer.ID("test-peer")

		// Create mock router
		mr := &mockRouter{}
		mockIter := newMockIter[*types.PeerRecord](ctx)
		mr.On("FindPeers", mock.Anything, pid, 10).Return(mockIter, nil)

		// Create empty cached address book
		cab, err := newCachedAddrBook()
		require.NoError(t, err)

		// Create cached router
		cr := NewCachedRouter(mr, cab)

		publicAddr := mustMultiaddr(t, "/ip4/137.21.14.12/tcp/4001")

		// Simulate peer response with addresses
		go func() {
			mockIter.ch <- iter.Result[*types.PeerRecord]{Val: &types.PeerRecord{
				Schema: "peer",
				ID:     &pid,
				Addrs:  []types.Multiaddr{publicAddr},
			}}
			close(mockIter.ch)
		}()

		it, err := cr.FindPeers(ctx, pid, 10)
		require.NoError(t, err)

		results, err := iter.ReadAllResults(it)
		require.NoError(t, err)
		require.Len(t, results, 1)

		// Verify addresses from response were returned
		require.Equal(t, pid, *results[0].ID)
		require.Len(t, results[0].Addrs, 1)
		require.Equal(t, publicAddr.String(), results[0].Addrs[0].String())
	})

}

func TestCacheFallbackIter(t *testing.T) {
	t.Parallel()

	t.Run("handles source iterator with no fallback needed", func(t *testing.T) {
		ctx := context.Background()
		pid := peer.ID("test-peer")
		publicAddr := mustMultiaddr(t, "/ip4/137.21.14.12/tcp/4001")

		// Create source iterator with addresses
		sourceIter := newMockResultIter([]iter.Result[types.Record]{
			{Val: &types.PeerRecord{Schema: "peer", ID: &pid, Addrs: []types.Multiaddr{publicAddr}}},
		})

		// Create cached router
		mr := &mockRouter{}
		cab, err := newCachedAddrBook()
		require.NoError(t, err)
		cr := NewCachedRouter(mr, cab)

		// Create fallback iterator
		fallbackIter := NewCacheFallbackIter(sourceIter, cr, ctx)

		// Read all results
		results, err := iter.ReadAllResults(fallbackIter)
		require.NoError(t, err)
		require.Len(t, results, 1)

		peerRecord := results[0].(*types.PeerRecord)
		require.Equal(t, pid, *peerRecord.ID)
		require.Len(t, peerRecord.Addrs, 1)
		require.Equal(t, publicAddr.String(), peerRecord.Addrs[0].String())
	})

	t.Run("uses cache when source has no addresses", func(t *testing.T) {
		ctx := context.Background()
		pid := peer.ID("test-peer")
		publicAddr := mustMultiaddr(t, "/ip4/137.21.14.12/tcp/4001")

		// Create source iterator without addresses
		sourceIter := newMockResultIter([]iter.Result[types.Record]{
			{Val: &types.PeerRecord{Schema: "peer", ID: &pid, Addrs: nil}},
		})

		// Create cached router with cached addresses
		mr := &mockRouter{}
		cab, err := newCachedAddrBook()
		require.NoError(t, err)
		cab.addrBook.AddAddrs(pid, []multiaddr.Multiaddr{publicAddr.Multiaddr}, time.Hour)
		cr := NewCachedRouter(mr, cab)

		// Create fallback iterator
		fallbackIter := NewCacheFallbackIter(sourceIter, cr, ctx)

		// Read all results
		results, err := iter.ReadAllResults(fallbackIter)
		require.NoError(t, err)
		require.Len(t, results, 1)

		peerRecord := results[0].(*types.PeerRecord)
		require.Equal(t, pid, *peerRecord.ID)
		require.Len(t, peerRecord.Addrs, 1)
		require.Equal(t, publicAddr.String(), peerRecord.Addrs[0].String())
	})

	t.Run("falls back to FindPeers when cache misses", func(t *testing.T) {
		ctx := context.Background()
		pid := peer.ID("test-peer")
		publicAddr := mustMultiaddr(t, "/ip4/137.21.14.12/tcp/4001")

		// Create source iterator without addresses
		sourceIter := newMockResultIter([]iter.Result[types.Record]{
			{Val: &types.PeerRecord{Schema: "peer", ID: &pid, Addrs: nil}},
		})

		// Create mock router that returns addresses via FindPeers
		mr := &mockRouter{}
		findPeersIter := newMockResultIter([]iter.Result[*types.PeerRecord]{
			{Val: &types.PeerRecord{Schema: "peer", ID: &pid, Addrs: []types.Multiaddr{publicAddr}}},
		})
		mr.On("FindPeers", mock.Anything, pid, 1).Return(findPeersIter, nil)

		// Create cached router with empty cache
		cab, err := newCachedAddrBook()
		require.NoError(t, err)
		cr := NewCachedRouter(mr, cab)

		// Create fallback iterator
		fallbackIter := NewCacheFallbackIter(sourceIter, cr, ctx)

		// Read all results
		results, err := iter.ReadAllResults(fallbackIter)
		require.NoError(t, err)
		require.Len(t, results, 1)

		peerRecord := results[0].(*types.PeerRecord)
		require.Equal(t, pid, *peerRecord.ID)
		require.Len(t, peerRecord.Addrs, 1)
		require.Equal(t, publicAddr.String(), peerRecord.Addrs[0].String())
	})

	t.Run("handles bitswap records", func(t *testing.T) {
		ctx := context.Background()
		pid := peer.ID("test-peer")
		publicAddr := mustMultiaddr(t, "/ip4/137.21.14.12/tcp/4001")

		// Create source iterator with bitswap record
		sourceIter := newMockResultIter([]iter.Result[types.Record]{
			//lint:ignore SA1019 // ignore staticcheck
			{Val: &types.BitswapRecord{Schema: types.SchemaBitswap, ID: &pid, Addrs: nil}},
		})

		// Create cached router with cached addresses
		mr := &mockRouter{}
		cab, err := newCachedAddrBook()
		require.NoError(t, err)
		cab.addrBook.AddAddrs(pid, []multiaddr.Multiaddr{publicAddr.Multiaddr}, time.Hour)
		cr := NewCachedRouter(mr, cab)

		// Create fallback iterator
		fallbackIter := NewCacheFallbackIter(sourceIter, cr, ctx)

		// Read all results
		results, err := iter.ReadAllResults(fallbackIter)
		require.NoError(t, err)
		require.Len(t, results, 1)

		peerRecord := results[0].(*types.PeerRecord)
		require.Equal(t, pid, *peerRecord.ID)
		require.Len(t, peerRecord.Addrs, 1)
		require.Equal(t, publicAddr.String(), peerRecord.Addrs[0].String())
	})

	t.Run("handles context cancellation", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())

		// Create source iterator that will block
		sourceIter := newMockIter[types.Record](ctx)

		// Create cached router
		mr := &mockRouter{}
		cab, err := newCachedAddrBook()
		require.NoError(t, err)
		cr := NewCachedRouter(mr, cab)

		// Create fallback iterator
		fallbackIter := NewCacheFallbackIter(sourceIter, cr, ctx)

		// Cancel context before sending any values
		cancel()

		// Verify iterator stops
		require.False(t, fallbackIter.Next())
		require.NoError(t, fallbackIter.Close())
	})

	t.Run("handles multiple Val() calls correctly", func(t *testing.T) {
		ctx := context.Background()
		pid := peer.ID("test-peer")
		publicAddr := mustMultiaddr(t, "/ip4/137.21.14.12/tcp/4001")

		// Create source iterator with a single record
		sourceIter := newMockResultIter([]iter.Result[types.Record]{
			{Val: &types.PeerRecord{Schema: "peer", ID: &pid, Addrs: []types.Multiaddr{publicAddr}}},
		})

		// Create cached router
		mr := &mockRouter{}
		cab, err := newCachedAddrBook()
		require.NoError(t, err)
		cr := NewCachedRouter(mr, cab)

		// Create fallback iterator
		fallbackIter := NewCacheFallbackIter(sourceIter, cr, ctx)

		// First Next() should succeed
		require.True(t, fallbackIter.Next())

		// Multiple Val() calls should return the same value
		val1 := fallbackIter.Val()
		val2 := fallbackIter.Val()
		require.Equal(t, val1, val2)

		// Value should be correct
		peerRecord := val1.Val.(*types.PeerRecord)
		require.Equal(t, pid, *peerRecord.ID)
		require.Equal(t, publicAddr.String(), peerRecord.Addrs[0].String())

		// After consuming the only value, Next() should return false
		require.False(t, fallbackIter.Next())
	})

	t.Run("handles context cancellation during lookup", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		pid := peer.ID("test-peer")

		// Create source iterator with record without addresses
		sourceIter := newMockResultIter([]iter.Result[types.Record]{
			{Val: &types.PeerRecord{Schema: "peer", ID: &pid, Addrs: nil}},
		})

		// Create mock router with FindPeers that returns ErrNotFound
		mr := &mockRouter{}
		mr.On("FindPeers", mock.Anything, pid, 1).Return(nil, routing.ErrNotFound)

		// Create cached router
		cab, err := newCachedAddrBook()
		require.NoError(t, err)
		cr := NewCachedRouter(mr, cab)

		// Create fallback iterator
		fallbackIter := NewCacheFallbackIter(sourceIter, cr, ctx)

		// First Next() should trigger lookup
		require.True(t, fallbackIter.Next())

		// Cancel context during lookup
		cancel()

		// Next() should return false
		require.False(t, fallbackIter.Next())

		// Val() should return the record with no addrs
		result := fallbackIter.Val()
		require.Equal(t, pid, *result.Val.(*types.PeerRecord).ID)
		require.Len(t, result.Val.(*types.PeerRecord).Addrs, 0)
	})

	t.Run("handles FindPeers error gracefully", func(t *testing.T) {
		ctx := context.Background()
		pid := peer.ID("test-peer")

		// Create source iterator without addresses
		sourceIter := newMockResultIter([]iter.Result[types.Record]{
			{Val: &types.PeerRecord{Schema: "peer", ID: &pid, Addrs: nil}},
		})

		// Create mock router that returns error from FindPeers
		mr := &mockRouter{}
		mr.On("FindPeers", mock.Anything, pid, 1).Return(nil, routing.ErrNotFound)

		// Create cached router with empty cache
		cab, err := newCachedAddrBook()
		require.NoError(t, err)
		cr := NewCachedRouter(mr, cab)

		// Create fallback iterator
		fallbackIter := NewCacheFallbackIter(sourceIter, cr, ctx)

		// Should still get a result, but with no addresses
		results, err := iter.ReadAllResults(fallbackIter)
		require.NoError(t, err)
		require.Len(t, results, 1)
		require.Empty(t, results[0].(*types.PeerRecord).Addrs)
	})

	t.Run("handles multiple records with mixed address states", func(t *testing.T) {
		ctx := context.Background()
		pid1 := peer.ID("test-peer-1")
		pid2 := peer.ID("test-peer-2")
		pid3 := peer.ID("test-peer-3")
		publicAddr := mustMultiaddr(t, "/ip4/137.21.14.12/tcp/4001")

		// Create source iterator with multiple records
		sourceIter := newMockResultIter([]iter.Result[types.Record]{
			{Val: &types.PeerRecord{Schema: "peer", ID: &pid1, Addrs: []types.Multiaddr{publicAddr}}}, // Has address
			{Val: &types.PeerRecord{Schema: "peer", ID: &pid2, Addrs: nil}},                           // No address, will use cache
			{Val: &types.PeerRecord{Schema: "peer", ID: &pid3, Addrs: nil}},                           // No address, will need FindPeers
		})

		// Create mock router
		mr := &mockRouter{}
		findPeersIter := newMockResultIter([]iter.Result[*types.PeerRecord]{
			{Val: &types.PeerRecord{Schema: "peer", ID: &pid3, Addrs: []types.Multiaddr{publicAddr}}},
		})
		mr.On("FindPeers", mock.Anything, pid3, 1).Return(findPeersIter, nil)

		// Create cached router with some cached addresses
		cab, err := newCachedAddrBook()
		require.NoError(t, err)
		cab.addrBook.AddAddrs(pid2, []multiaddr.Multiaddr{publicAddr.Multiaddr}, time.Hour)
		cr := NewCachedRouter(mr, cab)

		// Create fallback iterator
		fallbackIter := NewCacheFallbackIter(sourceIter, cr, ctx)

		// Should get all records with addresses
		results, err := iter.ReadAllResults(fallbackIter)
		require.NoError(t, err)
		require.Len(t, results, 3)

		// Verify each record has the expected addresses
		for _, result := range results {
			record := result.(*types.PeerRecord)
			require.Len(t, record.Addrs, 1)
			require.Equal(t, publicAddr.String(), record.Addrs[0].String())
		}
	})

}
