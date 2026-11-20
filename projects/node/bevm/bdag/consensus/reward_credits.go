package consensus

import (
	"math/big"

	"github.com/BlockdagNetworkLabs/bdag/bevm/params"
	qparams "github.com/BlockdagNetworkLabs/bdag/params"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/consensus"
	"github.com/ethereum/go-ethereum/core/state"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/holiman/uint256"
)

type RewardCreditChecker func(to common.Address, amount *uint256.Int) error

// CheckRewardCredits previews every native reward balance credit Finalize will
// apply, without mutating state.
func (me *bdagEngine) CheckRewardCredits(chain consensus.ChainHeaderReader, header *types.Header, state *state.StateDB, check RewardCreditChecker) error {
	if check == nil {
		return nil
	}
	totalSubsidy := me.OnExtraStateChange(chain, header, state)
	if totalSubsidy == nil {
		totalSubsidy = big.NewInt(0)
	}

	currentEpoch := stakingEpochForHeader(header)
	if currentEpoch < qparams.ActiveNetParams.StakingActivationEpoch {
		return checkRewardCredit(check, header.Coinbase, totalSubsidy)
	}

	minerSplit := minerSplitFromState(header, state)

	liquidReward := new(big.Int).Mul(totalSubsidy, minerSplit)
	liquidReward.Div(liquidReward, big.NewInt(100))
	if err := checkRewardCredit(check, header.Coinbase, liquidReward); err != nil {
		return err
	}

	stakersShare := new(big.Int).Sub(totalSubsidy, liquidReward)
	autoStakeReward := new(big.Int).Mul(stakersShare, big.NewInt(int64(params.AutoStakePct)))
	autoStakeReward.Div(autoStakeReward, big.NewInt(100))
	if err := checkRewardCredit(check, activeStakingAddress(header, state), autoStakeReward); err != nil {
		return err
	}

	distributableStakerReward := new(big.Int).Sub(stakersShare, autoStakeReward)
	poolTotalAssetsSlot := calculateMappingSlot(header.Coinbase, params.SlotPoolTotalAssetsBase)
	poolTotalAssets := state.GetState(activeStakingAddress(header, state), poolTotalAssetsSlot).Big()
	if poolTotalAssets != nil && poolTotalAssets.Sign() > 0 {
		if err := checkRewardCredit(check, activeStakingAddress(header, state), distributableStakerReward); err != nil {
			return err
		}
	}
	return nil
}

func checkRewardCredit(check RewardCreditChecker, to common.Address, amount *big.Int) error {
	if amount == nil || amount.Sign() <= 0 {
		return nil
	}
	return check(to, uint256.MustFromBig(amount))
}
