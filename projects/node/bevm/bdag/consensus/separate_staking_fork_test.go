package consensus

import (
	"bytes"
	"encoding/json"
	"github.com/BlockdagNetworkLabs/bdag/bevm/params"
	native "github.com/BlockdagNetworkLabs/bdag/upgrades"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core"
	"github.com/ethereum/go-ethereum/core/state"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/core/vm"
	ethparams "github.com/ethereum/go-ethereum/params"
	"github.com/holiman/uint256"
	"github.com/stretchr/testify/require"
	"math/big"
	"testing"
)

func separatedForkFixture(t *testing.T) (*ethparams.ChainConfig, *types.Header, *state.StateDB) {
	c, h, s, genesis := timingForkFixture(t)
	require.NoError(t, ApplyTestnetTimingFork(c, h, s))
	h.Number = big.NewInt(11)
	h.Time = genesis + 550
	require.NoError(t, prepareVersionedRewardClock(c, h, s))
	c.BdagSeparateStakingBlock = big.NewInt(12)
	c.BdagSeparateStaking = &ethparams.BDAGSeparateStakingForkConfig{Version: 1, ArtifactSHA256: separateStakingArtifactSHA256, SignalStartLayer: 10000, ActivationLayer: 13999, ReleaseDistributedAt: genesis - 86400, NoticePublishedAt: genesis - 86400, NotBeforeTimestamp: genesis}
	defs := native.GetUpgrades()
	defs[native.SeparateStakingV1Name] = &native.UpgradeDefinition{Name: native.SeparateStakingV1Name, BitMask: native.SeparateStakingV1BitMask, StartLayer: 10000, TimeoutLayer: 12000, WindowSize: 2000, Threshold: 1500, ActivationDelayBlocks: 2000, LockedIn: true, AnchorSet: true, ActivateAnchor: 13999}
	native.ReplaceUpgrades(defs)
	h.Number = big.NewInt(12)
	h.Time = genesis + 2340
	h.Extra = EncodeRewardExtraWithLayer(1, 13999)
	return c, h, s
}
func TestSeparateStakingNativeRuntimeInitializationAndSelfServiceDeposit(t *testing.T) {
	c, h, s := separatedForkFixture(t)
	require.NoError(t, ApplySeparateStakingFork(c, h, s))
	h.Difficulty = new(big.Int)
	h.BaseFee = new(big.Int)
	h.GasLimit = 30_000_000
	var b struct {
		Runtimes []struct {
			Address common.Address
			ABI     json.RawMessage
		}
	}
	require.NoError(t, json.Unmarshal(separateStakingBundleJSON, &b))
	abis := map[common.Address]abi.ABI{}
	for _, r := range b.Runtimes {
		a, err := abi.JSON(bytes.NewReader(r.ABI))
		require.NoError(t, err)
		abis[r.Address] = a
	}
	abis[params.SeparateStakingV2Addr] = abis[params.SeparateStakingV2ImplAddr]
	alice := common.HexToAddress("0x1234")
	s.SetBalance(alice, uint256.NewInt(1_000_000), 0)
	call := func(address common.Address, name string, value *uint256.Int, args ...interface{}) ([]byte, error) {
		input, err := abis[address].Pack(name, args...)
		require.NoError(t, err)
		evm := vm.NewEVM(vm.BlockContext{CanTransfer: core.CanTransfer, Transfer: core.Transfer, GetHash: func(uint64) common.Hash { return common.Hash{} }, BlockNumber: h.Number, Time: h.Time, Difficulty: h.Difficulty, BaseFee: h.BaseFee, GasLimit: h.GasLimit}, vm.TxContext{Origin: alice}, s, c, vm.Config{NoBaseFee: true})
		out, _, err := evm.Call(vm.AccountRef(alice), address, input, 10_000_000, value)
		return out, err
	}
	view := func(address common.Address, name string, args ...interface{}) interface{} {
		out, err := call(address, name, uint256.NewInt(0), args...)
		require.NoErrorf(t, err, "%s.%s", address, name)
		values, err := abis[address].Unpack(name, out)
		require.NoError(t, err)
		return values[0]
	}
	require.Equal(t, params.StakingContractAddr, view(params.ChainTallyAddr, "clockSource"))
	require.Equal(t, params.SeparateStakingV2Addr, view(params.ChainTallyAddr, "activeStakingSource"))
	require.Equal(t, timingStakingLockAddress, view(params.SeparateStakingV2Addr, "owner"))
	require.Equal(t, big.NewInt(7200), view(params.SeparateStakingV2Addr, "bondingPeriod"))
	miningCredential := common.HexToAddress("0x5FbDB2315678afecb367f032d93F642f64180aa3")
	domainBytes, err := call(miningCredential, "eip712Domain", uint256.NewInt(0))
	require.NoError(t, err)
	domain, err := abis[miningCredential].Unpack("eip712Domain", domainBytes)
	require.NoError(t, err)
	require.Equal(t, "2", domain[2], "new source generation must invalidate unused old delegation signatures")
	_, err = call(params.SeparateStakingV2Addr, "initialize", uint256.NewInt(0))
	require.Error(t, err)
	_, err = call(params.SeparateStakingV2Addr, "initializeV2", uint256.NewInt(0))
	require.Error(t, err)
	_, err = call(params.SeparateStakingV2Addr, "transferOwnership", uint256.NewInt(0), alice)
	require.Error(t, err)
	// User deposits are self-service; the DAO timelock is not a transaction co-signer.
	amount := big.NewInt(1_000_000_000_000_000)
	s.SetBalance(alice, uint256.MustFromBig(new(big.Int).Mul(amount, big.NewInt(2))), 0)
	_, err = call(params.SeparateStakingV2Addr, "stake", uint256.MustFromBig(amount), alice, amount, common.HexToAddress("0x4567"))
	require.NoError(t, err)
	require.Equal(t, amount, view(params.SeparateStakingV2Addr, "stakerPrincipalBDAG", alice, common.HexToAddress("0x4567")))
	require.Equal(t, new(big.Int).SetUint64(h.Time+7200), view(params.SeparateStakingV2Addr, "bondingEndsAt", alice))
}
func TestSeparateStakingAtomicInstallationPreservesOldFundsAndStorage(t *testing.T) {
	c, h, s := separatedForkFixture(t)
	arbitrary := common.HexToHash("0x712345")
	value := common.HexToHash("0xafe")
	s.SetState(params.StakingContractAddr, arbitrary, value)
	s.SetBalance(params.StakingContractAddr, uint256.NewInt(123456), 0)
	old := s.Copy()
	legacyCode := s.GetCodeHash(params.StakingContractAddr)
	require.NoError(t, ApplySeparateStakingFork(c, h, s))
	require.Equal(t, value, s.GetState(params.StakingContractAddr, arbitrary))
	require.Equal(t, legacyCode, s.GetCodeHash(params.StakingContractAddr))
	require.Equal(t, uint256.NewInt(123456), s.GetBalance(params.StakingContractAddr))
	require.Equal(t, timingStakingLockAddress, common.BytesToAddress(s.GetState(params.SeparateStakingV2Addr, common.Hash{}).Bytes()))
	require.Zero(t, s.GetBalance(params.SeparateStakingV2Addr).Sign())
	require.Equal(t, common.BigToHash(big.NewInt(11)), s.GetState(params.ChainTallyAddr, separateStakingCutoverEpochPlusSlot))
	require.Equal(t, params.SeparateStakingV2Addr, activeStakingAddress(h, s))
	root := s.IntermediateRoot(false)
	require.NoError(t, ApplySeparateStakingFork(c, h, s))
	require.Equal(t, root, s.IntermediateRoot(false))
	require.NoError(t, ApplySeparateStakingFork(c, h, old))
	require.Equal(t, root, old.IntermediateRoot(false), "independent replay must produce identical roots")
}
func TestSeparateStakingRejectsOccupiedDestinationBeforeAnyWrite(t *testing.T) {
	c, h, s := separatedForkFixture(t)
	s.SetNonce(params.SeparateStakingV2Addr, 1)
	before := s.IntermediateRoot(false)
	require.Error(t, ApplySeparateStakingFork(c, h, s))
	require.Equal(t, before, s.IntermediateRoot(false))
}

func TestSeparateStakingAppliedManifestCannotBeMovedToAFutureEVMFloor(t *testing.T) {
	c, h, s := separatedForkFixture(t)
	require.NoError(t, ApplySeparateStakingFork(c, h, s))
	before := s.IntermediateRoot(false)
	c.BdagSeparateStakingBlock = new(big.Int).Add(h.Number, big.NewInt(1))
	require.Error(t, ApplySeparateStakingFork(c, h, s), "a changed manifest must not escape validation behind its new future floor")
	require.Equal(t, before, s.IntermediateRoot(false))
}

func TestSeparateStakingRejectsMissingOrFutureGovernanceBootstrap(t *testing.T) {
	for _, marker := range []common.Hash{{}, common.BigToHash(big.NewInt(12))} {
		c, h, s := separatedForkFixture(t)
		s.SetState(params.ChainTallyAddr, governanceAppliedSlot, marker)
		before := s.IntermediateRoot(false)
		require.Error(t, ApplySeparateStakingFork(c, h, s))
		require.Equal(t, before, s.IntermediateRoot(false))
	}
}
