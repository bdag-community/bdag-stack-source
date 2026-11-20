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

func TestMainnetDAOProfileIsSealedAndHasNoRuntimeOverride(t *testing.T) {
	require.NotNil(t, BdagMainnetDAO)
	require.Equal(t, BdagMainnetDAO, BdagMainnetChainConfig.BdagMainnetDAO)
	require.NoError(t, BdagMainnetDAO.Validate(big.NewInt(1404)))
	require.EqualValues(t, 22420569, BdagMainnetDAO.SignalStartLayer)
	require.EqualValues(t, 29422976, BdagMainnetDAO.EarliestEVMBlock)
	require.EqualValues(t, 2000, BdagMainnetDAO.ActivationDelayLayers)
	require.Zero(t, BdagMainnetDAO.ReleaseDistributedAt)
	require.Zero(t, BdagMainnetDAO.NoticePublishedAt)
	require.Zero(t, BdagMainnetDAO.NotBeforeTimestamp)
	require.Equal(t, common.HexToHash("0xd64f893f7d1eae52b369d59d434f7c5f6745794fb0610a7d71456022d38aa33f"), BdagMainnetDAO.ArtifactSHA256)
	require.Nil(t, BdagTestnetChainConfig.BdagMainnetDAO)
	require.Nil(t, BdagDevnetChainConfig.BdagMainnetDAO)
	got, err := ParseMainnetDAORelease([]byte("null"))
	require.NoError(t, err)
	require.Nil(t, got)
	_, err = ParseMainnetDAORelease([]byte(`{"version":1,"unknown":true}`))
	require.Error(t, err)
}

func TestMainnetDAOProductionPolicyValidatesTimingMetadata(t *testing.T) {
	good := validMainnetDAOConfig()
	require.NoError(t, good.Validate(big.NewInt(1404)))
	// No extra notice or distribution interval is required for Mainnet.
	withoutNotice := *good
	withoutNotice.ReleaseDistributedAt = 0
	withoutNotice.NoticePublishedAt = 0
	withoutNotice.NotBeforeTimestamp = 0
	require.NoError(t, withoutNotice.Validate(big.NewInt(1404)))
	noDelay := *good
	noDelay.NoticePublishedAt = noDelay.ReleaseDistributedAt
	noDelay.NotBeforeTimestamp = noDelay.ReleaseDistributedAt
	require.NoError(t, noDelay.Validate(big.NewInt(1404)))
	for _, mutate := range []func(*ethparams.BDAGMainnetDAOConfig){
		func(c *ethparams.BDAGMainnetDAOConfig) { c.SignalStartLayer = 0 },
		func(c *ethparams.BDAGMainnetDAOConfig) { c.SignalStartLayer = math.MaxUint64 - 1000 },
		func(c *ethparams.BDAGMainnetDAOConfig) { c.ActivationDelayLayers = 1999 },
		func(c *ethparams.BDAGMainnetDAOConfig) { c.EarliestEVMBlock = 0 },
		func(c *ethparams.BDAGMainnetDAOConfig) { c.LegacyGenesisTime = 0 },
		func(c *ethparams.BDAGMainnetDAOConfig) { c.NotBeforeTimestamp = c.NoticePublishedAt - 1 },
		func(c *ethparams.BDAGMainnetDAOConfig) { c.NoticePublishedAt = c.ReleaseDistributedAt - 1 },
		func(c *ethparams.BDAGMainnetDAOConfig) { c.ReleaseDistributedAt = 0 },
		func(c *ethparams.BDAGMainnetDAOConfig) { c.NoticePublishedAt = 0 },
		func(c *ethparams.BDAGMainnetDAOConfig) { c.NotBeforeTimestamp = 0 },
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
