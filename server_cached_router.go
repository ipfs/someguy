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

	// source=providers|peers|closest indicates if query originated from provider, peer, or closest peers endpoint
	addrQueryOriginLabel        = "origin"
	addrQueryOriginProviders    = "providers"
	addrQueryOriginPeers        = "peers"
	addrQueryOriginClosestPeers = "closest"
	addrQueryOriginUnknown      = "unknown"

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

	iter := NewCacheFallbackIter(it, r, ctx, addrQueryOriginProviders)
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

func (r cachedRouter) GetClosestPeers(ctx context.Context, key cid.Cid) (iter.ResultIter[*types.PeerRecord], error) {
	it, err := r.router.GetClosestPeers(ctx, key)
	if err != nil {
		return nil, err
	}

	return r.applyPeerRecordCaching(it, ctx, addrQueryOriginClosestPeers), nil
}

// applyPeerRecordCaching applies cache fallback logic to a PeerRecord iterator
// by converting to Record iterator, applying caching, and converting back
func (r cachedRouter) applyPeerRecordCaching(it iter.ResultIter[*types.PeerRecord], ctx context.Context, queryOrigin string) iter.ResultIter[*types.PeerRecord] {
	// Convert *types.PeerRecord to types.Record
	recordIter := iter.Map(it, func(v iter.Result[*types.PeerRecord]) iter.Result[types.Record] {
		if v.Err != nil {
			return iter.Result[types.Record]{Err: v.Err}
		}
		return iter.Result[types.Record]{Val: v.Val}
	})

	// Apply caching
	cachedIter := NewCacheFallbackIter(recordIter, r, ctx, queryOrigin)

	// Convert back to *types.PeerRecord
	return iter.Map(cachedIter, func(v iter.Result[types.Record]) iter.Result[*types.PeerRecord] {
		if v.Err != nil {
			return iter.Result[*types.PeerRecord]{Err: v.Err}
		}
		peerRec, ok := v.Val.(*types.PeerRecord)
		if !ok {
			return iter.Result[*types.PeerRecord]{Err: errors.New("unexpected record type")}
		}
		return iter.Result[*types.PeerRecord]{Val: peerRec}
	})
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
	queryOrigin     string
	ctx             context.Context
	cancel          context.CancelFunc
	ongoingLookups  atomic.Int32
}

// NewCacheFallbackIter is a wrapper around a results iterator that will resolve peers with no addresses from cache and if no cached addresses, will look them up via FindPeers.
// It's a bit complex because it ensures we continue iterating without blocking on the FindPeers call.
func NewCacheFallbackIter(sourceIter iter.ResultIter[types.Record], router cachedRouter, ctx context.Context, queryOrigin string) *cacheFallbackIter {
	// Create a cancellable context for this iterator
	iterCtx, cancel := context.WithCancel(ctx)

	iter := &cacheFallbackIter{
		sourceIter:      sourceIter,
		router:          router,
		queryOrigin:     queryOrigin,
		ctx:             iterCtx,
		cancel:          cancel,
		findPeersResult: make(chan types.PeerRecord, 100), // Buffer to avoid drops in typical cases
		ongoingLookups:  atomic.Int32{},
	}

	return iter
}

func (it *cacheFallbackIter) Next() bool {
	for {
		// Try to get the next value from the source iterator first
		if it.sourceIter.Next() {
			val := it.sourceIter.Val()

			switch val.Val.GetSchema() {
			case types.SchemaPeer:
				if record, ok := val.Val.(*types.PeerRecord); ok {
					record.Addrs = it.router.withAddrsFromCache(it.queryOrigin, *record.ID, record.Addrs)
					if len(record.Addrs) > 0 {
						it.current = iter.Result[types.Record]{Val: record}
						return true
					}

					logger.Infow("no cached addresses found in cacheFallbackIter, dispatching find peers", "peer", record.ID)
					if it.router.cachedAddrBook.ShouldProbePeer(*record.ID) {
						it.ongoingLookups.Add(1) // important to increment before dispatchFindPeer
						// If a record has no addrs, we dispatch a lookup to find addresses
						go it.dispatchFindPeer(*record)
					}
					// Continue to try next source item
					continue
				}
			}
			it.current = val // pass through unknown schemas
			return true
		}

		// No more source items. If there are still ongoing lookups, wait for them
		ongoing := it.ongoingLookups.Load()
		if ongoing == 0 {
			// No more lookups, we're done
			return false
		}

		logger.Infow("waiting for ongoing find peers result", "ongoing", ongoing)

		// Use a timeout to recheck ongoingLookups periodically
		// This prevents deadlock if ongoingLookups becomes 0 after we check
		timer := time.NewTimer(100 * time.Millisecond)
		defer timer.Stop() // Ensure cleanup even if we return early
		select {
		case result, ok := <-it.findPeersResult:
			if !ok {
				return false // channel closed. We're done
			}
			if len(result.Addrs) > 0 { // Only if the lookup returned a result and it has addrs
				it.current = iter.Result[types.Record]{Val: &result}
				return true
			}
			// If no addresses, continue the loop to check for more source items or results
		case <-it.ctx.Done():
			return false
		case <-timer.C:
			// Timeout expired, loop back to try source iterator again and recheck ongoingLookups
		}
	}
}

func (it *cacheFallbackIter) Val() iter.Result[types.Record] {
	if it.current.Val != nil || it.current.Err != nil {
		return it.current
	}
	return iter.Result[types.Record]{Err: errNoValueAvailable}
}

func (it *cacheFallbackIter) Close() error {
	// Cancel the context to stop any ongoing lookups
	if it.cancel != nil {
		it.cancel()
	}

	// Close the source iterator
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
	if err == nil {
		defer peersIt.Close() // Ensure cleanup of the iterator
	}

	// Check if the parent context is done before sending
	if it.ctx.Err() != nil {
		return // Exit early if the parent context is done
	}

	// Helper to send result without blocking
	sendResult := func(r types.PeerRecord) {
		select {
		case it.findPeersResult <- r:
			// Sent successfully
		case <-it.ctx.Done():
			// Context cancelled, exit
		default:
			// Channel full or nobody listening anymore, drop the result
			// This is OK - these are best-effort background lookups
			logger.Debugw("dropping find peers result, nobody listening", "peer", r.ID)
		}
	}

	if err != nil {
		sendResult(record) // pass back the record with no addrs
		return
	}
	peers, err := iter.ReadAllResults(peersIt)
	if err != nil {
		sendResult(record) // pass back the record with no addrs
		return
	}
	if len(peers) > 0 {
		// If we found the peer, pass back the result
		sendResult(*peers[0])
	} else {
		sendResult(record) // pass back the record with no addrs
	}
}
