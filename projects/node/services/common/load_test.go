package common

import (
	"path/filepath"
	"testing"

	evmparams "github.com/BlockdagNetworkLabs/bdag/bevm/params"
	"github.com/BlockdagNetworkLabs/bdag/config"
	"github.com/BlockdagNetworkLabs/bdag/params"
	"github.com/BlockdagNetworkLabs/bdag/upgrades"
)

func TestSetupConfigAcceptsMainnetEnv(t *testing.T) {
	original := params.ActiveNetParams
	t.Cleanup(func() {
		params.ActiveNetParams = original
	})

	t.Setenv("BDAG_NETWORK", "mainnet")
	params.ActiveNetParams = &params.MixNetParam

	dataDir := t.TempDir()
	cfg := &config.Config{
		DataDir:       dataDir,
		NoFileLogging: true,
		DebugLevel:    "info",
	}

	if err := SetupConfig(cfg); err != nil {
		t.Fatalf("SetupConfig returned error for BDAG_NETWORK=mainnet: %v", err)
	}
	if params.ActiveNetParams != &params.MainNetParam {
		t.Fatalf("active params = %s, want mainnet", params.ActiveNetParams.Name)
	}
	if got, want := cfg.DataDir, filepath.Join(dataDir, "mainnet"); got != want {
		t.Fatalf("cfg.DataDir = %q, want %q", got, want)
	}
}

func TestRegisterTestnetRewardGovernanceUpgrade(t *testing.T) {
	originalNet := params.ActiveNetParams
	originalManifest := evmparams.BdagTestnetGovernanceFork
	manifest := *evmparams.TestnetGovernanceArtifactTemplate()
	manifest.SignalWindowStartLayer = 50_000
	manifest.ActivationLayer = 53_999
	manifest.InitialReleaseDistributedAt = 100
	manifest.NoticePublishedAt = 100
	manifest.NotBeforeTimestamp = 86_500
	evmparams.BdagTestnetGovernanceFork = &manifest
	originalUpgrades := upgrades.ReplaceUpgrades(map[string]*upgrades.UpgradeDefinition{})
	t.Cleanup(func() {
		params.ActiveNetParams = originalNet
		evmparams.BdagTestnetGovernanceFork = originalManifest
		upgrades.ReplaceUpgrades(originalUpgrades)
	})

	params.ActiveNetParams = &params.TestNetParam
	if err := registerTestnetRewardGovernanceUpgrade(); err != nil {
		t.Fatalf("register testnet reward governance: %v", err)
	}
	definition := upgrades.GetUpgrades()[upgrades.RewardGovernanceV3Name]
	if definition == nil {
		t.Fatal("reward governance upgrade was not registered")
	}
	if definition.StartLayer != manifest.SignalWindowStartLayer || definition.WindowSize != 2_000 || definition.Threshold != 1_500 || definition.ActivationDelayBlocks != 2_000 {
		t.Fatalf("unexpected testnet reward governance definition: %#v", definition)
	}
	if err := registerTestnetRewardGovernanceUpgrade(); err != nil {
		t.Fatalf("idempotent registration failed: %v", err)
	}

	definition.StartLayer++
	upgrades.ReplaceUpgrades(map[string]*upgrades.UpgradeDefinition{upgrades.RewardGovernanceV3Name: definition})
	if err := registerTestnetRewardGovernanceUpgrade(); err == nil {
		t.Fatal("accepted a conflicting persisted reward governance definition")
	}
}

func TestUnscheduledPreparationDoesNotRegisterOrFollowRewardUpgrade(t *testing.T) {
	originalNet, originalManifest := params.ActiveNetParams, evmparams.BdagTestnetGovernanceFork
	originalUpgrades := upgrades.ReplaceUpgrades(map[string]*upgrades.UpgradeDefinition{})
	t.Cleanup(func() {
		params.ActiveNetParams = originalNet
		evmparams.BdagTestnetGovernanceFork = originalManifest
		upgrades.ReplaceUpgrades(originalUpgrades)
	})
	params.ActiveNetParams, evmparams.BdagTestnetGovernanceFork = &params.TestNetParam, nil
	if err := registerTestnetRewardGovernanceUpgrade(); err != nil {
		t.Fatal(err)
	}
	if upgrades.GetUpgrades()[upgrades.RewardGovernanceV3Name] != nil {
		t.Fatal("unscheduled source registered an activation")
	}
	upgrades.ReplaceUpgrades(map[string]*upgrades.UpgradeDefinition{upgrades.RewardGovernanceV3Name: {Name: upgrades.RewardGovernanceV3Name}})
	if err := registerTestnetRewardGovernanceUpgrade(); err == nil {
		t.Fatal("unscheduled preparation followed a persisted upgrade")
	}
}
