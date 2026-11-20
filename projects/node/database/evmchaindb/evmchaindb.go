// Package evmchaindb opens the embedded go-ethereum chain database that
// lives alongside the BDAG chain DB.
//
// It exists so that offline tooling (snapshot export/import, state inspection,
// migrations, …) can access EVM chain data without spinning up a full
// go-ethereum node stack — which is what bevm/cmd.OpenEVMChainDB currently
// does and which has unwanted side-effects (keystore dir creation, listener
// initialisation, …) and a heavy dependency tail.
//
// Critically, Open uses the upstream go-ethereum rawdb (not the
// blockdag-corechain fork in `database/rawdb`) because the embedded EVM
// chain freezer uses geth's standard table schema (headers, hashes,
// bodies, receipts, diffs). The BDAG fork of rawdb is for the BDAG
// chain DB and uses a different schema (headers, blocks, dagblocks);
// pointing it at the EVM ancients directory raises confusing
// "blocks.cidx not found" errors.
package evmchaindb

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/ethdb"
)

// Layout constants. NodeName matches bevm.ClientIdentifier (which is the
// `node.Config.Name` the embedded go-ethereum stack runs with) and
// ChainDataName is the conventional geth chaindata directory name. They
// are kept here as local constants so this package has no dependency on
// bevm — the whole point of this package is to be importable by tools
// that can't pull in the bevm tree.
const (
	NodeName      = "bdageth"
	ChainDataName = "chaindata"
	AncientName   = "ancient"
)

// Path returns the absolute filesystem path where the EVM chain DB lives,
// given a BDAG node data directory.
func Path(dataDir string) string {
	return filepath.Join(dataDir, NodeName, ChainDataName)
}

// AncientsPath returns the absolute filesystem path of the EVM chain
// freezer (ancients) directory.
func AncientsPath(dataDir string) string {
	return filepath.Join(Path(dataDir), AncientName)
}

// Options tunes Open's behaviour. The zero value is the conservative
// default used by snapshot tooling: read-only, modest cache, modest file
// handle limit. See the field docs for what each value actually controls.
type Options struct {
	// ReadOnly opens the DB in read-only mode. A read-only open will
	// refuse to start if the DB has any unfinished compactions or write
	// state, so callers exporting snapshots SHOULD ensure the node is
	// stopped first.
	ReadOnly bool

	// Type forces a specific KV engine ("pebble" or "leveldb"). When
	// empty, the engine is auto-detected from the on-disk directory; if
	// the directory is missing, the open will fail.
	Type string

	// Cache is the cache size in megabytes. Zero defaults to a value
	// that's appropriate for offline tooling (128 MB).
	Cache int

	// Handles is the file-handle budget. Zero defaults to 512 which is
	// well below typical OS limits and plenty for read-only iteration.
	Handles int

	// Namespace is passed through to rawdb so metrics emitted by the DB
	// don't collide with other databases the caller might also have
	// open. Defaults to "evmchaindb/".
	Namespace string

	// Ephemeral disables fsync. For tooling that opens a fresh DB and
	// throws it away (tests, dry-runs); never set this for a real node
	// data directory.
	Ephemeral bool

	// SkipBackgroundFreeze passes through to geth rawdb.Open: do not start
	// the automatic chain freezer loop. Required for snapshot import so
	// bulk KV + ancient writes are not raced by background freezing.
	SkipBackgroundFreeze bool
}

// Open opens the EVM chain database (with its freezer) at the conventional
// path under dataDir. It returns the same kind of `ethdb.Database` that a
// running geth node would hand out, so it can be passed straight into
// rawdb accessors, snapshot.MultiWrite, etc.
func Open(dataDir string, opts Options) (ethdb.Database, error) {
	if dataDir == "" {
		return nil, fmt.Errorf("evmchaindb: empty data dir")
	}
	dir := Path(dataDir)
	ancient := AncientsPath(dataDir)

	if opts.ReadOnly {
		if _, err := os.Stat(dir); err != nil {
			return nil, fmt.Errorf("evmchaindb: chain DB not found at %s: %w", dir, err)
		}
	} else {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("evmchaindb: create chain DB dir: %w", err)
		}
		if err := os.MkdirAll(ancient, 0o755); err != nil {
			return nil, fmt.Errorf("evmchaindb: create ancient dir: %w", err)
		}
	}

	if opts.Cache <= 0 {
		opts.Cache = 128
	}
	if opts.Handles <= 0 {
		opts.Handles = 512
	}
	if opts.Namespace == "" {
		opts.Namespace = "evmchaindb/"
	}
	engine := opts.Type
	if engine == "" {
		engine = detectEngine(dir)
	}

	return rawdb.Open(rawdb.OpenOptions{
		Type:                 engine,
		Directory:            dir,
		AncientsDirectory:    ancient,
		Namespace:            opts.Namespace,
		Cache:                opts.Cache,
		Handles:              opts.Handles,
		ReadOnly:             opts.ReadOnly,
		Ephemeral:            opts.Ephemeral,
		SkipBackgroundFreeze: opts.SkipBackgroundFreeze,
	})
}

// detectEngine returns "pebble" or "leveldb" based on what's already on
// disk. Empty / missing directories fall back to "pebble", matching the
// modern default used by upstream geth and BDAG nodes.
func detectEngine(dir string) string {
	if existing := rawdb.PreexistingDatabase(dir); existing != "" {
		return existing
	}
	return "pebble"
}
