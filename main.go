package main

import (
	"github.com/urfave/cli/v2"
	"log"
	"os"

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
				Action: func(ctx *cli.Context) error {
					return start(ctx.Context, 8080)
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
