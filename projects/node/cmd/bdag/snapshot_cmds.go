package main

import (
	"compress/gzip"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	dag "github.com/BlockdagNetworkLabs/bdag/blockdag"
	"github.com/BlockdagNetworkLabs/bdag/common/util"
	"github.com/BlockdagNetworkLabs/bdag/config"
	"github.com/BlockdagNetworkLabs/bdag/core/blockchain"
	"github.com/BlockdagNetworkLabs/bdag/core/snapshot"
	"github.com/BlockdagNetworkLabs/bdag/core/state"
	"github.com/BlockdagNetworkLabs/bdag/database/chaindb"
	"github.com/BlockdagNetworkLabs/bdag/database/evmchaindb"
	bdagrawdb "github.com/BlockdagNetworkLabs/bdag/database/rawdb"
	"github.com/BlockdagNetworkLabs/bdag/log"
	"github.com/BlockdagNetworkLabs/bdag/params"
	bdagcommon "github.com/BlockdagNetworkLabs/bdag/services/common"
	"github.com/BlockdagNetworkLabs/bdag/version"
	"github.com/ethereum/go-ethereum/common"
	ethrawdb "github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/ethdb"
	"github.com/urfave/cli/v2"
)

// snapBefore is the Before hook used by every `bdag snap <subcmd>`
// invocation. It runs AFTER the subcommand's argv has been parsed, so
// any location-related flags the user passed on the subcommand
// (`--datadir`, `--testnet`, ...) are now visible via `c.IsSet`. The
// hook folds those overrides into the package-level cfg first, then
// runs the standard config sanitisation pass exactly once. Wiring it on
// every leaf — instead of on the snap parent — avoids the double-sanitise
// that would otherwise re-append the network name to DataDir.
//
// snapBefore differs from a plain `loadConfig` in one important way: a
// `bdag snap export --datadir /path/to/node/mainnet` invocation should
// open exactly `/path/to/node/mainnet/BdagChain`, NOT
// `/path/to/node/mainnet/<network>/BdagChain`. The standard
// `LoadConfig` always appends the active network name to `cfg.DataDir`
// because the regular node convention is "datadir is the parent of the
// per-network folder". For one-shot snapshot tooling that's
// counter-intuitive — the user types the actual data dir and expects
// it to be honoured verbatim. We therefore snapshot the user-provided
// `--datadir` BEFORE LoadConfig runs and restore it afterwards.
func snapBefore(c *cli.Context) error {
	bdagcommon.ApplyLocationOverrides(c)

	// Detect whether the user passed --datadir at ANY level (App,
	// snap, or leaf subcommand) and grab the literal value they typed
	// directly from the cli.Context. urfave/cli registers --datadir as
	// a separate flag instance per level so we walk the lineage to
	// also catch the `bdag --datadir /foo snap export` form.
	//
	// The value is read from the cli.Context (NOT config.Cfg) because
	// LoadConfig has not run yet — config.Cfg is still nil at this
	// point.
	hasUserDataDir := false
	userDataDir := ""
	for _, ctx := range c.Lineage() {
		if ctx.IsSet("datadir") {
			hasUserDataDir = true
			userDataDir = ctx.String("datadir")
			break
		}
	}

	if _, err := bdagcommon.LoadConfig(c, false); err != nil {
		return err
	}

	if hasUserDataDir {
		// Re-clean & expand (~ etc.) but SKIP the network-name
		// appending. For one-shot snapshot tooling the user types the
		// actual data dir; the standard "datadir is the parent of the
		// per-network folder" convention only makes sense for the
		// long-running node binary.
		config.Cfg.DataDir = util.CleanAndExpandPath(userDataDir)
	}
	return nil
}

