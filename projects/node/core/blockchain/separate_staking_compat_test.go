package blockchain

import (
	"strings"
	"testing"

	"github.com/BlockdagNetworkLabs/bdag/config"
	"github.com/BlockdagNetworkLabs/bdag/database/chaindb"
	"github.com/BlockdagNetworkLabs/bdag/params"
	"github.com/BlockdagNetworkLabs/bdag/upgrades"
)

// Exercise the persisted-state refusal with the exact new name. This guard's
// production source is unchanged from the supported v0.2.1 binary. It is a
// compatibility unit test, not permission to run old binaries on upgraded data.
func TestSeparateStakingStateRefusesDefinitionsFromThePreviousRelease(t *testing.T) {
	db, err := chaindb.New(&config.Config{DataDir: ""})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	bc := &BlockChain{consensus: &mockConsensus{db: db, params: &params.Params{}}}
	previous := upgrades.ReplaceUpgrades(map[string]*upgrades.UpgradeDefinition{})
	defer upgrades.ReplaceUpgrades(previous)
	d := &upgrades.UpgradeDefinition{Name: upgrades.SeparateStakingV1Name, BitMask: 0x40, WindowSize: 2000, Threshold: 1500, ActivationDelayBlocks: 2000, StartLayer: 600000, TimeoutLayer: 602000, Active: true, LockedIn: true, AnchorSet: true, ActivateAnchor: 603999, ThresholdAt: 601999}
	if err := bc.persistUpgradeState(d.Name, d); err != nil {
		t.Fatal(err)
	}
	if err := bc.loadUpgradeStates(); err == nil || !strings.Contains(err.Error(), "persisted upgrade state SeparateStakingV1 has no definition") {
		t.Fatalf("expected unchanged older-release startup guard, got %v", err)
	}
}
