package main

import (
	"runtime"

	"github.com/BlockdagNetworkLabs/bdag/bevm/bdag"
	"github.com/BlockdagNetworkLabs/bdag/bevm/cmd"
	"github.com/BlockdagNetworkLabs/bdag/common/system"
	"github.com/BlockdagNetworkLabs/bdag/config"
	"github.com/BlockdagNetworkLabs/bdag/consensus"
	"github.com/BlockdagNetworkLabs/bdag/database/chaindb"
	"github.com/BlockdagNetworkLabs/bdag/log"
	"github.com/BlockdagNetworkLabs/bdag/services/common"
	"github.com/BlockdagNetworkLabs/bdag/version"
	"github.com/urfave/cli/v2"
)

func commands() []*cli.Command {
	cmds := []*cli.Command{}
	cmds = append(cmds, indexCmd())
	cmds = append(cmds, consensusCmd())
	cmds = append(cmds, blockchainCmd())
	cmds = append(cmds, cmd.Commands...)
	cmds = append(cmds, dbCmd())
	cmds = append(cmds, recoveryCmd())
	// snap manages its own Before hooks at the leaf-subcommand level so
	// it can apply per-subcommand location overrides AFTER the
	// subcommand's flags have been parsed. The top-level snap command
	// must not run loadConfig itself, otherwise loadConfig would be
	// invoked twice (once here, once at the leaf) and side-effects like
	// appending the network name to DataDir would compound.
	snap := snapshotCmd()
	cmds = append(cmds, snap)
	dataset := datasetCmd()
	cmds = append(cmds, dataset)

	for _, cmd := range cmds {
		if cmd == snap || cmd == dataset {
			continue
		}
		cmd.Before = loadConfig
	}
	return cmds
}

func indexCmd() *cli.Command {
	return &cli.Command{
		Name:        "index",
		Aliases:     []string{"i"},
		Category:    "index",
		Usage:       "index manager",
		Description: "index manager",
		Subcommands: []*cli.Command{
			&cli.Command{
				Name:        "dropinvalidtxindex",
				Aliases:     []string{"di"},
				Usage:       "Deletes the invalid tx index from the database on start up and then exits",
				Description: "Deletes the invalid tx index from the database on start up and then exits",
				Action: func(ctx *cli.Context) error {
					cfg := config.Cfg
					defer func() {
						if log.LogWrite() != nil {
							log.LogWrite().Close()
						}
					}()
					interrupt := system.InterruptListener()
					log.Info("System info", "BDAG Version", version.String(), "Go version", runtime.Version())
					log.Info("System info", "Home dir", cfg.HomeDir)
					if cfg.NoFileLogging {
						log.Info("File logging disabled")
					}
				db, err := chaindb.Open(cfg, interrupt)
				if err != nil {
					log.Error("load block database", "error", err)
					return err
				}
				defer db.Close()

				return db.CleanInvalidTxIdx()
				},
			},
		},
		After: func(ctx *cli.Context) error {
			log.Info("Exit index command")
			return nil
		},
	}
}

func consensusCmd() *cli.Command {
	return &cli.Command{
		Name:        "consensus",
		Aliases:     []string{"c"},
		Category:    "consensus",
		Usage:       "consensus",
		Description: "consensus",
		Subcommands: []*cli.Command{
			&cli.Command{
				Name:        "rebuild",
				Aliases:     []string{"re"},
				Usage:       "rebuild consensus",
				Description: "rebuild consensus",
				Action: func(ctx *cli.Context) error {
					cfg := config.Cfg
					defer func() {
						if log.LogWrite() != nil {
							log.LogWrite().Close()
						}
					}()
					interrupt := system.InterruptListener()
					log.Info("System info", "BDAG Version", version.String(), "Go version", runtime.Version())
					log.Info("System info", "Home dir", cfg.HomeDir)
					if cfg.NoFileLogging {
						log.Info("File logging disabled")
					}
				db, err := chaindb.Open(cfg, interrupt)
				if err != nil {
					log.Error("load block database", "error", err)
					return err
				}
				defer db.Close()

				bdag.Cleanup(cfg)
					//
					cfg.InvalidTxIndex = false
					cfg.AddrIndex = false
					cons := consensus.New(cfg, db, interrupt, make(chan struct{}))
					err = cons.Init()
					if err != nil {
						log.Error(err.Error())
						return err
					}
					err = cons.BlockChain().Start()
					if err != nil {
						return err
					}
					return cons.Rebuild()
				},
			},
		},
	}
}

func loadConfig(ctx *cli.Context) error {
	_, err := common.LoadConfig(ctx, false)
	if err != nil {
		return err
	}
	return nil
}
