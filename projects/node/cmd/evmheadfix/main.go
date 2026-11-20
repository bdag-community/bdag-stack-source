// evmheadfix repairs EVM chaindata head markers after an aborted deep rewind
// and manages the snap-sync pivot marker that rewindHashHead uses as its
// backtrack limit.
//
// Usage:
//
//	evmheadfix <chaindata> prepare <head-block-number> <pivot-number>
//	evmheadfix <chaindata> clear-pivot
//
// TODO: temporary investigation/recovery tool; delete when done.
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/rawdb"
)

func main() {
	if len(os.Args) < 3 {
		fmt.Println("usage: evmheadfix <chaindata> prepare <head-num> <pivot-num> | clear-pivot")
		os.Exit(1)
	}
	chaindata, action := os.Args[1], os.Args[2]

	db, err := rawdb.Open(rawdb.OpenOptions{
		Directory:         chaindata,
		AncientsDirectory: filepath.Join(chaindata, "ancient"),
		Cache:             128,
		Handles:           128,
	})
	if err != nil {
		fmt.Printf("open failed: %v\n", err)
		os.Exit(1)
	}
	defer db.Close()

	switch action {
	case "show":
		headHash := rawdb.ReadHeadHeaderHash(db)
		blockHash := rawdb.ReadHeadBlockHash(db)
		fastHash := rawdb.ReadHeadFastBlockHash(db)
		for name, h := range map[string]common.Hash{"header": headHash, "block": blockHash, "fast": fastHash} {
			if num := rawdb.ReadHeaderNumber(db, h); num != nil {
				fmt.Printf("head %-6s: %d %s\n", name, *num, h.Hex())
			} else {
				fmt.Printf("head %-6s: ??? %s\n", name, h.Hex())
			}
		}
		if pivot := rawdb.ReadLastPivotNumber(db); pivot != nil {
			fmt.Printf("pivot: %d\n", *pivot)
		} else {
			fmt.Println("pivot: <nil>")
		}

	case "prepare":
		if len(os.Args) < 5 {
			fmt.Println("prepare needs <head-num> <pivot-num>")
			os.Exit(1)
		}
		headNum, _ := strconv.ParseUint(os.Args[3], 10, 64)
		pivotNum, _ := strconv.ParseUint(os.Args[4], 10, 64)

		hash := rawdb.ReadCanonicalHash(db, headNum)
		if hash == (common.Hash{}) {
			fmt.Printf("no canonical hash at %d\n", headNum)
			os.Exit(1)
		}
		header := rawdb.ReadHeader(db, hash, headNum)
		if header == nil {
			fmt.Printf("no header at %d\n", headNum)
			os.Exit(1)
		}
		if !rawdb.HasLegacyTrieNode(db, header.Root) {
			fmt.Printf("no state at %d (root %s)\n", headNum, header.Root.Hex())
			os.Exit(1)
		}
		rawdb.WriteHeadHeaderHash(db, hash)
		rawdb.WriteHeadBlockHash(db, hash)
		rawdb.WriteHeadFastBlockHash(db, hash)
		rawdb.WriteLastPivotNumber(db, pivotNum)
		fmt.Printf("head markers -> %d %s, pivot -> %d\n", headNum, hash.Hex(), pivotNum)

	case "clear-pivot":
		if err := db.Delete([]byte("LastPivot")); err != nil {
			fmt.Printf("delete pivot: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("pivot marker deleted")

	default:
		fmt.Printf("unknown action %q\n", action)
		os.Exit(1)
	}
}
