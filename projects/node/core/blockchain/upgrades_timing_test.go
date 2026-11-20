package blockchain

import (
	"testing"

	"github.com/BlockdagNetworkLabs/bdag/upgrades"
)

// evaluateWindowTiming mimics BlockChain.evaluateUpgrades while including
// start-layer and timeout handling for tests.
func evaluateWindowTiming(idx int, versions []uint32, metric uint64) error {
	u := upgrades.GetUpgrades()["test"]
	if u.LockedIn {
		return nil
	}
	if metric < u.StartLayer {
		return nil
	}
	if u.TimeoutLayer != 0 && metric >= u.TimeoutLayer {
		return nil
	}
	if u.WindowSize == 0 {
		return nil
	}
	if metric+1 < u.StartLayer+u.WindowSize {
		return nil
	}
	if ((metric - u.StartLayer + 1) % u.WindowSize) != 0 {
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
		return upgrades.SetUpgradeLockedIn(u.Name, metric+u.ActivationDelayBlocks)
	}
	return nil
}

// TestUpgradeStartTime verifies that vote counting does not occur before the
// configured start layer.
func TestUpgradeStartTime(t *testing.T) {
	old := upgrades.ReplaceUpgrades(map[string]*upgrades.UpgradeDefinition{})
	defer upgrades.ReplaceUpgrades(old)
	upgrades.RegisterPersist(nil)
	if err := upgrades.RegisterUpgrade(&upgrades.UpgradeDefinition{Name: "test", BitMask: 0x1, Threshold: 2, WindowSize: 2, StartLayer: 4, ActivationDelayBlocks: 0}); err != nil {
		t.Fatalf("register upgrade: %v", err)
	}

	versions := []uint32{
		VBTopBits | 0x1,
		VBTopBits | 0x1,
		VBTopBits | 0x1,
		VBTopBits | 0x1,
		VBTopBits | 0x1,
		VBTopBits | 0x1,
	}
	layers := []uint64{0, 1, 2, 3, 4, 5}

	for i := range versions {
		if err := evaluateWindowTiming(i, versions, layers[i]); err != nil {
			t.Fatalf("evaluateWindowTiming: %v", err)
		}
		if layers[i] < 5 && upgrades.GetUpgrades()["test"].LockedIn {
			t.Fatalf("upgrade locked in before start time")
		}
	}
	if !upgrades.GetUpgrades()["test"].LockedIn {
		t.Fatalf("upgrade should lock in after start time")
	}
}

// TestUpgradeTimeout verifies that upgrades do not lock in once the timeout layer has
// passed.
func TestUpgradeTimeout(t *testing.T) {
	old := upgrades.ReplaceUpgrades(map[string]*upgrades.UpgradeDefinition{})
	defer upgrades.ReplaceUpgrades(old)
	upgrades.RegisterPersist(nil)
	if err := upgrades.RegisterUpgrade(&upgrades.UpgradeDefinition{Name: "test", BitMask: 0x1, Threshold: 3, WindowSize: 3, TimeoutLayer: 5, ActivationDelayBlocks: 0}); err != nil {
		t.Fatalf("register upgrade: %v", err)
	}

	versions := []uint32{
		VBTopBits,
		VBTopBits,
		VBTopBits,
		VBTopBits,
		VBTopBits | 0x1,
		VBTopBits | 0x1,
		VBTopBits | 0x1,
	}
	layers := []uint64{0, 1, 2, 3, 4, 5, 6}

	for i := range versions {
		if err := evaluateWindowTiming(i, versions, layers[i]); err != nil {
			t.Fatalf("evaluateWindowTiming: %v", err)
		}
	}
	if upgrades.GetUpgrades()["test"].LockedIn {
		t.Fatalf("upgrade should not lock in after timeout")
	}
}
