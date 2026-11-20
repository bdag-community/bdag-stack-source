package common

import (
	"fmt"
	evmparams "github.com/BlockdagNetworkLabs/bdag/bevm/params"
	consensusupgrades "github.com/BlockdagNetworkLabs/bdag/consensus/upgrades"
	"github.com/BlockdagNetworkLabs/bdag/params"
	"github.com/BlockdagNetworkLabs/bdag/upgrades"
	"math/big"
)

func registerTestnetTimingUpgrade() error {
	if params.ActiveNetParams != &params.TestNetParam {
		return nil
	}
	manifest := evmparams.BdagTestnetTimingFork
	if manifest == nil {
		if upgrades.GetUpgrades()[upgrades.TestnetTimingV1Name] != nil {
			return fmt.Errorf("persisted timing upgrade requires its qualified immutable manifest")
		}
		return nil
	}
	if err := manifest.Validate(big.NewInt(1043)); err != nil {
		return err
	}
	want := consensusupgrades.TestnetTimingV1Definition(manifest.SignalStartLayer)
	if got := upgrades.GetUpgrades()[upgrades.TestnetTimingV1Name]; got != nil {
		if got.Name == want.Name && got.BitMask == want.BitMask && got.WindowSize == want.WindowSize &&
			got.Threshold == want.Threshold && got.ActivationDelayBlocks == want.ActivationDelayBlocks &&
			got.StartLayer == want.StartLayer && got.TimeoutLayer == want.TimeoutLayer {
			return nil
		}
		return fmt.Errorf("persisted timing definition conflicts with immutable release")
	}
	return upgrades.RegisterUpgrade(want)
}
