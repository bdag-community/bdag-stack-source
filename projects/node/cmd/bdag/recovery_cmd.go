package main

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	evmcmd "github.com/BlockdagNetworkLabs/bdag/bevm/cmd"
	evmparams "github.com/BlockdagNetworkLabs/bdag/bevm/params"
	"github.com/BlockdagNetworkLabs/bdag/common/system"
	"github.com/BlockdagNetworkLabs/bdag/config"
	"github.com/BlockdagNetworkLabs/bdag/consensus"
	"github.com/BlockdagNetworkLabs/bdag/core/blockchain"
	"github.com/BlockdagNetworkLabs/bdag/core/types"
	"github.com/BlockdagNetworkLabs/bdag/database/chaindb"
	"github.com/BlockdagNetworkLabs/bdag/log"
	"github.com/BlockdagNetworkLabs/bdag/version"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/urfave/cli/v2"
)

const defaultRecoveryEVMRPC = "https://rpc.blockdag.engineering"

func recoveryCmd() *cli.Command {
	var (
		anchorEVMNumber uint64 = evmparams.BdagPredeployMigrationBlockNumber
		targetOrder     uint64
		commitEvery     uint64 = 1_000
		progressEvery   uint64 = 10_000
		rpcURL          string = defaultRecoveryEVMRPC
		rpcFetchDelay   uint64 = 50 // milliseconds between successful RPC fetches
	)
	return &cli.Command{
		Name:        "recovery",
		Category:    "Recovery",
		Usage:       "offline recovery operations",
		Description: "Offline recovery operations",
		Subcommands: []*cli.Command{
			{
				Name:        "evm-rebuild",
				Usage:       "replay commitment-verified native blocks through the current EVM rules",
				Description: "Starts at an existing stateful EVM anchor and replays only native blocks whose immutable header StateRoot commits to the locally executed EVM result. Legacy uncommitted replay is forensic-only and requires an explicit override.",
				Flags: []cli.Flag{
					&cli.Uint64Flag{
						Name:        "anchor-evm-number",
						Usage:       "EVM block number to use as the trusted replay anchor",
						Value:       evmparams.BdagPredeployMigrationBlockNumber,
						Destination: &anchorEVMNumber,
					},
					&cli.Uint64Flag{
						Name:        "target-order",
						Usage:       "native order to rebuild through; 0 means current native main tip",
						Destination: &targetOrder,
					},
					&cli.Uint64Flag{
						Name:        "commit-every",
						Usage:       "native orders between DAG state commits",
						Value:       1_000,
						Destination: &commitEvery,
					},
					&cli.Uint64Flag{
						Name:        "progress-every",
						Usage:       "native orders between progress log lines",
						Value:       10_000,
						Destination: &progressEvery,
					},
					&cli.BoolFlag{
						Name:  "allow-uncommitted-native-replay",
						Usage: "permit legacy replay without immutable EVM StateRoot commitments; only for forensic scratch analysis",
					},
					&cli.BoolFlag{
						Name:  "allow-non-scratch",
						Usage: "permit mutating a datadir that is not under a scratch path",
					},
				},
				Action: func(ctx *cli.Context) error {
					return runEVMNativeRebuild(anchorEVMNumber, targetOrder, commitEvery, progressEvery, ctx.Bool("allow-uncommitted-native-replay"), ctx.Bool("allow-non-scratch"))
				},
			},
			{
				Name:        "evm-import",
				Usage:       "import canonical EVM blocks from RPC and rewrite native EVM state metadata",
				Description: "Rewinds EVM to a trusted anchor, fetches canonical EVM blocks from an upstream RPC, inserts them locally, and refreshes native block state EVM hashes/roots.",
				Flags: []cli.Flag{
					&cli.Uint64Flag{
						Name:        "anchor-evm-number",
						Usage:       "EVM block number to use as the trusted import anchor",
						Value:       evmparams.BdagPredeployMigrationBlockNumber,
						Destination: &anchorEVMNumber,
					},
					&cli.Uint64Flag{
						Name:        "target-order",
						Usage:       "native order to import through; 0 means current native main tip",
						Destination: &targetOrder,
					},
					&cli.StringFlag{
						Name:        "rpc-url",
						Usage:       "upstream canonical EVM JSON-RPC endpoint",
						Value:       defaultRecoveryEVMRPC,
						Destination: &rpcURL,
					},
					&cli.Uint64Flag{
						Name:        "commit-every",
						Usage:       "native orders between DAG state commits",
						Value:       1_000,
						Destination: &commitEvery,
					},
					&cli.Uint64Flag{
						Name:        "progress-every",
						Usage:       "native orders between progress log lines",
						Value:       10_000,
						Destination: &progressEvery,
					},
					&cli.Uint64Flag{
						Name:        "rpc-fetch-delay-ms",
						Usage:       "delay between successful upstream RPC block fetches",
						Value:       50,
						Destination: &rpcFetchDelay,
					},
					&cli.BoolFlag{
						Name:  "allow-non-scratch",
						Usage: "permit mutating a datadir that is not under a scratch path",
					},
				},
				Action: func(ctx *cli.Context) error {
					return runEVMRPCImport(anchorEVMNumber, targetOrder, rpcURL, commitEvery, progressEvery, rpcFetchDelay, ctx.Bool("allow-non-scratch"))
				},
			},
			{
				Name:        "probe-block",
				Usage:       "decode a native block and compare buildBlock commitment field-by-field",
				Description: "Offline diagnostic: loads a serialized native block (hex or file) and prints native header fields, buildBlock EVM template, and commitment inputs.",
				Flags: []cli.Flag{
					&cli.StringFlag{
						Name:  "block-hex",
						Usage: "serialized native block bytes as hex",
					},
					&cli.StringFlag{
						Name:  "block-file",
						Usage: "path to serialized native block bytes",
					},
				},
				Action: func(ctx *cli.Context) error {
					return runBlockProbe(ctx.String("block-hex"), ctx.String("block-file"))
				},
			},
		},
	}
}

