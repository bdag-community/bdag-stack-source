package blockchain

import (
	"testing"

	"github.com/BlockdagNetworkLabs/bdag/upgrades"
)

type testBlock struct {
	version uint32
	layer   uint64
	parent  *testBlock
}

// evaluateFromTip traverses back through the test blocks mimicking evaluateUpgrades.
func evaluateFromTip(tip *testBlock, metric uint64) error {
	u := upgrades.GetUpgrades()["test"]
	if u.LockedIn {
		if metric >= u.ActivateAnchor {
			if err := upgrades.SetUpgradeActivated(u.Name, u.ActivateAnchor); err != nil {
				return err
			}
		}
		return nil
	}
	yes, total := 0, 0
	cur := tip
	for uint64(total) < u.WindowSize && cur != nil {
		if cur.version&u.BitMask != 0 {
			yes++
		}
		total++
		cur = cur.parent
	}
	if uint64(total) == u.WindowSize && uint64(yes) >= u.Threshold {
		act := metric + u.ActivationDelayBlocks
		if err := upgrades.SetUpgradeLockedIn(u.Name, act); err != nil {
			return err
		}
		// 🔑 Immediate activation when delay==0 (or metric has already reached act).
		if metric >= act {
			if err := upgrades.SetUpgradeActivated(u.Name, act); err != nil {
				return err
			}
		}
	}
	return nil
}

// TestEvaluateUpgradesReorg builds two competing forks and ensures that after
// reorging to the fork with sufficient votes, the upgrade locks in and activates
// according to the new main chain.
func TestEvaluateUpgradesReorg(t *testing.T) {
	oldUpgrades := upgrades.ReplaceUpgrades(map[string]*upgrades.UpgradeDefinition{
		"test": {Name: "test", BitMask: 0x1, Threshold: 3, WindowSize: 4, ActivationDelayBlocks: 0},
	})
	upgrades.RegisterPersist(nil)
	defer func() {
		upgrades.ReplaceUpgrades(oldUpgrades)
	}()

	genesis := &testBlock{version: VBTopBits}
	a1 := &testBlock{version: VBTopBits, parent: genesis, layer: 1}
	a2 := &testBlock{version: VBTopBits, parent: a1, layer: 2}
	a3 := &testBlock{version: VBTopBits, parent: a2, layer: 3}

	if err := evaluateFromTip(a3, a3.layer); err != nil {
		t.Fatalf("evaluateFromTip: %v", err)
	}
	if upgrades.GetUpgrades()["test"].LockedIn {
		t.Fatalf("upgrade locked in prematurely on initial chain")
	}

	f2 := &testBlock{version: VBTopBits | 0x1, parent: a1, layer: 2}
	f3 := &testBlock{version: VBTopBits | 0x1, parent: f2, layer: 3}
	f4 := &testBlock{version: VBTopBits | 0x1, parent: f3, layer: 4}
	f5 := &testBlock{version: VBTopBits, parent: f4, layer: 5}

	// Reorg to the fork with votes.
	if err := evaluateFromTip(f5, f5.layer); err != nil {
		t.Fatalf("evaluateFromTip: %v", err)
	}
	u := upgrades.GetUpgrades()["test"]
	if !u.LockedIn {
		t.Fatalf("upgrade should lock in after reorg to voting fork")
	}
	if !u.Active {
		t.Fatalf("upgrade should activate immediately after lock-in")
	}
}
