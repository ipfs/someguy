package main

import (
	"context"
	"errors"
	"sync/atomic"
	"time"

	"github.com/ipfs/boxo/routing/http/types"
	"github.com/ipfs/boxo/routing/http/types/iter"
	"github.com/ipfs/go-cid"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/routing"
	ma "github.com/multiformats/go-multiaddr"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	_ router = cachedRouter{}

	// peerAddrLookups allows us reason if/how effective peer addr cache is
	peerAddrLookups = promauto.NewCounterVec(prometheus.CounterOpts{
		Name:      "peer_addr_lookups",
		Subsystem: "cached_router",
		Namespace: name,
		Help:      "Number of peer addr info lookups per origin and cache state",
	},
		[]string{addrCacheStateLabel, addrQueryOriginLabel},
	)

	errNoValueAvailable = errors.New("no value available")
)

const (
	// cache=unused|hit|miss, indicates how effective cache is
	addrCacheStateLabel  = "cache"
	addrCacheStateUnused = "unused"
	addrCacheStateHit    = "hit"
	addrCacheStateMiss   = "miss"

	// source=providers|peers indicates if query originated from provider or peer endpoint
	addrQueryOriginLabel     = "origin"
	addrQueryOriginProviders = "providers"
	addrQueryOriginPeers     = "peers"
	addrQueryOriginUnknown   = "unknown"
)

// cachedRouter wraps a router with the cachedAddrBook to retrieve cached addresses for peers without multiaddrs in FindProviders
type cachedRouter struct {
	router
	cachedAddrBook *cachedAddrBook
}

func NewCachedRouter(router router, cab *cachedAddrBook) cachedRouter {
	return cachedRouter{router, cab}
}

func (r cachedRouter) FindProviders(ctx context.Context, key cid.Cid, limit int) (iter.ResultIter[types.Record], error) {
	it, err := r.router.FindProviders(ctx, key, limit)
	if err != nil {
		return nil, err
	}

	return NewCacheFallbackIter(it, r, ctx), nil
}

// FindPeers uses a simpler approach than FindProviders because we're dealing with a single PeerRecord, and there's
// no point in trying to dispatch an additional FindPeer call.
func (r cachedRouter) FindPeers(ctx context.Context, pid peer.ID, limit int) (iter.ResultIter[*types.PeerRecord], error) {
	it, err := r.router.FindPeers(ctx, pid, limit)

	if err == routing.ErrNotFound {
		// ErrNotFound will be returned if either dialing the peer failed or the peer was not found
		r.cachedAddrBook.RecordFailedConnection(pid)
		// If we didn't find the peer, try the cache
		cachedAddrs := r.withAddrsFromCache(addrQueryOriginPeers, pid, nil)
		if len(cachedAddrs) > 0 {
			return iter.ToResultIter(iter.FromSlice([]*types.PeerRecord{
				{
					Schema: types.SchemaPeer,
					ID:     &pid,
					Addrs:  cachedAddrs,
				},
			})), nil
		}
		return nil, routing.ErrNotFound
	}

	if err != nil {
		return nil, err
	}

	// If the peer was found, there is likely no point in looking up the cache (because kad-dht will connect to it as part of FindPeers), but we'll do it just in case.
	return iter.Map(it, func(record iter.Result[*types.PeerRecord]) iter.Result[*types.PeerRecord] {
		record.Val.Addrs = r.withAddrsFromCache(addrQueryOriginPeers, *record.Val.ID, record.Val.Addrs)
		return record
	}), nil
}

// withAddrsFromCache returns the best list of addrs for specified [peer.ID].
// It will consult cache ONLY if the addrs slice passed to it is empty.
func (r cachedRouter) withAddrsFromCache(queryOrigin string, pid peer.ID, addrs []types.Multiaddr) []types.Multiaddr {
	// skip cache if we already have addrs
	if len(addrs) > 0 {
		peerAddrLookups.WithLabelValues(addrCacheStateUnused, queryOrigin).Inc()
		return addrs
	}

	cachedAddrs := r.cachedAddrBook.GetCachedAddrs(pid)
	if len(cachedAddrs) > 0 {
		logger.Debugw("found cached addresses", "peer", pid, "cachedAddrs", cachedAddrs)
		peerAddrLookups.WithLabelValues(addrCacheStateHit, queryOrigin).Inc()
		return cachedAddrs
	} else {
		// Cache miss. Queue peer for lookup.
		peerAddrLookups.WithLabelValues(addrCacheStateMiss, queryOrigin).Inc()
		return nil
	}
}

