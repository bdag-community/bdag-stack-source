package blockchain

import (
	"github.com/stretchr/testify/require"
	"testing"
)

// A cache opened on the first layer must reserve the entire window. Reserving
// just the observed prefix silently drops every subsequent incremental vote.
func TestIncrementalUpgradeTallyKeepsCompleteWindowCapacity(t *testing.T) {
	var chain BlockChain
	for _, yes := range []uint64{1499, 1500, 2000} {
		tally := chain.countUpgradeSignalsInRange(nil, 6000, 6000, 2000, 0x20)
		require.EqualValues(t, 2000, tally.windowSize)
		for offset := uint64(0); offset < 2000; offset++ {
			version := uint32(VBTopBits)
			if offset < yes {
				version |= 0x20
			}
			recordUpgradeSignal(&tally, offset, version, 0x20)
		}
		require.Equal(t, yes, tally.yesLayers)
		require.EqualValues(t, 2000, tally.uniqueLayers)
		definition := upgradeRebuildDefinition{WindowSize: 2000, Threshold: 1500}
		require.False(t, upgradeWindowLocksIn(definition, 6000, 7998, tally))
		require.Equal(t, yes >= 1500, upgradeWindowLocksIn(definition, 6000, 7999, tally))
		// Retries, duplicate DAG layers and out-of-window signals cannot add votes.
		recordUpgradeSignal(&tally, 0, VBTopBits|0x20, 0x20)
		recordUpgradeSignal(&tally, 2000, VBTopBits|0x20, 0x20)
		require.Equal(t, yes, tally.yesLayers)
	}
}