// snapshotCmd wires up the `bdag snapshot` command tree. Snapshots give
// operators a way to bootstrap a fresh node from a trusted archive
// instead of re-executing every block from genesis. The file format and
// integrity contract live in the core/snapshot package.
func snapshotCmd() *cli.Command {
	var (
		outputPath       string
		inputPath        string
		chunkTarget      int
		gzipLevel        int
		importBatchBytes int
		requireNet       bool
		skipEVM          bool
	)
	return &cli.Command{
		Name:     "snapshot",
		Aliases:  []string{"snap"},
		Category: "Snapshot",
		Usage:    "Create and apply chain-db snapshots for fast node bootstrap",
		Description: `A snapshot is a chunked, hash-verified archive of the chain database at a given tip. It lets a fresh node catch up without replaying every block.

Location and network flags (--datadir, --testnet, --devnet, ...) can be passed either on the bdag binary itself (before the subcommand) or on the snap subcommands directly. Both forms are accepted:

  bdag --datadir /var/lib/bdag snap export --path /tmp/snap.bdsnap
  bdag snap export --datadir /var/lib/bdag --path /tmp/snap.bdsnap`,
		Subcommands: []*cli.Command{
			{
				Name:    "export",
				Aliases: []string{"e"},
				Usage:   "Export the chain database into a snapshot archive",
				Description: `Exports the current chain DB into a snapshot archive at the given
path. The node MUST be stopped before running this command so the key/
value iterator captures a point-in-time view.

--datadir must be the node's network data directory (the folder that
directly contains BdagChain/), e.g. .../node/mainnet — not a parent like
.../bdagStack alone.

A side-car JSON manifest is written next to the archive for peers that
want to inspect snapshot metadata without downloading the archive
itself.`,
				Before: snapBefore,
				Flags: append(bdagcommon.LocationFlags(),
					&cli.StringFlag{
						Name:        "path",
						Aliases:     []string{"p"},
						Usage:       "Output file path (defaults to <datadir>/snapshot.bdsnap)",
						Destination: &outputPath,
					},
					&cli.IntFlag{
						Name:        "chunk-size",
						Aliases:     []string{"c"},
						Usage:       "Soft chunk size in bytes (uncompressed)",
						Value:       snapshot.DefaultChunkTargetBytes,
						Destination: &chunkTarget,
					},
					&cli.BoolFlag{
						Name:        "skip-evm",
						Usage:       "Do not include the EVM state DB in the snapshot (chain-only)",
						Destination: &skipEVM,
					},
					&cli.IntFlag{
						Name:        "gzip-level",
						Usage:       "Export compression: -1 for default, or 1 (fastest) through 9 (smallest)",
						Value:       gzip.DefaultCompression,
						Destination: &gzipLevel,
					},
				),
				Action: func(ctx *cli.Context) error {
					if err := validateSnapshotGzipLevel(gzipLevel); err != nil {
						return err
					}
					cfg := config.Cfg
					chdb, err := chaindb.NewForExport(cfg)
					if err != nil {
						if errors.Is(err, chaindb.ErrDBAbsent) {
							return fmt.Errorf("open chaindb: %w — need existing chain at %s (wrong --datadir? use the path that contains BdagChain, e.g. .../node/mainnet)", err, filepath.Join(cfg.DataDir, chaindb.DBDirectoryName))
						}
						return fmt.Errorf("open chaindb: %w", err)
					}
					defer chdb.Close()

					if outputPath == "" {
						outputPath = filepath.Join(cfg.DataDir, "snapshot"+snapshot.ArchiveFileSuffix)
					}
					info, err := buildManifestInfo(cfg, chdb)
					if err != nil {
						return err
					}

					sources := []snapshot.Source{
						{Name: snapshot.SourceChain, DB: chdb.DB()},
					}

					// Open the EVM chain DB side-by-side. A full
					// bootstrap snapshot MUST include it, otherwise a
					// freshly-imported node would still have to replay
					// every block to rebuild the EVM state trie — which
					// defeats the whole point of a snapshot.
					//
					// We use database/evmchaindb here (a thin wrapper
					// around upstream geth's rawdb) instead of
					// bevm/cmd.OpenEVMChainDB so the snapshot path does
					// not bring up a full geth node stack just to read
					// data. The lighter open avoids unwanted side
					// effects (keystore dir creation, listener init,
					// etc.) and uses the correct geth chain-freezer
					// schema (headers/hashes/bodies/receipts/diffs).
					if !skipEVM {
						evmDB, err := evmchaindb.Open(cfg.DataDir, evmchaindb.Options{ReadOnly: true})
						if err != nil {
							return fmt.Errorf("open evm chaindb: %w", err)
						}
						defer evmDB.Close()
						sources = append(sources, snapshot.Source{
							Name: snapshot.SourceEVM,
							DB:   evmDB,
						})
						// Enrich the manifest with the EVM head
						// information we can read for free from the
						// already-open DB. This lets peers sanity-check
						// a snapshot before downloading any chunks.
						if head := ethrawdb.ReadHeadHeaderHash(evmDB); head != (common.Hash{}) {
							info.StateRoot = resolveEVMStateRoot(evmDB, head, info.StateRoot)
						}
					}

					m, err := snapshot.MultiWrite(outputPath, sources, info, snapshot.WriteOptions{
						ChunkTargetBytes: chunkTarget,
						CompressionLevel: gzipLevel,
					})
					if err != nil {
						return err
					}
					// Always emit the side-car manifest so RPC endpoints
					// and http mirrors can serve it without re-reading
					// the archive.
					sidecar := outputPath + snapshot.ManifestFileSuffix
					if err := snapshot.WriteManifestFile(sidecar, m); err != nil {
						return fmt.Errorf("write manifest sidecar: %w", err)
					}
					srcSummary := make([]string, 0, len(m.Sources))
					for _, s := range m.Sources {
						srcSummary = append(srcSummary, fmt.Sprintf("%s=%d/%dMB",
							s.Name, s.TotalKeys, s.TotalCompressed/1024/1024))
					}
					log.Info("Snapshot export complete",
						"archive", outputPath,
						"manifest", sidecar,
						"tip", m.TipHash,
						"order", m.TipOrder,
						"chunks", len(m.Chunks),
						"sources", srcSummary,
						"root", m.RootHash,
					)
					return nil
				},
			},
			{
				Name:    "import",
				Aliases: []string{"i"},
				Usage:   "Import a snapshot archive into an empty chain database",
				Description: `Loads a snapshot archive into the configured data directory. The
destination chain database MUST be empty — run a cleanup first if you
are re-importing. The archive's manifest network is validated against
the active network params before any data is written.`,
				Before: snapBefore,
				Flags: append(bdagcommon.LocationFlags(),
					&cli.StringFlag{
						Name:        "path",
						Aliases:     []string{"p"},
						Usage:       "Input snapshot archive path",
						Destination: &inputPath,
						Required:    true,
					},
					&cli.BoolFlag{
						Name:        "no-network-check",
						Usage:       "Skip the manifest<->active network cross-check (expert)",
						Destination: &requireNet,
					},
					&cli.IntFlag{
						Name:        "batch-size",
						Usage:       "Maximum database batch size in bytes during import (lower uses less memory)",
						Value:       16 * 1024 * 1024,
						Destination: &importBatchBytes,
					},
				),
				Action: func(ctx *cli.Context) error {
					cfg := config.Cfg
					// Peek at the manifest first so we know which
					// sources the archive advertises and can open just
					// the destinations we actually need. This avoids
					// creating an empty EVM chaindata dir when the
					// operator is applying a chain-only snapshot.
					m0, err := snapshot.OpenManifest(inputPath)
					if err != nil {
						return fmt.Errorf("peek manifest: %w", err)
					}

					chdb, err := chaindb.NewForSnapshotImport(cfg)
					if err != nil {
						return fmt.Errorf("open chaindb: %w", err)
					}
					defer chdb.Close()

					dests := map[string]ethdb.Database{
						snapshot.SourceChain: chdb.DB(),
					}

					// Only open the EVM chain DB if the archive
					// actually carries EVM state. database/evmchaindb
					// (thin wrapper around upstream geth's rawdb) is
					// used here instead of bevm/cmd.OpenEVMChainDB so
					// import does not need to instantiate a full geth
					// node stack — that would create a keystore dir
					// and other unwanted artefacts on a clean import
					// destination.
					for _, s := range m0.EffectiveSources() {
						if s.Name == snapshot.SourceEVM {
							evmDB, err := evmchaindb.Open(cfg.DataDir, evmchaindb.Options{
								ReadOnly:             false,
								SkipBackgroundFreeze: true,
							})
							if err != nil {
								return fmt.Errorf("open evm chaindb: %w", err)
							}
							defer evmDB.Close()
							dests[snapshot.SourceEVM] = evmDB
							break
						}
					}

					network := params.ActiveNetParams.Name
					if requireNet {
						network = ""
					}
					m, err := snapshot.MultiRead(inputPath, dests, snapshot.ReadOptions{
						Network:      network,
						RequireEmpty: true,
						BatchBytes:   importBatchBytes,
					})
					if err != nil {
						return err
					}
					srcSummary := make([]string, 0, len(m.Sources))
					for _, s := range m.Sources {
						srcSummary = append(srcSummary, fmt.Sprintf("%s=%d", s.Name, s.TotalKeys))
					}
					log.Info("Snapshot import complete",
						"tip", m.TipHash,
						"order", m.TipOrder,
						"id", m.TipID,
						"keys", m.TotalKeys,
						"sources", srcSummary,
					)
					return nil
				},
			},
			{
				Name:    "verify",
				Aliases: []string{"v"},
				Usage:   "Verify a snapshot archive without writing to any database",
				Description: `Reads every chunk in the archive and checks that each chunk's sha256
matches the manifest. Does not touch any database. Useful when
distributing a snapshot to peers: run verify on both ends.`,
				Before: snapBefore,
				Flags: []cli.Flag{
					&cli.StringFlag{
						Name:        "path",
						Aliases:     []string{"p"},
						Usage:       "Archive path",
						Destination: &inputPath,
						Required:    true,
					},
				},
				Action: func(ctx *cli.Context) error {
					m, err := snapshot.Verify(inputPath)
					if err != nil {
						return err
					}
					log.Info("Snapshot archive OK",
						"tip", m.TipHash,
						"order", m.TipOrder,
						"chunks", len(m.Chunks),
						"keys", m.TotalKeys,
						"root", m.RootHash,
					)
					return nil
				},
			},
			{
				Name:    "info",
				Aliases: []string{"in"},
				Usage:   "Print the manifest of a snapshot archive or side-car",
				Description: `Decodes just the manifest from either an archive (.bdsnap) or a
stand-alone manifest JSON file and prints it to stdout. Does not read
any chunk data.`,
				Before: snapBefore,
				Flags: []cli.Flag{
					&cli.StringFlag{
						Name:        "path",
						Aliases:     []string{"p"},
						Usage:       "Archive or manifest path",
						Destination: &inputPath,
						Required:    true,
					},
				},
				Action: func(ctx *cli.Context) error {
					m, err := loadManifestFromPath(inputPath)
					if err != nil {
						return err
					}
					out, err := m.MarshalIndented()
					if err != nil {
						return err
					}
					fmt.Println(string(out))
					return nil
				},
			},
		},
	}
}

