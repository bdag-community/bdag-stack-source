package consensus

import (
	"math/big"

	"github.com/BlockdagNetworkLabs/bdag/bevm/params"
	qparams "github.com/BlockdagNetworkLabs/bdag/params"
	"github.com/ethereum/go-ethereum/core/state"
	"github.com/ethereum/go-ethereum/core/types"
)

// RewardBreakdown previews the reward/staking distribution Finalize would
// apply for header, without mutating state. Diagnostic use only (see
// core/blockchain/evm_state_probe.go).
type RewardBreakdown struct {
	Epoch                     uint64
	ActivationEpoch           uint64
	LegacyPath                bool
	TotalSubsidy              *big.Int
	MinerSplitPct             *big.Int
	LiquidReward              *big.Int
	StakersShare              *big.Int
	AutoStakeReward           *big.Int
	DistributableStakerReward *big.Int
	PoolTotalAssets           *big.Int
	PoolTotalShares           *big.Int
}

func ComputeRewardBreakdown(header *types.Header, statedb *state.StateDB) RewardBreakdown {
	totalSubsidy := big.NewInt(0)
	if RewardExtraRequired(header.Number) {
		if v, err := RewardExtraToWei(header.Extra); err == nil {
			totalSubsidy = v
		}
	}

	bd := RewardBreakdown{
		Epoch:           stakingEpochForHeader(header),
		ActivationEpoch: qparams.ActiveNetParams.StakingActivationEpoch,
		TotalSubsidy:    totalSubsidy,
	}

	if bd.Epoch < bd.ActivationEpoch {
		bd.LegacyPath = true
		return bd
	}

	minerSplit := minerSplitFromState(header, statedb)
	bd.MinerSplitPct = minerSplit

	liquidReward := new(big.Int).Mul(totalSubsidy, minerSplit)
	liquidReward.Div(liquidReward, big.NewInt(100))
	bd.LiquidReward = liquidReward

	stakersShare := new(big.Int).Sub(totalSubsidy, liquidReward)
	bd.StakersShare = stakersShare

	autoStakeReward := new(big.Int).Mul(stakersShare, big.NewInt(int64(params.AutoStakePct)))
	autoStakeReward.Div(autoStakeReward, big.NewInt(100))
	bd.AutoStakeReward = autoStakeReward

	bd.DistributableStakerReward = new(big.Int).Sub(stakersShare, autoStakeReward)

	poolAddr := header.Coinbase
	poolTotalAssetsSlot := calculateMappingSlot(poolAddr, params.SlotPoolTotalAssetsBase)
	if v := statedb.GetState(activeStakingAddress(header, statedb), poolTotalAssetsSlot).Big(); v != nil {
		bd.PoolTotalAssets = v
	} else {
		bd.PoolTotalAssets = big.NewInt(0)
	}

	poolTotalSharesSlot := calculateMappingSlot(poolAddr, params.SlotPoolTotalSharesBase)
	if v := statedb.GetState(activeStakingAddress(header, statedb), poolTotalSharesSlot).Big(); v != nil {
		bd.PoolTotalShares = v
	} else {
		bd.PoolTotalShares = big.NewInt(0)
	}

	return bd
}
