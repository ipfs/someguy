package main

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"net/http"
	"os"

	"github.com/urfave/cli/v2"

	"github.com/ipfs/go-cid"
	"github.com/ipfs/go-ipns"
	"github.com/ipld/go-ipld-prime/codec/dagjson"
	"github.com/libp2p/go-libp2p-core/peer"
	"github.com/libp2p/go-libp2p-core/routing"
	rhelpers "github.com/libp2p/go-libp2p-routing-helpers"

	"github.com/libp2p/go-libp2p"
	dht "github.com/libp2p/go-libp2p-kad-dht"

	drc "github.com/ipfs/go-delegated-routing/client"
	drp "github.com/ipfs/go-delegated-routing/gen/proto"
	drs "github.com/ipfs/go-delegated-routing/server"
)

const devIndexerEndpoint = "https://dev.cid.contact/reframe"

func main() {
	app := &cli.App{
		Name:  "someguy",
		Usage: "ask someguy for your Reframe routing requests",
		Commands: []*cli.Command{
			{
				Name: "start",
				Action: func(ctx *cli.Context) error {
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
					d, err := dht.New(ctx.Context, h, dht.Mode(dht.ModeClient), dht.BootstrapPeers(bsPeers...))
					if err != nil {
						return err
					}

					proxy := &delegatedRoutingProxy{
						indexer: &contentRoutingIndexerWrapper{indexer: drc.NewContentRoutingClient(drc.NewClient(indexerClient))},
						dht:     d,
					}

					f := drs.DelegatedRoutingAsyncHandler(proxy)
					http.Handle("/", f)
					return http.ListenAndServe(":8080", nil)
				},
			},
			{
				Name: "ask",
				Flags: []cli.Flag{
					&cli.StringFlag{
						Name:  "endpoint",
						Usage: "Reframe endpoint",
						Value: devIndexerEndpoint,
					},
				},
				Action: func(ctx *cli.Context) error {
					cstr := ctx.Args().Get(0)
					c, err := cid.Parse(cstr)
					if err != nil {
						return err
					}

					ic, err := drp.New_DelegatedRouting_Client(ctx.String("endpoint"))
					if err != nil {
						return err
					}

					resp, err := ic.FindProviders(ctx.Context, &drp.FindProvidersRequest{
						Key: drp.LinkToAny(c),
					})
					if err != nil {
						return err
					}
					for _, r := range resp {
						var buf bytes.Buffer
						if err := dagjson.Encode(r, &buf); err != nil {
							return err
						}
						fmt.Println(buf.String())
					}
					return nil
				},
			},
		},
	}

	err := app.Run(os.Args)
	if err != nil {
		log.Fatal(err)
	}
}

type delegatedRoutingProxy struct {
	indexer routing.Routing
	dht     routing.Routing
}

func (d *delegatedRoutingProxy) FindProviders(key cid.Cid) (<-chan drc.FindProvidersAsyncResult, error) {
	ctx := context.TODO()
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

func (d *delegatedRoutingProxy) GetIPNS(id []byte) (<-chan drc.GetIPNSAsyncResult, error) {
	ctx := context.TODO()
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

func (d *delegatedRoutingProxy) PutIPNS(id []byte, record []byte) (<-chan drc.PutIPNSAsyncResult, error) {
	ctx := context.TODO()
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
