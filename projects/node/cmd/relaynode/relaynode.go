package main

import (
	"os"
	"runtime"
	"runtime/debug"

	"github.com/BlockdagNetworkLabs/bdag/cmd/relaynode/config"
	"github.com/BlockdagNetworkLabs/bdag/common/roughtime"
	_ "github.com/BlockdagNetworkLabs/bdag/services/common"
	"github.com/BlockdagNetworkLabs/bdag/version"
	"github.com/urfave/cli/v2"
)

func main() {
	runtime.GOMAXPROCS(runtime.NumCPU())
	debug.SetGCPercent(20)
	if err := relayNode(); err != nil {
		log.Error(err.Error())
		os.Exit(1)
	}
}

func relayNode() error {
	node := &Node{}
	app := &cli.App{
		Name:     "RelayNode",
		Version:  version.String(),
		Compiled: roughtime.Now(),
		Authors: []*cli.Author{
			&cli.Author{
				Name: "BDAG",
			},
		},
		Usage:                "Relay Node",
		Flags:                config.AppFlags,
		EnableBashCompletion: true,
		Commands:             commands(),
		Action: func(c *cli.Context) error {
			err := node.init(config.Conf)
			if err != nil {
				return err
			}
			err = node.Start()
			if err != nil {
				return err
			}
			return node.Stop()
		},
	}

	return app.Run(os.Args)
}