func validateSnapshotGzipLevel(level int) error {
	if level != gzip.DefaultCompression && (level < gzip.BestSpeed || level > gzip.BestCompression) {
		return fmt.Errorf("snapshot: gzip-level must be -1 or between 1 and 9")
	}
	return nil
}

// buildManifestInfo assembles the chain-state anchor data that gets baked
// into the snapshot manifest. The chain is opened read-only and closed
// before returning so the core exporter has exclusive access to the DB
// handle for iteration.
func buildManifestInfo(cfg *config.Config, db *chaindb.ChainDB) (snapshot.BuildInfo, error) {
	info := snapshot.BuildInfo{
		Network:     params.ActiveNetParams.Name,
		BDAGVersion: version.String(),
		DBEngine:    db.DBEngine(),
	}
	// Pull the chain tip out of the best-chain-state blob. We avoid
	// spinning up a full consensus engine here because the export is a
	// cold-db operation; the extra machinery would slow us down and
	// risk mutating the DB we're trying to snapshot.
	serialized, err := db.GetBestChainState()
	if err != nil {
		return info, fmt.Errorf("read best chain state: %w", err)
	}
	if len(serialized) == 0 {
		chainPath := filepath.Join(cfg.DataDir, chaindb.DBDirectoryName)
		return info, fmt.Errorf("chain state is empty; nothing to snapshot under datadir %q (missing best-chain metadata). "+
			"If you meant to export a running node, pass --datadir as the directory that directly contains %q (pool-stack default: /var/lib/bdagStack/node/mainnet). "+
			"Wrong paths can leave an empty database under %q — delete that folder if it was created by mistake",
			cfg.DataDir, chaindb.DBDirectoryName, chainPath)
	}
	state, err := blockchain.DeserializeBestChainState(serialized)
	if err != nil {
		return info, fmt.Errorf("decode best chain state: %w", err)
	}
	tipHash, err := blockchain.BestChainStateHash(serialized)
	if err != nil {
		return info, fmt.Errorf("decode tip hash: %w", err)
	}
	tipOrder, tipID, err := snapshotNativeAnchor(db.DB(), tipHash, state.GetTotal())
	if err != nil {
		return info, err
	}
	info.TipHash = tipHash.String()
	info.TipOrder = tipOrder
	info.BlockTotal = state.GetTotal()
	info.TipID = tipID
	return info, nil
}

