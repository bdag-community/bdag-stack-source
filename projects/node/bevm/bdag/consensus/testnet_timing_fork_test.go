package consensus

import (
	"bytes"
	"encoding/json"
	"math/big"
	"testing"

	"github.com/BlockdagNetworkLabs/bdag/bevm/params"
	"github.com/BlockdagNetworkLabs/bdag/upgrades"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core"
	"github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/core/state"
	"github.com/ethereum/go-ethereum/core/tracing"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/core/vm"
	ethparams "github.com/ethereum/go-ethereum/params"
	"github.com/holiman/uint256"
	"github.com/stretchr/testify/require"
)

func TestTimingForkCanonicalRuntimeViewsInNativeEVM(t *testing.T) {
	c, h, s, genesis := timingForkFixture(t)
	h.Difficulty = new(big.Int)
	h.BaseFee = new(big.Int)
	h.GasLimit = 30_000_000
	require.NoError(t, ApplyTestnetTimingFork(c, h, s))
	var bundle struct {
		Runtimes []struct {
			Address common.Address  `json:"address"`
			ABI     json.RawMessage `json:"abi"`
		} `json:"runtimes"`
	}
	require.NoError(t, json.Unmarshal(testnetTimingBundleJSON, &bundle))
	abis := make(map[common.Address]abi.ABI)
	for _, row := range bundle.Runtimes {
		parsed, err := abi.JSON(bytes.NewReader(row.ABI))
		require.NoError(t, err)
		abis[row.Address] = parsed
	}
	abis[params.StakingContractAddr] = abis[params.StakingV2ImplAddr]
	view := func(address common.Address, method string, args ...interface{}) []interface{} {
		a := abis[address]
		input, err := a.Pack(method, args...)
		require.NoError(t, err)
		snapshot := s.Snapshot()
		defer s.RevertToSnapshot(snapshot)
		evm := vm.NewEVM(vm.BlockContext{CanTransfer: core.CanTransfer, Transfer: core.Transfer,
			GetHash: func(uint64) common.Hash { return common.Hash{} }, BlockNumber: h.Number, Time: h.Time, Difficulty: h.Difficulty, BaseFee: h.BaseFee, GasLimit: h.GasLimit}, vm.TxContext{}, s, c, vm.Config{NoBaseFee: true})
		output, _, err := evm.StaticCall(vm.AccountRef(common.Address{}), address, input, 10_000_000)
		require.NoErrorf(t, err, "%s.%s returned 0x%x", address, method, output)
		values, err := a.Unpack(method, output)
		require.NoError(t, err)
		return values
	}
	root := s.IntermediateRoot(false)
	require.Equal(t, params.StakingContractAddr, view(params.ChainTallyAddr, "clockSource")[0])
	require.Equal(t, timingGovernorAddress, view(timingModuleAddress, "governor")[0])
	require.Equal(t, params.ChainTallyAddr, view(timingModuleAddress, "clock")[0])
	require.Equal(t, timingStakingLockAddress, view(params.StakingContractAddr, "owner")[0])
	require.Equal(t, timingModuleAddress, view(params.StakingContractAddr, "timingModule")[0])
	require.Equal(t, big.NewInt(60), view(params.StakingContractAddr, "epochDuration")[0])
	require.Equal(t, big.NewInt(26), view(timingStakingLockAddress, "getMinDelay")[0])
	require.Equal(t, timingModuleAddress, view(timingGovernorAddress, "timingModule")[0])
	factory := common.HexToAddress("0x8A791620dd6260079BF849Dc5567aDC3F2FdC318")
	require.Equal(t, timingEscrowAddress, view(factory, "implementation")[0])
	require.Equal(t, common.HexToAddress("0x8aCd85898458400f7Db866d53FCFF6f0D49741FF"), view(factory, "legacyImplementation")[0])
	require.Equal(t, uint64(240), view(factory, "challengeSeconds")[0])
	require.Equal(t, root, s.IntermediateRoot(false), "static reads cannot mutate the upgrade")
	h.Time = genesis + 540
	h.Number = big.NewInt(11)
	require.NoError(t, prepareVersionedRewardClock(c, h, s))
	require.Equal(t, big.NewInt(1800), view(params.StakingContractAddr, "epochDuration")[0])
	require.Equal(t, big.NewInt(772), view(timingStakingLockAddress, "getMinDelay")[0])
	require.Equal(t, []interface{}{big.NewInt(772), big.NewInt(1800), big.NewInt(3600)}, view(timingModuleAddress, "delaysForDuration", big.NewInt(1800)))
	require.Equal(t, uint64(7200), view(factory, "challengeSeconds")[0])
	for _, address := range []common.Address{common.HexToAddress("0x5FbDB2315678afecb367f032d93F642f64180aa3"), common.HexToAddress("0xe7f1725E7734CE288F8367e1Bb143E90bb3F0512")} {
		require.Equal(t, big.NewInt(1800), view(address, "epochDuration")[0])
		require.Equal(t, big.NewInt(9), view(address, "currentEpoch")[0])
	}
}

