package upgrades

import (
	"testing"

	base "github.com/BlockdagNetworkLabs/bdag/upgrades"
)

func TestDefsContainExpectedUpgrades(t *testing.T) {
	expected := []string{
		base.WalletBlocklistName,
		base.SelectiveBlocklistName,
		base.AuthorizedMiningName,
		base.CanonicalEVMStateV1Name,
	}

	m := make(map[string]*base.UpgradeDefinition, len(Defs))
	for _, def := range Defs {
		m[def.Name] = def
	}

	for _, name := range expected {
		if _, ok := m[name]; !ok {
			t.Errorf("upgrade %s not found in defs", name)
		}
	}
	if t.Failed() {
		t.FailNow()
	}

	if m[base.WalletBlocklistName].StartLayer != mainnetBlocklistStartLayer {
		t.Errorf("wallet blocklist start layer = %d, want %d", m[base.WalletBlocklistName].StartLayer, mainnetBlocklistStartLayer)
	}
	if m[base.SelectiveBlocklistName].StartLayer != mainnetBlocklistStartLayer {
		t.Errorf("selective blocklist start layer = %d, want %d", m[base.SelectiveBlocklistName].StartLayer, mainnetBlocklistStartLayer)
	}
	if m[base.AuthorizedMiningName].StartLayer != mainnetBlocklistStartLayer {
		t.Errorf("authorized mining start layer = %d, want %d", m[base.AuthorizedMiningName].StartLayer, mainnetBlocklistStartLayer)
	}
	if m[base.CanonicalEVMStateV1Name].StartLayer != mainnetCanonicalEVMStartLayer {
		t.Errorf("canonical EVM start layer = %d, want %d", m[base.CanonicalEVMStateV1Name].StartLayer, mainnetCanonicalEVMStartLayer)
	}
}

func TestRewardGovernanceV3ManifestDefinition(t *testing.T) {
	def := RewardGovernanceV3Definition(12_345)
	if def.Name != base.RewardGovernanceV3Name || def.BitMask != 0x10 || def.StartLayer != 12_345 ||
		def.WindowSize != 2_000 || def.Threshold != 1_500 || def.ActivationDelayBlocks != 2_000 || def.TimeoutLayer != 0 {
		t.Fatalf("unexpected reward governance definition: %#v", def)
	}
}

func TestBlocklistUpgradesUseMainnetSchedule(t *testing.T) {
	for _, def := range Defs {
		switch def.Name {
		case base.WalletBlocklistName, base.SelectiveBlocklistName:
		default:
			continue
		}

		if def.StartLayer != mainnetBlocklistStartLayer {
			t.Errorf("%s StartLayer = %d, want %d", def.Name, def.StartLayer, mainnetBlocklistStartLayer)
		}
		if def.WindowSize != mainnetBlocklistWindowSize {
			t.Errorf("%s WindowSize = %d, want %d", def.Name, def.WindowSize, mainnetBlocklistWindowSize)
		}
		if def.Threshold != mainnetBlocklistThreshold {
			t.Errorf("%s Threshold = %d, want %d", def.Name, def.Threshold, mainnetBlocklistThreshold)
		}
		if def.ActivationDelayBlocks != mainnetBlocklistActivationDelay {
			t.Errorf("%s ActivationDelayBlocks = %d, want %d", def.Name, def.ActivationDelayBlocks, mainnetBlocklistActivationDelay)
		}
	}
}

func TestAuthorizedMiningCompatibilityDefinitionUsesHistoricalParameters(t *testing.T) {
	m := make(map[string]*base.UpgradeDefinition, len(Defs))
	for _, def := range Defs {
		m[def.Name] = def
	}

	def, ok := m[base.AuthorizedMiningName]
	if !ok {
		t.Fatalf("upgrade %s not found in defs", base.AuthorizedMiningName)
	}
	if def.BitMask != 0x4 {
		t.Errorf("AuthorizedMining BitMask = 0x%x, want 0x4", def.BitMask)
	}
	if def.StartLayer != mainnetBlocklistStartLayer {
		t.Errorf("AuthorizedMining StartLayer = %d, want %d", def.StartLayer, mainnetBlocklistStartLayer)
	}
	if def.WindowSize != mainnetBlocklistWindowSize {
		t.Errorf("AuthorizedMining WindowSize = %d, want %d", def.WindowSize, mainnetBlocklistWindowSize)
	}
	if def.Threshold != mainnetBlocklistThreshold {
		t.Errorf("AuthorizedMining Threshold = %d, want %d", def.Threshold, mainnetBlocklistThreshold)
	}
	if def.ActivationDelayBlocks != mainnetBlocklistActivationDelay {
		t.Errorf("AuthorizedMining ActivationDelayBlocks = %d, want %d", def.ActivationDelayBlocks, mainnetBlocklistActivationDelay)
	}
}

func TestCanonicalEVMStateV1CompatibilityDefinitionUsesHistoricalParameters(t *testing.T) {
	m := make(map[string]*base.UpgradeDefinition, len(Defs))
	for _, def := range Defs {
		m[def.Name] = def
	}

	def, ok := m[base.CanonicalEVMStateV1Name]
	if !ok {
		t.Fatalf("upgrade %s not found in defs", base.CanonicalEVMStateV1Name)
	}
	if def.BitMask != base.CanonicalEVMStateV1BitMask {
		t.Errorf("CanonicalEVMStateV1 BitMask = 0x%x, want 0x%x", def.BitMask, base.CanonicalEVMStateV1BitMask)
	}
	if def.StartLayer != mainnetCanonicalEVMStartLayer {
		t.Errorf("CanonicalEVMStateV1 StartLayer = %d, want %d", def.StartLayer, mainnetCanonicalEVMStartLayer)
	}
	if def.WindowSize != mainnetCanonicalEVMWindowSize {
		t.Errorf("CanonicalEVMStateV1 WindowSize = %d, want %d", def.WindowSize, mainnetCanonicalEVMWindowSize)
	}
	if def.Threshold != mainnetCanonicalEVMThreshold {
		t.Errorf("CanonicalEVMStateV1 Threshold = %d, want %d", def.Threshold, mainnetCanonicalEVMThreshold)
	}
	if def.ActivationDelayBlocks != mainnetCanonicalEVMActivationDelay {
		t.Errorf("CanonicalEVMStateV1 ActivationDelayBlocks = %d, want %d", def.ActivationDelayBlocks, mainnetCanonicalEVMActivationDelay)
	}
}
