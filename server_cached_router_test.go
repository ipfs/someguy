package main

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/ipfs/boxo/routing/http/types"
	"github.com/ipfs/boxo/routing/http/types/iter"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/multiformats/go-multiaddr"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestCachedRouter(t *testing.T) {
	t.Parallel()

	t.Run("FindProviders with cached addresses", func(t *testing.T) {
		ctx := context.Background()
		c := makeCID()
		pid := peer.ID("test-peer")

		// Create mock router
		mr := &mockRouter{}
		mockIter := newMockIter[types.Record](ctx)
		mr.On("FindProviders", mock.Anything, c, 10).Return(mockIter, nil)

		// Create cached address book with test addresses
		cab, err := newCachedAddrBook()
		require.NoError(t, err)

		publicAddr := mustMultiaddr(t, "/ip4/137.21.14.12/tcp/4001")
		cab.addrBook.AddAddrs(pid, []multiaddr.Multiaddr{publicAddr.Multiaddr}, time.Hour)

		// Create cached router
		cr := NewCachedRouter(mr, cab)

		// Simulate provider response without addresses
		go func() {
			mockIter.ch <- iter.Result[types.Record]{Val: &types.PeerRecord{
				Schema: "peer",
				ID:     &pid,
				Addrs:  nil, // No addresses in response
			}}
			close(mockIter.ch)
		}()

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
		mr.On("FindPeers", mock.Anything, pid, 10).Return(nil, errors.New("peer not found"))

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
		sourceIter := newMockIter[types.Record](ctx)
		go func() {
			sourceIter.ch <- iter.Result[types.Record]{Val: &types.PeerRecord{
				Schema: "peer",
				ID:     &pid,
				Addrs:  []types.Multiaddr{publicAddr},
			}}
			close(sourceIter.ch)
		}()

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
		sourceIter := newMockIter[types.Record](ctx)
		go func() {
			sourceIter.ch <- iter.Result[types.Record]{Val: &types.PeerRecord{
				Schema: "peer",
				ID:     &pid,
				Addrs:  nil,
			}}
			close(sourceIter.ch)
		}()

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
		sourceIter := newMockIter[types.Record](ctx)
		go func() {
			sourceIter.ch <- iter.Result[types.Record]{Val: &types.PeerRecord{
				Schema: "peer",
				ID:     &pid,
				Addrs:  nil,
			}}
			close(sourceIter.ch)
		}()

		// Create mock router that returns addresses via FindPeers
		mr := &mockRouter{}
		findPeersIter := newMockIter[*types.PeerRecord](ctx)
		mr.On("FindPeers", mock.Anything, pid, 1).Return(findPeersIter, nil)
		go func() {
			findPeersIter.ch <- iter.Result[*types.PeerRecord]{Val: &types.PeerRecord{
				Schema: "peer",
				ID:     &pid,
				Addrs:  []types.Multiaddr{publicAddr},
			}}
			close(findPeersIter.ch)
		}()

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
		sourceIter := newMockIter[types.Record](ctx)
		go func() {
			sourceIter.ch <- iter.Result[types.Record]{Val: &types.BitswapRecord{
				Schema: types.SchemaBitswap,
				ID:     &pid,
				Addrs:  nil,
			}}
			close(sourceIter.ch)
		}()

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
		// pid := peer.ID("test-peer")

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
}
