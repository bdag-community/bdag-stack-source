package blockchain

import "fmt"

// upgradeReconcileWindowPlan retains a result only when the voting window
// that established it is entirely before the changed selected-parent suffix.
// For unlocked upgrades, unchanged losing windows need not be counted again.
func upgradeReconcileWindowPlan(def upgradeRebuildDefinition, stored upgradeRebuildResult, storedLayer, targetLayer uint64, changed bool, changedFrom uint64) (upgradeRebuildResult, uint64, uint64, error) {
	oldCount := upgradeCompletedVotingWindows(storedLayer, def)
	newCount := upgradeCompletedVotingWindows(targetLayer, def)
	st := upgradeState{Active: stored.Active, LockedIn: stored.LockedIn, AnchorSet: stored.AnchorSet, ActivateAnchor: stored.ActivateAnchor, ThresholdAt: stored.ThresholdAt}
	old, err := upgradeForwardBaseResult(st, def, oldCount, oldCount, storedLayer)
	if err != nil || old != stored {
		return upgradeRebuildResult{}, 0, 0, fmt.Errorf("invalid checkpoint state for upgrade %s", def.Name)
	}
	preserved := oldCount
	if newCount < preserved {
		preserved = newCount
	}
	if changed {
		unchanged := uint64(0)
		if def.WindowSize != 0 && changedFrom > def.StartLayer {
			unchanged = (changedFrom - def.StartLayer) / def.WindowSize
		}
		if unchanged < preserved {
			preserved = unchanged
		}
	}
	result := upgradeRebuildResult{Name: def.Name}
	if stored.LockedIn {
		lockedWindow := (stored.ThresholdAt - def.StartLayer + 1) / def.WindowSize
		if lockedWindow <= preserved {
			result = stored
			result.Active = targetLayer >= result.ActivateAnchor
			return result, newCount, newCount, nil
		}
	}
	return result, preserved, newCount, nil
}
