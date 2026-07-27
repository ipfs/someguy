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
	"github.com/prometheus/client_golang/prometheus/testutil"
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

	t.Run("FindPeers serves cached addresses without consulting peer routing", func(t *testing.T) {
		ctx := context.Background()
		pid := peer.ID("test-peer")

		// Mock router with no FindPeers expectation: a cache hit must not reach it
		mr := &mockRouter{}

		// Create cached address book with test addresses (e.g. learned from a
		// prior provider lookup), the same peerbook FindProviders consults
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

		// Verify cached addresses were returned cache-first
		require.Equal(t, pid, *results[0].ID)
		require.Len(t, results[0].Addrs, 1)
		require.Equal(t, publicAddr.String(), results[0].Addrs[0].String())

		// Peer routing must not be consulted on a cache hit
		mr.AssertNotCalled(t, "FindPeers", mock.Anything, pid, 10)
	})

	t.Run("FindProviders caches observed addrs so FindPeers can serve them", func(t *testing.T) {
		ctx := context.Background()
		c := makeCID()
		pid := peer.ID("test-peer")
		publicAddr := mustMultiaddr(t, "/ip4/137.21.14.12/tcp/4001")

		// FindProviders returns a provider record with addrs embedded (as the
		// DHT does), while peer routing reports the peer as not found.
		mr := &mockRouter{}
		provIter := newMockResultIter([]iter.Result[types.Record]{
			{Val: &types.PeerRecord{Schema: "peer", ID: &pid, Addrs: []types.Multiaddr{publicAddr}}},
		})
		mr.On("FindProviders", mock.Anything, c, 10).Return(provIter, nil)
		mr.On("FindPeers", mock.Anything, pid, 10).Return(nil, routing.ErrNotFound)

		cab, err := newCachedAddrBook(WithAllowPrivateIPs())
		require.NoError(t, err)
		cr := NewCachedRouter(mr, cab)

		// Drain FindProviders so the observed addrs get cached
		provResults, err := cr.FindProviders(ctx, c, 10)
		require.NoError(t, err)
		_, err = iter.ReadAllResults(provResults)
		require.NoError(t, err)

		// FindPeers misses peer routing but should now serve cached addrs
		it, err := cr.FindPeers(ctx, pid, 10)
		require.NoError(t, err)
		results, err := iter.ReadAllResults(it)
		require.NoError(t, err)
		require.Len(t, results, 1)
		require.Equal(t, pid, *results[0].ID)
		require.Len(t, results[0].Addrs, 1)
		require.Equal(t, publicAddr.String(), results[0].Addrs[0].String())
	})

	t.Run("FindPeers enrich step does not double-count peer_addr_lookups", func(t *testing.T) {
		ctx := context.Background()
		pid := peer.ID("test-peer")
		publicAddr := mustMultiaddr(t, "/ip4/137.21.14.12/tcp/4001")

		// Cache is empty, so FindPeers falls through to peer routing, which
		// returns a record carrying its own addresses.
		mr := &mockRouter{}
		dhtIter := newMockResultIter([]iter.Result[*types.PeerRecord]{
			{Val: &types.PeerRecord{Schema: "peer", ID: &pid, Addrs: []types.Multiaddr{publicAddr}}},
		})
		mr.On("FindPeers", mock.Anything, pid, 10).Return(dhtIter, nil)

		cab, err := newCachedAddrBook()
		require.NoError(t, err)
		cr := NewCachedRouter(mr, cab)

		// The {unused, peers} series is written by exactly one line: the
		// cache-first lookup passes nil addrs and can never hit it, and no other
		// origin uses "peers". So this delta isolates the old enrich double-count
		// without being polluted by the process-global counter under -count or
		// parallel sibling tests (which only touch hit/miss).
		unusedBefore := testutil.ToFloat64(peerAddrLookups.WithLabelValues(addrCacheStateUnused, addrQueryOriginPeers))

		it, err := cr.FindPeers(ctx, pid, 10)
		require.NoError(t, err)
		_, err = iter.ReadAllResults(it)
		require.NoError(t, err)

		unusedAfter := testutil.ToFloat64(peerAddrLookups.WithLabelValues(addrCacheStateUnused, addrQueryOriginPeers))

		// The post-DHT enrich step must not record a second lookup for a record
		// the DHT already supplied addresses for.
		require.Equal(t, 0.0, unusedAfter-unusedBefore, "enrich step must not record an unused lookup")
	})

	t.Run("FindPeers not found with empty cache returns ErrNotFound", func(t *testing.T) {
		ctx := context.Background()
		pid := peer.ID("test-peer")

		// Create mock router that reports the peer as not found via peer routing
		mr := &mockRouter{}
		mr.On("FindPeers", mock.Anything, pid, 10).Return(nil, routing.ErrNotFound)

		// Create empty cached address book
		cab, err := newCachedAddrBook()
		require.NoError(t, err)

		// Create cached router
		cr := NewCachedRouter(mr, cab)

		_, err = cr.FindPeers(ctx, pid, 10)
		require.ErrorIs(t, err, routing.ErrNotFound)
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

	t.Run("GetClosestPeers with cached addresses", func(t *testing.T) {
		ctx := context.Background()
		c := makeCID()
		pid := peer.ID("test-peer")

		// Create mock router
		mr := &mockRouter{}
		mockIter := newMockResultIter([]iter.Result[*types.PeerRecord]{
			{Val: &types.PeerRecord{Schema: "peer", ID: &pid, Addrs: nil}},
		})
		mr.On("GetClosestPeers", mock.Anything, c).Return(mockIter, nil)

		// Create cached address book with test addresses
		cab, err := newCachedAddrBook()
		require.NoError(t, err)

		publicAddr := mustMultiaddr(t, "/ip4/137.21.14.12/tcp/4001")
		cab.addrBook.AddAddrs(pid, []multiaddr.Multiaddr{publicAddr.Multiaddr}, time.Hour)

		// Create cached router
		cr := NewCachedRouter(mr, cab)

		it, err := cr.GetClosestPeers(ctx, c)
		require.NoError(t, err)

		results, err := iter.ReadAllResults(it)
		require.NoError(t, err)
		require.Len(t, results, 1)

		// Verify cached addresses were added
		require.Equal(t, pid, *results[0].ID)
		require.Len(t, results[0].Addrs, 1)
		require.Equal(t, publicAddr.String(), results[0].Addrs[0].String())
	})

	t.Run("GetClosestPeers with fallback to FindPeers", func(t *testing.T) {
		ctx := context.Background()
		c := makeCID()
		pid := peer.ID("test-peer")
		publicAddr := mustMultiaddr(t, "/ip4/137.21.14.12/tcp/4001")

		// Create mock router
		mr := &mockRouter{}
		getClosestIter := newMockResultIter([]iter.Result[*types.PeerRecord]{
			{Val: &types.PeerRecord{Schema: "peer", ID: &pid, Addrs: nil}},
		})
		mr.On("GetClosestPeers", mock.Anything, c).Return(getClosestIter, nil)

		findPeersIter := newMockResultIter([]iter.Result[*types.PeerRecord]{
			{Val: &types.PeerRecord{Schema: "peer", ID: &pid, Addrs: []types.Multiaddr{publicAddr}}},
		})
		mr.On("FindPeers", mock.Anything, pid, 1).Return(findPeersIter, nil)

		// Create cached address book with empty cache
		cab, err := newCachedAddrBook()
		require.NoError(t, err)

		// Create cached router
		cr := NewCachedRouter(mr, cab)

		it, err := cr.GetClosestPeers(ctx, c)
		require.NoError(t, err)

		results, err := iter.ReadAllResults(it)
		require.NoError(t, err)
		require.Len(t, results, 1)

		// Verify addresses from FindPeers fallback
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
		fallbackIter := NewCacheFallbackIter(sourceIter, cr, ctx, addrQueryOriginUnknown)

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
		fallbackIter := NewCacheFallbackIter(sourceIter, cr, ctx, addrQueryOriginUnknown)

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
		fallbackIter := NewCacheFallbackIter(sourceIter, cr, ctx, addrQueryOriginUnknown)

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
		fallbackIter := NewCacheFallbackIter(sourceIter, cr, ctx, addrQueryOriginUnknown)

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
		fallbackIter := NewCacheFallbackIter(sourceIter, cr, ctx, addrQueryOriginUnknown)

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
		publicAddr := mustMultiaddr(t, "/ip4/137.21.14.12/tcp/4001")

		// Create source iterator with record without addresses
		sourceIter := newMockResultIter([]iter.Result[types.Record]{
			{Val: &types.PeerRecord{Schema: "peer", ID: &pid, Addrs: nil}},
		})

		// Create mock router with FindPeers that returns
		mr := &mockRouter{}
		// mr.On("FindPeers", mock.Anything, pid, 1).Return(nil, routing.ErrNotFound)
		findPeersIter := newMockResultIter([]iter.Result[*types.PeerRecord]{
			{Val: &types.PeerRecord{Schema: "peer", ID: &pid, Addrs: []types.Multiaddr{publicAddr}}},
		})
		mr.On("FindPeers", mock.Anything, pid, 1).Return(findPeersIter, nil)

		// Create cached router
		cab, err := newCachedAddrBook()
		require.NoError(t, err)
		cr := NewCachedRouter(mr, cab)

		// Create fallback iterator
		fallbackIter := NewCacheFallbackIter(sourceIter, cr, ctx, addrQueryOriginUnknown)

		// Cancel context during lookup
		cancel()

		// First Next() should trigger lookup
		require.False(t, fallbackIter.Next())
	})

	t.Run("Fallback FindPeers with no addresses is omitted from result", func(t *testing.T) {
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
		fallbackIter := NewCacheFallbackIter(sourceIter, cr, ctx, addrQueryOriginUnknown)

		// Should still get a result, but with no addresses
		results, err := iter.ReadAllResults(fallbackIter)
		require.NoError(t, err)
		require.Len(t, results, 0)
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
		fallbackIter := NewCacheFallbackIter(sourceIter, cr, ctx, addrQueryOriginUnknown)

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

func TestFindPeerConcurrencyCap(t *testing.T) {
	t.Parallel()

	publicAddr := mustMultiaddr(t, "/ip4/137.21.14.12/tcp/4001")

	t.Run("rejects dispatch once the cap is reached", func(t *testing.T) {
		t.Parallel()

		cab, err := newCachedAddrBook(WithMaxConcurrentFindPeers(2))
		require.NoError(t, err)

		require.True(t, cab.tryAcquireFindPeerSlot())
		require.True(t, cab.tryAcquireFindPeerSlot())
		require.False(t, cab.tryAcquireFindPeerSlot(), "third acquire must fail at a cap of 2")

		cab.releaseFindPeerSlot()
		require.True(t, cab.tryAcquireFindPeerSlot(), "a released slot must be reusable")
	})

	t.Run("invalid cap is rejected", func(t *testing.T) {
		t.Parallel()

		_, err := newCachedAddrBook(WithMaxConcurrentFindPeers(0))
		require.Error(t, err)
	})

	// The cap must not stall the iterator. ongoingLookups is what Next() waits
	// on, so a skipped dispatch that still incremented it would block until the
	// request context expired.
	t.Run("exhausted cap does not stall the iterator", func(t *testing.T) {
		t.Parallel()

		pid := peer.ID("test-peer-capped")
		sourceIter := newMockResultIter([]iter.Result[types.Record]{
			{Val: &types.PeerRecord{Schema: "peer", ID: &pid, Addrs: nil}},
		})

		mr := &mockRouter{}
		// No FindPeers expectation: reaching the router would fail the test.
		cab, err := newCachedAddrBook(WithMaxConcurrentFindPeers(1))
		require.NoError(t, err)
		require.True(t, cab.tryAcquireFindPeerSlot(), "occupy the only slot")
		cr := NewCachedRouter(mr, cab)

		fallbackIter := NewCacheFallbackIter(sourceIter, cr, t.Context(), addrQueryOriginProviders)

		done := make(chan struct{})
		var results []types.Record
		go func() {
			defer close(done)
			results, _ = iter.ReadAllResults(fallbackIter)
		}()

		select {
		case <-done:
			require.Empty(t, results, "the addr-less record has no addresses to return")
		case <-time.After(5 * time.Second):
			t.Fatal("iterator stalled after a dispatch was rejected: ongoingLookups was incremented for a lookup that never ran")
		}
		mr.AssertNotCalled(t, "FindPeers", mock.Anything, mock.Anything, mock.Anything)
	})

	t.Run("slot is released after the lookup finishes", func(t *testing.T) {
		t.Parallel()

		pid := peer.ID("test-peer-release")
		sourceIter := newMockResultIter([]iter.Result[types.Record]{
			{Val: &types.PeerRecord{Schema: "peer", ID: &pid, Addrs: nil}},
			{Val: &types.PeerRecord{Schema: "peer", ID: &pid, Addrs: []types.Multiaddr{publicAddr}}},
		})

		mr := &mockRouter{}
		mr.On("FindPeers", mock.Anything, pid, 1).Return(nil, routing.ErrNotFound)
		cab, err := newCachedAddrBook(WithMaxConcurrentFindPeers(4))
		require.NoError(t, err)
		cr := NewCachedRouter(mr, cab)

		fallbackIter := NewCacheFallbackIter(sourceIter, cr, t.Context(), addrQueryOriginProviders)
		_, err = iter.ReadAllResults(fallbackIter)
		require.NoError(t, err)

		require.Eventually(t, func() bool { return len(cab.findPeerSlots) == 0 }, 5*time.Second, 10*time.Millisecond,
			"every dispatched lookup must release its slot")
	})
}
