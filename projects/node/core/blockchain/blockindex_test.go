package blockchain

import "testing"

const syntheticHistoricalTipOrder uint64 = 11_500_000

func TestFindEVMTransitionOrderPreservesHistoricalPlateau(t *testing.T) {
	const transitionOrder uint64 = 137
	reads := 0

	order, ok := findEVMTransitionOrder(2, syntheticHistoricalTipOrder, func(order uint64) (uint64, bool) {
		reads++
		if order < transitionOrder {
			return 1, true
		}
		return 2, true
	})
	if !ok {
		t.Fatal("valid historical EVM transition was not found")
	}
	if order != transitionOrder {
		t.Fatalf("transition order = %d, want %d", order, transitionOrder)
	}
	if reads > 32 {
		t.Fatalf("lookup read %d native orders, want at most 32", reads)
	}
}

func TestFindEVMTransitionOrderRejectsDiscontinuousPlateauBounded(t *testing.T) {
	const plateauStart uint64 = 211

	tests := []struct {
		name   string
		target uint64
	}{
		{name: "missing number", target: 2},
		{name: "invalid plateau transition", target: 3},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			reads := 0
			order, ok := findEVMTransitionOrder(test.target, syntheticHistoricalTipOrder, func(order uint64) (uint64, bool) {
				reads++
				if order < plateauStart {
					return 1, true
				}
				return 3, true
			})
			if ok {
				t.Fatalf("discontinuous EVM metadata returned native order %d", order)
			}
			if reads > 32 {
				t.Fatalf("lookup read %d native orders, want at most 32", reads)
			}
		})
	}
}
