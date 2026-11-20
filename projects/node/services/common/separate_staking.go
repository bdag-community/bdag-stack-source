package common

import (
	"fmt"
	evmparams "github.com/BlockdagNetworkLabs/bdag/bevm/params"
	consensusupgrades "github.com/BlockdagNetworkLabs/bdag/consensus/upgrades"
	"github.com/BlockdagNetworkLabs/bdag/params"
	"github.com/BlockdagNetworkLabs/bdag/upgrades"
	"math/big"
)

func registerSeparateStakingUpgrade() error {
	if params.ActiveNetParams != &params.TestNetParam {
		return nil
	}
	manifest := evmparams.BdagSeparateStakingFork
	if manifest == nil {
		if upgrades.GetUpgrades()[upgrades.SeparateStakingV1Name] != nil {
			return fmt.Errorf("persisted separate staking upgrade requires its qualified immutable manifest")
		}
		return nil
	}
	if err := manifest.Validate(big.NewInt(1043)); err != nil {
		return err
	}
	want := consensusupgrades.SeparateStakingV1Definition(manifest.SignalStartLayer)
	if got := upgrades.GetUpgrades()[upgrades.SeparateStakingV1Name]; got != nil {
		if got.Name == want.Name && got.BitMask == want.BitMask && got.WindowSize == want.WindowSize &&
			got.Threshold == want.Threshold && got.ActivationDelayBlocks == want.ActivationDelayBlocks &&
			got.StartLayer == want.StartLayer && got.TimeoutLayer == want.TimeoutLayer {
			return nil
		}
		return fmt.Errorf("persisted separate staking definition conflicts with immutable separate staking release")
	}
	return upgrades.RegisterUpgrade(want)
}
