package consensus

import (
	"github.com/BlockdagNetworkLabs/bdag/bevm/params"
	qparams "github.com/BlockdagNetworkLabs/bdag/params"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/state"
	"github.com/ethereum/go-ethereum/core/types"
)

const mainnetStakingEpochDurationSeconds uint64 = 604800

func stakingEpochForHeader(header *types.Header) uint64 {
	if header == nil || header.Number == nil {
		return 0
	}

	if qparams.ActiveNetParams == nil {
		return 0
	}
	if qparams.ActiveNetParams.Net == qparams.MainNetParams.Net {
		return timestampStakingEpochForHeader(header, mainnetStakingEpochDurationSeconds)
	}

	if qparams.ActiveNetParams.StakingEpochDurationSeconds > 0 {
		return timestampStakingEpochForHeader(header, uint64(qparams.ActiveNetParams.StakingEpochDurationSeconds))
	}

	if qparams.ActiveNetParams.SubsidyReductionInterval <= 0 {
		return 0
	}
	return header.Number.Uint64() / uint64(qparams.ActiveNetParams.SubsidyReductionInterval)
}

func timestampStakingEpochForHeader(header *types.Header, durationSeconds uint64) uint64 {
	if durationSeconds == 0 || qparams.ActiveNetParams == nil {
		return 0
	}
	genesisTime := uint64(qparams.ActiveNetParams.GenesisBlock.Block().Header.Timestamp.Unix())
	if header.Time <= genesisTime {
		return 0
	}
	return (header.Time - genesisTime) / durationSeconds
}

func chainTallyEpochForHeader(header *types.Header) uint64 {
	return stakingEpochForHeader(header)
}

// rewardVotingEpochForHeader is used only by the separately activation-gated
// reward ledgers. Never use it to index historical tallies or financial locks.
// Positive native timestamps can decrease, but a closed reward epoch cannot
// receive new rewards. Both chambers use the same persisted voting floor.
func rewardVotingEpochForHeader(header *types.Header, statedb *state.StateDB) uint64 {
	if header == nil || header.Number == nil || qparams.ActiveNetParams == nil {
		return 0
	}
	// The pre-block hook validates and advances an installed versioned clock
	// before contract transactions or either reward chamber can observe it.
	if statedb != nil && (statedb.GetState(params.ChainTallyAddr, testnetTimingAppliedSlot) != (common.Hash{}) ||
		statedb.GetState(params.ChainTallyAddr, mainnetDAOAppliedSlot) != (common.Hash{})) {
		return statedb.GetState(params.ChainTallyAddr, params.SlotRewardVotingEpoch).Big().Uint64()
	}
	epoch := stakingEpochForHeader(header)
	if qparams.ActiveNetParams.Net == qparams.TestNetParams.Net {
		epoch = timestampStakingEpochForHeader(header, 60)
	}
	if statedb != nil {
		floor := statedb.GetState(params.ChainTallyAddr, params.SlotRewardVotingEpoch).Big().Uint64()
		if floor > epoch {
			epoch = floor
		}
	}
	return epoch
}
