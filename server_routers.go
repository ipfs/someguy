package main

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ipfs/boxo/ipns"
	"github.com/ipfs/boxo/routing/http/server"
	"github.com/ipfs/boxo/routing/http/types"
	"github.com/ipfs/boxo/routing/http/types/iter"
	"github.com/ipfs/go-cid"
	dht "github.com/libp2p/go-libp2p-kad-dht"
	"github.com/libp2p/go-libp2p-kad-dht/dual"
	"github.com/libp2p/go-libp2p-kad-dht/fullrt"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/routing"
	manet "github.com/multiformats/go-multiaddr/net"
)

type router interface {
	providersRouter
	peersRouter
	ipnsRouter
	dhtRouter
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

type dhtRouter interface {
	GetClosestPeers(ctx context.Context, key cid.Cid) (iter.ResultIter[*types.PeerRecord], error)
}

var _ server.ContentRouter = composableRouter{}

type composableRouter struct {
	providers providersRouter
	peers     peersRouter
	ipns      ipnsRouter
	dht       dhtRouter
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

func (r composableRouter) GetClosestPeers(ctx context.Context, key cid.Cid) (iter.ResultIter[*types.PeerRecord], error) {
	if r.dht == nil {
		// Return ErrNotSupported when no DHT is available (e.g., disabled via --dht=disabled CLI param).
		// This returns HTTP 501 Not Implemented instead of misleading HTTP 200 with empty results.
		return nil, routing.ErrNotSupported
	}
	return r.dht.GetClosestPeers(ctx, key)
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
			logger.Warnf("error from router: %w", itErr)
			err = errors.Join(err, itErr)
		} else {
			its = append(its, it)
		}
	}

	// If all iterators failed to be created, then return the error.
	if len(its) == 0 {
		logger.Warnf("failed to create all iterators: %w", err)
		return nil, err
	} else if err != nil {
		logger.Warnf("failed to create some iterators: %w", err)
	}

	// Otherwise return manyIter with remaining iterators.
	return newManyIter(ctx, its), nil
}

func (r parallelRouter) GetClosestPeers(ctx context.Context, key cid.Cid) (iter.ResultIter[*types.PeerRecord], error) {
	return find(ctx, r.routers, func(ri router) (iter.ResultIter[*types.PeerRecord], error) {
		return ri.GetClosestPeers(ctx, key)
	})
}

type manyIter[T any] struct {
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
	its    []iter.ResultIter[T]
	ch     chan iter.Result[T]
	val    iter.Result[T]
	done   bool
}

func newManyIter[T any](ctx context.Context, its []iter.ResultIter[T]) *manyIter[T] {
	ctx, cancel := context.WithCancel(ctx)

	mi := &manyIter[T]{
		ctx:    ctx,
		cancel: cancel,
		its:    its,
		ch:     make(chan iter.Result[T]),
	}

	for _, it := range its {
		mi.wg.Add(1)
		go func(it iter.ResultIter[T]) {
			defer mi.wg.Done()
			for it.Next() {
				select {
				case mi.ch <- it.Val():
				case <-ctx.Done():
					return
				}
			}
		}(it)
	}

	go func() {
		mi.wg.Wait()
		close(mi.ch)
	}()

	return mi
}

func (mi *manyIter[T]) Next() bool {
	if mi.done {
		return false
	}

	select {
	case val, ok := <-mi.ch:
		if ok {
			mi.val = val
		} else {
			mi.done = true
		}
	case <-mi.ctx.Done():
		mi.done = true
	}

	return !mi.done
}

func (mi *manyIter[T]) Val() iter.Result[T] {
	return mi.val
}

