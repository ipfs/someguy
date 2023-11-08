package main

import (
	"context"
	"log"
	"net/http"
	"strconv"
	"time"

	"github.com/ipfs/boxo/ipns"
	"github.com/ipfs/boxo/routing/http/client"
	"github.com/ipfs/boxo/routing/http/contentrouter"
	"github.com/ipfs/boxo/routing/http/server"
	"github.com/ipfs/boxo/routing/http/types"
	"github.com/ipfs/boxo/routing/http/types/iter"
	"github.com/ipfs/go-cid"
	"github.com/libp2p/go-libp2p"
	dht "github.com/libp2p/go-libp2p-kad-dht"
	"github.com/libp2p/go-libp2p-kad-dht/fullrt"
	record "github.com/libp2p/go-libp2p-record"
	routinghelpers "github.com/libp2p/go-libp2p-routing-helpers"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/routing"
	rcmgr "github.com/libp2p/go-libp2p/p2p/host/resource-manager"
)

func start(ctx context.Context, port int, runAcceleratedDHTClient bool, contentEndpoints, peerEndpoints, ipnsEndpoints []string) error {
	h, err := newHost(runAcceleratedDHTClient)
	if err != nil {
		return err
	}

	var dhtRouting routing.Routing
	if runAcceleratedDHTClient {
		wrappedDHT, err := newWrappedStandardAndAcceleratedDHTClient(ctx, h)
		if err != nil {
			return err
		}
		dhtRouting = wrappedDHT
	} else {
		standardDHT, err := dht.New(ctx, h, dht.Mode(dht.ModeClient), dht.BootstrapPeers(dht.GetDefaultBootstrapPeerAddrInfos()...))
		if err != nil {
			return err
		}
		dhtRouting = standardDHT
	}

	crRouters, err := getCombinedRouting(contentEndpoints, dhtRouting)
	if err != nil {
		return err
	}

	prRouters, err := getCombinedRouting(peerEndpoints, dhtRouting)
	if err != nil {
		return err
	}

	ipnsRouters, err := getCombinedRouting(ipnsEndpoints, dhtRouting)
	if err != nil {
		return err
	}

	proxy := &delegatedRoutingProxy{
		cr: crRouters,
		pr: prRouters,
		vs: ipnsRouters,
	}

	log.Printf("Listening on http://0.0.0.0:%d", port)
	log.Printf("Delegated Routing API on http://127.0.0.1:%d/routing/v1", port)

	http.Handle("/", server.Handler(proxy))
	return http.ListenAndServe(":"+strconv.Itoa(port), nil)
}

func newHost(highOutboundLimits bool) (host.Host, error) {
	if !highOutboundLimits {
		return libp2p.New()
	}

	defaultLimits := rcmgr.DefaultLimits
	libp2p.SetDefaultServiceLimits(&defaultLimits)
	// Outbound conns and FDs are set very high to allow for the accelerated DHT client to (re)load its routing table.
	// Currently it doesn't gracefully handle RM throttling--once it does we can lower these.
	// High outbound conn limits are considered less of a DoS risk than high inbound conn limits.
	// Also note that, due to the behavior of the accelerated DHT client, we don't need many streams, just conns.
	if minOutbound := 65536; defaultLimits.SystemBaseLimit.ConnsOutbound < minOutbound {
		defaultLimits.SystemBaseLimit.ConnsOutbound = minOutbound
		if defaultLimits.SystemBaseLimit.Conns < defaultLimits.SystemBaseLimit.ConnsOutbound {
			defaultLimits.SystemBaseLimit.Conns = defaultLimits.SystemBaseLimit.ConnsOutbound
		}
	}
	if minFD := 4096; defaultLimits.SystemBaseLimit.FD < minFD {
		defaultLimits.SystemBaseLimit.FD = minFD
	}
	defaultLimitConfig := defaultLimits.AutoScale()

	rm, err := rcmgr.NewResourceManager(rcmgr.NewFixedLimiter(defaultLimitConfig))
	if err != nil {
		return nil, err
	}
	h, err := libp2p.New(libp2p.ResourceManager(rm))
	if err != nil {
		return nil, err
	}

	return h, nil
}

type wrappedStandardAndAcceleratedDHTClient struct {
	standard    *dht.IpfsDHT
	accelerated *fullrt.FullRT
}

func newWrappedStandardAndAcceleratedDHTClient(ctx context.Context, h host.Host) (routing.Routing, error) {
	standardDHT, err := dht.New(ctx, h, dht.Mode(dht.ModeClient), dht.BootstrapPeers(dht.GetDefaultBootstrapPeerAddrInfos()...))
	if err != nil {
		return nil, err
	}

	acceleratedDHT, err := fullrt.NewFullRT(h, "/ipfs",
		fullrt.DHTOption(
			dht.BucketSize(20),
			dht.Validator(record.NamespacedValidator{
				"pk":   record.PublicKeyValidator{},
				"ipns": ipns.Validator{},
			}),
			dht.BootstrapPeers(dht.GetDefaultBootstrapPeerAddrInfos()...),
			dht.Mode(dht.ModeClient),
		))
	if err != nil {
		return nil, err
	}

	return &wrappedStandardAndAcceleratedDHTClient{
		standard:    standardDHT,
		accelerated: acceleratedDHT,
	}, nil
}

