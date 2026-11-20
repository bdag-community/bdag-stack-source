package consensus_test

import (
	"encoding/json"
	"math/big"
	"os"
	"testing"

	"github.com/BlockdagNetworkLabs/bdag/bevm/bdag"
	engine "github.com/BlockdagNetworkLabs/bdag/bevm/bdag/consensus"
	"github.com/BlockdagNetworkLabs/bdag/core/snapshot"
	qparams "github.com/BlockdagNetworkLabs/bdag/params"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core"
	"github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/core/vm"
	"github.com/ethereum/go-ethereum/ethdb"
	"github.com/ethereum/go-ethereum/ethdb/memorydb"
	"github.com/stretchr/testify/require"
)

// Forensic regression against the preserved September 21 archive. This does
// not modify it or claim native DAG replay coverage. The invalid global-clock
// case must be rejected, never accepted by weakening checkpoint validation.
func TestArchivedV14PreActivationClockReplay(t *testing.T) {
	archive := os.Getenv("BDAG_V14_ARCHIVE")
	if archive == "" {
		t.Skip("set BDAG_V14_ARCHIVE and BDAG_V14_ALLOCATIONS for archived replay evidence")
	}
	nativeDB := rawdb.NewMemoryDatabase()
	recordedDB, err := rawdb.NewDatabaseWithFreezer(memorydb.New(), t.TempDir(), "archived-replay", false)
	require.NoError(t, err)
	t.Cleanup(func() { nativeDB.Close(); recordedDB.Close() })
	_, err = snapshot.MultiRead(archive, map[string]ethdb.Database{
		snapshot.SourceChain: nativeDB, snapshot.SourceEVM: recordedDB,
	}, snapshot.ReadOptions{Network: "testnet", RequireEmpty: true})
	require.NoError(t, err)
	raw, err := os.ReadFile(os.Getenv("BDAG_V14_ALLOCATIONS"))
	require.NoError(t, err)
	var allocation struct {
		Allocations []struct {
			Address        string
			AllocationBdag string
		}
	}
	require.NoError(t, json.Unmarshal(raw, &allocation))
	previous := qparams.ActiveNetParams
	t.Cleanup(func() { qparams.ActiveNetParams = previous })
	for _, fast := range []bool{false, true} {
		name := "preserved_RC2_clock"
		if fast {
			name = "rejected_v14_global_clock"
		}
		t.Run(name, func(t *testing.T) {
			net := qparams.TestNetParams
			if fast {
				net.StakingEpochDurationSeconds = 60
			} else {
				net.StakingEpochDurationSeconds = 0
			}
			active := qparams.TestNetParam
			active.Params = &net
			qparams.ActiveNetParams = &active
			genesis := bdag.BdagTestnetGenesis(nil)
			for _, entry := range allocation.Allocations {
				require.True(t, common.IsHexAddress(entry.Address))
				amount, ok := new(big.Int).SetString(entry.AllocationBdag, 10)
				require.True(t, ok)
				amount.Mul(amount, new(big.Int).Exp(big.NewInt(10), big.NewInt(18), nil))
				bdag.AddGenesisAlloc(genesis, common.HexToAddress(entry.Address), amount)
			}
			db := rawdb.NewMemoryDatabase()
			chain, err := core.NewBlockChain(db, nil, genesis, nil, engine.New(db), vm.Config{}, nil)
			require.NoError(t, err)
			t.Cleanup(chain.Stop)
			require.Equal(t, rawdb.ReadCanonicalHash(recordedDB, 0), chain.Genesis().Hash())
			// Stop before the old native activation envelope. That abandoned
			// release deliberately retains separate, immutable forensic evidence.
			for n := uint64(1); n <= 4_000; n++ {
				hash := rawdb.ReadCanonicalHash(recordedDB, n)
				block := rawdb.ReadBlock(recordedDB, hash, n)
				require.NotNil(t, block, "archive block %d", n)
				_, err := chain.InsertChain(types.Blocks{block})
				if fast {
					require.Error(t, err, "global fast clock must not replay old RC2 state")
					t.Logf("Global 60-second clock rejects recorded block %d: %v", n, err)
					return
				}
				require.NoError(t, err, "legacy-clock archive block %d", n)
				require.Equal(t, block.Hash(), chain.CurrentBlock().Hash())
			}
			t.Logf("Replayed archived RC2 history through 4000: hash %s root %s", chain.CurrentBlock().Hash(), chain.CurrentBlock().Root)
		})
	}
}
