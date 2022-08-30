package main

import (
	"context"
	rcmgr "github.com/libp2p/go-libp2p-resource-manager"
	"net/http"
	"strconv"

	"github.com/ipfs/go-cid"
	"github.com/ipfs/go-ipns"
	"github.com/libp2p/go-libp2p"
	"github.com/libp2p/go-libp2p-core/host"
	"github.com/libp2p/go-libp2p-core/peer"
	"github.com/libp2p/go-libp2p-core/routing"
	dht "github.com/libp2p/go-libp2p-kad-dht"
	"github.com/libp2p/go-libp2p-kad-dht/fullrt"
	record "github.com/libp2p/go-libp2p-record"
	rhelpers "github.com/libp2p/go-libp2p-routing-helpers"

	drc "github.com/ipfs/go-delegated-routing/client"
	drp "github.com/ipfs/go-delegated-routing/gen/proto"
	drs "github.com/ipfs/go-delegated-routing/server"
)

func start(ctx context.Context, port int, runAcceleratedDHTClient bool) error {
	indexerClient, err := drp.New_DelegatedRouting_Client(devIndexerEndpoint)
	if err != nil {
		return err
	}

	h, err := newHost(runAcceleratedDHTClient)
	if err != nil {
		return err
	}

	var d routing.Routing

	if runAcceleratedDHTClient {
		wrappedDHT, err := wrapAcceleratedAndRegularClient(ctx, h)
		if err != nil {
			return err
		}
		d = wrappedDHT
	} else {
		bsPeers, err := peer.AddrInfosFromP2pAddrs(dht.DefaultBootstrapPeers...)
		if err != nil {
			return err
		}
		standardDHT, err := dht.New(ctx, h, dht.Mode(dht.ModeClient), dht.BootstrapPeers(bsPeers...))
		if err != nil {
			return err
		}
		d = standardDHT
	}

	proxy := &delegatedRoutingProxy{
		indexer: &contentRoutingIndexerWrapper{indexer: drc.NewContentRoutingClient(drc.NewClient(indexerClient))},
		dht:     d,
	}

	f := drs.DelegatedRoutingAsyncHandler(proxy)
	http.Handle("/", f)
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

var _ routing.Routing = (*wrappedStandardAndAcceleratedDHTClient)(nil)

func wrapAcceleratedAndRegularClient(ctx context.Context, h host.Host) (routing.Routing, error) {
	bsPeers, err := peer.AddrInfosFromP2pAddrs(dht.DefaultBootstrapPeers...)
	if err != nil {
		return nil, err
	}
	standardDHT, err := dht.New(ctx, h, dht.Mode(dht.ModeClient), dht.BootstrapPeers(bsPeers...))
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
			dht.BootstrapPeers(bsPeers...),
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

type delegatedRoutingProxy struct {
	indexer routing.Routing
	dht     routing.Routing
}

func (d *delegatedRoutingProxy) FindProviders(ctx context.Context, key cid.Cid) (<-chan drc.FindProvidersAsyncResult, error) {
	ctx, cancel := context.WithCancel(ctx)
	ais := rhelpers.Parallel{
		Routers: []routing.Routing{d.indexer, d.dht},
	}.FindProvidersAsync(ctx, key, 0)

	ch := make(chan drc.FindProvidersAsyncResult)

	go func() {
		defer close(ch)
		defer cancel()
		for ai := range ais {
			next := drc.FindProvidersAsyncResult{
				AddrInfo: []peer.AddrInfo{ai},
				Err:      nil,
			}
			select {
			case <-ctx.Done():
				return
			case ch <- next:
			}
		}
	}()
	return ch, nil
}

func (d *delegatedRoutingProxy) GetIPNS(ctx context.Context, id []byte) (<-chan drc.GetIPNSAsyncResult, error) {
	ctx, cancel := context.WithCancel(ctx)
	recs, err := d.dht.SearchValue(ctx, ipns.RecordKey(peer.ID(id)))
	if err != nil {
		cancel()
		return nil, err
	}

	ch := make(chan drc.GetIPNSAsyncResult)

	go func() {
		defer close(ch)
		defer cancel()
		for r := range recs {
			next := drc.GetIPNSAsyncResult{
				Record: r,
				Err:    nil,
			}
			select {
			case <-ctx.Done():
				return
			case ch <- next:
			}
		}
	}()
	return ch, nil
}

func (d *delegatedRoutingProxy) PutIPNS(ctx context.Context, id []byte, record []byte) (<-chan drc.PutIPNSAsyncResult, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	err := d.dht.PutValue(ctx, ipns.RecordKey(peer.ID(id)), record)
	ch := make(chan drc.PutIPNSAsyncResult, 1)
	ch <- drc.PutIPNSAsyncResult{Err: err}
	close(ch)
	return ch, nil
}

var _ drs.DelegatedRoutingService = (*delegatedRoutingProxy)(nil)

type contentRoutingIndexerWrapper struct {
	indexer *drc.ContentRoutingClient
}

func (c *contentRoutingIndexerWrapper) Provide(ctx context.Context, c2 cid.Cid, b bool) error {
	return routing.ErrNotSupported
}

func (c *contentRoutingIndexerWrapper) FindProvidersAsync(ctx context.Context, c2 cid.Cid, i int) <-chan peer.AddrInfo {
	return c.indexer.FindProvidersAsync(ctx, c2, i)
}

func (c *contentRoutingIndexerWrapper) FindPeer(ctx context.Context, id peer.ID) (peer.AddrInfo, error) {
	return peer.AddrInfo{}, routing.ErrNotSupported
}

func (c *contentRoutingIndexerWrapper) PutValue(ctx context.Context, s string, bytes []byte, option ...routing.Option) error {
	return routing.ErrNotSupported
}

func (c *contentRoutingIndexerWrapper) GetValue(ctx context.Context, s string, option ...routing.Option) ([]byte, error) {
	return nil, routing.ErrNotSupported
}

func (c *contentRoutingIndexerWrapper) SearchValue(ctx context.Context, s string, option ...routing.Option) (<-chan []byte, error) {
	return nil, routing.ErrNotSupported
}

func (c *contentRoutingIndexerWrapper) Bootstrap(ctx context.Context) error {
	return routing.ErrNotSupported
}

var _ routing.Routing = (*contentRoutingIndexerWrapper)(nil)
