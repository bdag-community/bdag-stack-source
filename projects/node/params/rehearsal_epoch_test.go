package params

import "testing"

func TestRehearsalEpochDuration(t *testing.T) {
	if got := TestNetParams.StakingEpochDurationSeconds; got != 0 {
		t.Fatalf("testnet historical financial clock changed: duration = %d, want legacy block-based clock", got)
	}
	if got := MainNetParams.StakingEpochSeconds(); got != 604800 {
		t.Fatalf("mainnet staking epoch = %d seconds, want 604800", got)
	}
}
