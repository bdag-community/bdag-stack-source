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

func TestMainnetDAORegistrationNeverReusesTestnetOrChangesPersistedPolicy(t *testing.T) {
	previousNet, previousProfile := params.ActiveNetParams, evmparams.BdagMainnetDAO
	previous := upgrades.ReplaceUpgrades(map[string]*upgrades.UpgradeDefinition{})
	t.Cleanup(func() {
		params.ActiveNetParams = previousNet
		evmparams.BdagMainnetDAO = previousProfile
		upgrades.ReplaceUpgrades(previous)
	})
	evmparams.BdagMainnetDAO = &ethparams.BDAGMainnetDAOConfig{Version: 1, ArtifactSHA256: common.HexToHash("0x1234"), SignalStartLayer: 30000000, ActivationDelayLayers: 2000, EarliestEVMBlock: 40000000,
		ReleaseDistributedAt: 1800000000, NoticePublishedAt: 1803024000, NotBeforeTimestamp: 1803628800, LegacyGenesisTime: 1770760980}
	params.ActiveNetParams = &params.TestNetParam
	require.NoError(t, registerMainnetDAOUpgrade())
	require.Empty(t, upgrades.GetUpgrades())
	params.ActiveNetParams = &params.MainNetParam
	require.NoError(t, registerMainnetDAOUpgrade())
	d := upgrades.GetUpgrades()[upgrades.MainnetDAOStakingV2Name]
	require.EqualValues(t, 100000, d.WindowSize)
	require.EqualValues(t, 75000, d.Threshold)
	require.EqualValues(t, 2000, d.ActivationDelayBlocks)
	require.EqualValues(t, 0, d.TimeoutLayer)
	require.NoError(t, registerMainnetDAOUpgrade())
	evmparams.BdagMainnetDAO.SignalStartLayer++
	require.Error(t, registerMainnetDAOUpgrade())
	evmparams.BdagMainnetDAO = nil
	require.Error(t, registerMainnetDAOUpgrade())
}