func (mi *manyIter[T]) Close() error {
	if mi.done {
		return nil // Already closed, idempotent
	}
	mi.done = true
	mi.cancel() // Signal goroutines to stop

	// The channel will be closed by the goroutine in newManyIter once all workers finish
	// We just need to drain it to unblock any pending sends
	// This is expected behavior when client terminates early (per HTTP routing spec)
	for range mi.ch {
		// Discard remaining values
	}

	// Now close child iterators
	var err error
	for _, it := range mi.its {
		err = errors.Join(err, it.Close())
	}
	if err != nil {
		logger.Warnf("errors on closing iterators: %w", err)
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
	host    host.Host
	routing routing.Routing
}

func (d libp2pRouter) FindProviders(ctx context.Context, key cid.Cid, limit int) (iter.ResultIter[types.Record], error) {
	ctx, cancel := context.WithCancel(ctx)
	ch := d.routing.FindProvidersAsync(ctx, key, limit)
	return iter.ToResultIter(&peerChanIter{
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

	return iter.ToResultIter(iter.FromSlice([]*types.PeerRecord{rec})), nil
}

func (d libp2pRouter) GetClosestPeers(ctx context.Context, key cid.Cid) (iter.ResultIter[*types.PeerRecord], error) {
	// Per the spec, if the peer ID is empty, we should use self.
	if key == cid.Undef {
		return nil, errors.New("GetClosestPeers: key is undefined")
	}

	keyStr := string(key.Hash())
	var peers []peer.ID
	var err error

	switch dhtClient := d.routing.(type) {
	case *dual.DHT:
		// Only use WAN DHT for public HTTP Routing API (same as Kubo)
		// LAN DHT contains private network peers that should not be exposed publicly.
		if dhtClient.WAN == nil {
			return nil, fmt.Errorf("GetClosestPeers not supported: WAN DHT is not available")
		}
		peers, err = dhtClient.WAN.GetClosestPeers(ctx, keyStr)
		if err != nil {
			return nil, err
		}
	case *fullrt.FullRT:
		peers, err = dhtClient.GetClosestPeers(ctx, keyStr)
		if err != nil {
			return nil, err
		}
	case *dht.IpfsDHT:
		peers, err = dhtClient.GetClosestPeers(ctx, keyStr)
		if err != nil {
			return nil, err
		}
	case *bundledDHT:
		// bundledDHT uses either fullRT (when ready) or standard DHT
		// We need to call GetClosestPeers on the active DHT
		activeDHT := dhtClient.getDHT()
		switch dht := activeDHT.(type) {
		case *fullrt.FullRT:
			peers, err = dht.GetClosestPeers(ctx, keyStr)
		case *dht.IpfsDHT:
			peers, err = dht.GetClosestPeers(ctx, keyStr)
		default:
			return nil, errors.New("bundledDHT returned unexpected DHT type")
		}
		if err != nil {
			return nil, err
		}
	default:
		return nil, errors.New("cannot call GetClosestPeers on DHT implementation")
	}

	// We have some DHT-closest peers. Find addresses for them.
	// The addresses should be in the peerstore.
	var records []*types.PeerRecord
	for _, p := range peers {
		addrs := d.host.Peerstore().Addrs(p)
		rAddrs := make([]types.Multiaddr, len(addrs))
		for i, addr := range addrs {
			rAddrs[i] = types.Multiaddr{Multiaddr: addr}
		}
		record := types.PeerRecord{
			ID:     &p,
			Schema: types.SchemaPeer,
			Addrs:  rAddrs,
			// we dont seem to care about protocol/extra infos
		}
		records = append(records, &record)
	}

	return iter.ToResultIter(iter.FromSlice(records)), nil
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

var _ server.ContentRouter = sanitizeRouter{}

// sanitizeRouter wraps a router with address filtering applied to every
// response. Filtering happens in two stages:
//
//  1. Fast inline stage (filterAddrs via iter.Map): strips private addrs
//     and, when a known-good connected addr exists, applies passive
//     stale-port filtering.
//
//  2. Async probing stage (probeFilterIter): for first-encounter peers
//     whose addr sets look suspicious (multi-port, multi-IP), dispatches
//     per-addr probing in background goroutines so the NDJSON stream is
//     not blocked. Probed results appear after non-probed records.
//
// Stage 2 is only active when both cab and prober are set.
type sanitizeRouter struct {
	router
	cab    *cachedAddrBook // optional: enables passive stale addr filtering
	prober *addrProber     // optional: enables active per-addr probing
}

func (r sanitizeRouter) FindProviders(ctx context.Context, key cid.Cid, limit int) (iter.ResultIter[types.Record], error) {
	it, err := r.router.FindProviders(ctx, key, limit)
	if err != nil {
		return nil, err
	}

	// Stage 1 (fast, inline): strip private addrs + passive stale-port filtering
	filtered := iter.Map(it, func(v iter.Result[types.Record]) iter.Result[types.Record] {
		if v.Err != nil || v.Val == nil {
			return v
		}

		switch v.Val.GetSchema() {
		case types.SchemaPeer:
			result, ok := v.Val.(*types.PeerRecord)
			if !ok {
				logger.Errorw("problem casting find providers result", "Schema", v.Val.GetSchema(), "Type", reflect.TypeFor[iter.Result[types.Record]]().String())
				return v
			}

			result.Addrs = r.filterAddrs(*result.ID, result.Addrs)
			v.Val = result

		//lint:ignore SA1019 // ignore staticcheck
		case types.SchemaBitswap:
			//lint:ignore SA1019 // ignore staticcheck
			result, ok := v.Val.(*types.BitswapRecord)
			if !ok {
				logger.Errorw("problem casting find providers result", "Schema", v.Val.GetSchema(), "Type", reflect.TypeFor[iter.Result[types.Record]]().String())
				return v
			}

			result.Addrs = filterPrivateMultiaddr(result.Addrs)
			v.Val = result
		}

		return v
	})

	// Stage 2 (async): wrap with probeFilterIter for per-addr probing
	// of first-encounter peers with suspicious addr sets.
	if r.prober != nil && r.cab != nil {
		return newProbeFilterIter(filtered, r, ctx), nil
	}
	return filtered, nil
}

func (r sanitizeRouter) FindPeers(ctx context.Context, pid peer.ID, limit int) (iter.ResultIter[*types.PeerRecord], error) {
	it, err := r.router.FindPeers(ctx, pid, limit)
	if err != nil {
		return nil, err
	}

	filtered := iter.Map(it, func(v iter.Result[*types.PeerRecord]) iter.Result[*types.PeerRecord] {
		if v.Err != nil || v.Val == nil {
			return v
		}

		v.Val.Addrs = r.filterAddrs(*v.Val.ID, v.Val.Addrs)
		return v
	})

	if r.prober != nil && r.cab != nil {
		return r.applyProbeFiltering(filtered, ctx), nil
	}
	return filtered, nil
}

func (r sanitizeRouter) GetClosestPeers(ctx context.Context, key cid.Cid) (iter.ResultIter[*types.PeerRecord], error) {
	it, err := r.router.GetClosestPeers(ctx, key)
	if err != nil {
		return nil, err
	}

	filtered := iter.Map(it, func(v iter.Result[*types.PeerRecord]) iter.Result[*types.PeerRecord] {
		if v.Err != nil || v.Val == nil {
			return v
		}

		v.Val.Addrs = r.filterAddrs(*v.Val.ID, v.Val.Addrs)
		return v
	})

	if r.prober != nil && r.cab != nil {
		return r.applyProbeFiltering(filtered, ctx), nil
	}
	return filtered, nil
}

//lint:ignore SA1019 // ignore staticcheck
func (r sanitizeRouter) ProvideBitswap(ctx context.Context, req *server.BitswapWriteProvideRequest) (time.Duration, error) {
	return 0, routing.ErrNotSupported
}

// filterAddrs applies fast address filters: removes private addrs and,
// when cached addr book has a known-good connected addr, removes stale
// addresses on the same IP with a different port. Does not do active
// probing (that is handled asynchronously by probeFilterIter).
func (r sanitizeRouter) filterAddrs(pid peer.ID, addrs []types.Multiaddr) []types.Multiaddr {
	addrs = filterPrivateMultiaddr(addrs)
	if r.cab != nil {
		if connAddr := r.cab.getConnectedAddr(pid); connAddr != nil {
			addrs = filterStalePortAddrs(addrs, connAddr)
		}
	}
	return addrs
}

// applyProbeFiltering wraps a *types.PeerRecord iterator with async probing.
// Since probeFilterIter operates on types.Record, this converts PeerRecord
// to Record and back, following the same pattern as applyPeerRecordCaching
// in server_cached_router.go.
func (r sanitizeRouter) applyProbeFiltering(it iter.ResultIter[*types.PeerRecord], ctx context.Context) iter.ResultIter[*types.PeerRecord] {
	recordIter := iter.Map(it, func(v iter.Result[*types.PeerRecord]) iter.Result[types.Record] {
		if v.Err != nil {
			return iter.Result[types.Record]{Err: v.Err}
		}
		return iter.Result[types.Record]{Val: v.Val}
	})

	probeIter := newProbeFilterIter(recordIter, r, ctx)

	return iter.Map(probeIter, func(v iter.Result[types.Record]) iter.Result[*types.PeerRecord] {
		if v.Err != nil {
			return iter.Result[*types.PeerRecord]{Err: v.Err}
		}
		peerRec, ok := v.Val.(*types.PeerRecord)
		if !ok {
			return iter.Result[*types.PeerRecord]{Err: errors.New("unexpected record type in probe filter")}
		}
		return iter.Result[*types.PeerRecord]{Val: peerRec}
	})
}

var _ iter.ResultIter[types.Record] = &probeFilterIter{}

// probeFilterIter wraps a types.Record iterator and dispatches per-addr
// probing asynchronously for peers whose address sets look suspicious
// (multiple ports per IP, or multiple IPs per address family).
//
// It follows the same async pattern as cacheFallbackIter (server_cached_router.go):
//
//  1. Pull records from the source iterator one at a time.
//  2. If a PeerRecord needs probing (needsProbing == true, not recently probed),
//     dispatch probing in a background goroutine and move to the next record
//     without blocking the stream.
//  3. Records that don't need probing pass through immediately.
//  4. After the source is exhausted, drain pending probe results from the
//     probeResults channel before signaling completion.
//
// This ensures the NDJSON stream starts flowing immediately for non-probed
// peers, and probed peers appear at the end once their handshakes complete.
type probeFilterIter struct {
	sourceIter    iter.ResultIter[types.Record]
	current       iter.Result[types.Record]
	probeResults  chan iter.Result[types.Record] // receives results from background dispatchProbe goroutines
	router        sanitizeRouter
	ctx           context.Context
	cancel        context.CancelFunc
	ongoingProbes atomic.Int32 // tracks in-flight dispatchProbe goroutines
}

func newProbeFilterIter(sourceIter iter.ResultIter[types.Record], r sanitizeRouter, ctx context.Context) *probeFilterIter {
	ctx, cancel := context.WithCancel(ctx)
	return &probeFilterIter{
		sourceIter:   sourceIter,
		router:       r,
		ctx:          ctx,
		cancel:       cancel,
		probeResults: make(chan iter.Result[types.Record], 100),
	}
}

func (it *probeFilterIter) Next() bool {
	for {
		// Phase 1: pull from source iterator, pass through or dispatch probing.
		if it.sourceIter.Next() {
			val := it.sourceIter.Val()
			if val.Err != nil || val.Val == nil {
				it.current = val
				return true
			}

			// only PeerRecords can be probed; pass through other schemas
			if val.Val.GetSchema() != types.SchemaPeer {
				it.current = val
				return true
			}

			record, ok := val.Val.(*types.PeerRecord)
			if !ok || record.ID == nil {
				it.current = val
				return true
			}

			// if the addr set looks suspicious and we haven't probed recently,
			// dispatch probing in the background and skip to next record
			if needsProbing(record.Addrs) && !it.router.cab.wasRecentlyProbed(*record.ID) {
				it.router.cab.recordProbe(*record.ID)
				it.ongoingProbes.Add(1) // must increment before goroutine launch
				go it.dispatchProbe(record)
				continue
			}

			// addr set looks clean, pass through immediately
			it.current = val
			return true
		}

		// Phase 2: source exhausted, wait for in-flight probe goroutines.
		// Same drain pattern as cacheFallbackIter: poll ongoingProbes with
		// a short timer to avoid deadlock if count reaches 0 between check
		// and channel read.
		if it.ongoingProbes.Load() == 0 {
			return false
		}

		timer := time.NewTimer(100 * time.Millisecond)
		select {
		case result, ok := <-it.probeResults:
			timer.Stop()
			if !ok {
				return false
			}
			it.current = result
			return true
		case <-it.ctx.Done():
			timer.Stop()
			return false
		case <-timer.C:
			// timeout, loop back to recheck ongoingProbes
		}
	}
}

func (it *probeFilterIter) Val() iter.Result[types.Record] {
	if it.current.Val != nil || it.current.Err != nil {
		return it.current
	}
	return iter.Result[types.Record]{Err: errNoValueAvailable}
}

func (it *probeFilterIter) Close() error {
	if it.cancel != nil {
		it.cancel()
	}
	return it.sourceIter.Close()
}

// dispatchProbe runs in a background goroutine. It probes all addrs for
// the peer (via addrProber.probeAddrs which handles dedup and fail-open),
// updates the record's addr list, and sends it back through probeResults.
func (it *probeFilterIter) dispatchProbe(record *types.PeerRecord) {
	defer it.ongoingProbes.Add(-1)

	probed := it.router.prober.probeAddrs(it.ctx, *record.ID, record.Addrs)
	record.Addrs = probed

	if it.ctx.Err() != nil {
		return
	}

	select {
	case it.probeResults <- iter.Result[types.Record]{Val: record}:
	case <-it.ctx.Done():
	default:
		// channel full or nobody listening, drop the result.
		// this is best-effort -- same as cacheFallbackIter.dispatchFindPeer
		logger.Debugw("dropping probe result, channel full", "peer", record.ID)
	}
}

func filterPrivateMultiaddr(a []types.Multiaddr) []types.Multiaddr {
	b := make([]types.Multiaddr, 0, len(a))

	for _, addr := range a {
		if manet.IsPrivateAddr(addr.Multiaddr) {
			continue
		}

		b = append(b, addr)
	}

	return b
}
