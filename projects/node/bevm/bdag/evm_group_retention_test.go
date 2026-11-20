package bdag

import (
	"math/big"
	"testing"

	beth "github.com/BlockdagNetworkLabs/bdag/bevm/eth"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/consensus"
	"github.com/ethereum/go-ethereum/consensus/ethash"
	"github.com/ethereum/go-ethereum/core"
	"github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/eth/ethconfig"
	"github.com/ethereum/go-ethereum/ethdb"
	"github.com/ethereum/go-ethereum/node"
	"github.com/ethereum/go-ethereum/params"
)

// Upstream removed recovery write groups. Ordinary intake must still retain
// the recent-state window needed to select an ancestor and follow a DAG reorder.
func TestEVMIntakePreservesReorgState(t *testing.T) {
	for _, scheme := range []string{rawdb.PathScheme, rawdb.HashScheme} {
		t.Run(scheme, func(t *testing.T) {
			genesis := &core.Genesis{Config: params.AllEthashProtocolChanges, BaseFee: big.NewInt(params.InitialBaseFee)}
			cfg := &beth.Config{Node: node.DefaultConfig, Eth: ethconfig.Defaults}
			cfg.Node.Name = "retention-test"
			cfg.Eth.Genesis = genesis
			cfg.Eth.ConsensusEngine = func(*params.ChainConfig, ethdb.Database) (consensus.Engine, error) {
				return ethash.NewFaker(), nil
			}
			chain, err := beth.NewETHChain(cfg, []string{"retention-test", "--datadir", t.TempDir(), "--db.engine", "pebble", "--state.scheme", scheme, "--cache", "64", "--nodiscover", "--maxpeers", "0", "--ipcdisable"})
			if err != nil {
				t.Fatal(err)
			}
			defer chain.Stop()
			bc := chain.Ether().BlockChain()
			generationDB, blocks, _ := core.GenerateChainWithGenesis(genesis, ethash.NewFaker(), 24, nil)
			defer generationDB.Close()
			if _, err := bc.InsertChain(blocks); err != nil {
				t.Fatal(err)
			}
			anchor := blocks[12]
			if !bc.HasState(anchor.Root()) {
				t.Fatal("fixture ancestor unavailable before catch-up")
			}
			if err := bc.SetHeadToExistingBlock(anchor.Hash(), anchor.NumberU64()); err != nil {
				t.Fatal(err)
			}
			alternate, _ := core.GenerateChain(genesis.Config, anchor, ethash.NewFaker(), generationDB, 13, func(i int, block *core.BlockGen) {
				block.SetCoinbase(common.HexToAddress("0x42"))
			})
			if _, err := bc.InsertChain(alternate); err != nil {
				t.Fatal(err)
			}
			if bc.CurrentBlock().Hash() != alternate[12].Hash() {
				t.Fatal("alternate chain did not become canonical")
			}
			if _, err := bc.State(); err != nil {
				t.Fatal(err)
			}
		})
	}
}
