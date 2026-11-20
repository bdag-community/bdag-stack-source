// splitsweep recovers the historical miner-split used by canonical miners at
// EVM block 12,073,951 (first block of staking epoch 19). It replays forward
// from the last locally-available state (block 12,024,290 in the divergent
// rebuild db), verifying every state root against the stored canonical
// headers, then re-runs the block-12,073,951 reward finalization with
// candidate split values until the canonical state root is reproduced.
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	bdagconsensus "github.com/BlockdagNetworkLabs/bdag/bevm/bdag/consensus"
	bevmparams "github.com/BlockdagNetworkLabs/bdag/bevm/params"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core"
	"github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/core/state"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/core/vm"
	"github.com/ethereum/go-ethereum/ethdb"
	"github.com/ethereum/go-ethereum/triedb"

	"math/big"
)

const (
	anchorNumber = 12_024_290
	targetNumber = 12_073_951
)

var (
	anchorRoot          = common.HexToHash("0x0e8f7ae669f6b7ec3176d1b9ac9aa75a127c460acd1cb516eaa8bfc9edf32314")
	canonicalTargetRoot = common.HexToHash("0x301a72b72eb3dade7f264d7fec558838c235f88cf7018c07a1066adcc832a5b6")
	divergentTargetRoot = common.HexToHash("0x1bc2585477ef4bb4fee16c1e1039159e8c492c675063acb345cd9ee803f2e14e")
)

func openDB(chaindata string) (ethdb.Database, error) {
	return rawdb.Open(rawdb.OpenOptions{
		Directory:         chaindata,
		AncientsDirectory: filepath.Join(chaindata, "ancient"),
		Cache:             512,
		Handles:           512,
		ReadOnly:          true,
	})
}

