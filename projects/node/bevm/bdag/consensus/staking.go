package consensus

import (
	"math/big"

	"github.com/BlockdagNetworkLabs/bdag/bevm/params"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/consensus"
	"github.com/ethereum/go-ethereum/core/state"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
)

var rewardVotingDomain = crypto.Keccak256Hash([]byte("bdag.reward.voting.v1"))

const stakingRewardAccumulatorScale int64 = 1_000_000

func (me *bdagEngine) updateStakingTallies(chain consensus.ChainHeaderReader, header *types.Header, statedb *state.StateDB, rewardAmount *big.Int) {
	// 1. Update Current Tally for the Miner
	minerAddr := header.Coinbase
	res := statedb.GetState(params.ChainTallyAddr, params.SlotCurrentEpoch)
	currentEpoch := res.Big()

	// Get the Current Tally Base Hash from storage
	currentBaseHash := statedb.GetState(params.ChainTallyAddr, params.SlotCurrentTallyBase)

	// If currentBaseHash is empty (uninitialized), this is the very first time we are tallying.
	// We must initialize the current epoch and base hash dynamically!
	if currentBaseHash == (common.Hash{}) {
		// If staking code is deployed mid-network, calculate current mathematical epoch.
		initialEpoch := chainTallyEpochForHeader(header)
		currentEpoch = new(big.Int).SetUint64(initialEpoch)

		// Immediately write this calculated epoch to the Chain Tally
		statedb.SetState(params.ChainTallyAddr, params.SlotCurrentEpoch, common.BigToHash(currentEpoch))

		currentBaseHash = crypto.Keccak256Hash(currentEpoch.Bytes())
		statedb.SetState(params.ChainTallyAddr, params.SlotCurrentTallyBase, currentBaseHash)
	}

	// Prevent EIP-161 State Clearing from wiping the Tally Address.
	// Since the address has 0 balance and no code, a Nonce of 0 makes it "empty".
	if statedb.GetNonce(params.ChainTallyAddr) == 0 {
		statedb.SetNonce(params.ChainTallyAddr, 1)
	}

	// Calculate storage slot for miner's tally in current epoch
	// Slot = Keccak256(MinerAddr | currentBaseHash)
	tallySlot := calculateMappingSlot(minerAddr, currentBaseHash)

	// Get current tally and increment by Reward Amount (Wei)
	currentTally := statedb.GetState(params.ChainTallyAddr, tallySlot).Big()
	if currentTally == nil {
		currentTally = big.NewInt(0)
	}
	newTally := new(big.Int).Add(currentTally, rewardAmount)
	statedb.SetState(params.ChainTallyAddr, tallySlot, common.BigToHash(newTally))

	// 2. Check for Epoch Transition
	expectedEpoch := chainTallyEpochForHeader(header)

	if currentEpoch.Uint64() < expectedEpoch {
		me.handleEpochTransition(statedb, new(big.Int).SetUint64(expectedEpoch))
	}
}

func (me *bdagEngine) handleEpochTransition(statedb *state.StateDB, targetEpoch *big.Int) {
	// 1. Rotate Base Slots (the "Update Base Slots" strategy)
	// This makes the "Current" mapping become the "Last" mapping instantly
	currentBaseHash := statedb.GetState(params.ChainTallyAddr, params.SlotCurrentTallyBase)
	statedb.SetState(params.ChainTallyAddr, params.SlotLastTallyBase, currentBaseHash)

	// 2. Set Target Epoch
	statedb.SetState(params.ChainTallyAddr, params.SlotCurrentEpoch, common.BigToHash(targetEpoch))

	// 3. Generate a new base slot for the new epoch to "reset" current tallies
	// Any future lookups for "Current" tallies will now use this new base, which is empty.
	newBaseHash := crypto.Keccak256Hash(targetEpoch.Bytes())
	statedb.SetState(params.ChainTallyAddr, params.SlotCurrentTallyBase, newBaseHash)
}

func calculateMappingSlot(addr common.Address, baseSlot common.Hash) common.Hash {
	b := make([]byte, 64)
	copy(b[12:32], addr.Bytes())
	copy(b[32:], baseSlot.Bytes())
	return crypto.Keccak256Hash(b)
}

func calculateDoubleMappingSlot(outer, inner common.Address, baseSlot common.Hash) common.Hash {
	return calculateMappingSlot(inner, calculateMappingSlot(outer, baseSlot))
}

