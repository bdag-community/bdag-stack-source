// stateprobe inspects an EVM chaindata directory: reports head pointers and,
// for given block numbers (or scan ranges), whether canonical header/body and
// trie state are present.
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"github.com/ethereum/go-ethereum/core/rawdb"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Println("usage: stateprobe <chaindata-dir> [block | from:to ...]")
		os.Exit(1)
	}
	chaindata := os.Args[1]
	db, err := rawdb.Open(rawdb.OpenOptions{
		Directory:         chaindata,
		AncientsDirectory: filepath.Join(chaindata, "ancient"),
		Cache:             256,
		Handles:           256,
		ReadOnly:          true,
	})
	if err != nil {
		fmt.Printf("open failed: %v\n", err)
		os.Exit(1)
	}
	defer db.Close()

	headHash := rawdb.ReadHeadHeaderHash(db)
	headBlockHash := rawdb.ReadHeadBlockHash(db)
	if num := rawdb.ReadHeaderNumber(db, headHash); num != nil {
		fmt.Printf("head header: %d %s\n", *num, headHash.Hex())
	} else {
		fmt.Printf("head header hash %s has no number\n", headHash.Hex())
	}
	if num := rawdb.ReadHeaderNumber(db, headBlockHash); num != nil {
		fmt.Printf("head block:  %d %s\n", *num, headBlockHash.Hex())
	}
	if frozen, err := db.Ancients(); err == nil {
		fmt.Printf("ancients (frozen items): %d\n", frozen)
	}

	check := func(n uint64) {
		hash := rawdb.ReadCanonicalHash(db, n)
		header := rawdb.ReadHeader(db, hash, n)
		hasBody := rawdb.HasBody(db, hash, n)
		if header == nil {
			fmt.Printf("block %d: MISSING header (canonical hash=%s body=%v)\n", n, hash.Hex(), hasBody)
			return
		}
		hasState := rawdb.HasLegacyTrieNode(db, header.Root)
		fmt.Printf("block %d: hash=%s body=%v state=%v\n", n, header.Hash().Hex(), hasBody, hasState)
	}

	for _, arg := range os.Args[2:] {
		if strings.Contains(arg, ":") {
			parts := strings.SplitN(arg, ":", 2)
			from, _ := strconv.ParseUint(parts[0], 10, 64)
			to, _ := strconv.ParseUint(parts[1], 10, 64)
			for n := from; n <= to; n++ {
				check(n)
			}
			continue
		}
		n, _ := strconv.ParseUint(arg, 10, 64)
		check(n)
	}
}
