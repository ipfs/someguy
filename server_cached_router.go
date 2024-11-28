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
)

var _ server.ContentRouter = cachedRouter{}

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
			if len(result.Addrs) == 0 {
				result.Addrs = r.getMaddrsFromCache(result.ID)
			}

			v.Val = result

		//lint:ignore SA1019 // ignore staticcheck
		case types.SchemaBitswap:
			//lint:ignore SA1019 // ignore staticcheck
			result, ok := v.Val.(*types.BitswapRecord)
			if !ok {
				logger.Errorw("problem casting find providers result", "Schema", v.Val.GetSchema(), "Type", reflect.TypeOf(v).String())
				return v
			}

			if len(result.Addrs) == 0 {
				result.Addrs = r.getMaddrsFromCache(result.ID)
			}
			v.Val = result
		}

		return v
	}), nil
}

func (r cachedRouter) FindPeers(ctx context.Context, pid peer.ID, limit int) (iter.ResultIter[*types.PeerRecord], error) {
	// If FindPeers fails, it seems like there's no point returning results from the cache?
	return r.router.FindPeers(ctx, pid, limit)
}

//lint:ignore SA1019 // ignore staticcheck
func (r cachedRouter) ProvideBitswap(ctx context.Context, req *server.BitswapWriteProvideRequest) (time.Duration, error) {
	return 0, routing.ErrNotSupported
}

// GetPeer returns a peer record for a given peer ID, or nil if the peer is not found
func (r cachedRouter) getMaddrsFromCache(pid *peer.ID) []types.Multiaddr {
	cachedAddrs := r.cachedAddrBook.GetCachedAddrs(pid)
	if len(cachedAddrs) > 0 {
		logger.Debugw("found cached addresses", "peer", pid, "cachedAddrs", cachedAddrs)
		return cachedAddrs
	} else {
		return nil
	}
}
