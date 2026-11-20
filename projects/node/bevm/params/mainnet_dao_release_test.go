package params

import (
	"encoding/json"
	"math"
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	ethparams "github.com/ethereum/go-ethereum/params"
	"github.com/stretchr/testify/require"
)

func validMainnetDAOConfig() *ethparams.BDAGMainnetDAOConfig {
	return &ethparams.BDAGMainnetDAOConfig{Version: 1, ArtifactSHA256: common.HexToHash("0x1234"),
		SignalStartLayer: 30_000_000, ActivationDelayLayers: 2_000, EarliestEVMBlock: 40_000_000,
		ReleaseDistributedAt: 1_800_000_000, NoticePublishedAt: 1_803_024_000, NotBeforeTimestamp: 1_803_628_800,
		LegacyGenesisTime: 1770760980}
}

func TestMainnetDAOProfileIsUnsealedAndHasNoRuntimeOverride(t *testing.T) {
	require.Nil(t, BdagMainnetChainConfig.BdagMainnetDAO)
	require.Nil(t, BdagTestnetChainConfig.BdagMainnetDAO)
	require.Nil(t, BdagDevnetChainConfig.BdagMainnetDAO)
	got, err := ParseMainnetDAORelease([]byte("null"))
	require.NoError(t, err)
	require.Nil(t, got)
	_, err = ParseMainnetDAORelease([]byte(`{"version":1,"unknown":true}`))
	require.Error(t, err)
}

func TestMainnetDAOProductionPolicyCannotUseTestnetTimings(t *testing.T) {
	good := validMainnetDAOConfig()
	require.NoError(t, good.Validate(big.NewInt(1404)))
	for _, mutate := range []func(*ethparams.BDAGMainnetDAOConfig){
		func(c *ethparams.BDAGMainnetDAOConfig) { c.SignalStartLayer = 0 },
		func(c *ethparams.BDAGMainnetDAOConfig) { c.SignalStartLayer = math.MaxUint64 - 1000 },
		func(c *ethparams.BDAGMainnetDAOConfig) { c.ActivationDelayLayers = 1999 },
		func(c *ethparams.BDAGMainnetDAOConfig) { c.EarliestEVMBlock = 0 },
		func(c *ethparams.BDAGMainnetDAOConfig) { c.LegacyGenesisTime = 0 },
		func(c *ethparams.BDAGMainnetDAOConfig) { c.NotBeforeTimestamp = c.NoticePublishedAt + 86400 },
		func(c *ethparams.BDAGMainnetDAOConfig) { c.ReleaseDistributedAt = c.NotBeforeTimestamp - 86400 },
		func(c *ethparams.BDAGMainnetDAOConfig) { c.ArtifactSHA256 = common.Hash{} },
	} {
		copy := *good
		mutate(&copy)
		require.Error(t, copy.Validate(big.NewInt(1404)))
	}
	require.Error(t, good.Validate(big.NewInt(1043)))
	encoded, err := json.Marshal(good)
	require.NoError(t, err)
	decoded, err := ParseMainnetDAORelease(encoded)
	require.NoError(t, err)
	require.Equal(t, good, decoded)
}

func TestMainnetDAOPersistedProfileCannotChangeAfterItsEVMFloor(t *testing.T) {
	a := *BdagMainnetChainConfig
	a.BdagMainnetDAO = validMainnetDAOConfig()
	b := a
	changed := *a.BdagMainnetDAO
	b.BdagMainnetDAO = &changed
	changed.NotBeforeTimestamp++
	require.Nil(t, a.CheckCompatible(&b, a.BdagMainnetDAO.EarliestEVMBlock-1, 0))
	require.NotNil(t, a.CheckCompatible(&b, a.BdagMainnetDAO.EarliestEVMBlock, 0))
	changed.EarliestEVMBlock++
	require.NotNil(t, a.CheckCompatible(&b, a.BdagMainnetDAO.EarliestEVMBlock, 0))
}