func TestTimingForkBuilderImporterHookAgreement(t *testing.T) {
	c, h, s, _ := timingForkFixture(t)
	g := c.BdagGovernanceFork
	c.BdagTestnetTiming.SignalStartLayer = g.ActivationLayer + 6001
	c.BdagTestnetTiming.ActivationLayer = g.ActivationLayer + 10000
	h.Extra = EncodeRewardExtraWithLayer(1, c.BdagTestnetTiming.ActivationLayer)
	d := upgrades.GetUpgrades()[upgrades.TestnetTimingV1Name]
	d.StartLayer = c.BdagTestnetTiming.SignalStartLayer
	d.TimeoutLayer = d.StartLayer + 2000
	d.ActivateAnchor = c.BdagTestnetTiming.ActivationLayer
	upgrades.ReplaceUpgrades(map[string]*upgrades.UpgradeDefinition{
		upgrades.TestnetTimingV1Name:    d,
		upgrades.RewardGovernanceV3Name: {Name: upgrades.RewardGovernanceV3Name, LockedIn: true, AnchorSet: true, ActivateAnchor: g.ActivationLayer},
	})
	builder, importer := s.Copy(), s.Copy()
	engine := New(rawdb.NewMemoryDatabase())
	chain := finalizeTestChainReader{config: c}
	ApplyPreBlockStateChanges(engine, chain, h, builder)
	engine.ApplyPreBlockStateChange(chain, h, importer)
	require.Equal(t, common.BigToHash(h.Number), builder.GetState(params.ChainTallyAddr, testnetTimingAppliedSlot))
	require.Equal(t, builder.IntermediateRoot(false), importer.IntermediateRoot(false))
}

func TestTimingForkCannotRepeatWithChangedCommitment(t *testing.T) {
	c, h, s, _ := timingForkFixture(t)
	require.NoError(t, ApplyTestnetTimingFork(c, h, s))
	before := s.IntermediateRoot(false)
	c.BdagTestnetTiming.NotBeforeTimestamp++
	require.Error(t, ApplyTestnetTimingFork(c, h, s))
	require.Equal(t, before, s.IntermediateRoot(false))
}

func timingForkFixture(t *testing.T) (*ethparams.ChainConfig, *types.Header, *state.StateDB, uint64) {
	t.Helper()
	c, h, s, d, genesis := timingPlanFixture(t)
	for _, account := range testnetGovernanceBootstrapAccounts {
		s.SetCode(account.address, account.code)
		s.SetNonce(account.address, account.nonce)
		for _, slot := range account.storage {
			s.SetState(account.address, slot.key, slot.value)
		}
	}
	c.BdagTestnetTiming.ArtifactSHA256 = common.HexToHash("0xb8d100d8c69746070d02b497ee3e4c2837f9020981d036eff7d5a94ef61a5275")
	old := upgrades.ReplaceUpgrades(map[string]*upgrades.UpgradeDefinition{upgrades.TestnetTimingV1Name: d})
	t.Cleanup(func() { upgrades.ReplaceUpgrades(old) })
	return c, h, s, genesis
}

