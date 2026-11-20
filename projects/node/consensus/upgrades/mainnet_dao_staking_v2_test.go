package upgrades

import (
	base "github.com/BlockdagNetworkLabs/bdag/upgrades"
	"math"
	"testing"
)

func TestMainnetDAOStakingV2Definition(t *testing.T) {
	def, err := MainnetDAOStakingV2Definition(30_000_000, 2_000)
	if err != nil {
		t.Fatal(err)
	}
	if def.Name != base.MainnetDAOStakingV2Name || def.BitMask != 0x80 || def.WindowSize != 100_000 || def.Threshold != 75_000 || def.StartLayer != 30_000_000 || def.ActivationDelayBlocks != 2_000 || def.TimeoutLayer != 0 {
		t.Fatal("incorrect new production signal policy")
	}
	old := RewardGovernanceV3Definition(10_000)
	if old.WindowSize != 2_000 || old.Threshold != 1_500 || old.Name == def.Name || old.BitMask == def.BitMask {
		t.Fatal("historical definition changed or was reused")
	}
	for _, existing := range Defs {
		if existing.Name == def.Name || existing.BitMask == def.BitMask {
			t.Fatal("review definition was automatically registered")
		}
	}
	for _, input := range [][2]uint64{{0, 2000}, {1, 1999}, {math.MaxUint64 - 99999, 2000}, {math.MaxUint64 - 100001, 2000}} {
		if _, err := MainnetDAOStakingV2Definition(input[0], input[1]); err == nil {
			t.Fatalf("invalid boundary accepted: %v", input)
		}
	}
}
