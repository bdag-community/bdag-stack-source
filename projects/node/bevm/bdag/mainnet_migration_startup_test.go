package bdag

import (
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/consensus/ethash"
	ethcore "github.com/ethereum/go-ethereum/core"
	"github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/core/vm"
	ethparams "github.com/ethereum/go-ethereum/params"
	"github.com/stretchr/testify/require"
)

func TestMainnetDAOStartupNeverSilentlyRewindsPairedHistory(t *testing.T) {
	for _, beforeFloor := range []bool{true, false} {
		t.Run(map[bool]string{true: "before floor installs without rewind", false: "late incompatible profile stops without writes"}[beforeFloor], func(t *testing.T) {
			config := *ethparams.AllEthashProtocolChanges
			config.ChainID = big.NewInt(1404)
			genesis := &ethcore.Genesis{Config: &config, GasLimit: 45_000_000}
			db := rawdb.NewMemoryDatabase()
			t.Cleanup(func() { db.Close() })
			engine := ethash.NewFaker()
			chain, err := ethcore.NewBlockChain(db, nil, genesis, nil, engine, vm.Config{}, nil)
			require.NoError(t, err)
			_, blocks, _ := ethcore.GenerateChainWithGenesis(genesis, engine, 5, nil)
			_, err = chain.InsertChain(blocks)
			require.NoError(t, err)
			chain.Stop()
			oldHead := rawdb.ReadHeadBlockHash(db)
			oldHeader := rawdb.ReadHeadHeaderHash(db)
			oldConfig := rawdb.ReadChainConfig(db, genesis.ToBlock().Hash())
			future := config
			floor := uint64(2)
			if beforeFloor {
				floor = 10
			}
			future.BdagMainnetDAO = &ethparams.BDAGMainnetDAOConfig{Version: 1, ArtifactSHA256: common.HexToHash("0x1234"), SignalStartLayer: 30000000, ActivationDelayLayers: 2000, EarliestEVMBlock: floor,
				ReleaseDistributedAt: 1800000000, NoticePublishedAt: 1803024000, NotBeforeTimestamp: 1803628800, LegacyGenesisTime: 1770760980}
			newGenesis := *genesis
			newGenesis.Config = &future
			opened, err := ethcore.NewBlockChain(db, nil, &newGenesis, nil, engine, vm.Config{}, nil)
			if opened != nil {
				t.Cleanup(opened.Stop)
			}
			if beforeFloor {
				require.NoError(t, err)
				require.NotNil(t, opened)
			} else {
				require.ErrorContains(t, err, "automatic rewind is disabled")
				require.Nil(t, opened)
				require.Equal(t, oldConfig, rawdb.ReadChainConfig(db, genesis.ToBlock().Hash()))
			}
			require.Equal(t, oldHead, rawdb.ReadHeadBlockHash(db))
			require.Equal(t, oldHeader, rawdb.ReadHeadHeaderHash(db))
		})
	}
}
