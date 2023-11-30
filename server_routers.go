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

type router interface {
	providersRouter
	peersRouter
	ipnsRouter
}

type providersRouter interface {
	FindProviders(ctx context.Context, cid cid.Cid, limit int) (iter.ResultIter[types.Record], error)
}

type peersRouter interface {
	FindPeers(ctx context.Context, pid peer.ID, limit int) (iter.ResultIter[*types.PeerRecord], error)
}

type ipnsRouter interface {
	GetIPNS(ctx context.Context, name ipns.Name) (*ipns.Record, error)
	PutIPNS(ctx context.Context, name ipns.Name, record *ipns.Record) error
}

var _ server.ContentRouter = composableRouter{}

type composableRouter struct {
	providers providersRouter
	peers     peersRouter
	ipns      ipnsRouter
}

func (r composableRouter) FindProviders(ctx context.Context, key cid.Cid, limit int) (iter.ResultIter[types.Record], error) {
	if r.providers == nil {
		return iter.ToResultIter(iter.FromSlice([]types.Record{})), nil
	}
	return r.providers.FindProviders(ctx, key, limit)
}

func (r composableRouter) FindPeers(ctx context.Context, pid peer.ID, limit int) (iter.ResultIter[*types.PeerRecord], error) {
	if r.peers == nil {
		return iter.ToResultIter(iter.FromSlice([]*types.PeerRecord{})), nil
	}
	return r.peers.FindPeers(ctx, pid, limit)
}

func (r composableRouter) GetIPNS(ctx context.Context, name ipns.Name) (*ipns.Record, error) {
	if r.ipns == nil {
		return nil, routing.ErrNotFound
	}
	return r.ipns.GetIPNS(ctx, name)
}

func (r composableRouter) PutIPNS(ctx context.Context, name ipns.Name, record *ipns.Record) error {
	if r.ipns == nil {
		return nil
	}
	return r.ipns.PutIPNS(ctx, name, record)
}

//lint:ignore SA1019 // ignore staticcheck
func (r composableRouter) ProvideBitswap(ctx context.Context, req *server.BitswapWriteProvideRequest) (time.Duration, error) {
	return 0, routing.ErrNotSupported
}

var _ server.ContentRouter = parallelRouter{}

type parallelRouter struct {
	routers []router
}

func (r parallelRouter) FindProviders(ctx context.Context, key cid.Cid, limit int) (iter.ResultIter[types.Record], error) {
	return find(ctx, r.routers, func(ri router) (iter.ResultIter[types.Record], error) {
		return ri.FindProviders(ctx, key, limit)
	})
}

func (r parallelRouter) FindPeers(ctx context.Context, pid peer.ID, limit int) (iter.ResultIter[*types.PeerRecord], error) {
	return find(ctx, r.routers, func(ri router) (iter.ResultIter[*types.PeerRecord], error) {
		return ri.FindPeers(ctx, pid, limit)
	})
}

func find[T any](ctx context.Context, routers []router, call func(router) (iter.ResultIter[T], error)) (iter.ResultIter[T], error) {
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
		go func(ri router) {
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
		go func(ri router, i int) {
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

//lint:ignore SA1019 // ignore staticcheck
func (r parallelRouter) ProvideBitswap(ctx context.Context, req *server.BitswapWriteProvideRequest) (time.Duration, error) {
	return 0, routing.ErrNotSupported
}

var _ router = libp2pRouter{}

type libp2pRouter struct {
	routing routing.Routing
}

func (d libp2pRouter) FindProviders(ctx context.Context, key cid.Cid, limit int) (iter.ResultIter[types.Record], error) {
	ctx, cancel := context.WithCancel(ctx)
	ch := d.routing.FindProvidersAsync(ctx, key, limit)
	return iter.ToResultIter[types.Record](&peerChanIter{
		ch:     ch,
		cancel: cancel,
	}), nil
}

func (d libp2pRouter) FindPeers(ctx context.Context, pid peer.ID, limit int) (iter.ResultIter[*types.PeerRecord], error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	addr, err := d.routing.FindPeer(ctx, pid)
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

func (d libp2pRouter) GetIPNS(ctx context.Context, name ipns.Name) (*ipns.Record, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	raw, err := d.routing.GetValue(ctx, string(name.RoutingKey()))
	if err != nil {
		return nil, err
	}

	return ipns.UnmarshalRecord(raw)
}

func (d libp2pRouter) PutIPNS(ctx context.Context, name ipns.Name, record *ipns.Record) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	raw, err := ipns.MarshalRecord(record)
	if err != nil {
		return err
	}

	return d.routing.PutValue(ctx, string(name.RoutingKey()), raw)
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

var _ router = clientRouter{}

type clientRouter struct {
	*client.Client
}

func (d clientRouter) FindProviders(ctx context.Context, cid cid.Cid, limit int) (iter.ResultIter[types.Record], error) {
	return d.Client.FindProviders(ctx, cid)
}

func (d clientRouter) FindPeers(ctx context.Context, pid peer.ID, limit int) (iter.ResultIter[*types.PeerRecord], error) {
	return d.Client.FindPeers(ctx, pid)
}