type recoveryChain struct {
	bc    *blockchain.BlockChain
	close func()
}

func openRecoveryChain() (*recoveryChain, error) {
	cfg := config.Cfg
	interrupt := system.InterruptListener()
	log.Info("System info", "BDAG Version", version.String(), "Go version", runtime.Version())
	log.Info("System info", "Home dir", cfg.HomeDir)
	if cfg.NoFileLogging {
		log.Info("File logging disabled")
	}

	if err := writeRecoveryEVMChainConfig(cfg); err != nil {
		return nil, err
	}

	db, err := chaindb.Open(cfg, interrupt)
	if err != nil {
		return nil, err
	}

	cfg.InvalidTxIndex = false
	cfg.AddrIndex = false
	cons := consensus.New(cfg, db, interrupt, make(chan struct{}))
	if err := cons.Init(); err != nil {
		db.Close()
		return nil, err
	}

	bc, ok := cons.BlockChain().(*blockchain.BlockChain)
	if !ok {
		db.Close()
		return nil, fmt.Errorf("unexpected blockchain implementation %T", cons.BlockChain())
	}
	bc.EVMChain().DisablePoolSubscriptionsForRecovery()

	return &recoveryChain{
		bc: bc,
		close: func() {
			if err := bc.EVMChain().Stop(); err != nil {
				log.Warn("EVM chain stop after recovery failed", "err", err)
			}
			db.Close()
			if log.LogWrite() != nil {
				log.LogWrite().Close()
			}
		},
	}, nil
}

func runEVMNativeRebuild(anchorEVMNumber, targetOrder, commitEvery, progressEvery uint64, allowUncommittedNativeReplay, allowNonScratch bool) error {
	if anchorEVMNumber == 0 {
		return fmt.Errorf("anchor-evm-number is required")
	}
	if err := validateRecoveryScratchDatadir(allowNonScratch); err != nil {
		return err
	}
	rc, err := openRecoveryChain()
	if err != nil {
		return err
	}
	defer rc.close()

	if anchor := rc.bc.GetBlockByNumber(anchorEVMNumber); anchor != nil && anchor.GetState() != nil {
		log.Info("EVM native rebuild anchor",
			"evmNumber", anchorEVMNumber,
			"evmHash", anchor.GetState().GetEVMHash(),
			"evmRoot", anchor.GetState().GetEVMRoot())
	}

	result, err := rc.bc.RebuildEVMNativeState(blockchain.EVMNativeRebuildOptions{
		AnchorEVMNumber:        anchorEVMNumber,
		TargetOrder:            targetOrder,
		CommitEvery:            commitEvery,
		ProgressEvery:          progressEvery,
		RequireStateCommitment: !allowUncommittedNativeReplay,
	})
	logEVMNativeRebuildResult(result)
	return err
}