// snapshotNativeAnchor reads only durable native metadata. DAG total, block ID
// and main-chain order are different counters and must never substitute for one
// another, including when an old or incomplete database lacks an index entry.
func snapshotNativeAnchor(db ethdb.Database, bestHash common.Hash, total uint64) (uint64, uint64, error) {
	dag.SetBlockStateFactory(state.CreateBlockState, state.CreateBlockStateFromBytes)
	tipID := bdagrawdb.ReadMainChainTip(db)
	if tipID == nil {
		return 0, 0, fmt.Errorf("snapshot: designated native main tip is unavailable")
	}
	tip := bdagrawdb.ReadDAGBlock(db, *tipID)
	if tip == nil || tip.GetState() == nil || tip.GetHash() == nil {
		return 0, 0, fmt.Errorf("snapshot: native main tip %d or its state is unavailable", *tipID)
	}
	if *tip.GetHash() != bestHash {
		return 0, 0, fmt.Errorf("snapshot: native main tip does not match best-chain hash")
	}
	order := uint64(tip.GetOrder())
	if *tipID >= total || order >= total {
		return 0, 0, fmt.Errorf("snapshot: native main tip ID/order is outside DAG total %d", total)
	}
	hashID := bdagrawdb.ReadBlockID(db, &bestHash)
	if hashID == nil || *hashID != *tipID {
		return 0, 0, fmt.Errorf("snapshot: native main tip hash index is unavailable or inconsistent")
	}
	orderID := bdagrawdb.ReadBlockOrderSnapshot(db, order)
	if orderID == nil || *orderID != *tipID {
		return 0, 0, fmt.Errorf("snapshot: native main tip order index is unavailable or inconsistent")
	}
	return order, *tipID, nil
}

