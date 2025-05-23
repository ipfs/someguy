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

	DispatchedFindPeersTimeout = time.Minute
)

// cachedRouter wraps a router with the cachedAddrBook to retrieve cached addresses for peers without multiaddrs in FindProviders
// it will also dispatch a FindPeer when a provider has no multiaddrs using the cacheFallbackIter
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

	iter := NewCacheFallbackIter(it, r, ctx)
	return iter, nil
}

// FindPeers uses a simpler approach than FindProviders because we're dealing with a single PeerRecord, and there's
// no point in trying to dispatch an additional FindPeer call.
func (r cachedRouter) FindPeers(ctx context.Context, pid peer.ID, limit int) (iter.ResultIter[*types.PeerRecord], error) {
	it, err := r.router.FindPeers(ctx, pid, limit)

	if err == routing.ErrNotFound {
		// ErrNotFound will be returned if either dialing the peer failed or the peer was not found
		r.cachedAddrBook.RecordFailedConnection(pid) // record the failure used for probing/backoff purposes
		return nil, routing.ErrNotFound
	}

	if err != nil {
		return nil, err
	}

	// update the metrics to indicate that we didn't look up the cache for this lookup
	peerAddrLookups.WithLabelValues(addrCacheStateUnused, addrQueryOriginPeers).Inc()
	return it, nil
}

// withAddrsFromCache returns the best list of addrs for specified [peer.ID].
// It will consult cache ONLY if the addrs slice passed to it is empty.
func (r cachedRouter) withAddrsFromCache(queryOrigin string, pid peer.ID, addrs []types.Multiaddr) []types.Multiaddr {
	// skip cache if we already have addrs
	if len(addrs) > 0 {
		peerAddrLookups.WithLabelValues(addrCacheStateUnused, queryOrigin).Inc()
		return addrs
	}

	cachedAddrs := r.cachedAddrBook.GetCachedAddrs(pid) // Get cached addresses

	if len(cachedAddrs) > 0 {
		logger.Debugw("found cached addresses", "peer", pid, "cachedAddrs", cachedAddrs)
		peerAddrLookups.WithLabelValues(addrCacheStateHit, queryOrigin).Inc() // Cache hit
		return cachedAddrs
	} else {
		peerAddrLookups.WithLabelValues(addrCacheStateMiss, queryOrigin).Inc() // Cache miss
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
	ongoingLookups  atomic.Int32
}

// NewCacheFallbackIter is a wrapper around a results iterator that will resolve peers with no addresses from cache and if no cached addresses, will look them up via FindPeers.
// It's a bit complex because it ensures we continue iterating without blocking on the FindPeers call.
func NewCacheFallbackIter(sourceIter iter.ResultIter[types.Record], router cachedRouter, ctx context.Context) *cacheFallbackIter {
	iter := &cacheFallbackIter{
		sourceIter:      sourceIter,
		router:          router,
		ctx:             ctx,
		findPeersResult: make(chan types.PeerRecord),
		ongoingLookups:  atomic.Int32{},
	}

	return iter
}

func (it *cacheFallbackIter) Next() bool {
	// Try to get the next value from the source iterator first
	if it.sourceIter.Next() {
		val := it.sourceIter.Val()
		handleRecord := func(id *peer.ID, record *types.PeerRecord) bool {
			record.Addrs = it.router.withAddrsFromCache(addrQueryOriginProviders, *id, record.Addrs)
			if len(record.Addrs) > 0 {
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
			if !ok {
				return false // channel closed. We're done
			}
			if len(result.Addrs) > 0 { // Only if the lookup returned a result and it has addrs
				it.current = iter.Result[types.Record]{Val: &result}
				return true
			} else {
				return it.Next() // recursively call Next() in case there are more ongoing lookups
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

func (it *cacheFallbackIter) Close() error {
	return it.sourceIter.Close()
}

func (it *cacheFallbackIter) dispatchFindPeer(record types.PeerRecord) {
	defer it.ongoingLookups.Add(-1)

	// Create a new context with a timeout that is independent of the main request context
	// This is important because finishing (and determining whether this peer is reachable) the
	// FindPeer will benefit other requests and keep the cache up to date.
	ctx, cancel := context.WithTimeout(context.Background(), DispatchedFindPeersTimeout)
	defer cancel()

	peersIt, err := it.router.FindPeers(ctx, *record.ID, 1)

	// Check if the parent context is done before sending
	if it.ctx.Err() != nil {
		return // Exit early if the parent context is done
	}

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
		// If we found the peer, pass back the result
		it.findPeersResult <- *peers[0]
	} else {
		it.findPeersResult <- record // pass back the record with no addrs
	}
}