func runEVMRPCImport(anchorEVMNumber, targetOrder uint64, rpcURL string, commitEvery, progressEvery, rpcFetchDelayMs uint64, allowNonScratch bool) error {
	if anchorEVMNumber == 0 {
		return fmt.Errorf("anchor-evm-number is required")
	}
	if rpcURL == "" {
		return fmt.Errorf("rpc-url is required")
	}
	if err := validateRecoveryScratchDatadir(allowNonScratch); err != nil {
		return err
	}
	rc, err := openRecoveryChain()
	if err != nil {
		return err
	}
	defer rc.close()

	if anchor := rc.bc.GetBlockByNumber(anchorEVMNumber); anchor != nil && anchor.GetState() != nil {
		log.Info("EVM RPC import anchor",
			"evmNumber", anchorEVMNumber,
			"evmHash", anchor.GetState().GetEVMHash(),
			"evmRoot", anchor.GetState().GetEVMRoot(),
			"rpcURL", rpcURL)
	}

	result, err := rc.bc.ImportEVMFromRPC(blockchain.EVMRPCImportOptions{
		AnchorEVMNumber: anchorEVMNumber,
		TargetOrder:     targetOrder,
		RPCURL:          rpcURL,
		CommitEvery:     commitEvery,
		ProgressEvery:   progressEvery,
		RPCFetchDelay:   time.Duration(rpcFetchDelayMs) * time.Millisecond,
	})
	if result != nil {
		log.Info("EVM RPC import result",
			"anchorOrder", result.AnchorOrder,
			"startOrder", result.StartOrder,
			"targetOrder", result.TargetOrder,
			"mainTipOrder", result.MainTipOrder,
			"processed", result.Processed,
			"imported", result.Imported,
			"skippedExisting", result.SkippedExisting,
			"skippedInvalid", result.SkippedInvalid,
			"skippedNoAdvance", result.SkippedNoAdvance,
			"finalEVMNumber", result.FinalEVMNumber,
			"finalEVMHash", result.FinalEVMHash.String(),
			"finalEVMRoot", result.FinalEVMRoot.String(),
			"safeCheckpointKnown", result.SafeCheckpointKnown,
			"safeCheckpointNumber", result.SafeCheckpointNumber,
			"safeCheckpointHash", result.SafeCheckpointHash.String(),
			"safeCheckpointRoot", result.SafeCheckpointRoot.String())
	}
	return err
}

func validateEVMRecoveryScratchPath(chaindata string, allowNonScratch bool) error {
	if _, err := os.Stat(chaindata); err != nil {
		return fmt.Errorf("EVM chaindata path %s is unavailable: %w", chaindata, err)
	}
	if allowNonScratch {
		return nil
	}
	clean, err := filepath.Abs(chaindata)
	if err != nil {
		return err
	}
	if resolved, err := filepath.EvalSymlinks(clean); err == nil {
		clean = resolved
	}
	for _, part := range strings.Split(filepath.Clean(clean), string(os.PathSeparator)) {
		if part == "scratch" {
			return nil
		}
	}
	return fmt.Errorf("refusing to mutate non-scratch EVM chaindata %s; copy the dataset under a scratch directory or pass --allow-non-scratch", clean)
}

func validateRecoveryScratchDatadir(allowNonScratch bool) error {
	cfg := config.Cfg
	if cfg == nil {
		return fmt.Errorf("config is unavailable")
	}
	return validateEVMRecoveryScratchPath(filepath.Join(cfg.DataDir, "bdageth", "chaindata"), allowNonScratch)
}

func logEVMNativeRebuildResult(result *blockchain.EVMNativeRebuildResult) {
	if result == nil {
		return
	}
	log.Info("EVM native rebuild result",
		"anchorOrder", result.AnchorOrder,
		"startOrder", result.StartOrder,
		"targetOrder", result.TargetOrder,
		"mainTipOrder", result.MainTipOrder,
		"processed", result.Processed,
		"replayed", result.Replayed,
		"skippedInvalid", result.SkippedInvalid,
		"skippedNoAdvance", result.SkippedNoAdvance,
		"finalEVMNumber", result.FinalEVMNumber,
		"finalEVMHash", result.FinalEVMHash.String(),
		"finalEVMRoot", result.FinalEVMRoot.String(),
		"safeCheckpointKnown", result.SafeCheckpointKnown,
		"safeCheckpointNumber", result.SafeCheckpointNumber,
		"safeCheckpointHash", result.SafeCheckpointHash.String(),
		"safeCheckpointRoot", result.SafeCheckpointRoot.String())
}

func writeRecoveryEVMChainConfig(cfg *config.Config) error {
	db, closeDB, err := evmcmd.OpenEVMChainDB(cfg, false)
	if err != nil {
		return fmt.Errorf("open EVM DB to write recovery chain config: %w", err)
	}
	defer closeDB()

	genesisHash := rawdb.ReadCanonicalHash(db, 0)
	if genesisHash == (common.Hash{}) {
		return fmt.Errorf("EVM genesis hash is unavailable")
	}
	rawdb.WriteChainConfig(db, genesisHash, evmparams.BdagMainnetChainConfig)
	log.Info("Recorded recovery EVM chain config", "genesis", genesisHash.String())
	return nil
}

func runBlockProbe(blockHex, blockFile string) error {
	if blockHex == "" && blockFile == "" {
		return fmt.Errorf("one of --block-hex or --block-file is required")
	}
	var (
		block *types.SerializedBlock
		err   error
	)
	if blockFile != "" {
		block, err = blockchain.LoadProbeBlockFromFile(blockFile)
	} else {
		block, err = blockchain.LoadProbeBlockFromHex(blockHex)
	}
	if err != nil {
		return err
	}

	rc, err := openRecoveryChain()
	if err != nil {
		return err
	}
	defer rc.close()

	if err := rc.bc.EVMChain().Start(); err != nil {
		return fmt.Errorf("start EVM chain: %w", err)
	}

	return blockchain.ProbeEVMStateCommitment(rc.bc, block)
}
