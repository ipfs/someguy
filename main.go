package main

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ipfs/boxo/autoconf"
	"github.com/ipfs/boxo/ipns"
	"github.com/ipfs/go-cid"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/multiformats/go-multibase"
	"github.com/multiformats/go-multihash"
	"github.com/urfave/cli/v2"
)

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
						Value:   cli.NewStringSlice(autoconf.AutoPlaceholder),
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
					&cli.StringFlag{
						Name:    "datadir",
						Value:   "",
						EnvVars: []string{"SOMEGUY_DATADIR"},
						Usage:   "Directory for persistent data (autoconf cache)",
					},
					&cli.BoolFlag{
						Name:    "autoconf",
						Value:   true,
						EnvVars: []string{"SOMEGUY_AUTOCONF"},
						Usage:   "Enable autoconf for bootstrap, DNS resolvers, and HTTP routers",
					},
					&cli.StringFlag{
						Name:    "autoconf-url",
						Value:   "https://conf.ipfs-mainnet.org/autoconf.json",
						EnvVars: []string{"SOMEGUY_AUTOCONF_URL"},
						Usage:   "URL to fetch autoconf data from",
					},
					&cli.DurationFlag{
						Name:    "autoconf-refresh",
						Value:   24 * time.Hour,
						EnvVars: []string{"SOMEGUY_AUTOCONF_REFRESH"},
						Usage:   "How often to refresh autoconf data",
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

						autoConf: autoConfConfig{
							enabled:         ctx.Bool("autoconf"),
							url:             ctx.String("autoconf-url"),
							refreshInterval: ctx.Duration("autoconf-refresh"),
							cacheDir:        filepath.Join(ctx.String("datadir"), ".autoconf-cache"),
						},
					}

					fmt.Printf("Starting %s %s\n", name, version)

					fmt.Printf("SOMEGUY_DHT = %s\n", cfg.dhtType)
					printIfListConfigured("SOMEGUY_PROVIDER_ENDPOINTS = ", cfg.contentEndpoints)
					printIfListConfigured("SOMEGUY_PEER_ENDPOINTS = ", cfg.peerEndpoints)
					printIfListConfigured("SOMEGUY_IPNS_ENDPOINTS = ", cfg.ipnsEndpoints)

					if len(cfg.blockProviderEndpoints) > 0 && len(cfg.blockProviderPeerIDs) == 0 {
						fmt.Printf("SOMEGUY_HTTP_BLOCK_PROVIDER_ENDPOINTS is set but SOMEGUY_HTTP_BLOCK_PROVIDER_PEERIDS were not. PeerIDs will be autogenerated.\n")
						// Generate synthetic PeerIDs for HTTP block providers. These are deterministic
						// identifiers based on endpoint URLs, used solely for routing system compatibility.
						// Since HTTP providers use trustless gateway protocol, these PeerIDs are never
						// used for cryptographic operations or libp2p authentication.
						for i := 0; i < len(cfg.blockProviderEndpoints); i++ {
							digest := sha256.Sum256([]byte(cfg.blockProviderEndpoints[i]))
							mh, err := multihash.Encode((digest[:]), multihash.SHA2_256)
							if err != nil {
								return err
							}
							p, err := peer.IDFromBytes(mh)
							if err != nil {
								return err
							}
							cfg.blockProviderPeerIDs = append(cfg.blockProviderPeerIDs, p.String())
						}
					}

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
						Value: autoconf.AutoPlaceholder,
						Usage: "the Delegated Routing V1 endpoint to ask",
					},
					&cli.BoolFlag{
						Name:  "pretty",
						Value: false,
						Usage: "output data in a prettier format that may convey less information",
					},
					&cli.StringFlag{
						Name:    "datadir",
						Value:   "",
						EnvVars: []string{"SOMEGUY_DATADIR"},
						Usage:   "Directory for persistent data (autoconf cache)",
					},
					&cli.BoolFlag{
						Name:    "autoconf",
						Value:   true,
						EnvVars: []string{"SOMEGUY_AUTOCONF"},
						Usage:   "Enable autoconf for bootstrap, DNS resolvers, and HTTP routers",
					},
					&cli.StringFlag{
						Name:    "autoconf-url",
						Value:   "https://conf.ipfs-mainnet.org/autoconf.json",
						EnvVars: []string{"SOMEGUY_AUTOCONF_URL"},
						Usage:   "URL to fetch autoconf data from",
					},
					&cli.DurationFlag{
						Name:    "autoconf-refresh",
						Value:   24 * time.Hour,
						EnvVars: []string{"SOMEGUY_AUTOCONF_REFRESH"},
						Usage:   "How often to refresh autoconf data",
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

							cfg := &config{
								dhtType:          "none",
								contentEndpoints: []string{ctx.String("endpoint")},
								autoConf: autoConfConfig{
									enabled:         ctx.Bool("autoconf"),
									url:             ctx.String("autoconf-url"),
									refreshInterval: ctx.Duration("autoconf-refresh"),
									cacheDir:        filepath.Join(ctx.String("datadir"), ".autoconf-cache"),
								},
							}

							autoConf, err := startAutoConf(ctx.Context, cfg)
							if err != nil {
								logger.Error(err.Error())
							}

							if err = expandContentEndpoints(cfg, autoConf); err != nil {
								return err
							}
							if len(cfg.contentEndpoints) == 0 {
								return errors.New("no delegated routing endpoint configured, use --endpoint to specify")
							}

							endPoint := cfg.contentEndpoints[0]
							logger.Debugf("delegated routing endpoint: %s", endPoint)

							return findProviders(ctx.Context, c, endPoint, ctx.Bool("pretty"))
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
							cfg := &config{
								dhtType:          "none",
								contentEndpoints: []string{ctx.String("endpoint")},
								autoConf: autoConfConfig{
									enabled:         ctx.Bool("autoconf"),
									url:             ctx.String("autoconf-url"),
									refreshInterval: ctx.Duration("autoconf-refresh"),
									cacheDir:        filepath.Join(ctx.String("datadir"), ".autoconf-cache"),
								},
							}

							autoConf, err := startAutoConf(ctx.Context, cfg)
							if err != nil {
								logger.Error(err.Error())
							}

							if err = expandContentEndpoints(cfg, autoConf); err != nil {
								return err
							}
							if len(cfg.contentEndpoints) == 0 {
								return errors.New("no delegated routing endpoint configured, use --endpoint to specify")
							}

							endPoint := cfg.contentEndpoints[0]
							logger.Debugf("delegated routing endpoint: %s", endPoint)

							return findPeers(ctx.Context, pid, endPoint, ctx.Bool("pretty"))
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
