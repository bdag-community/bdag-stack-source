package consensus

import (
	"encoding/json"
	"fmt"
	"math/big"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	evmparams "github.com/BlockdagNetworkLabs/bdag/bevm/params"
	"github.com/BlockdagNetworkLabs/bdag/core/blockchain"
	"github.com/BlockdagNetworkLabs/bdag/database/chaindb"
	"github.com/BlockdagNetworkLabs/bdag/database/evmchaindb"
	nativeraw "github.com/BlockdagNetworkLabs/bdag/database/rawdb"
	"github.com/BlockdagNetworkLabs/bdag/params"
	"github.com/BlockdagNetworkLabs/bdag/services/common"
	"github.com/BlockdagNetworkLabs/bdag/upgrades"
	ethcommon "github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/rlp"
	"github.com/stretchr/testify/require"
)

// Only an authenticated offline pair may be supplied. The read-only input is
// copied into a disposable directory. No identity, peer, signer or miner runs.
func TestSeparateStakingReplacementColdOpenPreservesPredecessor(t *testing.T) {
	fixture := os.Getenv("BDAG_SEPARATE_PREDECESSOR_PAIR")
	if fixture == "" {
		t.Skip("authenticated v0.3.0-rc.1 pair not supplied")
	}
	root := t.TempDir()
	output, err := exec.Command("cp", "-a", fixture+"/.", root).CombinedOutput()
	require.NoError(t, err, "%s", output)
	data := filepath.Join(root, "testnet")
	previousNet := params.ActiveNetParams
	previousDefs := upgrades.GetUpgrades()
	t.Cleanup(func() { params.ActiveNetParams = previousNet; upgrades.ReplaceUpgrades(previousDefs) })
	params.ActiveNetParams = &params.TestNetParam
	nativePath := filepath.Join(data, "BdagChain")
	native, err := nativeraw.Open(nativeraw.OpenOptions{Directory: nativePath, AncientsDirectory: filepath.Join(nativePath, "ancient"), ReadOnly: true, Cache: 64, Handles: 64})
	require.NoError(t, err)
	tipID := nativeraw.ReadMainChainTip(native)
	require.NotNil(t, tipID)
	tip := nativeraw.ReadDAGBlock(native, *tipID)
	require.NotNil(t, tip)
	require.Less(t, uint64(tip.GetLayer()), uint64(562000), "replacement window must remain future")
	persisted, err := native.Get(nativeraw.UpgradeStateKey(upgrades.SeparateStakingV1Name))
	if err == nil && len(persisted) != 0 {
		var status struct {
			Active, LockedIn            bool
			ActivationTime              uint64
			AnchorSet                   bool
			ActivateAnchor, ThresholdAt uint64
		}
		require.NoError(t, rlp.DecodeBytes(persisted, &status))
		require.Zero(t, status, "predecessor already signalled or activated")
	}
	require.NoError(t, native.Close())
	evm, err := evmchaindb.Open(data, evmchaindb.Options{ReadOnly: true, Cache: 64, Handles: 64})
	require.NoError(t, err)
	expected := rawdb.ReadHeadHeader(evm)
	require.NotNil(t, expected)
	genesis := rawdb.ReadCanonicalHash(evm, 0)
	stored := rawdb.ReadChainConfig(evm, genesis)
	require.NotNil(t, stored)
	require.NotNil(t, stored.BdagSeparateStaking)
	require.EqualValues(t, 605000, stored.BdagSeparateStaking.SignalStartLayer, "input must be installed predecessor state")
	require.Less(t, expected.Number.Uint64(), uint64(620000))
	require.Nil(t, stored.CheckCompatible(evmparams.BdagTestnetChainConfig, expected.Number.Uint64(), expected.Time))
	require.NoError(t, evm.Close())
	var alloc map[string]string
	raw, err := os.ReadFile(os.Getenv("BDAG_RC2_GENESIS_ALLOC"))
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(raw, &alloc))
	rawConfig := *common.DefaultConfig(t.TempDir())
	rawConfig.DataDir, rawConfig.NoFileLogging, rawConfig.DebugLevel = root, true, "error"
	rawConfig.EVMEnv = "--cache 64 --db.engine pebble"
	unit := new(big.Int).Exp(big.NewInt(10), big.NewInt(18), nil)
	for address, amount := range alloc {
		wei, ok := new(big.Int).SetString(amount, 10)
		require.True(t, ok)
		whole, remainder := new(big.Int), new(big.Int)
		whole.QuoRem(wei, unit, remainder)
		require.Zero(t, remainder.Sign())
		rawConfig.GenesisAlloc = append(rawConfig.GenesisAlloc, fmt.Sprintf("%s:%s", address, whole))
	}
	t.Setenv("BDAG_NETWORK", "testnet")
	for attempt := 0; attempt < 2; attempt++ {
		cfg := rawConfig
		upgrades.ReplaceUpgrades(previousDefs)
		require.NoError(t, common.SetupConfig(&cfg))
		interrupt := make(chan struct{})
		db, err := chaindb.Open(&cfg, interrupt)
		require.NoError(t, err)
		cons := New(&cfg, db, interrupt, make(chan struct{}, 1))
		require.NoError(t, cons.Init())
		bc := cons.BlockChain().(*blockchain.BlockChain)
		// Finish the real startup preparation synchronously, rather than start
		// a background preparer and cancel it during fixture cleanup.
		prepared, err := bc.EVMChain().PrepareEnvironment(bc.Blockdag().GetMainChainTip().GetState())
		require.NoError(t, err)
		require.Equal(t, expected.Hash(), prepared.Hash())
		actual := bc.EVMChain().ETHChain().Ether().BlockChain().CurrentBlock()
		require.Equal(t, expected.Number, actual.Number, "replacement must not rewind or advance the offline pair")
		require.Equal(t, expected.Hash(), actual.Hash())
		require.Equal(t, expected.Root, actual.Root)
		require.Equal(t, *tip.GetHash(), *bc.Blockdag().GetMainChainTip().GetHash())
		require.NoError(t, bc.CheckEVMCheckpoint(actual.Number.Uint64(), actual.Hash(), actual.Root))
		definition := upgrades.GetUpgrades()[upgrades.SeparateStakingV1Name]
		require.NotNil(t, definition)
		require.EqualValues(t, 562000, definition.StartLayer)
		require.False(t, definition.Active || definition.LockedIn || definition.AnchorSet)
		state, err := bc.EVMChain().ETHChain().Ether().BlockChain().State()
		require.NoError(t, err)
		require.Zero(t, state.GetCodeSize(evmparams.SeparateStakingV2Addr))
		require.NotEqual(t, ethcommon.Hash{}, state.GetCodeHash(evmparams.StakingContractAddr))
		close(interrupt)
		require.NoError(t, bc.EVMChain().Stop())
		db.Close()
		t.Logf("PASS replacement cold-open %d native=%s EVM=%s hash=%s root=%s", attempt+1, tip.GetHash(), actual.Number, actual.Hash(), actual.Root)
	}
}
