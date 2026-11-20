package blockchain

import (
	"testing"

	"github.com/BlockdagNetworkLabs/bdag/upgrades"
)

// TestUpgradeLocksInAndActivates simulates a sequence of block versions
// that vote for an upgrade using version bits. Once the threshold is met
// the upgrade should lock in and later activate after the required delay.
func TestUpgradeLocksInAndActivates(t *testing.T) {
	// Preserve existing upgrades and restore after the test.
	oldUpgrades := upgrades.ReplaceUpgrades(map[string]*upgrades.UpgradeDefinition{})
	if err := upgrades.RegisterUpgrade(&upgrades.UpgradeDefinition{Name: "test", BitMask: 0x1, Threshold: 3, WindowSize: 4, ActivationDelayBlocks: 2}); err != nil {
		t.Fatalf("register upgrade: %v", err)
	}
	upgrades.RegisterPersist(nil) // disable persistence callbacks during the test

	defer func() {
		upgrades.ReplaceUpgrades(oldUpgrades)
	}()

	versions := []uint32{
		VBTopBits | 0x1, // vote
		VBTopBits | 0x1, // vote
		VBTopBits | 0x1, // vote
		VBTopBits,       // no vote -> 3/4 = 75% yes => lock-in
		VBTopBits,       // still before activation metric
		VBTopBits | 0x1, // metric high enough to trigger activation
	}

	layers := []uint64{0, 1, 2, 3, 4, 5}

	for i := range versions {
		if err := evaluateWindow(i, versions, layers[i]); err != nil {
			t.Fatalf("evaluateWindow: %v", err)
		}
	}

	u := upgrades.GetUpgrades()["test"]
	if !u.LockedIn {
		t.Fatalf("upgrade should be locked in after threshold is met")
	}
	if !u.Active {
		t.Fatalf("upgrade should be active after activation time")
	}
}

// evaluateWindow mimics the BlockChain.evaluateUpgrades logic for the test
// by counting votes in the trailing window and updating the upgrade state.
func evaluateWindow(idx int, versions []uint32, metric uint64) error {
	u := upgrades.GetUpgrades()["test"]
	if u.LockedIn {
		if metric >= u.ActivateAnchor {
			if err := upgrades.SetUpgradeActivated(u.Name, u.ActivateAnchor); err != nil {
				return err
			}
		}
		return nil
	}
	start := idx - int(u.WindowSize) + 1
	if start < 0 {
		start = 0
	}
	yes := 0
	total := 0
	for j := start; j <= idx && total < int(u.WindowSize); j++ {
		total++
		if versions[j]&u.BitMask != 0 {
			yes++
		}
	}
	if uint64(total) == u.WindowSize && uint64(yes) >= u.Threshold {
		if err := upgrades.SetUpgradeLockedIn(u.Name, metric+u.ActivationDelayBlocks); err != nil {
			return err
		}
	}
	return nil
}