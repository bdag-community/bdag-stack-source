package common

import (
	"fmt"
	"math/big"

	evmparams "github.com/BlockdagNetworkLabs/bdag/bevm/params"
	consensusupgrades "github.com/BlockdagNetworkLabs/bdag/consensus/upgrades"
	"github.com/BlockdagNetworkLabs/bdag/params"
	"github.com/BlockdagNetworkLabs/bdag/upgrades"
)

func registerMainnetDAOUpgrade() error {
	if params.ActiveNetParams != &params.MainNetParam {
		return nil
	}
	g := evmparams.BdagMainnetDAO
	if g == nil {
		if upgrades.GetUpgrades()[upgrades.MainnetDAOStakingV2Name] != nil {
			return fmt.Errorf("persisted Mainnet DAO requires its immutable release profile")
		}
		return nil
	}
	if err := g.Validate(big.NewInt(1404)); err != nil {
		return err
	}
	want, err := consensusupgrades.MainnetDAOStakingV2Definition(g.SignalStartLayer, g.ActivationDelayLayers)
	if err != nil {
		return err
	}
	if got := upgrades.GetUpgrades()[want.Name]; got != nil {
		if got.Name == want.Name && got.BitMask == want.BitMask && got.StartLayer == want.StartLayer && got.TimeoutLayer == want.TimeoutLayer &&
			got.WindowSize == want.WindowSize && got.Threshold == want.Threshold && got.ActivationDelayBlocks == want.ActivationDelayBlocks {
			return nil
		}
		return fmt.Errorf("persisted Mainnet DAO definition differs from compiled release")
	}
	return upgrades.RegisterUpgrade(want)
}
