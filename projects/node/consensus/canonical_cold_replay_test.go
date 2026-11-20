package consensus

import (
	"encoding/json"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"testing"

	"github.com/BlockdagNetworkLabs/bdag/core/blockchain"
	qtypes "github.com/BlockdagNetworkLabs/bdag/core/types"
	"github.com/BlockdagNetworkLabs/bdag/database/chaindb"
	"github.com/BlockdagNetworkLabs/bdag/database/evmchaindb"
	nativeraw "github.com/BlockdagNetworkLabs/bdag/database/rawdb"
	"github.com/BlockdagNetworkLabs/bdag/params"
	"github.com/BlockdagNetworkLabs/bdag/services/common"
	"github.com/BlockdagNetworkLabs/bdag/upgrades"
	"github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/stretchr/testify/require"
)

// Optional offline acceptance: read only a verified cold canonical database
// pair, replay its native bodies through normal validation into fresh temporary
// databases, and compare the final native/EVM identity. No peer, signer, miner,
// source-state writes, fast-add flag or checkpoint exception is involved.
func TestCanonicalColdNativeReplay(t *testing.T) {
	fixture := os.Getenv("BDAG_CANONICAL_COLD_DATA")
	if fixture == "" {
		t.Skip("verified canonical cold database pair not configured")
	}
	previous := params.ActiveNetParams
	originalUpgrades := upgrades.ReplaceUpgrades(map[string]*upgrades.UpgradeDefinition{})
	t.Cleanup(func() { params.ActiveNetParams = previous; upgrades.ReplaceUpgrades(originalUpgrades) })
	params.ActiveNetParams = &params.TestNetParam
	nativePath := filepath.Join(fixture, "BdagChain")
	source, err := nativeraw.Open(nativeraw.OpenOptions{
		Directory: nativePath, AncientsDirectory: filepath.Join(nativePath, "ancient"),
		ReadOnly: true, Cache: 64, Handles: 64, Namespace: "cold-native-replay/",
	})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, source.Close()) })
	evmSource, err := evmchaindb.Open(fixture, evmchaindb.Options{ReadOnly: true, Cache: 64, Handles: 64})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, evmSource.Close()) })
	tipID := nativeraw.ReadMainChainTip(source)
	require.NotNil(t, tipID)
	tip := nativeraw.ReadDAGBlock(source, *tipID)
	require.NotNil(t, tip)
	expectedEVM := rawdb.ReadHeadHeader(evmSource)
	require.NotNil(t, expectedEVM)
	t.Logf("Cold source native ID %d order %d hash %s; EVM %d hash %s root %s", *tipID, tip.GetOrder(), tip.GetHash(), expectedEVM.Number.Uint64(), expectedEVM.Hash(), expectedEVM.Root)

	allocRaw, err := os.ReadFile(os.Getenv("BDAG_RC2_GENESIS_ALLOC"))
	require.NoError(t, err)
	var allocations map[string]string
	require.NoError(t, json.Unmarshal(allocRaw, &allocations))
	cfg := common.DefaultConfig(t.TempDir())
	cfg.NoFileLogging, cfg.DebugLevel = true, "error"
	cfg.EVMEnv = "--cache 64 --db.engine pebble"
	for address, amount := range allocations {
		wei, ok := new(big.Int).SetString(amount, 10)
		require.True(t, ok)
		unit := new(big.Int).Exp(big.NewInt(10), big.NewInt(18), nil)
		whole, remainder := new(big.Int), new(big.Int)
		whole.QuoRem(wei, unit, remainder)
		require.Zero(t, remainder.Sign(), "fixture allocation must be whole BDAG")
		cfg.GenesisAlloc = append(cfg.GenesisAlloc, fmt.Sprintf("%s:%s", address, whole))
	}
	t.Setenv("BDAG_NETWORK", "testnet")
	require.NoError(t, common.SetupConfig(cfg))
	interrupt := make(chan struct{})
	db, err := chaindb.Open(cfg, interrupt)
	require.NoError(t, err)
	cons := New(cfg, db, interrupt, make(chan struct{}, 1))
	require.NoError(t, cons.Init())
	bc := cons.BlockChain().(*blockchain.BlockChain)
	require.NoError(t, bc.Start())
	t.Cleanup(func() { close(interrupt); require.NoError(t, bc.Stop()); db.Close() })
	lastID := *tipID
	for _, id := range nativeraw.ReadDAGTips(source) {
		if id > lastID {
			lastID = id
		}
	}
	var processed, vacant uint64
	for id := uint64(1); id <= lastID; id++ {
		dagRaw := nativeraw.ReadDAGBlockBaw(source, id)
		bodyRaw := nativeraw.ReadBodyRawByID(source, id)
		present, err := canonicalReplaySlot(dagRaw, bodyRaw)
		require.NoError(t, err, "native ID %d", id)
		if !present {
			vacant++
			continue
		}
		body, err := qtypes.NewBlockFromBytes(bodyRaw)
		require.NoError(t, err, "decode native body ID %d", id)
		_, orphan, err := bc.ProcessBlock(body, blockchain.BFNone, nil)
		require.NoError(t, err, "normal native acceptance ID %d", id)
		require.False(t, orphan, "native orphan ID %d", id)
		processed++
		if id%10000 == 0 {
			head := bc.EVMChain().ETHChain().Ether().BlockChain().CurrentBlock()
			t.Logf("Native bodies replayed %d; EVM %d hash %s root %s", id, head.Number.Uint64(), head.Hash(), head.Root)
		}
	}
	rebuilt := bc.Blockdag().GetMainChainTip()
	require.Equal(t, *tip.GetHash(), *rebuilt.GetHash(), "canonical native main tip")
	head := bc.EVMChain().ETHChain().Ether().BlockChain().CurrentBlock()
	require.Equal(t, expectedEVM.Number, head.Number, "canonical EVM number")
	require.Equal(t, expectedEVM.Hash(), head.Hash(), "canonical EVM hash")
	require.Equal(t, expectedEVM.Root, head.Root, "canonical EVM state root")
	require.NoError(t, bc.CheckEVMCheckpoint(head.Number.Uint64(), head.Hash(), head.Root))
	t.Logf("PASS: %d native bodies replayed from genesis (%d vacant local IDs); EVM %d hash %s root %s", processed, vacant, head.Number.Uint64(), head.Hash(), head.Root)
}

// Local allocation counters can contain holes after rejected native blocks.
// Only a slot absent from BOTH indexes is vacant; recorded blocks still require
// their exact decodable body and normal consensus validation above.
func canonicalReplaySlot(dagRaw, bodyRaw []byte) (bool, error) {
	if len(dagRaw) == 0 && len(bodyRaw) == 0 {
		return false, nil
	}
	if len(dagRaw) == 0 || len(bodyRaw) == 0 {
		return false, fmt.Errorf("incomplete recorded native block")
	}
	return true, nil
}

func TestCanonicalReplaySlot(t *testing.T) {
	for _, tc := range []struct {
		name             string
		dag, body        []byte
		present, invalid bool
	}{
		{"vacant counter", nil, nil, false, false},
		{"recorded body missing", []byte{1}, nil, false, true},
		{"body index missing", nil, []byte{1}, false, true},
		{"complete record", []byte{1}, []byte{1}, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			present, err := canonicalReplaySlot(tc.dag, tc.body)
			require.Equal(t, tc.present, present)
			require.Equal(t, tc.invalid, err != nil)
		})
	}
}