func main() {
	if len(os.Args) < 2 {
		fmt.Println("usage: splitsweep <state-chaindata> [canonical-chaindata] [continue-blocks]")
		os.Exit(1)
	}
	db, err := openDB(os.Args[1])
	if err != nil {
		fmt.Printf("open failed: %v\n", err)
		os.Exit(1)
	}
	defer db.Close()

	// Optional second db holding canonical blocks past the divergence point.
	canonDB := db
	continueBlocks := uint64(0)
	if len(os.Args) >= 4 {
		canonDB, err = openDB(os.Args[2])
		if err != nil {
			fmt.Printf("open canonical db failed: %v\n", err)
			os.Exit(1)
		}
		defer canonDB.Close()
		fmt.Sscanf(os.Args[3], "%d", &continueBlocks)
	}

	chainCfg := bevmparams.BdagMainnetChainConfig
	engine := bdagconsensus.New(db)
	// Header lookups (BLOCKHASH etc.) served from the canonical db, which
	// shares all headers with the state db up to the divergence point.
	hc, err := core.NewHeaderChain(canonDB, chainCfg, engine, func() bool { return false })
	if err != nil {
		fmt.Printf("headerchain: %v\n", err)
		os.Exit(1)
	}
	processor := core.NewStateProcessor(chainCfg, hc)

	tdb := triedb.NewDatabase(db, triedb.HashDefaults)
	sdb := state.NewDatabase(tdb, nil)
	statedb, err := state.New(anchorRoot, sdb)
	if err != nil {
		fmt.Printf("state.New(anchor): %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("replaying %d -> %d ...\n", anchorNumber+1, targetNumber-1)
	start := time.Now()
	for n := uint64(anchorNumber + 1); n < targetNumber; n++ {
		hash := rawdb.ReadCanonicalHash(canonDB, n)
		block := rawdb.ReadBlock(canonDB, hash, n)
		if block == nil {
			fmt.Printf("missing block %d\n", n)
			os.Exit(1)
		}
		if _, err := processor.Process(block, statedb, vm.Config{}); err != nil {
			fmt.Printf("process block %d: %v\n", n, err)
			os.Exit(1)
		}
		root := statedb.IntermediateRoot(chainCfg.IsEIP158(block.Number()))
		if root != block.Root() {
			fmt.Printf("REPLAY DIVERGED at block %d: computed=%s stored=%s\n", n, root.Hex(), block.Root().Hex())
			os.Exit(1)
		}
		if (n-anchorNumber)%5000 == 0 {
			fmt.Printf("  at %d (%.0fs elapsed, root ok)\n", n, time.Since(start).Seconds())
		}
	}
	fmt.Printf("replay done in %.0fs; parent state at %d verified\n", time.Since(start).Seconds(), targetNumber-1)

	// Target header: take fields from the locally stored (divergent) header,
	// which shares coinbase/time/extra with canonical (only Root differs).
	tHash := rawdb.ReadCanonicalHash(canonDB, targetNumber)
	tHeader := rawdb.ReadHeader(canonDB, tHash, targetNumber)
	if tHeader == nil {
		fmt.Printf("missing target header %d\n", targetNumber)
		os.Exit(1)
	}
	fmt.Printf("target header: coinbase=%s time=%d extra=%x\n", tHeader.Coinbase.Hex(), tHeader.Time, tHeader.Extra)

	try := func(split int64, legacy bool) common.Hash {
		st := statedb.Copy()
		h := types.CopyHeader(tHeader)
		engine.FinalizeWithMinerSplitOverride(hc, h, st, big.NewInt(split), legacy)
		return h.Root
	}

	// Sanity check first: default split 70 should reproduce the divergent root.
	root70 := try(70, false)
	fmt.Printf("split=70: root=%s (divergent match=%v, canonical match=%v)\n",
		root70.Hex(), root70 == divergentTargetRoot, root70 == canonicalTargetRoot)

	rootLegacy := try(0, true)
	fmt.Printf("legacy(100%% miner): root=%s (canonical match=%v)\n", rootLegacy.Hex(), rootLegacy == canonicalTargetRoot)

	foundCanonical, foundDivergent := false, false
	for s := int64(0); s <= 200; s++ {
		root := try(s, false)
		if root == canonicalTargetRoot {
			fmt.Printf(">>> CANONICAL MATCH: minerSplit=%d\n", s)
			foundCanonical = true
		}
		if root == divergentTargetRoot {
			fmt.Printf(">>> DIVERGENT MATCH: minerSplit=%d\n", s)
			foundDivergent = true
		}
	}
	if !foundCanonical {
		fmt.Println("no split value in 0..200 reproduces the canonical root")
	}
	if !foundDivergent {
		fmt.Println("no split value in 0..200 reproduces the divergent root")
	}

	if continueBlocks == 0 {
		return
	}

	// Continue replaying canonical blocks past the boundary to see how far
	// deterministic replay tracks the canonical chain.
	fmt.Printf("continuing canonical replay %d -> %d ...\n", targetNumber, targetNumber+continueBlocks)
	start = time.Now()
	for n := uint64(targetNumber); n <= targetNumber+continueBlocks; n++ {
		hash := rawdb.ReadCanonicalHash(canonDB, n)
		block := rawdb.ReadBlock(canonDB, hash, n)
		if block == nil {
			fmt.Printf("missing canonical block %d, stopping\n", n)
			return
		}
		if _, err := processor.Process(block, statedb, vm.Config{}); err != nil {
			fmt.Printf("process block %d: %v\n", n, err)
			return
		}
		root := statedb.IntermediateRoot(chainCfg.IsEIP158(block.Number()))
		if root != block.Root() {
			fmt.Printf("REPLAY DIVERGED at block %d: computed=%s canonical=%s\n", n, root.Hex(), block.Root().Hex())
			return
		}
		if (n-targetNumber)%10000 == 0 {
			fmt.Printf("  at %d (%.0fs elapsed, root ok)\n", n, time.Since(start).Seconds())
		}
	}
	fmt.Printf("canonical replay held for all %d blocks past the boundary\n", continueBlocks)
}
