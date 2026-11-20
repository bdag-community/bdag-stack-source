package consensus

// Diagnostic-only entry point used by cmd/splitsweep to recover the historical
// epoch-19 miner split. Mirrors Finalize exactly, but takes the miner split as
// a parameter instead of reading it from contract state, and optionally
// replays the pre-activation (legacy 100%-to-miner) path.
//
// TODO: delete together with cmd/splitsweep once the investigation is done.

import (
	"math/big"

	"github.com/BlockdagNetworkLabs/bdag/bevm/params"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/consensus"
	"github.com/ethereum/go-ethereum/core/state"
	"github.com/ethereum/go-ethereum/core/tracing"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/holiman/uint256"
)

func (me *bdagEngine) FinalizeWithMinerSplitOverride(chain consensus.ChainHeaderReader, header *types.Header, state *state.StateDB, minerSplit *big.Int, legacy bool) {
	totalSubsidy := me.OnExtraStateChange(chain, header, state)
	if totalSubsidy == nil {
		totalSubsidy = big.NewInt(0)
	}

	if legacy {
		// Pre-activation path: 100% to miner, tallies only.
		if totalSubsidy.Sign() > 0 {
			state.AddBalance(header.Coinbase, uint256.MustFromBig(totalSubsidy), tracing.BalanceIncreaseRewardMineBlock)
		}
		me.updateStakingTallies(chain, header, state, totalSubsidy)
		header.Root = state.IntermediateRoot(chain.Config().IsEIP158(header.Number))
		return
	}

	liquidReward := new(big.Int).Mul(totalSubsidy, minerSplit)
	liquidReward.Div(liquidReward, big.NewInt(100))

	stakersShare := new(big.Int).Sub(totalSubsidy, liquidReward)

	if liquidReward.Sign() > 0 {
		state.AddBalance(header.Coinbase, uint256.MustFromBig(liquidReward), tracing.BalanceIncreaseRewardMineBlock)
	}

	poolAddr := header.Coinbase
	poolTotalAssetsSlot := calculateMappingSlot(poolAddr, params.SlotPoolTotalAssetsBase)
	poolTotalAssets := state.GetState(activeStakingAddress(header, state), poolTotalAssetsSlot).Big()
	if poolTotalAssets == nil {
		poolTotalAssets = big.NewInt(0)
	}

	var distributableStakerReward *big.Int = big.NewInt(0)

	autoStakeReward := new(big.Int).Mul(stakersShare, big.NewInt(int64(params.AutoStakePct)))
	autoStakeReward.Div(autoStakeReward, big.NewInt(100))

	state.AddBalance(activeStakingAddress(header, state), uint256.MustFromBig(autoStakeReward), tracing.BalanceIncreaseRewardMineBlock)

	if autoStakeReward.Sign() > 0 {
		me.updateAutoStake(state, header, autoStakeReward)
	}

	distributableStakerReward.Sub(stakersShare, autoStakeReward)

	poolTotalAssetsSlot = calculateMappingSlot(poolAddr, params.SlotPoolTotalAssetsBase)
	poolTotalAssets = state.GetState(activeStakingAddress(header, state), poolTotalAssetsSlot).Big()
	if poolTotalAssets == nil {
		poolTotalAssets = big.NewInt(0)
	}

	if poolTotalAssets.Sign() > 0 && distributableStakerReward.Sign() > 0 {
		newTotalAssets := new(big.Int).Add(poolTotalAssets, distributableStakerReward)
		state.SetState(activeStakingAddress(header, state), poolTotalAssetsSlot, common.BigToHash(newTotalAssets))
		state.AddBalance(activeStakingAddress(header, state), uint256.MustFromBig(distributableStakerReward), tracing.BalanceIncreaseRewardMineBlock)
	}

	me.updateStakingTallies(chain, header, state, totalSubsidy)

	header.Root = state.IntermediateRoot(chain.Config().IsEIP158(header.Number))
}
