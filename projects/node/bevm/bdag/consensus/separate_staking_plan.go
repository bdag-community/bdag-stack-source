package consensus

import (
	"fmt"
	"github.com/BlockdagNetworkLabs/bdag/bevm/params"
	qparams "github.com/BlockdagNetworkLabs/bdag/params"
	"github.com/BlockdagNetworkLabs/bdag/upgrades"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/state"
	"github.com/ethereum/go-ethereum/core/types"
	ethparams "github.com/ethereum/go-ethereum/params"
	"math"
	"math/big"
)

// Read-only: first block of a new reward epoch after both immutable gates.
// The parent epoch floor proves no rewards from that epoch went to V1.
func planSeparateStaking(c *ethparams.ChainConfig, h *types.Header, s *state.StateDB, d *upgrades.UpgradeDefinition) (uint64, bool, error) {
	if c == nil || (c.BdagSeparateStakingBlock == nil && c.BdagSeparateStaking == nil) {
		return 0, false, nil
	}
	p := c.BdagSeparateStaking
	if err := p.Validate(c.ChainID); err != nil {
		return 0, false, err
	}
	if qparams.ActiveNetParams == nil || qparams.ActiveNetParams.Net != qparams.TestNetParams.Net {
		return 0, false, fmt.Errorf("separate staking requires native Testnet")
	}
	if c.BdagSeparateStakingBlock == nil || c.BdagSeparateStakingBlock.Sign() <= 0 || h == nil || h.Number == nil || h.Number.Sign() <= 0 || h.Number.BitLen() > 256 {
		return 0, false, fmt.Errorf("invalid separate staking EVM boundary/header")
	}
	if h.Number.Cmp(c.BdagSeparateStakingBlock) < 0 || d == nil {
		return 0, false, nil
	}
	if d.Name != upgrades.SeparateStakingV1Name || d.BitMask != upgrades.SeparateStakingV1BitMask || d.StartLayer != p.SignalStartLayer || d.TimeoutLayer != p.SignalStartLayer+2000 || d.WindowSize != 2000 || d.Threshold != 1500 || d.ActivationDelayBlocks != 2000 {
		return 0, false, fmt.Errorf("separate staking definition differs from sealed policy")
	}
	if !d.LockedIn && !d.AnchorSet && !d.Active && d.ActivateAnchor == 0 {
		return 0, false, nil
	}
	if !d.LockedIn || !d.AnchorSet || d.ActivateAnchor != p.ActivationLayer {
		return 0, false, fmt.Errorf("invalid separate staking native anchor")
	}
	_, layer, err := DecodeRewardExtraWithLayer(h.Extra)
	if err != nil {
		return 0, false, err
	}
	if layer < p.ActivationLayer || h.Time < p.NotBeforeTimestamp {
		return 0, false, nil
	}
	if s == nil {
		return 0, false, fmt.Errorf("separate staking needs canonical parent state")
	}
	timing := s.GetState(params.ChainTallyAddr, testnetTimingAppliedSlot).Big()
	if timing.Sign() == 0 || timing.Cmp(h.Number) >= 0 || c.BdagTestnetTiming == nil || c.BdagTestnetTimingBlock == nil {
		return 0, false, fmt.Errorf("separate staking requires prior timing transition")
	}
	bootstrap := s.GetState(params.ChainTallyAddr, governanceAppliedSlot).Big()
	if c.BdagGovernanceFork == nil || c.BdagGovernanceForkBlock == nil ||
		h.Number.Cmp(c.BdagGovernanceForkBlock) < 0 || bootstrap.Sign() == 0 || bootstrap.Cmp(timing) >= 0 {
		return 0, false, fmt.Errorf("separate staking requires prior governance bootstrap")
	}
	clock, err := rewardEpochScheduleFromState(s)
	if err != nil {
		return 0, false, err
	}
	parent := s.GetState(params.ChainTallyAddr, params.SlotRewardVotingEpoch).Big()
	floor := s.GetState(params.ChainTallyAddr, params.SlotGovernanceTimeFloor).Big()
	if parent.BitLen() > 64 || floor.BitLen() > 64 || parent.Uint64() == math.MaxUint64 {
		return 0, false, fmt.Errorf("separate staking clock overflow")
	}
	status, err := clock.At(max(h.Time, floor.Uint64()), parent.Uint64())
	if err != nil {
		return 0, false, err
	}
	if status.Epoch <= parent.Uint64() || status.Start < p.NotBeforeTimestamp {
		return 0, false, nil
	}
	if status.Epoch == math.MaxUint64 || status.Duration != 1800 {
		return 0, false, fmt.Errorf("separate staking requires existing 30-minute policy")
	}
	for _, slot := range []common.Hash{separateStakingAppliedSlot, separateStakingCommitmentSlot, separateStakingActiveAddressSlot, separateStakingCutoverEpochPlusSlot} {
		if s.GetState(params.ChainTallyAddr, slot) != (common.Hash{}) {
			return 0, false, fmt.Errorf("partial or pre-existing staking transition")
		}
	}
	if s.GetState(params.StakingContractAddr, params.SlotGenesisTime) != common.BigToHash(new(big.Int).SetUint64(uint64(qparams.TestNetParams.GenesisBlock.Block().Header.Timestamp.Unix()))) {
		return 0, false, fmt.Errorf("retiring staking genesis mismatch")
	}
	return status.Epoch, true, nil
}
