package common

import (
	evmconsensus "github.com/BlockdagNetworkLabs/bdag/bevm/bdag/consensus"
	evmparams "github.com/BlockdagNetworkLabs/bdag/bevm/params"
	chainparams "github.com/BlockdagNetworkLabs/bdag/params"
	"github.com/BlockdagNetworkLabs/bdag/upgrades"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestCompiledMainnetDAORelease(t *testing.T) {
	previousNet := chainparams.ActiveNetParams
	previous := upgrades.ReplaceUpgrades(map[string]*upgrades.UpgradeDefinition{})
	t.Cleanup(func() { chainparams.ActiveNetParams = previousNet; upgrades.ReplaceUpgrades(previous) })
	require.NotNil(t, evmparams.BdagMainnetDAO)
	require.EqualValues(t, 22419500, evmparams.BdagMainnetDAO.SignalStartLayer)
	require.NoError(t, evmconsensus.ValidateCompiledMainnetDAOArtifacts())
	require.Nil(t, evmparams.BdagTestnetChainConfig.BdagMainnetDAO)
	require.Nil(t, evmparams.BdagDevnetChainConfig.BdagMainnetDAO)
	chainparams.ActiveNetParams = &chainparams.MainNetParam
	require.NoError(t, registerMainnetDAOUpgrade())
	require.NoError(t, registerMainnetDAOUpgrade())
	got := upgrades.GetUpgrades()[upgrades.MainnetDAOStakingV2Name]
	require.NotNil(t, got)
	require.EqualValues(t, 100000, got.WindowSize)
	require.EqualValues(t, 75000, got.Threshold)
	require.EqualValues(t, 2000, got.ActivationDelayBlocks)
}