func (w *wrappedStandardAndAcceleratedDHTClient) Provide(ctx context.Context, c cid.Cid, b bool) error {
	if w.accelerated.Ready() {
		return w.accelerated.Provide(ctx, c, b)
	}
	return w.standard.Provide(ctx, c, b)
}

func (w *wrappedStandardAndAcceleratedDHTClient) FindProvidersAsync(ctx context.Context, c cid.Cid, i int) <-chan peer.AddrInfo {
	if w.accelerated.Ready() {
		return w.accelerated.FindProvidersAsync(ctx, c, i)
	}
	return w.standard.FindProvidersAsync(ctx, c, i)
}

func (w *wrappedStandardAndAcceleratedDHTClient) FindPeer(ctx context.Context, p peer.ID) (peer.AddrInfo, error) {
	if w.accelerated.Ready() {
		return w.accelerated.FindPeer(ctx, p)
	}
	return w.standard.FindPeer(ctx, p)
}

func (w *wrappedStandardAndAcceleratedDHTClient) PutValue(ctx context.Context, key string, value []byte, opts ...routing.Option) error {
	if w.accelerated.Ready() {
		return w.accelerated.PutValue(ctx, key, value, opts...)
	}
	return w.standard.PutValue(ctx, key, value, opts...)
}

func (w *wrappedStandardAndAcceleratedDHTClient) GetValue(ctx context.Context, s string, opts ...routing.Option) ([]byte, error) {
	if w.accelerated.Ready() {
		return w.accelerated.GetValue(ctx, s, opts...)
	}
	return w.standard.GetValue(ctx, s, opts...)
}

func (w *wrappedStandardAndAcceleratedDHTClient) SearchValue(ctx context.Context, s string, opts ...routing.Option) (<-chan []byte, error) {
	if w.accelerated.Ready() {
		return w.accelerated.SearchValue(ctx, s, opts...)
	}
	return w.standard.SearchValue(ctx, s, opts...)
}

func (w *wrappedStandardAndAcceleratedDHTClient) Bootstrap(ctx context.Context) error {
	return w.standard.Bootstrap(ctx)
}

func getCombinedRouting(endpoints []string, dht routing.Routing) (routing.Routing, error) {
	if len(endpoints) == 0 {
		return dht, nil
	}

	var routers []routing.Routing

	for _, endpoint := range endpoints {
		drclient, err := client.New(endpoint)
		if err != nil {
			return nil, err
		}
		routers = append(routers, newWrappedDelegatedRouting(drclient))
	}

	return routinghelpers.Parallel{
		Routers: append(routers, dht),
	}, nil
}

type wrappedDelegatedRouting struct {
	routing.ValueStore
	routing.PeerRouting
	routing.ContentRouting
}

func newWrappedDelegatedRouting(drc *client.Client) routing.Routing {
	v := contentrouter.NewContentRoutingClient(drc)

	return &wrappedDelegatedRouting{
		ValueStore:     v,
		PeerRouting:    v,
		ContentRouting: v,
	}
}

func (c *wrappedDelegatedRouting) Bootstrap(ctx context.Context) error {
	return routing.ErrNotSupported
}

type delegatedRoutingProxy struct {
	cr routing.ContentRouting
	pr routing.PeerRouting
	vs routing.ValueStore
}

func (d *delegatedRoutingProxy) FindProviders(ctx context.Context, key cid.Cid, limit int) (iter.ResultIter[types.Record], error) {
	ctx, cancel := context.WithCancel(ctx)
	ch := d.cr.FindProvidersAsync(ctx, key, limit)
	return iter.ToResultIter[types.Record](&peerChanIter{
		ch:     ch,
		cancel: cancel,
	}), nil
}

//lint:ignore SA1019 // ignore staticcheck
func (d *delegatedRoutingProxy) ProvideBitswap(ctx context.Context, req *server.BitswapWriteProvideRequest) (time.Duration, error) {
	return 0, routing.ErrNotSupported
}

func (d *delegatedRoutingProxy) FindPeers(ctx context.Context, pid peer.ID, limit int) (iter.ResultIter[*types.PeerRecord], error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	addr, err := d.pr.FindPeer(ctx, pid)
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

func (d *delegatedRoutingProxy) GetIPNS(ctx context.Context, name ipns.Name) (*ipns.Record, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	raw, err := d.vs.GetValue(ctx, string(name.RoutingKey()))
	if err != nil {
		return nil, err
	}

	return ipns.UnmarshalRecord(raw)
}

func (d *delegatedRoutingProxy) PutIPNS(ctx context.Context, name ipns.Name, record *ipns.Record) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	raw, err := ipns.MarshalRecord(record)
	if err != nil {
		return err
	}

	return d.vs.PutValue(ctx, string(name.RoutingKey()), raw)
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
