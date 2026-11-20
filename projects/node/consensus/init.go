package consensus

import (
	consensusupgrades "github.com/BlockdagNetworkLabs/bdag/consensus/upgrades"
	"github.com/BlockdagNetworkLabs/bdag/upgrades"
)

func init() {
	for _, def := range consensusupgrades.Defs {
		if err := upgrades.RegisterUpgrade(def); err != nil {
			panic(err)
		}
	}
}