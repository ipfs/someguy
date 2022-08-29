package main

import (
	"log"
	"os"

	"github.com/urfave/cli/v2"

	"github.com/ipfs/go-cid"
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
				},
				Action: func(ctx *cli.Context) error {
					cstr := ctx.Args().Get(0)
					c, err := cid.Parse(cstr)
					if err != nil {
						return err
					}
					return ask(ctx.Context, c, ctx.String("endpoint"))
				},
			},
		},
	}

	err := app.Run(os.Args)
	if err != nil {
		log.Fatal(err)
	}
}