func calculateUintMappingSlot(key uint64, baseSlot common.Hash) common.Hash {
	b := make([]byte, 64)
	keyBytes := new(big.Int).SetUint64(key).Bytes()
	copy(b[32-len(keyBytes):32], keyBytes)
	copy(b[32:], baseSlot.Bytes())
	return crypto.Keccak256Hash(b)
}

func calculateAddressUintMappingSlot(account common.Address, key uint64, baseSlot common.Hash) common.Hash {
	return calculateUintMappingSlot(key, calculateMappingSlot(account, baseSlot))
}

// updateStakingRewardVotingLedger allocates native pool yield in exact scaled
// share units. The residual numerator is carried forward so no credited yield
// can be counted twice or silently rounded into voting power.
func (me *bdagEngine) updateStakingRewardVotingLedger(header *types.Header, statedb *state.StateDB, earned *big.Int) {
	if header == nil || statedb == nil || earned == nil || earned.Sign() <= 0 {
		return
	}
	pool := header.Coinbase
	totalShares := statedb.GetState(activeStakingAddress(header, statedb), calculateMappingSlot(pool, params.SlotPoolTotalSharesBase)).Big()
	if totalShares.Sign() == 0 {
		return
	}
	remainderSlot := calculateMappingSlot(pool, params.SlotPoolRewardRemainderBase)
	remainder := statedb.GetState(activeStakingAddress(header, statedb), remainderSlot).Big()
	numerator := new(big.Int).Mul(earned, big.NewInt(stakingRewardAccumulatorScale))
	numerator.Add(numerator, remainder)
	delta, nextRemainder := new(big.Int), new(big.Int)
	delta.QuoRem(numerator, totalShares, nextRemainder)
	statedb.SetState(activeStakingAddress(header, statedb), remainderSlot, common.BigToHash(nextRemainder))
	if delta.Sign() == 0 {
		return
	}
	epoch := rewardVotingEpochForHeader(header, statedb)
	poolEpochSlot := calculateAddressUintMappingSlot(pool, epoch, params.SlotPoolEpochRewardPerShareBase)
	poolAccumulator := statedb.GetState(activeStakingAddress(header, statedb), poolEpochSlot).Big()
	poolAccumulator.Add(poolAccumulator, delta)
	statedb.SetState(activeStakingAddress(header, statedb), poolEpochSlot, common.BigToHash(poolAccumulator))

	units := new(big.Int).Mul(totalShares, delta)
	totalSlot := calculateUintMappingSlot(epoch, params.SlotStakingEpochVotingTotalBase)
	total := statedb.GetState(activeStakingAddress(header, statedb), totalSlot).Big()
	total.Add(total, units)
	statedb.SetState(activeStakingAddress(header, statedb), totalSlot, common.BigToHash(total))

	operatorLiquid := statedb.GetState(activeStakingAddress(header, statedb), calculateDoubleMappingSlot(pool, pool, params.SlotStakerPoolSharesBase)).Big()
	operatorLocked := statedb.GetState(activeStakingAddress(header, statedb), calculateDoubleMappingSlot(pool, pool, params.SlotStakerTotalLockedSharesBase)).Big()
	operatorShares := new(big.Int).Add(operatorLiquid, operatorLocked)
	if operatorShares.Sign() > 0 {
		operatorSlot := calculateAddressUintMappingSlot(pool, epoch, params.SlotOperatorEpochVotingBase)
		operatorUnits := statedb.GetState(activeStakingAddress(header, statedb), operatorSlot).Big()
		operatorUnits.Add(operatorUnits, new(big.Int).Mul(operatorShares, delta))
		statedb.SetState(activeStakingAddress(header, statedb), operatorSlot, common.BigToHash(operatorUnits))
	}
}

func stakingPoolEpochRewardPerShare(statedb *state.StateDB, pool common.Address, epoch uint64) *big.Int {
	return statedb.GetState(stakingAddressForRewardEpoch(statedb, epoch), calculateAddressUintMappingSlot(pool, epoch, params.SlotPoolEpochRewardPerShareBase)).Big()
}

func stakingEpochVotingTotal(statedb *state.StateDB, epoch uint64) *big.Int {
	return statedb.GetState(stakingAddressForRewardEpoch(statedb, epoch), calculateUintMappingSlot(epoch, params.SlotStakingEpochVotingTotalBase)).Big()
}

