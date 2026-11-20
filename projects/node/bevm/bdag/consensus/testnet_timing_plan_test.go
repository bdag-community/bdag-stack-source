package consensus

import (
	"math"
	"math/big"
	"testing"

	"github.com/BlockdagNetworkLabs/bdag/bevm/params"
	qparams "github.com/BlockdagNetworkLabs/bdag/params"
	"github.com/BlockdagNetworkLabs/bdag/upgrades"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/state"
	"github.com/ethereum/go-ethereum/core/types"
	ethparams "github.com/ethereum/go-ethereum/params"
	"github.com/stretchr/testify/require"
)

func timingPlanFixture(t *testing.T) (*ethparams.ChainConfig, *types.Header, *state.StateDB, *upgrades.UpgradeDefinition, uint64) {
	t.Helper()
	s, _, chain, genesis := versionedClockFixture(t)
	s.SetState(params.ChainTallyAddr, testnetTimingAppliedSlot, common.Hash{})
	s.SetState(params.ChainTallyAddr, params.SlotRewardEpochScheduleLength, common.Hash{})
	for i := int64(0); i < 6; i++ {
		s.SetState(params.ChainTallyAddr, scheduleWordSlot(i), common.Hash{})
	}
	c := chain.config
	c.BdagTestnetTimingBlock = big.NewInt(10)
	c.BdagTestnetTiming = &ethparams.BDAGTestnetTimingForkConfig{
		Version: 1, ArtifactSHA256: common.HexToHash("0x01"), SignalStartLayer: 1000,
		ActivationLayer: 4999, ReleaseDistributedAt: genesis - 86400,
		NoticePublishedAt: genesis - 86400, NotBeforeTimestamp: genesis,
		EpochSeconds: 1800, TargetBlockSeconds: 2,
	}
	h := &types.Header{Number: big.NewInt(10), Time: genesis + 539, Extra: EncodeRewardExtraWithLayer(1, 4999)}
	d := &upgrades.UpgradeDefinition{Name: upgrades.TestnetTimingV1Name, BitMask: upgrades.TestnetTimingV1BitMask,
		StartLayer: 1000, TimeoutLayer: 3000, WindowSize: 2000, Threshold: 1500, ActivationDelayBlocks: 2000,
		LockedIn: true, AnchorSet: true, ActivateAnchor: 4999}
	return c, h, s, d, genesis
}

func TestTimingPlanPreservesCurrentEpochAndIsReadOnly(t *testing.T) {
	for _, sample := range []struct{ seconds, first uint64 }{{539, 9}, {540, 10}, {600, 11}, {0, 9}} {
		c, h, s, d, genesis := timingPlanFixture(t)
		h.Time = genesis + sample.seconds
		before := s.IntermediateRoot(true)
		plan, err := planTestnetTimingClock(c, h, s, d)
		require.NoError(t, err)
		require.NotNil(t, plan)
		require.EqualValues(t, sample.first, plan.FirstEpoch)
		require.EqualValues(t, genesis+sample.first*60, plan.StartTime)
		require.EqualValues(t, 1800, plan.Duration)
		require.Equal(t, before, s.IntermediateRoot(true), "planning cannot write clock, rewards, balances or marker")
	}
}

func TestTimingPlanWaitsForBothGatesWithoutMutation(t *testing.T) {
	for _, name := range []string{"no release", "before EVM floor", "before native anchor", "before notice", "no native result", "not locked"} {
		t.Run(name, func(t *testing.T) {
			c, h, s, d, _ := timingPlanFixture(t)
			switch name {
			case "no release":
				c.BdagTestnetTimingBlock = nil
				c.BdagTestnetTiming = nil
			case "before EVM floor":
				h.Number = big.NewInt(9)
			case "before native anchor":
				h.Extra = EncodeRewardExtraWithLayer(1, 4998)
			case "before notice":
				h.Time = c.BdagTestnetTiming.NotBeforeTimestamp - 1
			case "no native result":
				d = nil
			case "not locked":
				d.LockedIn = false
				d.AnchorSet = false
				d.ActivateAnchor = 0
			}
			before := s.IntermediateRoot(true)
			plan, err := planTestnetTimingClock(c, h, s, d)
			require.NoError(t, err)
			require.Nil(t, plan)
			require.Equal(t, before, s.IntermediateRoot(true))
		})
	}
}

func TestTimingPlanRejectsUnsafeStateWithoutMutation(t *testing.T) {
	for _, name := range []string{"mainnet", "native mainnet", "partial config", "bad policy", "wrong bit", "wrong threshold", "wrong window", "wrong delay", "wrong start", "wrong timeout", "wrong anchor", "partial lock", "bad header", "missing V3", "future V3", "missing V3 config", "existing schedule", "stale segment", "unexpected time floor", "epoch overflow", "time overflow", "missing bootstrap", "bad first eligible"} {
		t.Run(name, func(t *testing.T) {
			c, h, s, d, _ := timingPlanFixture(t)
			switch name {
			case "mainnet":
				c.ChainID = big.NewInt(1404)
			case "native mainnet":
				qparams.ActiveNetParams = &qparams.MainNetParam
			case "partial config":
				c.BdagTestnetTiming = nil
			case "bad policy":
				c.BdagTestnetTiming.EpochSeconds = 1
			case "wrong bit":
				d.BitMask = upgrades.RewardGovernanceV3BitMask
			case "wrong threshold":
				d.Threshold = 1001
			case "wrong window":
				d.WindowSize = 1000
			case "wrong delay":
				d.ActivationDelayBlocks = 1
			case "wrong start":
				d.StartLayer++
			case "wrong timeout":
				d.TimeoutLayer++
			case "wrong anchor":
				d.ActivateAnchor++
			case "partial lock":
				d.AnchorSet = false
			case "bad header":
				h.Extra = EncodeRewardExtra(1)
			case "missing V3":
				s.SetState(params.ChainTallyAddr, governanceAppliedSlot, common.Hash{})
			case "future V3":
				s.SetState(params.ChainTallyAddr, governanceAppliedSlot, common.BigToHash(big.NewInt(11)))
			case "missing V3 config":
				c.BdagGovernanceFork = nil
			case "existing schedule":
				s.SetState(params.ChainTallyAddr, params.SlotRewardEpochScheduleLength, common.BigToHash(big.NewInt(1)))
			case "stale segment":
				s.SetState(params.ChainTallyAddr, scheduleWordSlot(5), common.BigToHash(big.NewInt(1)))
			case "unexpected time floor":
				s.SetState(params.ChainTallyAddr, params.SlotGovernanceTimeFloor, common.BigToHash(big.NewInt(1)))
			case "epoch overflow":
				s.SetState(params.ChainTallyAddr, params.SlotRewardVotingEpoch, common.BigToHash(new(big.Int).SetUint64(math.MaxUint64)))
			case "time overflow":
				h.Time = math.MaxUint64
			case "missing bootstrap":
				s.SetState(params.ChainTallyAddr, params.SlotRewardVotingBootstrapEpochPlus, common.Hash{})
			case "bad first eligible":
				s.SetState(params.ChainTallyAddr, params.SlotRewardVotingFirstEpochPlus, common.BigToHash(big.NewInt(9)))
			}
			before := s.IntermediateRoot(true)
			plan, err := planTestnetTimingClock(c, h, s, d)
			require.Error(t, err)
			require.Nil(t, plan)
			require.Equal(t, before, s.IntermediateRoot(true))
		})
	}
}
