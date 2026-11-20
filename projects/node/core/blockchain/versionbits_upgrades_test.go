package blockchain

import (
	"testing"

	"github.com/BlockdagNetworkLabs/bdag/upgrades"
)

// TestCalcNextBlockVersionSignalsUpgrade ensures that CalcNextBlockVersion
// includes version-bit signaling for pending upgrades and stops signaling
// once the upgrade is locked in and active.
func TestCalcNextBlockVersionSignalsUpgrade(t *testing.T) {
	oldUpgrades := upgrades.ReplaceUpgrades(map[string]*upgrades.UpgradeDefinition{})
	defer upgrades.ReplaceUpgrades(oldUpgrades)

	// Register a dummy upgrade that uses bit 0 and requires only a single vote.
	if err := upgrades.RegisterUpgrade(&upgrades.UpgradeDefinition{
		Name:                  "dummy",
		BitMask:               0x1,
		Threshold:             1,
		WindowSize:            1,
		ActivationDelayBlocks: 0,
	}); err != nil {
		t.Fatalf("register upgrade: %v", err)
	}

	var b BlockChain
	version, err := b.CalcNextBlockVersion()
	if err != nil {
		t.Fatalf("CalcNextBlockVersion: %v", err)
	}
	if want := VBTopBits | uint32(0x1); version != want {
		t.Fatalf("unexpected version: want %x, got %x", want, version)
	}

	// Lock in the upgrade and ensure the bit stops being signaled.
	if err := upgrades.SetUpgradeLockedIn("dummy", 1); err != nil {
		t.Fatalf("lock in: %v", err)
	}
	version, err = b.CalcNextBlockVersion()
	if err != nil {
		t.Fatalf("CalcNextBlockVersion after lock-in: %v", err)
	}
	if version != VBTopBits {
		t.Fatalf("expected version bits cleared after lock-in, got %x", version)
	}

	// Activate the upgrade and ensure it reports active.
	if err := upgrades.SetUpgradeActivated("dummy", 1); err != nil {
		t.Fatalf("activate: %v", err)
	}
	if !upgrades.IsUpgradeActive("dummy", 1) {
		t.Fatalf("upgrade should be active")
	}
}

func TestCalcNextBlockVersionNeverSignalsRetiredCanonicalEVMV1(t *testing.T) {
	oldUpgrades := upgrades.ReplaceUpgrades(map[string]*upgrades.UpgradeDefinition{
		upgrades.CanonicalEVMStateV1Name: {
			Name:         upgrades.CanonicalEVMStateV1Name,
			BitMask:      upgrades.CanonicalEVMStateV1BitMask,
			Threshold:    1,
			WindowSize:   1,
			StartLayer:   0,
			TimeoutLayer: 0,
		},
	})
	defer upgrades.ReplaceUpgrades(oldUpgrades)

	var b BlockChain
	version, err := b.CalcNextBlockVersion()
	if err != nil {
		t.Fatalf("CalcNextBlockVersion without checkpoint requirement: %v", err)
	}
	if version&upgrades.CanonicalEVMStateV1BitMask != 0 {
		t.Fatalf("retired canonical EVM bit was signaled when the checkpoint gate was disabled: %x", version)
	}
}
