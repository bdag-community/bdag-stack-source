package blockchain

import (
	"crypto/sha256"
	"fmt"
	"math"
	"time"

	blockdag "github.com/BlockdagNetworkLabs/bdag/blockdag"
	"github.com/BlockdagNetworkLabs/bdag/database/rawdb"
	l "github.com/BlockdagNetworkLabs/bdag/log"
	"github.com/BlockdagNetworkLabs/bdag/upgrades"
	"github.com/ethereum/go-ethereum/ethdb"
	"github.com/ethereum/go-ethereum/rlp"
)

const maxUpgradeReconcileWindows = 128
const maxUpgradeReconcileLayerSpan = 1000000

type reconcileWindow struct {
	result     int
	definition upgradeRebuildDefinition
	start, end uint64
	tally      upgradeSignalTally
}

// commitUpgradeResults publishes a complete checkpoint and its live state as
// one database batch. Caller holds chainLock and upgradeRebuildMu.
func (b *BlockChain) commitUpgradeResults(marker upgradeRebuildState, results []upgradeRebuildResult) error {
	raw, ok := b.DB().(interface{ DB() ethdb.Database })
	if !ok {
		return fmt.Errorf("database cannot atomically persist upgrade states")
	}
	encodedResults, err := rlp.EncodeToBytes(results)
	if err != nil {
		return err
	}
	marker.ResultsHash = sha256.Sum256(encodedResults)
	encodedMarker, err := rlp.EncodeToBytes(marker)
	if err != nil {
		return err
	}
	batch := raw.DB().NewBatch()
	if err := batch.Put(rawdb.UpgradeRebuildResultsKey(), encodedResults); err != nil {
		return err
	}
	if err := batch.Put(rawdb.UpgradeRebuildStateKey(), encodedMarker); err != nil {
		return err
	}
	states := make(map[string]upgrades.RebuiltState, len(results))
	for _, r := range results {
		if _, exists := states[r.Name]; exists {
			return fmt.Errorf("duplicate upgrade result %s", r.Name)
		}
		states[r.Name] = upgrades.RebuiltState{Active: r.Active, LockedIn: r.LockedIn, AnchorSet: r.AnchorSet, ActivateAnchor: r.ActivateAnchor, ThresholdAt: r.ThresholdAt}
		st := upgradeState{Active: r.Active, LockedIn: r.LockedIn, AnchorSet: r.AnchorSet, ActivateAnchor: r.ActivateAnchor, ActivationTime: r.ActivateAnchor, ThresholdAt: r.ThresholdAt}
		enc, err := rlp.EncodeToBytes(st)
		if err != nil {
			return err
		}
		if err := batch.Put(rawdb.UpgradeStateKey(r.Name), enc); err != nil {
			return err
		}
	}
	if err := upgrades.PublishRebuiltStates(states, batch.Write); err != nil {
		return err
	}
	b.upgradeRebuildTipHeight.Store(marker.TipHeight)
	b.resetUpgradeCache()
	return nil
}

func (b *BlockChain) loadUpgradeCheckpoint() (*upgradeRebuildState, []upgradeRebuildResult, bool) {
	marker, found, err := b.loadUpgradeRebuildState()
	if err != nil || !found || marker.Version != upgradeRebuildStateVersion || marker.DefinitionsHash != upgradeDefinitionsHash() {
		return nil, nil, false
	}
	data, err := b.DB().Get(rawdb.UpgradeRebuildResultsKey())
	if err != nil || sha256.Sum256(data) != marker.ResultsHash {
		return nil, nil, false
	}
	var results []upgradeRebuildResult
	if err := rlp.DecodeBytes(data, &results); err != nil {
		return nil, nil, false
	}
	defs := currentUpgradeRebuildDefinitions()
	if len(results) != len(defs) {
		return nil, nil, false
	}
	for i := range defs {
		if results[i].Name != defs[i].Name {
			return nil, nil, false
		}
	}
	return marker, results, true
}