// resolveEVMStateRoot returns the EVM state root reachable from the
// given head header hash, falling back to the provided defaultRoot if
// anything goes wrong. We read directly from rawdb rather than opening
// a full state trie because the exporter runs against a cold DB and we
// want to keep the hot path side-effect free.
func resolveEVMStateRoot(db ethdb.Database, head common.Hash, defaultRoot string) string {
	num := ethrawdb.ReadHeaderNumber(db, head)
	if num == nil {
		return defaultRoot
	}
	hdr := ethrawdb.ReadHeader(db, head, *num)
	if hdr == nil {
		return defaultRoot
	}
	return hdr.Root.Hex()
}

// loadManifestFromPath transparently handles both archive files and
// side-car JSON manifests. We detect the file type by sniffing the
// magic header so callers don't have to juggle two flags.
func loadManifestFromPath(path string) (*snapshot.Manifest, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	magic := make([]byte, len(snapshot.Magic))
	n, _ := f.Read(magic)
	_ = f.Close()
	if n == len(snapshot.Magic) && string(magic) == snapshot.Magic {
		return snapshot.OpenManifest(path)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	m := &snapshot.Manifest{}
	if err := json.Unmarshal(raw, m); err != nil {
		return nil, fmt.Errorf("parse manifest json: %w", err)
	}
	return m, nil
}
