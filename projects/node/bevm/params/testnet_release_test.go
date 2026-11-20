package params

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestUnscheduledTestnetSourceCannotReuseRetiredActivation(t *testing.T) {
	if !bytes.Equal(bytes.TrimSpace(testnetGovernanceScheduleJSON), []byte("null")) {
		require.NoError(t, BdagTestnetGovernanceFork.Validate())
		require.NotEqualValues(t, 1000, BdagTestnetGovernanceFork.SignalWindowStartLayer)
		require.NotEqualValues(t, 4999, BdagTestnetGovernanceFork.ActivationLayer)
		return
	}
	require.Nil(t, BdagTestnetGovernanceForkBlock, "preparation source must not reuse the expired rehearsal schedule")
	require.Nil(t, BdagTestnetGovernanceFork)
	require.Nil(t, BdagTestnetChainConfig.BdagGovernanceForkBlock)
	require.Nil(t, BdagTestnetChainConfig.BdagGovernanceFork)
}

func TestCompiledTestnetScheduleIsStrictAndKeepsAllGates(t *testing.T) {
	valid := `{"earliestEvmBlock":50000,"signalStartLayer":50000,"activationLayer":53999,"releaseDistributedAt":100,"noticePublishedAt":100,"notBeforeTimestamp":86500}`
	block, manifest := mustTestnetGovernanceSchedule([]byte(valid))
	require.EqualValues(t, 50000, block.Uint64())
	require.EqualValues(t, 1500, manifest.SignalThresholdLayers)
	require.EqualValues(t, 2000, manifest.SignalWindowLayers)
	require.EqualValues(t, 2000, manifest.ActivationDelayLayers)
	require.Equal(t, TestnetGovernanceArtifactTemplate().ManifestHash, manifest.ManifestHash)
	for _, invalid := range []string{
		`{}`, `null {}`, `[]`, `{"signalStartLayer":-1}`, `{"signalStartLayer":18446744073709551616}`,
		`{"earliestEvmBlock":50000,"signalStartLayer":50000,"activationLayer":53998,"releaseDistributedAt":100,"noticePublishedAt":100,"notBeforeTimestamp":86500}`,
		`{"earliestEvmBlock":50000,"signalStartLayer":50000,"activationLayer":53999,"releaseDistributedAt":100,"noticePublishedAt":100,"notBeforeTimestamp":86499}`,
		`{"earliestEvmBlock":50000,"signalStartLayer":50000,"activationLayer":53999,"releaseDistributedAt":100,"noticePublishedAt":100,"notBeforeTimestamp":86500,"guardian":"0x1234"}`,
	} {
		require.Panics(t, func() { mustTestnetGovernanceSchedule([]byte(invalid)) }, invalid)
	}
}

func TestReplacementOperatorTrialSchedule(t *testing.T) {
	require.NotNil(t, BdagTestnetGovernanceFork)
	g := BdagTestnetGovernanceFork
	require.EqualValues(t, 52000, BdagTestnetGovernanceForkBlock.Uint64())
	require.EqualValues(t, 52000, g.SignalWindowStartLayer)
	require.EqualValues(t, 55999, g.ActivationLayer)
	require.EqualValues(t, 1790704800, g.InitialReleaseDistributedAt)
	require.EqualValues(t, 1790704800, g.NoticePublishedAt)
	require.EqualValues(t, 1790791200, g.NotBeforeTimestamp)
	require.NoError(t, g.Validate())
}
