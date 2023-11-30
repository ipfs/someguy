package main

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/ipfs/boxo/ipns"
	"github.com/ipfs/boxo/routing/http/client"
	"github.com/ipfs/boxo/routing/http/server"
	"github.com/ipfs/boxo/routing/http/types"
	"github.com/ipfs/boxo/routing/http/types/iter"
	"github.com/ipfs/go-cid"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/routing"
)

var _ server.ContentRouter = composableRouter{}

type composableRouter struct {
	providers server.ContentRouter
	peers     server.ContentRouter
	ipns      server.ContentRouter
}

func (r composableRouter) FindProviders(ctx context.Context, key cid.Cid, limit int) (iter.ResultIter[types.Record], error) {
	return r.providers.FindProviders(ctx, key, limit)
}

//lint:ignore SA1019 // ignore staticcheck
func (r composableRouter) ProvideBitswap(ctx context.Context, req *server.BitswapWriteProvideRequest) (time.Duration, error) {
	return 0, routing.ErrNotSupported
}

func (r composableRouter) FindPeers(ctx context.Context, pid peer.ID, limit int) (iter.ResultIter[*types.PeerRecord], error) {
	return r.providers.FindPeers(ctx, pid, limit)
}

func (r composableRouter) GetIPNS(ctx context.Context, name ipns.Name) (*ipns.Record, error) {
	return r.ipns.GetIPNS(ctx, name)
}

func (r composableRouter) PutIPNS(ctx context.Context, name ipns.Name, record *ipns.Record) error {
	return r.ipns.PutIPNS(ctx, name, record)
}

var _ server.ContentRouter = parallelRouter{}

type parallelRouter struct {
	routers []server.ContentRouter
}

func (r parallelRouter) FindProviders(ctx context.Context, key cid.Cid, limit int) (iter.ResultIter[types.Record], error) {
	return find(ctx, r.routers, func(ri server.ContentRouter) (iter.ResultIter[types.Record], error) {
		return ri.FindProviders(ctx, key, limit)
	})
}

//lint:ignore SA1019 // ignore staticcheck
func (r parallelRouter) ProvideBitswap(ctx context.Context, req *server.BitswapWriteProvideRequest) (time.Duration, error) {
	return 0, routing.ErrNotSupported
}

func (r parallelRouter) FindPeers(ctx context.Context, pid peer.ID, limit int) (iter.ResultIter[*types.PeerRecord], error) {
	return find(ctx, r.routers, func(ri server.ContentRouter) (iter.ResultIter[*types.PeerRecord], error) {
		return ri.FindPeers(ctx, pid, limit)
	})
}

func find[T any](ctx context.Context, routers []server.ContentRouter, call func(server.ContentRouter) (iter.ResultIter[T], error)) (iter.ResultIter[T], error) {
	switch len(routers) {
	case 0:
		return iter.ToResultIter(iter.FromSlice([]T{})), nil
	case 1:
		return call(routers[0])
	}

	its := make([]iter.ResultIter[T], 0, len(routers))
	var err error
	for _, ri := range routers {
		it, itErr := call(ri)

		if itErr != nil {
			err = errors.Join(err, itErr)
		} else {
			its = append(its, it)
		}
	}

	// If all iterators failed to be created, then return the error.
	if len(its) == 0 {
		return nil, err
	}

	// Otherwise return manyIter with remaining iterators.
	return newManyIter(ctx, its), nil
}

type manyIter[T any] struct {
	ctx    context.Context
	its    []iter.ResultIter[T]
	nextCh chan int
	next   int
}

func newManyIter[T any](ctx context.Context, its []iter.ResultIter[T]) *manyIter[T] {
	nextCh := make(chan int)

	for i, it := range its {
		go func(ch chan int, it iter.ResultIter[T], index int) {
			for it.Next() {
				ch <- index
			}
		}(nextCh, it, i)
	}

	return &manyIter[T]{
		ctx:    ctx,
		its:    its,
		nextCh: nextCh,
		next:   -1,
	}
}

func (mi *manyIter[T]) Next() bool {
	select {
	case i := <-mi.nextCh:
		mi.next = i
		return true
	case <-mi.ctx.Done():
		mi.next = -1
		return false
	}
}

func (mi *manyIter[T]) Val() iter.Result[T] {
	if mi.next == -1 {
		return iter.Result[T]{Err: errors.New("no next value")}
	}
	return mi.its[mi.next].Val()
}

func (mi *manyIter[T]) Close() error {
	var err error
	for _, it := range mi.its {
		err = errors.Join(err, it.Close())
	}
	return err
}

