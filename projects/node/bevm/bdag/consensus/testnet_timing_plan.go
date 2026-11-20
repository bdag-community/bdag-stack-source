package consensus

import (
	"fmt"
	"math"

	"github.com/BlockdagNetworkLabs/bdag/bevm/params"
	"github.com/BlockdagNetworkLabs/bdag/common/epochclock"
	qparams "github.com/BlockdagNetworkLabs/bdag/params"
	"github.com/BlockdagNetworkLabs/bdag/upgrades"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	ethparams "github.com/ethereum/go-ethereum/params"
)

// planTestnetTimingClock is a read-only preflight for the uninstalled candidate.
// A non-nil result is NOT installation authority by itself: the atomic writer
// must additionally validate every pinned old/new runtime, immutable binding,
// protected storage word and empty destination before committing any write.
// Builders and importers reach it through ApplyTestnetTimingFork. A release
// manifest remains unset until full candidate qualification is complete.
func planTestnetTimingClock(config *ethparams.ChainConfig, header *types.Header, s epochScheduleState, definition *upgrades.UpgradeDefinition) (*epochclock.Segment, error) {
	if config == nil || (config.BdagTestnetTimingBlock == nil && config.BdagTestnetTiming == nil) {
		return nil, nil
	}
	policy := config.BdagTestnetTiming
	if err := policy.Validate(config.ChainID); err != nil {
		return nil, err
	}
	if qparams.ActiveNetParams == nil || qparams.ActiveNetParams.Net != qparams.TestNetParams.Net {
		return nil, fmt.Errorf("timing correction requires native Testnet")
	}
	if config.BdagTestnetTimingBlock == nil || config.BdagTestnetTimingBlock.Sign() <= 0 ||
		header == nil || header.Number == nil || header.Number.Sign() <= 0 || header.Number.BitLen() > 256 {
		return nil, fmt.Errorf("invalid timing correction block configuration")
	}
	if header.Number.Cmp(config.BdagTestnetTimingBlock) < 0 {
		return nil, nil
	}
	if definition == nil {
		return nil, nil
	} // no qualifying native result yet
	if definition.Name != upgrades.TestnetTimingV1Name || definition.BitMask != upgrades.TestnetTimingV1BitMask ||
		definition.StartLayer != policy.SignalStartLayer || definition.TimeoutLayer != policy.SignalStartLayer+2000 ||
		definition.WindowSize != 2000 || definition.Threshold != 1500 || definition.ActivationDelayBlocks != 2000 {
		return nil, fmt.Errorf("native timing definition does not match immutable policy")
	}
	if !definition.LockedIn && !definition.AnchorSet && !definition.Active && definition.ActivateAnchor == 0 {
		return nil, nil
	}
	if !definition.LockedIn || !definition.AnchorSet || definition.ActivateAnchor != policy.ActivationLayer {
		return nil, fmt.Errorf("native timing lock-in or anchor is inconsistent")
	}
	_, layer, err := DecodeRewardExtraWithLayer(header.Extra)
	if err != nil {
		return nil, fmt.Errorf("timing native-layer binding: %w", err)
	}
	if layer < policy.ActivationLayer || header.Time < policy.NotBeforeTimestamp {
		return nil, nil
	}
	if s == nil {
		return nil, fmt.Errorf("timing correction requires parent state")
	}
	bootstrapBlock := s.GetState(params.ChainTallyAddr, governanceAppliedSlot).Big()
	if config.BdagGovernanceForkBlock == nil || config.BdagGovernanceFork == nil ||
		header.Number.Cmp(config.BdagGovernanceForkBlock) < 0 || bootstrapBlock.Sign() == 0 || bootstrapBlock.Cmp(header.Number) >= 0 {
		return nil, fmt.Errorf("timing correction requires a prior governance bootstrap")
	}
	// The first correction cannot overwrite a partial/mixed clock installation.
	// Repeated invocation after successful installation belongs to the atomic
	// writer's separate marker/commitment validation, not a new segment append.
	for _, slot := range []common.Hash{testnetTimingAppliedSlot, params.SlotRewardEpochScheduleLength, params.SlotGovernanceTimeFloor} {
		if s.GetState(params.ChainTallyAddr, slot) != (common.Hash{}) {
			return nil, fmt.Errorf("timing destination is not empty at %s", slot)
		}
	}
	for i := int64(0); i < 6; i++ {
		if s.GetState(params.ChainTallyAddr, rewardScheduleWordSlot(i)) != (common.Hash{}) {
			return nil, fmt.Errorf("timing segment destination is not empty")
		}
	}
	var words [3]uint64
	for i, slot := range []common.Hash{params.SlotRewardVotingEpoch, params.SlotRewardVotingBootstrapEpochPlus, params.SlotRewardVotingFirstEpochPlus} {
		value := s.GetState(params.ChainTallyAddr, slot).Big()
		if value.BitLen() > 64 {
			return nil, epochclock.ErrOverflow
		}
		words[i] = value.Uint64()
	}
	current, bootstrapPlus, firstPlus := words[0], words[1], words[2]
	if current == math.MaxUint64 || bootstrapPlus == 0 || bootstrapPlus-1 > current ||
		(firstPlus != 0 && (firstPlus <= bootstrapPlus || firstPlus > current)) {
		return nil, fmt.Errorf("invalid existing reward eligibility markers")
	}
	genesis := uint64(qparams.TestNetParams.GenesisBlock.Block().Header.Timestamp.Unix())
	origin := epochclock.Segment{FirstEpoch: 0, StartTime: genesis, Duration: 60}
	old, err := epochclock.New(origin)
	if err != nil {
		return nil, err
	}
	status, err := old.At(header.Time, current)
	if err != nil {
		return nil, err
	}
	if status.Epoch == math.MaxUint64 {
		return nil, epochclock.ErrOverflow
	}
	segment := epochclock.Segment{FirstEpoch: status.Epoch + 1, StartTime: status.End, Duration: policy.EpochSeconds}
	if _, err := epochclock.New(origin, segment); err != nil {
		return nil, err
	}
	return &segment, nil
}
