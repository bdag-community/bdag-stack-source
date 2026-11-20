package bdag

import (
	"math/big"
	"testing"

	bdagconsensus "github.com/BlockdagNetworkLabs/bdag/bevm/bdag/consensus"
	"github.com/BlockdagNetworkLabs/bdag/consensus/model"
	"github.com/BlockdagNetworkLabs/bdag/upgrades"
	"github.com/ethereum/go-ethereum/core/types"
	ethparams "github.com/ethereum/go-ethereum/params"
	"github.com/stretchr/testify/require"
)

type timingTemplateTip struct {
	model.Block
	layer uint
}

func (b timingTemplateTip) GetLayer() uint { return b.layer }

func TestTimingTemplateBindsNativeLayerWithoutRelaxingImporter(t *testing.T) {
	previous := upgrades.ReplaceUpgrades(map[string]*upgrades.UpgradeDefinition{
		upgrades.RewardGovernanceV3Name: {Name: upgrades.RewardGovernanceV3Name, LockedIn: true, AnchorSet: true, ActivateAnchor: 4999},
	})
	t.Cleanup(func() { upgrades.ReplaceUpgrades(previous) })
	c := &ethparams.ChainConfig{ChainID: big.NewInt(1043), BdagTestnetTiming: &ethparams.BDAGTestnetTimingForkConfig{}}
	for _, layer := range []uint{4997, 4998, 7999, 9998, 10000} {
		h := &types.Header{Extra: bdagconsensus.EncodeRewardExtra(0)}
		err := prepareTimingTemplateRewardExtra(c, h, timingTemplateTip{layer: layer})
		require.NoError(t, err)
		require.NoError(t, bdagconsensus.ValidateRewardExtraAtLayer(h.Extra, 0, uint64(layer)+1, layer+1 >= 4999))
		if layer+1 >= 4999 {
			require.Error(t, bdagconsensus.ValidateRewardExtraAtLayer(bdagconsensus.EncodeRewardExtra(0), 0, uint64(layer)+1, true))
		}
	}
	h := &types.Header{Extra: bdagconsensus.EncodeRewardExtra(0)}
	require.Error(t, prepareTimingTemplateRewardExtra(c, h, nil))
	c.BdagTestnetTiming = nil
	require.NoError(t, prepareTimingTemplateRewardExtra(c, h, nil))
	require.Equal(t, bdagconsensus.EncodeRewardExtra(0), h.Extra, "unscheduled historical template unchanged")
}

func TestMainnetDAOTemplateAndReplayUseItsOwnNativeSignal(t *testing.T) {
	previous := upgrades.ReplaceUpgrades(map[string]*upgrades.UpgradeDefinition{
		upgrades.MainnetDAOStakingV2Name: {Name: upgrades.MainnetDAOStakingV2Name, LockedIn: true, AnchorSet: true, ActivateAnchor: 30101999},
	})
	t.Cleanup(func() { upgrades.ReplaceUpgrades(previous) })
	c := &ethparams.ChainConfig{ChainID: big.NewInt(1404), BdagMainnetDAO: &ethparams.BDAGMainnetDAOConfig{}}
	for _, layer := range []uint{30101997, 30101998, 30103000} {
		h := &types.Header{Extra: bdagconsensus.EncodeRewardExtra(0)}
		require.NoError(t, prepareTimingTemplateRewardExtra(c, h, timingTemplateTip{layer: layer}))
		active := uint64(layer)+1 >= 30101999
		require.NoError(t, bdagconsensus.ValidateRewardExtraAtLayer(h.Extra, 0, uint64(layer)+1, active))
		require.Equal(t, active, ConsensusGovernanceAdmissibilityAtLayer(uint64(layer)+1).rewardGovernance)
		require.Equal(t, active, ReplayGovernanceAdmissibilityAtLayer(uint64(layer)+1).rewardGovernance)
	}
}