func (r parallelRouter) GetIPNS(ctx context.Context, name ipns.Name) (*ipns.Record, error) {
	switch len(r.routers) {
	case 0:
		return nil, routing.ErrNotFound
	case 1:
		return r.routers[0].GetIPNS(ctx, name)
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	results := make(chan struct {
		val *ipns.Record
		err error
	})
	for _, ri := range r.routers {
		go func(ri server.ContentRouter) {
			value, err := ri.GetIPNS(ctx, name)
			select {
			case results <- struct {
				val *ipns.Record
				err error
			}{
				val: value,
				err: err,
			}:
			case <-ctx.Done():
			}
		}(ri)
	}

	var errs error

	for range r.routers {
		select {
		case res := <-results:
			switch res.err {
			case nil:
				return res.val, nil
			case routing.ErrNotFound, routing.ErrNotSupported:
				continue
			}
			// If the context has expired, just return that error
			// and ignore the other errors.
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}

			errs = errors.Join(errs, res.err)
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}

	if errs == nil {
		return nil, routing.ErrNotFound
	}

	return nil, errs
}

func (r parallelRouter) PutIPNS(ctx context.Context, name ipns.Name, record *ipns.Record) error {
	switch len(r.routers) {
	case 0:
		return nil
	case 1:
		return r.routers[0].PutIPNS(ctx, name, record)
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	var wg sync.WaitGroup
	results := make([]error, len(r.routers))
	wg.Add(len(r.routers))
	for i, ri := range r.routers {
		go func(ri server.ContentRouter, i int) {
			results[i] = ri.PutIPNS(ctx, name, record)
			wg.Done()
		}(ri, i)
	}
	wg.Wait()

	var errs error
	for _, err := range results {
		errs = errors.Join(errs, err)
	}
	return errs
}

var _ server.ContentRouter = dhtRouter{}

type dhtRouter struct {
	dht routing.Routing
}

func (d dhtRouter) FindProviders(ctx context.Context, key cid.Cid, limit int) (iter.ResultIter[types.Record], error) {
	ctx, cancel := context.WithCancel(ctx)
	ch := d.dht.FindProvidersAsync(ctx, key, limit)
	return iter.ToResultIter[types.Record](&peerChanIter{
		ch:     ch,
		cancel: cancel,
	}), nil
}

func (d dhtRouter) FindPeers(ctx context.Context, pid peer.ID, limit int) (iter.ResultIter[*types.PeerRecord], error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	addr, err := d.dht.FindPeer(ctx, pid)
	if err != nil {
		return nil, err
	}

	rec := &types.PeerRecord{
		Schema: types.SchemaPeer,
		ID:     &addr.ID,
	}

	for _, addr := range addr.Addrs {
		rec.Addrs = append(rec.Addrs, types.Multiaddr{Multiaddr: addr})
	}

	return iter.ToResultIter[*types.PeerRecord](iter.FromSlice[*types.PeerRecord]([]*types.PeerRecord{rec})), nil
}

func (d dhtRouter) GetIPNS(ctx context.Context, name ipns.Name) (*ipns.Record, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	raw, err := d.dht.GetValue(ctx, string(name.RoutingKey()))
	if err != nil {
		return nil, err
	}

	return ipns.UnmarshalRecord(raw)
}

func (d dhtRouter) PutIPNS(ctx context.Context, name ipns.Name, record *ipns.Record) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	raw, err := ipns.MarshalRecord(record)
	if err != nil {
		return err
	}

	return d.dht.PutValue(ctx, string(name.RoutingKey()), raw)
}

//lint:ignore SA1019 // ignore staticcheck
func (d dhtRouter) ProvideBitswap(ctx context.Context, req *server.BitswapWriteProvideRequest) (time.Duration, error) {
	return 0, routing.ErrNotSupported
}

type peerChanIter struct {
	ch     <-chan peer.AddrInfo
	cancel context.CancelFunc
	next   *peer.AddrInfo
}

func (it *peerChanIter) Next() bool {
	addr, ok := <-it.ch
	if ok {
		it.next = &addr
		return true
	}
	it.next = nil
	return false
}

func (it *peerChanIter) Val() types.Record {
	if it.next == nil {
		return nil
	}

	rec := &types.PeerRecord{
		Schema: types.SchemaPeer,
		ID:     &it.next.ID,
	}

	for _, addr := range it.next.Addrs {
		rec.Addrs = append(rec.Addrs, types.Multiaddr{Multiaddr: addr})
	}

	return rec
}

func (it *peerChanIter) Close() error {
	it.cancel()
	return nil
}

var _ server.ContentRouter = wrappedClient{}

type wrappedClient struct {
	*client.Client
}

func (d wrappedClient) FindProviders(ctx context.Context, cid cid.Cid, limit int) (iter.ResultIter[types.Record], error) {
	return d.Client.FindProviders(ctx, cid)
}

func (d wrappedClient) FindPeers(ctx context.Context, pid peer.ID, limit int) (iter.ResultIter[*types.PeerRecord], error) {
	return d.Client.FindPeers(ctx, pid)
}

//lint:ignore SA1019 // ignore staticcheck
func (d wrappedClient) ProvideBitswap(ctx context.Context, req *server.BitswapWriteProvideRequest) (time.Duration, error) {
	return 0, routing.ErrNotSupported
}