func operatorEpochVotingWeight(statedb *state.StateDB, operator common.Address, epoch uint64) *big.Int {
	return statedb.GetState(stakingAddressForRewardEpoch(statedb, epoch), calculateAddressUintMappingSlot(operator, epoch, params.SlotOperatorEpochVotingBase)).Big()
}

// updateRewardVotingLedger records only rewards actually earned by the mining
// chamber: the liquid coinbase payment plus its locked operator auto-stake.
// It is deliberately independent from the historical full-subsidy ChainTally.
func (me *bdagEngine) updateRewardVotingLedger(header *types.Header, statedb *state.StateDB, earned *big.Int) {
	if header == nil || statedb == nil || earned == nil || earned.Sign() < 0 {
		return
	}
	epoch := rewardVotingEpochForHeader(header, statedb)
	advanceRewardVotingEpoch(statedb, epoch)
	if earned.Sign() == 0 {
		return
	}
	weightSlot := rewardVotingWeightSlot(header.Coinbase, epoch)
	weight := statedb.GetState(params.ChainTallyAddr, weightSlot).Big()
	weight.Add(weight, earned)
	statedb.SetState(params.ChainTallyAddr, weightSlot, common.BigToHash(weight))
	totalSlot := rewardVotingTotalSlot(epoch)
	total := statedb.GetState(params.ChainTallyAddr, totalSlot).Big()
	total.Add(total, earned)
	statedb.SetState(params.ChainTallyAddr, totalSlot, common.BigToHash(total))
}

func advanceRewardVotingEpoch(statedb *state.StateDB, epoch uint64) {
	bootstrapPlus := statedb.GetState(params.ChainTallyAddr, params.SlotRewardVotingBootstrapEpochPlus).Big()
	if bootstrapPlus.Sign() == 0 {
		statedb.SetState(params.ChainTallyAddr, params.SlotRewardVotingEpoch, common.BigToHash(new(big.Int).SetUint64(epoch)))
		statedb.SetState(params.ChainTallyAddr, params.SlotRewardVotingBootstrapEpochPlus, common.BigToHash(new(big.Int).SetUint64(epoch+1)))
	} else {
		current := statedb.GetState(params.ChainTallyAddr, params.SlotRewardVotingEpoch).Big().Uint64()
		if epoch > current {
			bootstrap := bootstrapPlus.Uint64() - 1
			firstPlus := statedb.GetState(params.ChainTallyAddr, params.SlotRewardVotingFirstEpochPlus).Big()
			if firstPlus.Sign() == 0 && current > bootstrap {
				statedb.SetState(params.ChainTallyAddr, params.SlotRewardVotingFirstEpochPlus, common.BigToHash(new(big.Int).SetUint64(current+1)))
			}
			statedb.SetState(params.ChainTallyAddr, params.SlotRewardVotingEpoch, common.BigToHash(new(big.Int).SetUint64(epoch)))
		}
	}
}

func rewardVotingEpochBase(epoch uint64) common.Hash {
	return crypto.Keccak256Hash(rewardVotingDomain.Bytes(), common.LeftPadBytes(new(big.Int).SetUint64(epoch).Bytes(), 32))
}

func rewardVotingWeightSlot(miner common.Address, epoch uint64) common.Hash {
	return calculateMappingSlot(miner, rewardVotingEpochBase(epoch))
}

func rewardVotingTotalSlot(epoch uint64) common.Hash {
	return calculateUintMappingSlot(epoch, params.SlotRewardVotingTotalBase)
}

func rewardVotingWeight(statedb *state.StateDB, miner common.Address, epoch uint64) *big.Int {
	return statedb.GetState(params.ChainTallyAddr, rewardVotingWeightSlot(miner, epoch)).Big()
}

func rewardVotingTotal(statedb *state.StateDB, epoch uint64) *big.Int {
	return statedb.GetState(params.ChainTallyAddr, rewardVotingTotalSlot(epoch)).Big()
}

func rewardVotingEpochFinalized(statedb *state.StateDB, epoch uint64) bool {
	firstPlus := statedb.GetState(params.ChainTallyAddr, params.SlotRewardVotingFirstEpochPlus).Big()
	if firstPlus.Sign() == 0 || epoch+1 < firstPlus.Uint64() {
		return false
	}
	return epoch < statedb.GetState(params.ChainTallyAddr, params.SlotRewardVotingEpoch).Big().Uint64()
}
