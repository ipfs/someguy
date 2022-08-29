package main

import (
	"context"
	"net/http"
	"strconv"

	"github.com/ipfs/go-cid"
	"github.com/ipfs/go-ipns"
	"github.com/libp2p/go-libp2p"
	"github.com/libp2p/go-libp2p-core/peer"
	"github.com/libp2p/go-libp2p-core/routing"
	dht "github.com/libp2p/go-libp2p-kad-dht"
	rhelpers "github.com/libp2p/go-libp2p-routing-helpers"

	drc "github.com/ipfs/go-delegated-routing/client"
	drp "github.com/ipfs/go-delegated-routing/gen/proto"
	drs "github.com/ipfs/go-delegated-routing/server"
)

func start(ctx context.Context, port int) error {
	indexerClient, err := drp.New_DelegatedRouting_Client(devIndexerEndpoint)
	if err != nil {
		return err
	}

	h, err := libp2p.New()
	if err != nil {
		return err
	}

	bsPeers, err := peer.AddrInfosFromP2pAddrs(dht.DefaultBootstrapPeers...)
	if err != nil {
		return err
	}
	d, err := dht.New(ctx, h, dht.Mode(dht.ModeClient), dht.BootstrapPeers(bsPeers...))
	if err != nil {
		return err
	}

	proxy := &delegatedRoutingProxy{
		indexer: &contentRoutingIndexerWrapper{indexer: drc.NewContentRoutingClient(drc.NewClient(indexerClient))},
		dht:     d,
	}

	f := drs.DelegatedRoutingAsyncHandler(proxy)
	http.Handle("/", f)
	return http.ListenAndServe(":"+strconv.Itoa(port), nil)
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
	return nil, d.dht.PutValue(ctx, ipns.RecordKey(peer.ID(id)), record)
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

