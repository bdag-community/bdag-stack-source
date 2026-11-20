package consensus

import (
	"github.com/BlockdagNetworkLabs/bdag/upgrades"
	"github.com/ethereum/go-ethereum/common"
	ethparams "github.com/ethereum/go-ethereum/params"
	"github.com/stretchr/testify/require"
	"math/big"
	"testing"
)

func TestSeparateStakingWaitsForNativeAndTimeGatesThenAnEpochBoundary(t *testing.T) {
	c, h, s, genesis := timingForkFixture(t)
	require.NoError(t, ApplyTestnetTimingFork(c, h, s))
	h.Number = big.NewInt(11)
	h.Time = genesis + 550
	require.NoError(t, prepareVersionedRewardClock(c, h, s))
	c.BdagSeparateStakingBlock = big.NewInt(12)
	c.BdagSeparateStaking = &ethparams.BDAGSeparateStakingForkConfig{Version: 1, ArtifactSHA256: common.HexToHash("0x01"), SignalStartLayer: 10000, ActivationLayer: 13999, ReleaseDistributedAt: genesis - 86400, NoticePublishedAt: genesis - 86400, NotBeforeTimestamp: genesis}
	d := &upgrades.UpgradeDefinition{Name: upgrades.SeparateStakingV1Name, BitMask: upgrades.SeparateStakingV1BitMask, StartLayer: 10000, TimeoutLayer: 12000, WindowSize: 2000, Threshold: 1500, ActivationDelayBlocks: 2000, LockedIn: true, AnchorSet: true, ActivateAnchor: 13999}
	h.Number = big.NewInt(12)
	h.Time = genesis + 550
	h.Extra = EncodeRewardExtraWithLayer(1, 13999)
	before := s.IntermediateRoot(false)
	epoch, ready, err := planSeparateStaking(c, h, s, d)
	require.NoError(t, err)
	require.False(t, ready)
	require.Zero(t, epoch)
	require.Equal(t, before, s.IntermediateRoot(false))
	h.Time = genesis + 2340 // first new 30-minute epoch after the installed segment
	_, ready, err = planSeparateStaking(c, h, s, nil)
	require.NoError(t, err)
	require.False(t, ready)
	h.Extra = EncodeRewardExtraWithLayer(1, 13998)
	_, ready, err = planSeparateStaking(c, h, s, d)
	require.NoError(t, err)
	require.False(t, ready)
	h.Extra = EncodeRewardExtraWithLayer(1, 13999)
	epoch, ready, err = planSeparateStaking(c, h, s, d)
	require.NoError(t, err)
	require.True(t, ready)
	require.EqualValues(t, 10, epoch)
	require.Equal(t, before, s.IntermediateRoot(false), "planner must not mutate state")
	d.Threshold = 1499
	_, _, err = planSeparateStaking(c, h, s, d)
	require.Error(t, err)
}
