package params

import (
	"testing"
)

func TestBdagMainnetConfigSetsPredeployMigrationForkBlock(t *testing.T) {
	if BdagMainnetChainConfig.BdagPredeployMigrationBlock == nil {
		t.Fatal("mainnet predeploy migration block is not configured")
	}
	if have := BdagMainnetChainConfig.BdagPredeployMigrationBlock.Uint64(); have != BdagPredeployMigrationBlockNumber {
		t.Fatalf("mainnet predeploy migration block mismatch: have %d, want %d", have, BdagPredeployMigrationBlockNumber)
	}
}

func TestParseForkBlockEnv(t *testing.T) {
	for _, v := range []string{"", "0", "off", "OFF", " none ", "disabled"} {
		if got := parseForkBlockEnv(v); got != nil {
			t.Fatalf("parseForkBlockEnv(%q) = %v, want nil", v, got)
		}
	}
	if got := parseForkBlockEnv(" 600 "); got == nil || got.Uint64() != 600 {
		t.Fatalf("parseForkBlockEnv(\"600\") = %v, want 600", got)
	}
	for _, v := range []string{"abc", "-1", "0x10", "6.0"} {
		func() {
			defer func() {
				if recover() == nil {
					t.Fatalf("parseForkBlockEnv(%q) did not panic", v)
				}
			}()
			parseForkBlockEnv(v)
		}()
	}
}

func TestStakingV2ForkBlockIsDevnetOnly(t *testing.T) {
	if BdagMainnetChainConfig.BdagStakingV2Block != nil {
		t.Fatal("mainnet must not schedule the staking V2 swap")
	}
	if BdagTestnetChainConfig.BdagStakingV2Block != nil || BdagMixnetChainConfig.BdagStakingV2Block != nil || BdagPrivnetChainConfig.BdagStakingV2Block != nil {
		t.Fatal("only devnet may schedule the staking V2 swap")
	}
	if (BdagDevnetChainConfig.BdagStakingV2Block == nil) != (BdagDevnetStakingV2Block == nil) {
		t.Fatal("devnet config must mirror the STAKING_V2_FORK_BLOCK env parse")
	}
}
