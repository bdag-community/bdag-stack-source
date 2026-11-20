package common

import (
	"testing"

	evmparams "github.com/BlockdagNetworkLabs/bdag/bevm/params"
	"github.com/BlockdagNetworkLabs/bdag/params"
	"github.com/BlockdagNetworkLabs/bdag/upgrades"
	"github.com/ethereum/go-ethereum/common"
	ethparams "github.com/ethereum/go-ethereum/params"
	"github.com/stretchr/testify/require"
)

func TestSeparateStakingRegistrationPreservesHistoricalDefinitionsAndRejectsPolicyChanges(t *testing.T) {
	originalNet, originalPolicy := params.ActiveNetParams, evmparams.BdagSeparateStakingFork
	original := upgrades.ReplaceUpgrades(map[string]*upgrades.UpgradeDefinition{})
	t.Cleanup(func() {
		params.ActiveNetParams = originalNet
		evmparams.BdagSeparateStakingFork = originalPolicy
		upgrades.ReplaceUpgrades(original)
	})
	evmparams.BdagSeparateStakingFork = &ethparams.BDAGSeparateStakingForkConfig{Version: 1, ArtifactSHA256: common.HexToHash("0x1234"), SignalStartLayer: 600000, ActivationLayer: 603999, ReleaseDistributedAt: 100000, NoticePublishedAt: 100000, NotBeforeTimestamp: 186400}
	params.ActiveNetParams = &params.MainNetParam
	require.NoError(t, registerSeparateStakingUpgrade())
	require.Empty(t, upgrades.GetUpgrades())
	params.ActiveNetParams = &params.TestNetParam
	require.NoError(t, registerSeparateStakingUpgrade())
	d := upgrades.GetUpgrades()[upgrades.SeparateStakingV1Name]
	require.EqualValues(t, 0x40, d.BitMask)
	require.EqualValues(t, 1500, d.Threshold)
	require.EqualValues(t, 2000, d.WindowSize)
	require.EqualValues(t, 2000, d.ActivationDelayBlocks)
	require.EqualValues(t, 602000, d.TimeoutLayer)
	require.Len(t, upgrades.GetUpgrades(), 1)
	require.NoError(t, registerSeparateStakingUpgrade())
	evmparams.BdagSeparateStakingFork.SignalStartLayer++
	evmparams.BdagSeparateStakingFork.ActivationLayer++
	require.Error(t, registerSeparateStakingUpgrade())
	evmparams.BdagSeparateStakingFork = nil
	require.Error(t, registerSeparateStakingUpgrade())
}
