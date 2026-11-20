package common

import (
	evmparams "github.com/BlockdagNetworkLabs/bdag/bevm/params"
	"github.com/BlockdagNetworkLabs/bdag/params"
	"github.com/BlockdagNetworkLabs/bdag/upgrades"
	"github.com/ethereum/go-ethereum/common"
	ethparams "github.com/ethereum/go-ethereum/params"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestTimingUpgradeIsSeparateOneWindowAndCannotRegisterOnMainnet(t *testing.T) {
	originalNet, originalPolicy := params.ActiveNetParams, evmparams.BdagTestnetTimingFork
	originalUpgrades := upgrades.ReplaceUpgrades(map[string]*upgrades.UpgradeDefinition{})
	t.Cleanup(func() {
		params.ActiveNetParams = originalNet
		evmparams.BdagTestnetTimingFork = originalPolicy
		upgrades.ReplaceUpgrades(originalUpgrades)
	})
	evmparams.BdagTestnetTimingFork = &ethparams.BDAGTestnetTimingForkConfig{Version: 1, ArtifactSHA256: common.HexToHash("0x1234"), SignalStartLayer: 120000, ActivationLayer: 123999, ReleaseDistributedAt: 200000, NoticePublishedAt: 200000, NotBeforeTimestamp: 286400, EpochSeconds: 1800, TargetBlockSeconds: 2}
	params.ActiveNetParams = &params.MainNetParam
	require.NoError(t, registerTestnetTimingUpgrade())
	require.Empty(t, upgrades.GetUpgrades())
	params.ActiveNetParams = &params.TestNetParam
	require.NoError(t, registerTestnetTimingUpgrade())
	def := upgrades.GetUpgrades()[upgrades.TestnetTimingV1Name]
	require.NotNil(t, def)
	require.EqualValues(t, 0x20, def.BitMask)
	require.EqualValues(t, 1500, def.Threshold)
	require.EqualValues(t, 2000, def.WindowSize)
	require.EqualValues(t, 2000, def.ActivationDelayBlocks)
	require.EqualValues(t, 122000, def.TimeoutLayer)
	require.Nil(t, upgrades.GetUpgrades()[upgrades.RewardGovernanceV3Name])
	require.NoError(t, registerTestnetTimingUpgrade())
	evmparams.BdagTestnetTimingFork.SignalStartLayer++
	evmparams.BdagTestnetTimingFork.ActivationLayer++
	require.Error(t, registerTestnetTimingUpgrade())
	evmparams.BdagTestnetTimingFork = nil
	require.Error(t, registerTestnetTimingUpgrade(), "unconfigured binary must reject persisted new upgrade")
}
