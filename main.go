package main

import (
	"errors"
	"log"
	"os"

	"github.com/urfave/cli/v2"

	"github.com/ipfs/go-cid"
	"github.com/libp2p/go-libp2p-core/peer"
	"github.com/multiformats/go-multibase"
)

const devIndexerEndpoint = "https://dev.cid.contact/reframe"

func main() {
	app := &cli.App{
		Name:  "someguy",
		Usage: "ask someguy for your Reframe routing requests",
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
						Usage: "run the accelerated dht client",
						Value: true,
					},
				},
				Action: func(ctx *cli.Context) error {
					return start(ctx.Context, ctx.Int("port"), ctx.Bool("accelerated-dht"))
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
					&cli.BoolFlag{
						Name:  "pretty",
						Usage: "output data in a prettier format that may convey less information",
						Value: false,
					},
				},
				Subcommands: []*cli.Command{
					{
						Name:      "identify",
						UsageText: "Find which methods are supported by the reframe endpoint",
						Action: func(ctx *cli.Context) error {
							if ctx.NArg() != 0 {
								return errors.New("invalid command, see help")
							}
							return identify(ctx.Context, ctx.String("endpoint"), ctx.Bool("pretty"))
						},
					},
					{
						Name:      "findprovs",
						Usage:     "findprovs <cid>",
						UsageText: "Find providers of a given CID",
						Action: func(ctx *cli.Context) error {
							if ctx.NArg() != 1 {
								return errors.New("invalid command, see help")
							}
							cstr := ctx.Args().Get(0)
							c, err := cid.Parse(cstr)
							if err != nil {
								return err
							}
							return findprovs(ctx.Context, c, ctx.String("endpoint"), ctx.Bool("pretty"))
						},
					},
					{
						Name:  "getipns",
						Usage: "getipns <ipns-id>",
						UsageText: "Get the value of an IPNS ID",
						Action: func(ctx *cli.Context) error {
							if ctx.NArg() != 1 {
								return errors.New("invalid command, see help")
							}
							pstr := ctx.Args().Get(0)
							p, err := peer.Decode(pstr)
							if err != nil {
								return err
							}
							return getIPNS(ctx.Context, p, ctx.String("endpoint"), ctx.Bool("pretty"))
						},
					},
					{
						Name:  "putipns",
						Usage: "putipns <ipns-id> <multibase-encoded-record>",
						UsageText: "Put an IPNS record",
						Flags: []cli.Flag{
						},
						Action: func(ctx *cli.Context) error {
							if ctx.NArg() != 2 {
								return errors.New("invalid command, see help")
							}
							pstr := ctx.Args().Get(0)
							p, err := peer.Decode(pstr)
							if err != nil {
								return err
							}
							recordStr := ctx.Args().Get(1)
							_, recBytes, err := multibase.Decode(recordStr)
							if err != nil {
								return err
							}
							return putIPNS(ctx.Context, p, recBytes, ctx.String("endpoint"))
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
