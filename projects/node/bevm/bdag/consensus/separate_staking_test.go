package consensus

import (
	"math/big"
	"testing"

	"github.com/BlockdagNetworkLabs/bdag/bevm/params"
	qparams "github.com/BlockdagNetworkLabs/bdag/params"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/core/state"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/holiman/uint256"
	"github.com/stretchr/testify/require"
)

func TestSeparatedStakingRewardDestinationPreservesLegacyBacking(t *testing.T) {
	s, err := state.New(common.Hash{}, state.NewDatabaseForTesting())
	require.NoError(t, err)
	h := &types.Header{Number: big.NewInt(100), Coinbase: common.HexToAddress("0x1234")}
	oldAssets := calculateMappingSlot(h.Coinbase, params.SlotPoolTotalAssetsBase)
	s.SetState(params.StakingContractAddr, oldAssets, common.BigToHash(big.NewInt(1000)))
	require.Equal(t, params.StakingContractAddr, activeStakingAddress(h, s))
	s.SetState(params.ChainTallyAddr, separateStakingAppliedSlot, common.BigToHash(big.NewInt(100)))
	s.SetState(params.ChainTallyAddr, separateStakingActiveAddressSlot, common.BytesToHash(params.SeparateStakingV2Addr.Bytes()))
	s.SetState(params.ChainTallyAddr, separateStakingCutoverEpochPlusSlot, common.BigToHash(big.NewInt(4)))
	s.SetCode(params.SeparateStakingV2Addr, []byte{0x00})
	engine := New(rawdb.NewMemoryDatabase())
	engine.updateAutoStake(s, h, big.NewInt(10))
	require.Equal(t, big.NewInt(1000), s.GetState(params.StakingContractAddr, oldAssets).Big())
	require.Equal(t, big.NewInt(10), s.GetState(params.SeparateStakingV2Addr, oldAssets).Big())
	require.Equal(t, params.SeparateStakingV2Addr, activeStakingAddress(h, s))
	require.Equal(t, params.StakingContractAddr, activeStakingAddress(&types.Header{Number: big.NewInt(99)}, s), "historical resolution cannot follow a future marker")
}

func TestSeparatedStakingRewardPreviewAndChecksAgree(t *testing.T) {
	originalNet := qparams.ActiveNetParams
	qparams.ActiveNetParams = &qparams.TestNetParam
	t.Cleanup(func() { qparams.ActiveNetParams = originalNet })
	s, err := state.New(common.Hash{}, state.NewDatabaseForTesting())
	require.NoError(t, err)
	h := &types.Header{Number: big.NewInt(100), Coinbase: common.HexToAddress("0x1234"), Extra: EncodeRewardExtra(100)}
	s.SetState(params.ChainTallyAddr, separateStakingAppliedSlot, common.BigToHash(big.NewInt(100)))
	s.SetState(params.ChainTallyAddr, separateStakingActiveAddressSlot, common.BytesToHash(params.SeparateStakingV2Addr.Bytes()))
	s.SetState(params.ChainTallyAddr, separateStakingCutoverEpochPlusSlot, common.BigToHash(big.NewInt(4)))
	s.SetCode(params.SeparateStakingV2Addr, []byte{0x00})
	s.SetState(params.SeparateStakingV2Addr, params.SlotMinerSplit, common.BigToHash(big.NewInt(70)))
	s.SetState(params.SeparateStakingV2Addr, calculateMappingSlot(h.Coinbase, params.SlotPoolTotalAssetsBase), common.BigToHash(big.NewInt(1)))
	// Ensure the restricted-value checker sees the exact active address, never V1.
	var destinations []common.Address
	engine := New(rawdb.NewMemoryDatabase())
	require.NoError(t, engine.CheckRewardCredits(nil, h, s, func(to common.Address, _ *uint256.Int) error { destinations = append(destinations, to); return nil }))
	require.Equal(t, []common.Address{h.Coinbase, params.SeparateStakingV2Addr, params.SeparateStakingV2Addr}, destinations)
	preview := ComputeRewardBreakdown(h, s)
	require.Equal(t, big.NewInt(1), preview.PoolTotalAssets)
	for _, to := range destinations {
		require.NotEqual(t, params.StakingContractAddr, to)
	}
}
