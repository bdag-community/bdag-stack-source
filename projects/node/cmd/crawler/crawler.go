package main

import (
	"os"

	"github.com/BlockdagNetworkLabs/bdag/cmd/crawler/config"
	"github.com/BlockdagNetworkLabs/bdag/cmd/crawler/log"
	"github.com/BlockdagNetworkLabs/bdag/cmd/crawler/node"
	"github.com/BlockdagNetworkLabs/bdag/common/roughtime"
	_ "github.com/BlockdagNetworkLabs/bdag/services/common"
	"github.com/urfave/cli/v2"
)

func main() {
	if err := crawlerNode(); err != nil {
		log.Log.Error(err.Error())
		os.Exit(1)
	}
}

func crawlerNode() error {
	n := &node.Node{}
	app := &cli.App{
		Name:     "CrawlerNode",
		Version:  "V0.0.1",
		Compiled: roughtime.Now(),
		Authors: []*cli.Author{
			&cli.Author{
				Name: "Blockdag",
			},
		},
		Usage:                "Crawler Node",
		Flags:                config.AppFlags,
		EnableBashCompletion: true,
		Before: func(c *cli.Context) error {
			return n.Init(config.Conf)
		},
		After: func(c *cli.Context) error {
			return n.Exit()
		},
		Action: func(c *cli.Context) error {
			return n.Run()
		},
	}

	return app.Run(os.Args)
}
