package blockchain

import "testing"

func TestUpgradeReconcileWindowPlan(t *testing.T) {
	def := upgradeRebuildDefinition{Name: "vote", StartLayer: 10, WindowSize: 4, Threshold: 3, ActivationDelayBlocks: 2}
	locked := upgradeRebuildResult{Name: "vote", LockedIn: true, AnchorSet: true, ThresholdAt: 13, ActivateAnchor: 15, Active: true}
	unlocked := upgradeRebuildResult{Name: "vote"}
	cases := []struct {
		name               string
		stored             upgradeRebuildResult
		oldLayer, newLayer uint64
		changed            bool
		changedFrom        uint64
		want               upgradeRebuildResult
		first, end         uint64
	}{
		{"forward unlocked", unlocked, 17, 21, false, 0, unlocked, 2, 3},
		{"same checkpoint changed votes", unlocked, 17, 17, true, 15, unlocked, 1, 2},
		{"locked result preserved", locked, 17, 21, true, 18, locked, 3, 3},
		{"lock window changed", locked, 17, 17, true, 12, unlocked, 0, 2},
		{"rewind before activation", locked, 17, 14, true, 15, upgradeRebuildResult{Name: "vote", LockedIn: true, AnchorSet: true, ThresholdAt: 13, ActivateAnchor: 15}, 1, 1},
		{"change before start", unlocked, 17, 17, true, 0, unlocked, 0, 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, first, end, err := upgradeReconcileWindowPlan(def, tc.stored, tc.oldLayer, tc.newLayer, tc.changed, tc.changedFrom)
			if err != nil || got != tc.want || first != tc.first || end != tc.end {
				t.Fatalf("got result=%+v windows=[%d,%d) err=%v; want %+v [%d,%d)", got, first, end, err, tc.want, tc.first, tc.end)
			}
		})
	}
	bad := locked
	bad.ThresholdAt = 12
	if _, _, _, err := upgradeReconcileWindowPlan(def, bad, 17, 17, false, 0); err == nil {
		t.Fatal("accepted checkpoint state with invalid lock-in window")
	}
}

func TestUpgradeReconcileMatchesFullRecount(t *testing.T) {
	def := upgradeRebuildDefinition{Name: "vote", BitMask: 1, StartLayer: 2, WindowSize: 4, Threshold: 3, ActivationDelayBlocks: 2}
	old := []bool{false, false, true, true, false, true, false, true, false, false, true, true, true, true, false, false, true, true}
	full := func(votes []bool, tip uint64) upgradeRebuildResult {
		result := upgradeRebuildResult{Name: def.Name}
		count := upgradeCompletedVotingWindows(tip, def)
		for i := uint64(0); i < count; i++ {
			start := def.StartLayer + i*def.WindowSize
			tally := newUpgradeSignalTally(def.WindowSize)
			for off := uint64(0); off < def.WindowSize; off++ {
				version := uint32(0)
				if votes[start+off] {
					version = 1
				}
				recordUpgradeSignal(&tally, off, version, 1)
				// Repeated headers in a layer must never add votes.
				recordUpgradeSignal(&tally, off, version, 1)
			}
			applyUpgradeWindowResult(&result, tip, def, start, tally)
		}
		return result
	}
	for fork := uint64(0); fork < 16; fork++ {
		for oldTip := uint64(9); oldTip < 17; oldTip++ {
			for newTip := uint64(9); newTip < 18; newTip++ {
				changed := fork <= oldTip && fork <= newTip
				votes := append([]bool(nil), old...)
				if changed {
					for i := fork; i <= newTip; i++ {
						votes[i] = !votes[i]
					}
				}
				stored := full(old, oldTip)
				got, first, end, err := upgradeReconcileWindowPlan(def, stored, oldTip, newTip, changed, fork)
				if err != nil {
					t.Fatal(err)
				}
				for i := first; i < end; i++ {
					start := def.StartLayer + i*def.WindowSize
					tally := newUpgradeSignalTally(def.WindowSize)
					for off := uint64(0); off < def.WindowSize; off++ {
						version := uint32(0)
						if votes[start+off] {
							version = 1
						}
						recordUpgradeSignal(&tally, off, version, 1)
					}
					applyUpgradeWindowResult(&got, newTip, def, start, tally)
				}
				want := full(votes, newTip)
				if got != want {
					t.Fatalf("fork=%d oldTip=%d newTip=%d changed=%v got=%+v want=%+v", fork, oldTip, newTip, changed, got, want)
				}
			}
		}
	}
}
