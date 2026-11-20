package params

import (
	"github.com/stretchr/testify/require"
	"strings"
	"testing"
)

func TestCompiledTimingReleaseRequiresStrictSeparateManifest(t *testing.T) {
	valid := `{"earliestEvmBlock":120000,"policy":{"version":1,"artifactSha256":"0x0000000000000000000000000000000000000000000000000000000000001234","signalStartLayer":120000,"activationLayer":123999,"releaseDistributedAt":200000,"noticePublishedAt":200000,"notBeforeTimestamp":286400,"epochSeconds":1800,"targetBlockSeconds":2}}`
	block, policy := mustTestnetTimingSchedule([]byte(valid))
	require.EqualValues(t, 120000, block.Uint64())
	require.EqualValues(t, 1800, policy.EpochSeconds)
	for _, invalid := range []string{`{}`, `null {}`, `[]`, strings.Replace(valid, `"earliestEvmBlock":120000`, `"earliestEvmBlock":0`, 1), strings.Replace(valid, `"version":1`, `"version":1,"guardian":true`, 1), strings.Replace(valid, `"notBeforeTimestamp":286400`, `"notBeforeTimestamp":286399`, 1)} {
		require.Panics(t, func() { mustTestnetTimingSchedule([]byte(invalid)) }, invalid)
	}
	block, policy = mustTestnetTimingSchedule([]byte(`null`))
	require.Nil(t, block)
	require.Nil(t, policy)
	if strings.TrimSpace(string(testnetTimingScheduleJSON)) == "null" {
		require.Nil(t, BdagTestnetTimingFork, "preparation build must not select a live schedule")
	} else {
		require.NoError(t, BdagTestnetTimingFork.Validate(BdagTestnetChainConfig.ChainID))
	}
	require.Nil(t, BdagMainnetChainConfig.BdagTestnetTiming)
}

func TestTimingOperatorCandidateSchedule(t *testing.T) {
	require.NotNil(t, BdagTestnetTimingFork)
	p := BdagTestnetTimingFork
	require.EqualValues(t, 115000, BdagTestnetTimingForkBlock.Uint64())
	require.EqualValues(t, 115000, p.SignalStartLayer)
	require.EqualValues(t, 118999, p.ActivationLayer)
	require.EqualValues(t, 1791550800, p.ReleaseDistributedAt)
	require.EqualValues(t, 1791550800, p.NoticePublishedAt)
	require.EqualValues(t, 1791637200, p.NotBeforeTimestamp)
	require.EqualValues(t, 1800, p.EpochSeconds)
	require.EqualValues(t, 2, p.TargetBlockSeconds)
	require.Nil(t, BdagMainnetChainConfig.BdagTestnetTiming)
}