func TestTimingForkInstallsAtomicallyPreservesHistoryAndReplays(t *testing.T) {
	c, h, s, genesis := timingForkFixture(t)
	// Model live balances and arbitrary user/ballot/exit mapping words beyond
	// the constructor allocation. None may be reset by a runtime replacement.
	key := common.HexToHash("0x8f22001234")
	value := common.HexToHash("0xabcde")
	for _, a := range testnetGovernanceBootstrapAccounts {
		s.SetState(a.address, key, value)
		s.SetBalance(a.address, uint256.NewInt(123456), tracing.BalanceChangeUnspecified)
	}
	parent := s.Copy()
	require.NoError(t, ApplyTestnetTimingFork(c, h, s))
	require.Equal(t, common.BigToHash(h.Number), s.GetState(params.ChainTallyAddr, testnetTimingAppliedSlot))
	clock, err := rewardEpochScheduleFromState(s)
	require.NoError(t, err)
	old, err := clock.Bounds(8)
	require.NoError(t, err)
	require.EqualValues(t, 60, old.Duration)
	next, err := clock.Bounds(9)
	require.NoError(t, err)
	require.EqualValues(t, 1800, next.Duration)
	require.EqualValues(t, genesis+540, next.Start)
	for _, a := range testnetGovernanceBootstrapAccounts {
		require.Equal(t, value, s.GetState(a.address, key))
		require.Equal(t, uint256.NewInt(123456), s.GetBalance(a.address))
		require.Equal(t, a.nonce, s.GetNonce(a.address))
		for _, slot := range a.storage {
			require.Equal(t, slot.value, s.GetState(a.address, slot.key), "preserve %s %s", a.address, slot.key)
		}
	}
	root := s.IntermediateRoot(false)
	require.NoError(t, ApplyTestnetTimingFork(c, h, s))
	require.Equal(t, root, s.IntermediateRoot(false))
	require.NoError(t, ApplyTestnetTimingFork(c, h, parent))
	require.Equal(t, root, parent.IntermediateRoot(false))
	// Reorgs discard the whole candidate state, and rebuilding that parent is deterministic.
	snapshot := parent.Snapshot()
	h.Time = genesis + 2400
	h.Number = big.NewInt(11)
	require.NoError(t, prepareVersionedRewardClock(c, h, parent))
	parent.RevertToSnapshot(snapshot)
	require.Equal(t, root, parent.IntermediateRoot(false))
}

func TestTimingForkRejectsUnexpectedCodeAuthorityAndDestinationsBeforeWrites(t *testing.T) {
	for _, name := range []string{"old code", "new code", "new balance", "new nonce", "owner", "proxy", "genesis", "guardian", "new staking slot", "artifact hash"} {
		t.Run(name, func(t *testing.T) {
			c, h, s, _ := timingForkFixture(t)
			fresh := common.HexToAddress("0x1000000000000000000000000000000000000018")
			switch name {
			case "old code":
				s.SetCode(params.StakingV2ImplAddr, []byte{0xfe})
			case "new code":
				s.SetCode(fresh, []byte{0xfe})
			case "new balance":
				s.SetBalance(fresh, uint256.NewInt(1), tracing.BalanceChangeUnspecified)
			case "new nonce":
				s.SetNonce(fresh, 1)
			case "owner":
				s.SetState(params.StakingContractAddr, common.Hash{}, common.HexToHash("0x1234"))
			case "proxy":
				s.SetState(params.StakingContractAddr, erc1967ImplementationSlot, common.Hash{})
			case "genesis":
				s.SetState(params.StakingContractAddr, common.BigToHash(big.NewInt(8)), common.HexToHash("0x1234"))
			case "guardian":
				s.SetState(params.StakingContractAddr, common.BigToHash(big.NewInt(22)), common.HexToHash("0x1234"))
			case "new staking slot":
				s.SetState(params.StakingContractAddr, common.BigToHash(big.NewInt(41)), common.HexToHash("0x1234"))
			case "artifact hash":
				c.BdagTestnetTiming.ArtifactSHA256 = common.HexToHash("0x01")
			}
			before := s.IntermediateRoot(false)
			require.Error(t, ApplyTestnetTimingFork(c, h, s))
			require.Equal(t, before, s.IntermediateRoot(false))
		})
	}
}

func TestTimingForkBothGatesAndUnscheduledHistoryRemainUntouched(t *testing.T) {
	for _, name := range []string{"no release", "not yet layer", "not yet time", "below threshold"} {
		t.Run(name, func(t *testing.T) {
			c, h, s, _ := timingForkFixture(t)
			switch name {
			case "no release":
				c.BdagTestnetTiming = nil
				c.BdagTestnetTimingBlock = nil
			case "not yet layer":
				h.Extra = EncodeRewardExtraWithLayer(1, 4998)
			case "not yet time":
				h.Time = c.BdagTestnetTiming.NotBeforeTimestamp - 1
			case "below threshold":
				upgrades.ReplaceUpgrades(map[string]*upgrades.UpgradeDefinition{})
			}
			before := s.IntermediateRoot(false)
			require.NoError(t, ApplyTestnetTimingFork(c, h, s))
			require.Equal(t, before, s.IntermediateRoot(false))
		})
	}
}
