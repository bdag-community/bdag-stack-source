package consensus

import (
	"encoding/json"
	"fmt"
	"os"
	"testing"

	"github.com/BlockdagNetworkLabs/bdag/core/blockchain"
	"github.com/BlockdagNetworkLabs/bdag/core/snapshot"
	"github.com/BlockdagNetworkLabs/bdag/database/chaindb"
	nativeraw "github.com/BlockdagNetworkLabs/bdag/database/rawdb"
	"github.com/BlockdagNetworkLabs/bdag/params"
	"github.com/BlockdagNetworkLabs/bdag/services/common"
	"github.com/BlockdagNetworkLabs/bdag/upgrades"
	"github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/ethdb"
	"github.com/ethereum/go-ethereum/ethdb/memorydb"
	"github.com/stretchr/testify/require"
)

// Read only the archived snapshot, then replay native blocks into a separate
// temporary native+EVM database with the normal acceptance path and no fast-add
// flags. This is not a network join or a post-activation rehearsal.
func TestArchivedNativePreActivationReplay(t *testing.T) {
	archive := os.Getenv("BDAG_V14_ARCHIVE")
	if archive == "" {
		t.Skip("archived native acceptance evidence not configured")
	}
	previous := params.ActiveNetParams
	originalUpgrades := upgrades.ReplaceUpgrades(map[string]*upgrades.UpgradeDefinition{})
	t.Cleanup(func() { params.ActiveNetParams = previous; upgrades.ReplaceUpgrades(originalUpgrades) })
	params.ActiveNetParams = &params.TestNetParam
	nativeDB := rawdb.NewMemoryDatabase()
	evmDB, err := rawdb.NewDatabaseWithFreezer(memorydb.New(), t.TempDir(), "native-replay-archive", false)
	require.NoError(t, err)
	t.Cleanup(func() { nativeDB.Close(); evmDB.Close() })
	_, err = snapshot.MultiRead(archive, map[string]ethdb.Database{snapshot.SourceChain: nativeDB, snapshot.SourceEVM: evmDB}, snapshot.ReadOptions{Network: "testnet", RequireEmpty: true})
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
	cfg := common.DefaultConfig(t.TempDir())
	cfg.NoFileLogging = true
	cfg.EVMEnv = "--cache 64 --db.engine pebble"
	cfg.DebugLevel = "error"
	for _, row := range allocation.Allocations {
		cfg.GenesisAlloc = append(cfg.GenesisAlloc, fmt.Sprintf("%s:%s", row.Address, row.AllocationBdag))
	}
	t.Setenv("BDAG_NETWORK", "testnet")
	require.NoError(t, common.SetupConfig(cfg))
	interrupt := make(chan struct{})
	db, err := chaindb.Open(cfg, interrupt)
	require.NoError(t, err)
	t.Cleanup(db.Close)
	cons := New(cfg, db, interrupt, make(chan struct{}, 1))
	require.NoError(t, cons.Init())
	bc := cons.BlockChain().(*blockchain.BlockChain)
	require.NoError(t, bc.Start())
	t.Cleanup(func() { close(interrupt); require.NoError(t, bc.Stop()) })
	for id := uint64(1); id <= 4000; id++ {
		block := nativeraw.ReadBodyByID(nativeDB, id)
		require.NotNil(t, block, "native block %d", id)
		_, orphan, err := bc.ProcessBlock(block, blockchain.BFNone, nil)
		require.NoError(t, err, "normal acceptance of archived native block %d", id)
		require.False(t, orphan, "archived native block %d", id)
	}
	head := bc.Blockdag().GetMainChainTip().GetState()
	recorded := rawdb.ReadHeader(evmDB, rawdb.ReadCanonicalHash(evmDB, head.GetEVMNumber()), head.GetEVMNumber())
	require.NotNil(t, recorded)
	require.Equal(t, recorded.Hash(), head.GetEVMHash())
	require.Equal(t, recorded.Root, head.GetEVMRoot())
	t.Logf("Native replay through ID 4000 matches archived EVM block %d: hash %s root %s", head.GetEVMNumber(), head.GetEVMHash(), head.GetEVMRoot())
}
