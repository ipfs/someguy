package main

import (
	"errors"
	"log"
	"os"
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
					&cli.BoolFlag{
						Name:    "accelerated-dht",
						Value:   true,
						EnvVars: []string{"SOMEGUY_ACCELERATED_DHT"},
						Usage:   "run the accelerated DHT client",
					},
					&cli.StringSliceFlag{
						Name:    "provider-endpoints",
						Value:   cli.NewStringSlice(cidContactEndpoint),
						EnvVars: []string{"SOMEGUY_PROVIDER_ENDPOINTS"},
						Usage:   "other Delegated Routing V1 endpoints to proxy provider requests to",
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
				},
				Action: func(ctx *cli.Context) error {
					cfg := &config{
						listenAddress:        ctx.String("listen-address"),
						acceleratedDHTClient: ctx.Bool("accelerated-dht"),

						contentEndpoints: ctx.StringSlice("provider-endpoints"),
						peerEndpoints:    ctx.StringSlice("peer-endpoints"),
						ipnsEndpoints:    ctx.StringSlice("ipns-endpoints"),

						connMgrLow:   ctx.Int("libp2p-connmgr-low"),
						connMgrHi:    ctx.Int("libp2p-connmgr-high"),
						connMgrGrace: ctx.Duration("libp2p-connmgr-grace"),
						maxMemory:    ctx.Uint64("libp2p-max-memory"),
						maxFD:        ctx.Int("libp2p-max-fd"),
					}

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
