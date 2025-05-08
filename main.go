package main

import (
	"errors"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/ipfs/boxo/ipns"
	"github.com/ipfs/go-cid"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/multiformats/go-multibase"
	"github.com/urfave/cli/v2"
)

const cidContactEndpoint = "https://cid.contact"

func main() {
	app := &cli.App{
		Name:    name,
		Usage:   "A Delegated Routing V1 server and proxy for all your routing needs.",
		Version: version,
		Commands: []*cli.Command{
			{
				Name: "start",
				Flags: []cli.Flag{
					&cli.StringFlag{
						Name:    "listen-address",
						Value:   "127.0.0.1:8190",
						EnvVars: []string{"SOMEGUY_LISTEN_ADDRESS"},
						Usage:   "listen address",
					},
					&cli.StringFlag{
						Name:    "dht",
						Value:   "accelerated",
						EnvVars: []string{"SOMEGUY_DHT"},
						Usage:   "mode with which to run the Amino DHT client. Options are 'accelerated' or 'standard' or 'disabled'.",
					},
					&cli.BoolFlag{
						Name:    "cached-addr-book",
						Value:   true,
						EnvVars: []string{"SOMEGUY_CACHED_ADDR_BOOK"},
						Usage:   "use a cached address book to improve provider lookup responses",
					},
					&cli.BoolFlag{
						Name:    "cached-addr-book-active-probing",
						Value:   true,
						EnvVars: []string{"SOMEGUY_CACHED_ADDR_BOOK_ACTIVE_PROBING"},
						Usage:   "actively probe peers in cache to keep their multiaddrs up to date",
					},
					&cli.DurationFlag{
						Name:        "cached-addr-book-recent-ttl",
						DefaultText: DefaultRecentlyConnectedAddrTTL.String(),
						Value:       DefaultRecentlyConnectedAddrTTL,
						EnvVars:     []string{"SOMEGUY_CACHED_ADDR_BOOK_RECENT_TTL"},
						Usage:       "TTL for recently connected peers' multiaddrs in the cached address book",
					},
					&cli.StringSliceFlag{
						Name:    "provider-endpoints",
						Value:   cli.NewStringSlice(cidContactEndpoint),
						EnvVars: []string{"SOMEGUY_PROVIDER_ENDPOINTS"},
						Usage:   "other Delegated Routing V1 endpoints to proxy provider requests to",
					},
					&cli.StringSliceFlag{
						Name:    "http-block-provider-endpoints",
						Value:   nil,
						EnvVars: []string{"SOMEGUY_HTTP_BLOCK_PROVIDER_ENDPOINTS"},
						Usage:   "list of HTTP trustless gateway endpoints to proxy provider requests to",
					},
					&cli.StringSliceFlag{
						Name:    "http-block-provider-peerids",
						Value:   nil,
						EnvVars: []string{"SOMEGUY_HTTP_BLOCK_PROVIDER_PEERIDS"},
						Usage:   "list of peerIDs for the HTTP trustless gateway endpoints to proxy provider requests to",
					},
					&cli.StringSliceFlag{
						Name:    "peer-endpoints",
						Value:   cli.NewStringSlice(),
						EnvVars: []string{"SOMEGUY_PEER_ENDPOINTS"},
						Usage:   "other Delegated Routing V1 endpoints to proxy peer requests to",
					},
					&cli.StringSliceFlag{
						Name:    "ipns-endpoints",
						Value:   cli.NewStringSlice(),
						EnvVars: []string{"SOMEGUY_IPNS_ENDPOINTS"},
						Usage:   "other Delegated Routing V1 endpoints to proxy IPNS requests to",
					},
					&cli.StringSliceFlag{
						Name: "libp2p-listen-addrs",
						Value: cli.NewStringSlice(
							"/ip4/0.0.0.0/tcp/4004",
							"/ip4/0.0.0.0/udp/4004/quic-v1",
							"/ip4/0.0.0.0/udp/4004/webrtc-direct",
							"/ip4/0.0.0.0/udp/4004/quic-v1/webtransport",
							"/ip6/::/tcp/4004",
							"/ip6/::/udp/4004/quic-v1",
							"/ip6/::/udp/4004/webrtc-direct",
							"/ip6/::/udp/4004/quic-v1/webtransport"),
						EnvVars: []string{"SOMEGUY_LIBP2P_LISTEN_ADDRS"},
						Usage:   "Multiaddresses for libp2p host to listen on (comma-separated)",
					},
					&cli.IntFlag{
						Name:    "libp2p-connmgr-low",
						Value:   100,
						EnvVars: []string{"SOMEGUY_LIBP2P_CONNMGR_LOW"},
						Usage:   "minimum number of libp2p connections to keep",
					},
					&cli.IntFlag{
						Name:    "libp2p-connmgr-high",
						Value:   3000,
						EnvVars: []string{"SOMEGUY_LIBP2P_CONNMGR_HIGH"},
						Usage:   "maximum number of libp2p connections to keep",
					},
					&cli.DurationFlag{
						Name:    "libp2p-connmgr-grace",
						Value:   time.Minute,
						EnvVars: []string{"SOMEGUY_LIBP2P_CONNMGR_GRACE_PERIOD"},
						Usage:   "minimum libp2p connection TTL",
					},
					&cli.Uint64Flag{
						Name:    "libp2p-max-memory",
						Value:   0,
						EnvVars: []string{"SOMEGUY_LIBP2P_MAX_MEMORY"},
						Usage:   "maximum memory to use for libp2p. Defaults to 85% of the system's available RAM",
					},
					&cli.Uint64Flag{
						Name:    "libp2p-max-fd",
						Value:   0,
						EnvVars: []string{"SOMEGUY_LIBP2P_MAX_FD"},
						Usage:   "maximum number of file descriptors used by libp2p node. Defaults to 50% of the process' limit",
					},
					&cli.StringFlag{
						Name:    "tracing-auth",
						Value:   "",
						EnvVars: []string{"SOMEGUY_TRACING_AUTH"},
						Usage:   "If set the key gates use of the Traceparent header by requiring the key to be passed in the Authorization header",
					},
					&cli.Float64Flag{
						Name:    "sampling-fraction",
						Value:   0,
						EnvVars: []string{"SOMEGUY_SAMPLING_FRACTION"},
						Usage:   "Rate at which to sample gateway requests. Does not include requests with traceheaders which will always sample",
					},
				},
				Action: func(ctx *cli.Context) error {
					cfg := &config{
						listenAddress:               ctx.String("listen-address"),
						dhtType:                     ctx.String("dht"),
						cachedAddrBook:              ctx.Bool("cached-addr-book"),
						cachedAddrBookActiveProbing: ctx.Bool("cached-addr-book-active-probing"),
						cachedAddrBookRecentTTL:     ctx.Duration("cached-addr-book-recent-ttl"),

						contentEndpoints:       ctx.StringSlice("provider-endpoints"),
						peerEndpoints:          ctx.StringSlice("peer-endpoints"),
						ipnsEndpoints:          ctx.StringSlice("ipns-endpoints"),
						blockProviderEndpoints: ctx.StringSlice("http-block-provider-endpoints"),
						blockProviderPeerIDs:   ctx.StringSlice("http-block-provider-peerids"),

						libp2pListenAddress: ctx.StringSlice("libp2p-listen-addrs"),
						connMgrLow:          ctx.Int("libp2p-connmgr-low"),
						connMgrHi:           ctx.Int("libp2p-connmgr-high"),
						connMgrGrace:        ctx.Duration("libp2p-connmgr-grace"),
						maxMemory:           ctx.Uint64("libp2p-max-memory"),
						maxFD:               ctx.Int("libp2p-max-fd"),

						tracingAuth:      ctx.String("tracing-auth"),
						samplingFraction: ctx.Float64("sampling-fraction"),
					}

					fmt.Printf("Starting %s %s\n", name, version)

					fmt.Printf("SOMEGUY_DHT = %s\n", cfg.dhtType)
					printIfListConfigured("SOMEGUY_PROVIDER_ENDPOINTS = ", cfg.contentEndpoints)
					printIfListConfigured("SOMEGUY_PEER_ENDPOINTS = ", cfg.peerEndpoints)
					printIfListConfigured("SOMEGUY_IPNS_ENDPOINTS = ", cfg.ipnsEndpoints)
					printIfListConfigured("SOMEGUY_HTTP_BLOCK_PROVIDER_ENDPOINTS = ", cfg.blockProviderEndpoints)
					printIfListConfigured("SOMEGUY_HTTP_BLOCK_PROVIDER_PEERIDS = ", cfg.blockProviderPeerIDs)

					return start(ctx.Context, cfg)
				},
			},
			{
				Name: "ask",
				Flags: []cli.Flag{
					&cli.StringFlag{
						Name:  "endpoint",
						Value: cidContactEndpoint,
						Usage: "the Delegated Routing V1 endpoint to ask",
					},
					&cli.BoolFlag{
						Name:  "pretty",
						Value: false,
						Usage: "output data in a prettier format that may convey less information",
					},
				},
				Subcommands: []*cli.Command{
					{
						Name:      "findprovs",
						Usage:     "findprovs <cid>",
						UsageText: "Find providers of a given CID",
						Action: func(ctx *cli.Context) error {
							if ctx.NArg() != 1 {
								return errors.New("invalid command, see help")
							}
							cidStr := ctx.Args().Get(0)
							c, err := cid.Parse(cidStr)
							if err != nil {
								return err
							}
							return findProviders(ctx.Context, c, ctx.String("endpoint"), ctx.Bool("pretty"))
						},
					},
					{
						Name:      "findpeers",
						Usage:     "findpeers <pid>",
						UsageText: "Find a peer of a given PID",
						Action: func(ctx *cli.Context) error {
							if ctx.NArg() != 1 {
								return errors.New("invalid command, see help")
							}
							pidStr := ctx.Args().Get(0)
							pid, err := peer.Decode(pidStr)
							if err != nil {
								return err
							}
							return findPeers(ctx.Context, pid, ctx.String("endpoint"), ctx.Bool("pretty"))
						},
					},
					{
						Name:      "getipns",
						Usage:     "getipns <ipns-id>",
						UsageText: "Get the value of an IPNS ID",
						Action: func(ctx *cli.Context) error {
							if ctx.NArg() != 1 {
								return errors.New("invalid command, see help")
							}
							nameStr := ctx.Args().Get(0)
							name, err := ipns.NameFromString(nameStr)
							if err != nil {
								return err
							}
							return getIPNS(ctx.Context, name, ctx.String("endpoint"), ctx.Bool("pretty"))
						},
					},
					{
						Name:      "putipns",
						Usage:     "putipns <ipns-id> <multibase-encoded-record>",
						UsageText: "Put an IPNS record",
						Flags:     []cli.Flag{},
						Action: func(ctx *cli.Context) error {
							if ctx.NArg() != 2 {
								return errors.New("invalid command, see help")
							}
							nameStr := ctx.Args().Get(0)
							name, err := ipns.NameFromString(nameStr)
							if err != nil {
								return err
							}
							recordStr := ctx.Args().Get(1)
							_, recBytes, err := multibase.Decode(recordStr)
							if err != nil {
								return err
							}
							return putIPNS(ctx.Context, name, recBytes, ctx.String("endpoint"))
						},
					},
				},
			},
		},
	}

	err := app.Run(os.Args)
	if err != nil {
		log.Fatal(err)
	}
}

func printIfListConfigured(message string, list []string) {
	if len(list) > 0 {
		fmt.Printf(message+"%v\n", strings.Join(list, ", "))
	}
}
