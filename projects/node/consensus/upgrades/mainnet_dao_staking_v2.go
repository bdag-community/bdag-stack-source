package upgrades

import (
	"fmt"
	base "github.com/BlockdagNetworkLabs/bdag/upgrades"
	"math"
)

// MainnetDAOStakingV2Definition prepares the new 75%-of-100000 policy for a
// reviewed immutable Mainnet manifest. It does NOT register or activate it.
// The future installer must independently enforce distribution, notice, epoch,
// artifact, authority and persisted-anchor gates. Defaults remain unscheduled.
func MainnetDAOStakingV2Definition(startLayer, delayLayers uint64) (*base.UpgradeDefinition, error) {
	const window uint64 = 100_000
	if startLayer == 0 || delayLayers < 2_000 || startLayer > math.MaxUint64-(window-1) {
		return nil, fmt.Errorf("invalid Mainnet DAO signalling start/delay")
	}
	lastLayer := startLayer + window - 1
	if lastLayer > math.MaxUint64-delayLayers {
		return nil, fmt.Errorf("Mainnet DAO activation anchor overflows")
	}
	return &base.UpgradeDefinition{
		Name: base.MainnetDAOStakingV2Name, BitMask: base.MainnetDAOStakingV2BitMask,
		WindowSize: window, Threshold: 75_000, StartLayer: startLayer,
		ActivationDelayBlocks: delayLayers,
	}, nil
}
