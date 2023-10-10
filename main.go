package main

import (
	"errors"
	"log"
	"os"

	"github.com/ipfs/boxo/ipns"
	"github.com/ipfs/go-cid"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/multiformats/go-multibase"
	"github.com/urfave/cli/v2"
)

const cidContactEndpoint = "https://cid.contact"

func main() {
	app := &cli.App{
		Name:  "someguy",
		Usage: "ask someguy for your Delegated Routing requests",
		Commands: []*cli.Command{
			{
				Name: "start",
				Flags: []cli.Flag{
					&cli.IntFlag{
						Name:  "port",
						Usage: "port to serve requests on",
						Value: 8080,
					},
					&cli.BoolFlag{
						Name:  "accelerated-dht",
						Usage: "run the accelerated DHT client",
						Value: true,
					},
					&cli.StringSliceFlag{
						Name:  "content-endpoints",
						Usage: "other Delegated Routing V1 endpoints to proxy provider requests to",
						Value: cli.NewStringSlice(cidContactEndpoint),
					},
					&cli.StringSliceFlag{
						Name:  "peer-endpoints",
						Usage: "other Delegated Routing V1 endpoints to proxy peer requests to",
						Value: cli.NewStringSlice(),
					},
					&cli.StringSliceFlag{
						Name:  "ipns-endpoints",
						Usage: "other Delegated Routing V1 endpoints to proxy IPNS requests to",
						Value: cli.NewStringSlice(),
					},
				},
				Action: func(ctx *cli.Context) error {
					return start(ctx.Context, ctx.Int("port"), ctx.Bool("accelerated-dht"), ctx.StringSlice("content-endpoints"), ctx.StringSlice("peer-endpoints"), ctx.StringSlice("ipns-endpoints"))
				},
			},
			{
				Name: "ask",
				Flags: []cli.Flag{
					&cli.StringFlag{
						Name:  "endpoint",
						Usage: "the Delegated Routing V1 endpoint to ask",
						Value: cidContactEndpoint,
					},
					&cli.BoolFlag{
						Name:  "pretty",
						Usage: "output data in a prettier format that may convey less information",
						Value: false,
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
