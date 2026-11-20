package core

import (
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/consensus/ethash"
	"github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/core/types"
	"testing"
)

// An existing path-state can live above the persistent disk layer. Selecting it
// as a recovery anchor must not invoke snapshot-sync initialization or erase the
// available descendants that a subsequent native DAG reorganization may need.
func TestExistingStateAnchorPreservesPathAndHashHistory(t *testing.T) {
	for _, scheme := range []string{rawdb.PathScheme, rawdb.HashScheme} {
		t.Run(scheme, func(t *testing.T) {
			_, _, chain, err := newCanonical(ethash.NewFaker(), 8, true, scheme)
			if err != nil {
				t.Fatal(err)
			}
			defer chain.Stop()
			target, future := chain.GetBlockByNumber(4), chain.GetBlockByNumber(8)
			if target.Root() == chain.genesisBlock.Root() {
				t.Fatal("fixture needs a changed ancestor state")
			}
			if !chain.HasState(target.Root()) || !chain.HasState(future.Root()) {
				t.Fatal("fixture states unavailable")
			}
			stateID := rawdb.ReadPersistentStateID(chain.db)
			for _, block := range []*types.Block{target, future, target} {
				if err := chain.SetHeadToExistingBlock(block.Hash(), block.NumberU64()); err != nil {
					t.Fatal(err)
				}
				if chain.CurrentBlock().Hash() != block.Hash() {
					t.Fatal("wrong selected head")
				}
				if !chain.HasState(target.Root()) || !chain.HasState(future.Root()) {
					t.Fatal("available ancestor/descendant state lost")
				}
				if got := chain.GetBlockByNumber(8); got == nil || got.Hash() != future.Hash() {
					t.Fatal("later canonical block pruned")
				}
				if rawdb.ReadPersistentStateID(chain.db) != stateID {
					t.Fatal("existing-state selection reset persistent state history")
				}
			}
			invalidHeader := target.Header()
			invalidHeader.Root = common.HexToHash("0xdeadbeef")
			invalid := types.NewBlockWithHeader(invalidHeader)
			rawdb.WriteBlock(chain.db, invalid)
			if err := chain.SetHeadToExistingBlock(invalid.Hash(), invalid.NumberU64()); err == nil {
				t.Fatal("missing state accepted")
			}
			if chain.CurrentBlock().Hash() != target.Hash() || !chain.HasState(future.Root()) {
				t.Fatal("failed selection changed state")
			}
		})
	}
}
