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

// cachedRouter wraps a router with the cachedAddrBook to retrieve cached addresses for peers

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
				result.Addrs = r.cachedAddrBook.getCachedAddrs(result.ID)
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

			// TODO: use cachedAddrBook to filter private addresses
			v.Val = result
		}

		return v
	}), nil
}

func (r cachedRouter) FindPeers(ctx context.Context, pid peer.ID, limit int) (iter.ResultIter[*types.PeerRecord], error) {
	it, err := r.router.FindPeers(ctx, pid, limit)
	if err != nil {
		return nil, err
	}

	return iter.Map(it, func(v iter.Result[*types.PeerRecord]) iter.Result[*types.PeerRecord] {
		if v.Err != nil || v.Val == nil {
			return v
		}

		// If no addresses were found by router, use cached addresses
		if len(v.Val.Addrs) == 0 {
			v.Val.Addrs = r.cachedAddrBook.getCachedAddrs(v.Val.ID)
		}
		return v
	}), nil
}

//lint:ignore SA1019 // ignore staticcheck
func (r cachedRouter) ProvideBitswap(ctx context.Context, req *server.BitswapWriteProvideRequest) (time.Duration, error) {
	return 0, routing.ErrNotSupported
}