var _ iter.ResultIter[types.Record] = &cacheFallbackIter{}

type cacheFallbackIter struct {
	sourceIter      iter.ResultIter[types.Record]
	current         iter.Result[types.Record]
	findPeersResult chan types.PeerRecord
	router          cachedRouter
	ctx             context.Context
	cancel          context.CancelFunc
	ongoingLookups  atomic.Int32
}

// NewCacheFallbackIter is a wrapper around a results iterator that will resolve peers with no addresses from cache and if no cached addresses, will look them up via FindPeers.
// It's a bit complex because it ensures we continue iterating without blocking on the FindPeers call.
func NewCacheFallbackIter(sourceIter iter.ResultIter[types.Record], router cachedRouter, ctx context.Context) *cacheFallbackIter {
	ctx, cancel := context.WithCancel(ctx)
	return &cacheFallbackIter{
		sourceIter:      sourceIter,
		router:          router,
		ctx:             ctx,
		cancel:          cancel,
		findPeersResult: make(chan types.PeerRecord),
		ongoingLookups:  atomic.Int32{},
	}
}

func (it *cacheFallbackIter) Next() bool {
	// Try to get the next value from the source iterator first
	if it.sourceIter.Next() {
		val := it.sourceIter.Val()
		handleRecord := func(id *peer.ID, record *types.PeerRecord) bool {
			record.Addrs = it.router.withAddrsFromCache(addrQueryOriginProviders, *id, record.Addrs)
			if record.Addrs != nil {
				it.current = iter.Result[types.Record]{Val: record}
				return true
			}
			logger.Infow("no cached addresses found in cacheFallbackIter, dispatching find peers", "peer", id)

			if it.router.cachedAddrBook.ShouldProbePeer(*id) {
				it.ongoingLookups.Add(1) // important to increment before dispatchFindPeer
				// If a record has no addrs, we dispatch a lookup to find addresses
				go it.dispatchFindPeer(*record)
			}

			return it.Next() // Recursively call Next() to either read from sourceIter or wait for lookup result
		}

		switch val.Val.GetSchema() {
		case types.SchemaPeer:
			if record, ok := val.Val.(*types.PeerRecord); ok {
				return handleRecord(record.ID, record)
			}
		}
		it.current = val // pass through unknown schemas
		return true
	}

	// If there are still ongoing lookups, wait for them
	if it.ongoingLookups.Load() > 0 {
		logger.Infow("waiting for ongoing find peers result")
		select {
		case result, ok := <-it.findPeersResult:
			if ok {
				it.current = iter.Result[types.Record]{Val: &result}
				return true
			}
		case <-it.ctx.Done():
			return false
		}
	}

	return false
}

func (it *cacheFallbackIter) Val() iter.Result[types.Record] {
	if it.current.Val != nil || it.current.Err != nil {
		return it.current
	}
	return iter.Result[types.Record]{Err: errNoValueAvailable}
}

func (it *cacheFallbackIter) dispatchFindPeer(record types.PeerRecord) {
	defer it.ongoingLookups.Add(-1)
	// FindPeers is weird in that it accepts a limit. But we only want one result, ideally from the libp2p router.
	peersIt, err := it.router.FindPeers(it.ctx, *record.ID, 1)

	if err != nil {
		it.findPeersResult <- record // pass back the record with no addrs
		return
	}
	peers, err := iter.ReadAllResults(peersIt)
	if err != nil {
		it.findPeersResult <- record // pass back the record with no addrs
		return
	}
	if len(peers) > 0 {
		// If we found the peer, pass back
		it.findPeersResult <- *peers[0]
	} else {
		it.findPeersResult <- record // pass back the record with no addrs
	}
}

func (it *cacheFallbackIter) Close() error {
	it.cancel()
	go func() {
		for it.ongoingLookups.Load() > 0 {
			time.Sleep(time.Millisecond * 100)
		}
		close(it.findPeersResult)
	}()
	return it.sourceIter.Close()
}

func ToMultiaddrs(addrs []ma.Multiaddr) []types.Multiaddr {
	var result []types.Multiaddr
	for _, addr := range addrs {
		result = append(result, types.Multiaddr{Multiaddr: addr})
	}
	return result
}
