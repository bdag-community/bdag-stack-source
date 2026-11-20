package main

import (
	"fmt"
	"os"

	"github.com/BlockdagNetworkLabs/bdag/cmd/chaosworker/chaos"
	"github.com/BlockdagNetworkLabs/bdag/log"
	"github.com/urfave/cli/v2"
)

var (
	app = &cli.App{
		Name:  "chaosworker",
		Usage: "A tool to intentionally corrupt blockdag node data for testing",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:  "datadir",
				Usage: "Path to the blockdag data directory",
				Value: "/root/.bdag",
			},
			&cli.StringFlag{
				Name:  "mode",
				Usage: "Corruption mode: tip-delete",
				Value: "tip-delete",
			},
		},
		Action: func(c *cli.Context) error {
			datadir := c.String("datadir")
			mode := c.String("mode")

			if datadir == "" {
				return fmt.Errorf("datadir is required")
			}
            
            // Should initialize logging
            log.Info("Starting chaosworker")

			return chaos.RunChaos(datadir, mode)
		},
	}
)

func main() {
	if err := app.Run(os.Args); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
