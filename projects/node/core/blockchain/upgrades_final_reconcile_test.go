package blockchain

import (
	"testing"

	"github.com/ethereum/go-ethereum/rlp"
)

func TestUpgradeFinalReconcileValidatesIrreversibleStateAtBothTips(t *testing.T) {
	def := upgradeRebuildDefinition{Name: "final", WindowSize: 4, StartLayer: 10, Threshold: 3, ActivationDelayBlocks: 2}
	state := upgradeState{Active: true, LockedIn: true, AnchorSet: true, ActivateAnchor: 15, ThresholdAt: 13}
	stored, err := upgradeForwardBaseResult(state, def, 2, 2, 20)
	if err != nil || !stored.Active || stored != upgradeResultFromState(def.Name, state) {
		t.Fatalf("stored final state was not accepted exactly: result=%+v err=%v", stored, err)
	}
	advanced, err := upgradeForwardBaseResult(state, def, 2, 7, 40)
	if err != nil || advanced != stored {
		t.Fatalf("final state changed while marker advanced: result=%+v err=%v", advanced, err)
	}
}

func TestUpgradeFinalReconcileRejectsUnsafePersistedStates(t *testing.T) {
	def := upgradeRebuildDefinition{Name: "final", WindowSize: 4, StartLayer: 10, Threshold: 3, ActivationDelayBlocks: 2, TimeoutLayer: 30}
	valid := upgradeState{Active: true, LockedIn: true, AnchorSet: true, ActivateAnchor: 15, ThresholdAt: 13}
	for name, state := range map[string]upgradeState{
		"unlocked":          {Active: true},
		"anchor-disagrees":  {Active: true, LockedIn: true},
		"wrong-window":      {Active: true, LockedIn: true, AnchorSet: true, ThresholdAt: 12, ActivateAnchor: 14},
		"future-threshold":  {Active: true, LockedIn: true, AnchorSet: true, ThresholdAt: 21, ActivateAnchor: 23},
		"wrong-anchor":      {Active: true, LockedIn: true, AnchorSet: true, ThresholdAt: 13, ActivateAnchor: 16},
		"timeout-threshold": {Active: true, LockedIn: true, AnchorSet: true, ThresholdAt: 33, ActivateAnchor: 35},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := upgradeForwardBaseResult(state, def, 2, 7, 40); err == nil {
				t.Fatal("unsafe persisted state accepted")
			}
		})
	}
	if _, err := upgradeForwardBaseResult(valid, def, 0, 7, 40); err == nil {
		t.Fatal("future threshold relative to stored checkpoint accepted")
	}
}

func TestUpgradeFinalReconcileKeepsV2MarkerPredecessorDecodable(t *testing.T) {
	marker := upgradeRebuildState{Version: upgradeRebuildStateVersion, TipID: 7, TipLayer: 42}
	encoded, err := rlp.EncodeToBytes(marker)
	if err != nil {
		t.Fatalf("encode v2 marker: %v", err)
	}
	var decoded upgradeRebuildState
	if err := rlp.DecodeBytes(encoded, &decoded); err != nil || decoded != marker {
		t.Fatalf("v2 marker no longer decodes: marker=%+v decoded=%+v err=%v", marker, decoded, err)
	}
}
