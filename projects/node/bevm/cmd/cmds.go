package cmd

import (
	"fmt"

	"github.com/BlockdagNetworkLabs/bdag/bevm/bdag"
	"github.com/BlockdagNetworkLabs/bdag/bevm/eth"
	"github.com/BlockdagNetworkLabs/bdag/config"
	"github.com/ethereum/go-ethereum/cmd/utils"
	"github.com/ethereum/go-ethereum/ethdb"
	"github.com/ethereum/go-ethereum/log"
	"github.com/ethereum/go-ethereum/node"
	"github.com/urfave/cli/v2"
)

var (
	Commands = []*cli.Command{
		removedbCommand,
		// See accountcmd.go:
		accountCommand,
		walletCommand,
		// See consolecmd.go:
		attachCommand,
		// see dbcmd.go
		dbCommand,
		// See cmd/utils/flags_legacy.go
		utils.ShowDeprecated,
		// See snapshot.go
		snapshotCommand,
	}
)

func makeConfigNode(ctx *cli.Context, cfg *config.Config) (*node.Node, *eth.Config) {
	eth.InitLog(cfg.DebugLevel, cfg.DebugPrintOrigins)
	//
	var ecfg *eth.Config
	var args []string
	var err error

	ecfg, args, err = bdag.MakeParams(cfg)
	if err != nil {
		log.Error(err.Error())
		return nil, nil
	}

	var n *node.Node
	n, _, err = eth.MakeNakedNode(ecfg, args)
	if err != nil {
		log.Error(err.Error())
		return nil, nil
	}
	return n, ecfg
}

func makeConfig(cfg *config.Config) (*eth.Config, error) {
	var ecfg *eth.Config
	var err error

	ecfg, err = bdag.MakeConfig(cfg)
	if err != nil {
		log.Error(err.Error())
		return nil, nil
	}

	return ecfg, nil
}

// OpenEVMChainDB opens the embedded go-ethereum chain database (the
// "bdageth" data directory) in read-only or read-write mode without
// starting any node services. It is intended for offline tooling
// (snapshot export/import, state inspection, etc.) that needs raw
// access to EVM state while the node itself is stopped.
//
// The returned closer MUST be called; it closes the DB and the
// underlying node stack in the correct order. Calling it is safe even
// if an error propagates out of the caller.
func OpenEVMChainDB(cfg *config.Config, readonly bool) (ethdb.Database, func(), error) {
	eth.InitLog(cfg.DebugLevel, cfg.DebugPrintOrigins)

	ecfg, args, err := bdag.MakeParams(cfg)
	if err != nil {
		return nil, nil, fmt.Errorf("bevm: build eth config: %w", err)
	}

	stack, cctx, err := eth.MakeNakedNode(ecfg, args)
	if err != nil {
		return nil, nil, fmt.Errorf("bevm: create node stack: %w", err)
	}
	db := utils.MakeChainDatabase(cctx, stack, readonly)

	closer := func() {
		// Close DB first so file locks are released before the node
		// tears down the data directory registry.
		if db != nil {
			_ = db.Close()
		}
		if stack != nil {
			_ = stack.Close()
		}
	}
	return db, closer, nil
}
