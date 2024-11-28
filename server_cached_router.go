package main

import (
	"context"
	"reflect"
	"time"

	"github.com/ipfs/boxo/routing/http/server"
	"github.com/ipfs/boxo/routing/http/types"
	"github.com/ipfs/boxo/routing/http/types/iter"
	"github.com/ipfs/go-cid"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/routing"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	_ server.ContentRouter = cachedRouter{}

	// peerAddrLookups allows us reason if/how effective peer addr cache is
	peerAddrLookups = promauto.NewCounterVec(prometheus.CounterOpts{
		Name:      "peer_addr_lookups",
		Subsystem: "cached_router",
		Namespace: "someguy",
		Help:      "Number of peer addr info lookups per origin and cache state",
	},
		[]string{addrCacheStateLabel, addrQueryOriginLabel},
	)
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

func (r cachedRouter) FindProviders(ctx context.Context, key cid.Cid, limit int) (iter.ResultIter[types.Record], error) {
	it, err := r.router.FindProviders(ctx, key, limit)
	if err != nil {
		return nil, err
	}
	return iter.Map(it, func(v iter.Result[types.Record]) iter.Result[types.Record] {
		if v.Err != nil || v.Val == nil {
			return v
		}
		switch v.Val.GetSchema() {
		case types.SchemaPeer:
			result, ok := v.Val.(*types.PeerRecord)
			if !ok {
				logger.Errorw("problem casting find providers result", "Schema", v.Val.GetSchema(), "Type", reflect.TypeOf(v).String())
				return v
			}
			result.Addrs = r.withAddrsFromCache(addrQueryOriginProviders, result.ID, result.Addrs)
			v.Val = result
		//lint:ignore SA1019 // ignore staticcheck
		case types.SchemaBitswap:
			//lint:ignore SA1019 // ignore staticcheck
			result, ok := v.Val.(*types.BitswapRecord)
			if !ok {
				logger.Errorw("problem casting find providers result", "Schema", v.Val.GetSchema(), "Type", reflect.TypeOf(v).String())
				return v
			}
			result.Addrs = r.withAddrsFromCache(addrQueryOriginProviders, result.ID, result.Addrs)
			v.Val = result
		}

		return v
	}), nil
}

func (r cachedRouter) FindPeers(ctx context.Context, pid peer.ID, limit int) (iter.ResultIter[*types.PeerRecord], error) {
	it, err := r.router.FindPeers(ctx, pid, limit)
	if err != nil {
		// check cache, if peer is unknown, return original error
		cachedAddrs := r.withAddrsFromCache(addrQueryOriginPeers, &pid, nil)
		if len(cachedAddrs) == 0 {
			return nil, err
		}
		// if found in cache, return synthetic peer result based on cached addrs
		var sliceIt iter.Iter[*types.PeerRecord] = iter.FromSlice([]*types.PeerRecord{&types.PeerRecord{
			Schema: types.SchemaPeer,
			ID:     &pid,
			Addrs:  cachedAddrs,
		}})
		it = iter.ToResultIter(sliceIt)
	}
	return iter.Map(it, func(v iter.Result[*types.PeerRecord]) iter.Result[*types.PeerRecord] {
		if v.Err != nil || v.Val == nil {
			return v
		}
		switch v.Val.GetSchema() {
		case types.SchemaPeer:
			v.Val.Addrs = r.withAddrsFromCache(addrQueryOriginPeers, v.Val.ID, v.Val.Addrs)
		}
		return v
	}), nil
}

//lint:ignore SA1019 // ignore staticcheck
func (r cachedRouter) ProvideBitswap(ctx context.Context, req *server.BitswapWriteProvideRequest) (time.Duration, error) {
	return 0, routing.ErrNotSupported
}

// withAddrsFromCache returns the best list of addrs for specified [peer.ID].
// It will consult cache only if the addrs slice passed to it is empty.
func (r cachedRouter) withAddrsFromCache(queryOrigin string, pid *peer.ID, addrs []types.Multiaddr) []types.Multiaddr {
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
		peerAddrLookups.WithLabelValues(addrCacheStateMiss, queryOrigin).Inc()
		return nil
	}
}