// reconcileUpgradeCheckpoint only trusts a prior checkpoint after proving its
// ancestry against the pre-reorg tip. Changed completed windows are recounted.
// Caller holds chainLock and upgradeRebuildMu.
func (b *BlockChain) reconcileUpgradeCheckpoint(tip, previous blockdag.IBlock, reason string) (bool, error) {
	marker, stored, ok := b.loadUpgradeCheckpoint()
	if !ok {
		l.Info("upgrade checkpoint unavailable; full rebuild required", "reason", reason)
		return false, nil
	}
	steps := uint64(0)
	parent := func(block blockdag.IBlock) (blockdag.IBlock, bool) { return b.upgradeMainParent(block, &steps) }
	changed, changedFrom := false, uint64(0)
	proofTip := tip
	if previous != nil {
		proofTip = previous
		changed = !upgradeRebuildBlocksMatch(previous, tip)
		if changed {
			var proved bool
			changedFrom, proved = selectedParentReorgChangedFrom(previous, tip, parent)
			if !proved {
				l.Info("upgrade reorg ancestry proof failed; full rebuild required", "reason", reason)
				return false, nil
			}
		}
	}
	if !upgradeMarkerIsAncestor(*marker, proofTip, parent) {
		l.Info("upgrade checkpoint ancestry proof failed; full rebuild required", "reason", reason, "stored_tip_layer", marker.TipLayer, "tip_layer", tip.GetLayer())
		return false, nil
	}
	defs := currentUpgradeRebuildDefinitions()
	results := make([]upgradeRebuildResult, len(defs))
	windows := make([]reconcileWindow, 0)
	minStart := uint64(math.MaxUint64)
	for i, def := range defs {
		result, first, end, err := upgradeReconcileWindowPlan(def, stored[i], marker.TipLayer, uint64(tip.GetLayer()), changed, changedFrom)
		if err != nil {
			return false, nil
		}
		results[i] = result
		if end-first > maxUpgradeReconcileWindows || uint64(len(windows))+end-first > maxUpgradeReconcileWindows {
			return false, nil
		}
		for index := first; index < end; index++ {
			if def.WindowSize == 0 || index > (math.MaxUint64-def.StartLayer)/def.WindowSize {
				return false, nil
			}
			start := def.StartLayer + index*def.WindowSize
			if start > math.MaxUint64-(def.WindowSize-1) {
				return false, nil
			}
			endLayer := start + def.WindowSize - 1
			windows = append(windows, reconcileWindow{result: i, definition: def, start: start, end: endLayer, tally: newUpgradeSignalTally(def.WindowSize)})
			if start < minStart {
				minStart = start
			}
		}
	}
	started := time.Now()
	var processed uint64
	if len(windows) > 0 {
		if uint64(tip.GetLayer()) < minStart || uint64(tip.GetLayer())-minStart > maxUpgradeReconcileLayerSpan {
			return false, nil
		}
		var scanErr error
		processed, scanErr = scanUpgradeReconcileWindows(tip, windows, minStart,
			func(block blockdag.IBlock) (uint32, bool) {
				hdr := b.GetBlockHeader(block)
				if hdr == nil {
					return 0, false
				}
				return hdr.Version, true
			},
			func(block blockdag.IBlock) (blockdag.IBlock, error) {
				parent := block.GetMainParent()
				if parent == blockdag.MaxId {
					return nil, nil
				}
				next, err := b.bd.ReadBlockById(parent)
				if err != nil || next == nil {
					return nil, fmt.Errorf("missing upgrade parent %d: %v", parent, err)
				}
				return next, nil
			})
		if scanErr != nil {
			return false, scanErr
		}
	}
	for i := range windows {
		w := &windows[i]
		applyUpgradeWindowResult(&results[w.result], uint64(tip.GetLayer()), w.definition, w.start, w.tally)
	}
	if err := b.commitUpgradeResults(b.newUpgradeRebuildState(tip), results); err != nil {
		return false, err
	}
	fromLayer := uint64(tip.GetLayer())
	if len(windows) != 0 {
		fromLayer = minStart
	}
	l.Info("upgrade checkpoint reconciled", "reason", reason, "stored_tip_layer", marker.TipLayer,
		"tip_layer", tip.GetLayer(), "changed", changed, "changed_from", changedFrom,
		"stored_checkpoint", upgradeRebuildCheckpoint(marker.TipLayer, defs),
		"expected_checkpoint", upgradeRebuildCheckpoint(uint64(tip.GetLayer()), defs),
		"from_layer", fromLayer, "windows", len(windows), "headers", processed, "duration", time.Since(started))
	return true, nil
}

// scanUpgradeReconcileWindows counts only complete windows on the proven chain.
// It reports incomplete ancestry instead of returning partial consensus state.
func scanUpgradeReconcileWindows(tip blockdag.IBlock, windows []reconcileWindow, minStart uint64,
	header func(blockdag.IBlock) (uint32, bool), parent func(blockdag.IBlock) (blockdag.IBlock, error)) (uint64, error) {
	var processed uint64
	for cur := tip; cur != nil; {
		layer := uint64(cur.GetLayer())
		if layer < minStart {
			break
		}
		version, ok := header(cur)
		if !ok {
			return processed, fmt.Errorf("missing upgrade header at layer %d", layer)
		}
		for i := range windows {
			w := &windows[i]
			if layer >= w.start && layer <= w.end {
				recordUpgradeSignal(&w.tally, layer-w.start, version, w.definition.BitMask)
			}
		}
		processed++
		next, err := parent(cur)
		if err != nil {
			return processed, err
		}
		if next == nil {
			if layer > minStart {
				return processed, fmt.Errorf("missing upgrade ancestry before layer %d", layer)
			}
			break
		}
		if next.GetHeight() >= cur.GetHeight() {
			return processed, fmt.Errorf("invalid upgrade ancestry at layer %d", layer)
		}
		cur = next
	}
	return processed, nil
}

// UpgradeReconciliationStatus prevents mining while consensus upgrade state is uncertain.
func (b *BlockChain) UpgradeReconciliationStatus() (bool, string) {
	if b != nil && b.upgradeRebuildUnavailable.Load() {
		return true, "upgrade state unavailable; reconciliation required"
	}
	return false, ""
}
