package blockchain

import (
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	blockdag "github.com/BlockdagNetworkLabs/bdag/blockdag"
	"github.com/BlockdagNetworkLabs/bdag/core/types"
	"github.com/BlockdagNetworkLabs/bdag/database/rawdb"
	l "github.com/BlockdagNetworkLabs/bdag/log"
	"github.com/BlockdagNetworkLabs/bdag/upgrades"
	"github.com/cockroachdb/pebble"
	ethcommon "github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/ethdb"
	"github.com/ethereum/go-ethereum/rlp"
	"github.com/syndtr/goleveldb/leveldb"
)

type upgradeState struct {
	Active         bool
	LockedIn       bool
	ActivationTime uint64 // legacy field for compatibility
	AnchorSet      bool
	ActivateAnchor uint64
	ThresholdAt    uint64
}

type upgradeWindowCache struct {
	start uint64
	end   uint64
	tally upgradeSignalTally
}

// upgradeRebuildStateVersion is bumped when the persisted rebuild marker no
// longer proves that the current tally algorithm has scanned historical votes.
const upgradeRebuildStateVersion uint64 = 3

// finalUpgradeReconcileMaxParents bounds every final-state ancestry proof. A
// proof failure deliberately falls back to the historical rebuild.
const finalUpgradeReconcileMaxParents = 8192

type upgradeRebuildState struct {
	Version         uint64
	TipID           uint64
	TipLayer        uint64
	TipHash         ethcommon.Hash
	DefinitionsHash ethcommon.Hash
	ResultsHash     ethcommon.Hash `rlp:"optional"`
	TipHeight       uint64         `rlp:"optional"`
}

type upgradeRebuildDefinition struct {
	Name                  string
	BitMask               uint32
	Threshold             uint64
	WindowSize            uint64
	ActivationDelayBlocks uint64
	StartLayer            uint64
	TimeoutLayer          uint64
}

type upgradeRebuildResult struct {
	Name           string
	Active         bool
	LockedIn       bool
	AnchorSet      bool
	ActivateAnchor uint64
	ThresholdAt    uint64
}

type upgradeSignalTally struct {
	windowSize      uint64
	headers         uint64
	yesHeaders      uint64
	uniqueLayers    uint64
	yesLayers       uint64
	duplicateLayers uint64
	seenLayers      []uint64
	yesLayerBits    []uint64
}

type upgradeRebuildScanState struct {
	def             upgradeRebuildDefinition
	result          upgradeRebuildResult
	relevant        bool
	initialized     bool
	windowIndex     uint64
	windowStart     uint64
	tally           upgradeSignalTally
	duplicateWins   uint64
	duplicateLayers uint64
}

func isUpgradeStateNotFound(err error) bool {
	return errors.Is(err, leveldb.ErrNotFound) ||
		errors.Is(err, pebble.ErrNotFound) ||
		strings.Contains(strings.ToLower(err.Error()), "not found")
}

// loadUpgradeStates restores upgrade states from the database.
func (b *BlockChain) loadUpgradeStates() error {
	names := make([]string, 0)
	upgrades.ForEachUpgrade(func(name string, _ *upgrades.UpgradeDefinition) {
		names = append(names, name)
	})

	for _, name := range names {
		data, err := b.DB().Get(rawdb.UpgradeStateKey(name))
		if err != nil {
			if isUpgradeStateNotFound(err) {
				continue
			}
			l.Error("failed to read upgrade state", "name", name, "err", err)
			return err
		}
		if len(data) == 0 {
			continue
		}
		var st upgradeState
		if err := rlp.DecodeBytes(data, &st); err != nil {
			l.Warn("failed to decode upgrade state", "name", name, "err", err)
			continue
		}
		upgrades.UpdateUpgrade(name, func(u *upgrades.UpgradeDefinition) {
			u.Active = st.Active
			u.LockedIn = st.LockedIn
			u.AnchorSet = st.AnchorSet
			anchor := st.ActivateAnchor
			if anchor == 0 {
				anchor = st.ActivationTime
			}
			u.ActivateAnchor = anchor
			u.ThresholdAt = st.ThresholdAt
		})
	}

	// Ensure no persisted upgrade states exist without a corresponding definition.
	raw, ok := b.DB().(interface{ DB() ethdb.Database })
	if !ok {
		return fmt.Errorf("database does not expose raw access for upgrade state validation")
	}
	it := raw.DB().NewIterator(rawdb.UpgradeStateKey(""), nil)
	defer it.Release()

	known := upgrades.GetUpgrades()
	for it.Next() {
		key := it.Key()
		name := string(key[len(rawdb.UpgradeStateKey("")):])
		if _, ok := known[name]; !ok {
			return fmt.Errorf("persisted upgrade state %s has no definition", name)
		}
	}
	if err := it.Error(); err != nil {
		l.Error("failed to iterate upgrade states", "err", err)
		return err
	}
	return nil
}

// persistUpgradeState stores the provided upgrade state to the database.
func (b *BlockChain) persistUpgradeState(name string, u *upgrades.UpgradeDefinition) error {
	st := upgradeState{
		Active:         u.Active,
		LockedIn:       u.LockedIn,
		ActivationTime: u.ActivateAnchor,
		AnchorSet:      u.AnchorSet,
		ActivateAnchor: u.ActivateAnchor,
		ThresholdAt:    u.ThresholdAt,
	}
	enc, err := rlp.EncodeToBytes(st)
	if err != nil {
		l.Warn("failed to encode upgrade state", "name", name, "err", err)
		return err
	}
	if err := b.DB().Put(rawdb.UpgradeStateKey(name), enc); err != nil {
		l.Warn("failed to store upgrade state", "name", name, "err", err)
		return err
	}
	return nil
}

func (b *BlockChain) loadPersistedUpgradeState(name string) (upgradeState, bool, error) {
	data, err := b.DB().Get(rawdb.UpgradeStateKey(name))
	if err != nil {
		if isUpgradeStateNotFound(err) {
			return upgradeState{}, false, nil
		}
		return upgradeState{}, false, err
	}
	if len(data) == 0 {
		return upgradeState{}, false, nil
	}
	var state upgradeState
	if err := rlp.DecodeBytes(data, &state); err != nil {
		return upgradeState{}, false, err
	}
	if state.ActivateAnchor == 0 {
		state.ActivateAnchor = state.ActivationTime
	}
	return state, true, nil
}

func upgradeResultFromState(name string, state upgradeState) upgradeRebuildResult {
	return upgradeRebuildResult{Name: name, Active: state.Active, LockedIn: state.LockedIn, AnchorSet: state.AnchorSet, ActivateAnchor: state.ActivateAnchor, ThresholdAt: state.ThresholdAt}
}

func currentUpgradeRebuildResults(definitions []upgradeRebuildDefinition) ([]upgradeRebuildResult, error) {
	current := upgrades.GetUpgrades()
	results := make([]upgradeRebuildResult, 0, len(definitions))
	for _, def := range definitions {
		state, ok := current[def.Name]
		if !ok || state == nil {
			return nil, fmt.Errorf("upgrade definition %s has no current state", def.Name)
		}
		results = append(results, upgradeRebuildResult{Name: def.Name, Active: state.Active, LockedIn: state.LockedIn, AnchorSet: state.AnchorSet, ActivateAnchor: state.ActivateAnchor, ThresholdAt: state.ThresholdAt})
	}
	return results, nil
}

func upgradeRebuildResultsEqual(left, right []upgradeRebuildResult) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

// upgradeForwardBaseResult validates a persisted state against the immutable
// voting definition without replaying headers. It is usable only after every
// definition has reached its irreversible final state.
func upgradeForwardBaseResult(state upgradeState, def upgradeRebuildDefinition, storedCompleted, expectedCompleted, expectedLayer uint64) (upgradeRebuildResult, error) {
	result := upgradeRebuildResult{Name: def.Name}
	if state.Active && (!state.LockedIn || !state.AnchorSet) {
		return result, errors.New("active state is not locked and anchored")
	}
	if state.LockedIn != state.AnchorSet {
		return result, errors.New("locked and anchor flags disagree")
	}
	if !state.LockedIn {
		if state.Active || state.ActivateAnchor != 0 || state.ThresholdAt != 0 {
			return result, errors.New("unlocked state has activation metadata")
		}
		return result, nil
	}
	if def.WindowSize == 0 || state.ThresholdAt < def.StartLayer {
		return result, errors.New("threshold is outside voting window")
	}
	offset := state.ThresholdAt - def.StartLayer + 1
	if offset == 0 || offset%def.WindowSize != 0 {
		return result, errors.New("threshold is not voting window end")
	}
	lockedWindow := offset / def.WindowSize
	if lockedWindow > storedCompleted || lockedWindow > expectedCompleted {
		return result, errors.New("threshold is ahead of checkpoint")
	}
	if def.TimeoutLayer != 0 && state.ThresholdAt >= def.TimeoutLayer {
		return result, errors.New("threshold is at or after timeout")
	}
	if state.ThresholdAt > math.MaxUint64-def.ActivationDelayBlocks || state.ActivateAnchor != state.ThresholdAt+def.ActivationDelayBlocks {
		return result, errors.New("activation anchor mismatch")
	}
	return upgradeRebuildResult{Name: def.Name, Active: expectedLayer >= state.ActivateAnchor, LockedIn: true, AnchorSet: true, ActivateAnchor: state.ActivateAnchor, ThresholdAt: state.ThresholdAt}, nil
}

func upgradeRebuildBlocksMatch(left, right blockdag.IBlock) bool {
	return left != nil && right != nil && left.GetHash() != nil && right.GetHash() != nil && left.GetID() == right.GetID() && left.GetLayer() == right.GetLayer() && *left.GetHash() == *right.GetHash()
}

func upgradeRebuildTipMatches(tip blockdag.IBlock, marker upgradeRebuildState) bool {
	return tip != nil && tip.GetHash() != nil && uint64(tip.GetID()) == marker.TipID && uint64(tip.GetLayer()) == marker.TipLayer && *tip.GetHash() == marker.TipHash
}

func (b *BlockChain) upgradeMainParent(block blockdag.IBlock, steps *uint64) (blockdag.IBlock, bool) {
	if block == nil || *steps >= finalUpgradeReconcileMaxParents || block.GetMainParent() == blockdag.MaxId {
		return nil, false
	}
	*steps++
	parent := b.bd.GetBlockById(block.GetMainParent())
	if parent == nil || parent.GetHeight() >= block.GetHeight() {
		return nil, false
	}
	return parent, true
}

func upgradeMarkerIsAncestor(marker upgradeRebuildState, tip blockdag.IBlock, parent func(blockdag.IBlock) (blockdag.IBlock, bool)) bool {
	for current := tip; current != nil; {
		if uint64(current.GetID()) == marker.TipID {
			return upgradeRebuildTipMatches(current, marker)
		}
		if uint64(current.GetLayer()) < marker.TipLayer {
			return false
		}
		next, ok := parent(current)
		if !ok {
			return false
		}
		current = next
	}
	return false
}

// selectedParentReorgChangedFrom returns the first changed suffix layer. Both
// legs share one bounded traversal budget so malformed ancestry cannot turn a
// final-state shortcut into an unbounded walk.
func selectedParentReorgChangedFrom(previous, current blockdag.IBlock, parent func(blockdag.IBlock) (blockdag.IBlock, bool)) (uint64, bool) {
	if previous == nil || current == nil {
		return 0, false
	}
	if upgradeRebuildBlocksMatch(previous, current) {
		return 0, true
	}
	changed := uint64(math.MaxUint64)
	record := func(block blockdag.IBlock) {
		if uint64(block.GetLayer()) < changed {
			changed = uint64(block.GetLayer())
		}
	}
	for previous.GetHeight() > current.GetHeight() {
		record(previous)
		var ok bool
		previous, ok = parent(previous)
		if !ok {
			return 0, false
		}
	}
	for current.GetHeight() > previous.GetHeight() {
		record(current)
		var ok bool
		current, ok = parent(current)
		if !ok {
			return 0, false
		}
	}
	for !upgradeRebuildBlocksMatch(previous, current) {
		record(previous)
		record(current)
		var ok bool
		previous, ok = parent(previous)
		if !ok {
			return 0, false
		}
		current, ok = parent(current)
		if !ok {
			return 0, false
		}
	}
	return changed, changed != math.MaxUint64
}

func (b *BlockChain) persistUpgradeRebuildState(st upgradeRebuildState) error {
	enc, err := rlp.EncodeToBytes(st)
	if err != nil {
		return err
	}
	return b.DB().Put(rawdb.UpgradeRebuildStateKey(), enc)
}

func (b *BlockChain) loadUpgradeRebuildState() (*upgradeRebuildState, bool, error) {
	data, err := b.DB().Get(rawdb.UpgradeRebuildStateKey())
	if err != nil {
		if isUpgradeStateNotFound(err) {
			return nil, false, nil
		}
		return nil, false, err
	}
	if len(data) == 0 {
		return nil, false, nil
	}

	var st upgradeRebuildState
	if err := rlp.DecodeBytes(data, &st); err != nil {
		return nil, false, err
	}
	return &st, true, nil
}

func (b *BlockChain) newUpgradeRebuildState(tip blockdag.IBlock) upgradeRebuildState {
	tipHash := ethcommon.Hash{}
	if tip != nil && tip.GetHash() != nil {
		tipHash = *tip.GetHash()
	}
	return upgradeRebuildState{
		Version:         upgradeRebuildStateVersion,
		TipID:           uint64(tip.GetID()),
		TipLayer:        uint64(tip.GetLayer()),
		TipHeight:       uint64(tip.GetHeight()),
		TipHash:         tipHash,
		DefinitionsHash: upgradeDefinitionsHash(),
	}
}

// Matching window counters alone cannot establish selected-parent ancestry.
func upgradeRebuildStateMatches(st, expected upgradeRebuildState) bool {
	return st == expected
}

func upgradeRebuildCheckpointMatches(storedLayer, expectedLayer uint64, definitions []upgradeRebuildDefinition) bool {
	for _, def := range definitions {
		if upgradeCompletedVotingWindows(storedLayer, def) != upgradeCompletedVotingWindows(expectedLayer, def) {
			return false
		}
	}
	return true
}

func upgradeRebuildCheckpoint(layer uint64, definitions []upgradeRebuildDefinition) string {
	parts := make([]string, 0, len(definitions))
	for _, def := range definitions {
		parts = append(parts, fmt.Sprintf("%s:%d", def.Name, upgradeCompletedVotingWindows(layer, def)))
	}
	return strings.Join(parts, ",")
}

func upgradeCompletedVotingWindows(layer uint64, def upgradeRebuildDefinition) uint64 {
	if def.WindowSize == 0 || layer < def.StartLayer {
		return 0
	}

	complete := (layer - def.StartLayer + 1) / def.WindowSize
	if def.TimeoutLayer == 0 || def.TimeoutLayer <= def.StartLayer {
		return complete
	}

	maxWindowsBeforeTimeout := (def.TimeoutLayer - def.StartLayer + def.WindowSize - 1) / def.WindowSize
	if complete > maxWindowsBeforeTimeout {
		return maxWindowsBeforeTimeout
	}
	return complete
}

func upgradeDefinitionsHash() ethcommon.Hash {
	definitions := currentUpgradeRebuildDefinitions()
	h := sha256.New()
	var buf [8]byte
	for _, def := range definitions {
		h.Write([]byte(def.Name))
		h.Write([]byte{0})

		binary.BigEndian.PutUint32(buf[:4], def.BitMask)
		h.Write(buf[:4])
		binary.BigEndian.PutUint64(buf[:], def.Threshold)
		h.Write(buf[:])
		binary.BigEndian.PutUint64(buf[:], def.WindowSize)
		h.Write(buf[:])
		binary.BigEndian.PutUint64(buf[:], def.ActivationDelayBlocks)
		h.Write(buf[:])
		binary.BigEndian.PutUint64(buf[:], def.StartLayer)
		h.Write(buf[:])
		binary.BigEndian.PutUint64(buf[:], def.TimeoutLayer)
		h.Write(buf[:])
	}
	return ethcommon.BytesToHash(h.Sum(nil))
}

// MaybeRebuildUpgradesFromHeaders validates the persisted checkpoint on startup.
func (b *BlockChain) MaybeRebuildUpgradesFromHeaders() error {
	b.ChainLock()
	defer b.ChainUnlock()
	return b.reconcileUpgradesHeld("startup", nil, false)
}

// RebuildUpgradesFromHeadersIfNeeded reconciles the selected-parent state after sync.
func (b *BlockChain) RebuildUpgradesFromHeadersIfNeeded(reason string) {
	b.ChainLock()
	defer b.ChainUnlock()
	if err := b.reconcileUpgradesHeld(reason, nil, false); err != nil {
		l.Error("upgrade reconciliation failed", "reason", reason, "err", err)
	}
}

// RebuildUpgradesFromHeaders forces a complete selected-parent recount.
func (b *BlockChain) RebuildUpgradesFromHeaders() {
	b.ChainLock()
	defer b.ChainUnlock()
	if err := b.reconcileUpgradesHeld("forced", nil, true); err != nil {
		l.Error("upgrade rebuild failed", "err", err)
	}
}

// reconcileUpgradesHeld keeps the chain stable through scanning and publication.
// The chain lock is always acquired before upgradeRebuildMu.
func (b *BlockChain) reconcileUpgradesHeld(reason string, previous blockdag.IBlock, force bool) (err error) {
	b.upgradeRebuildMu.Lock()
	defer b.upgradeRebuildMu.Unlock()
	b.upgradeRebuildUnavailable.Store(true)
	defer func() { b.upgradeRebuildUnavailable.Store(err != nil) }()
	tip := b.bd.GetMainChainTip()
	if tip == nil {
		return fmt.Errorf("upgrade reconciliation has no selected-parent tip")
	}
	if !force {
		var handled bool
		handled, err = b.reconcileUpgradeCheckpoint(tip, previous, reason)
		if err != nil || handled {
			return err
		}
	}
	definitions := currentUpgradeRebuildDefinitions()
	l.Info("upgrade rebuild started", "reason", reason, "tip_id", tip.GetID(), "tip_layer", tip.GetLayer(), "upgrades", len(definitions))
	results, complete := b.rebuildUpgradeResultsFromHeaders(tip, uint64(tip.GetLayer()), definitions)
	if !complete {
		return fmt.Errorf("incomplete upgrade header scan")
	}
	if err = b.commitUpgradeResults(b.newUpgradeRebuildState(tip), results); err != nil {
		return fmt.Errorf("persist upgrade checkpoint: %w", err)
	}
	l.Info("upgrade rebuild finished", "reason", reason, "tip_id", tip.GetID(), "tip_layer", tip.GetLayer())
	return nil
}

func currentUpgradeRebuildDefinitions() []upgradeRebuildDefinition {
	raw := upgrades.GetUpgrades()
	names := make([]string, 0, len(raw))
	for name := range raw {
		names = append(names, name)
	}
	sort.Strings(names)

	definitions := make([]upgradeRebuildDefinition, 0, len(names))
	for _, name := range names {
		u := raw[name]
		definitions = append(definitions, upgradeRebuildDefinition{
			Name:                  name,
			BitMask:               u.BitMask,
			Threshold:             u.Threshold,
			WindowSize:            u.WindowSize,
			ActivationDelayBlocks: u.ActivationDelayBlocks,
			StartLayer:            u.StartLayer,
			TimeoutLayer:          u.TimeoutLayer,
		})
	}
	return definitions
}

func upgradeRebuildNeededAtLayer(layer uint64, definitions []upgradeRebuildDefinition) bool {
	for _, def := range definitions {
		if def.WindowSize == 0 {
			continue
		}
		if layer >= def.StartLayer {
			return true
		}
	}
	return false
}

func nextUpgradeRebuildStartLayer(definitions []upgradeRebuildDefinition) (uint64, bool) {
	var next uint64
	found := false
	for _, def := range definitions {
		if def.WindowSize == 0 {
			continue
		}
		if !found || def.StartLayer < next {
			next = def.StartLayer
			found = true
		}
	}
	return next, found
}

func (b *BlockChain) rebuildUpgradeResultsFromHeaders(tip blockdag.IBlock, now uint64, definitions []upgradeRebuildDefinition) ([]upgradeRebuildResult, bool) {
	states := newUpgradeRebuildScanStates(now, definitions)
	if tip == nil || len(definitions) == 0 {
		return finalizeUpgradeRebuildScanStates(states, now), true
	}

	minStart := uint64(0)
	relevantCount := 0
	for _, state := range states {
		if !state.relevant {
			continue
		}
		if relevantCount == 0 || state.def.StartLayer < minStart {
			minStart = state.def.StartLayer
		}
		relevantCount++
	}
	if relevantCount == 0 {
		return finalizeUpgradeRebuildScanStates(states, now), true
	}

	l.Info("upgrade rebuild header scan started", "from_layer", minStart, "to_layer", now, "upgrades", relevantCount)

	// The caller holds chainLock through the scan and publication.
	started := time.Now()
	lastLog := started
	var processed uint64
	for cur := tip; cur != nil; {
		layer := uint64(cur.GetLayer())
		if layer < minStart {
			break
		}

		hdr := b.GetBlockHeader(cur)
		if hdr == nil {
			l.Warn("upgrade rebuild stopped at missing header", "block_id", cur.GetID(), "layer", layer)
			return finalizeUpgradeRebuildScanStates(states, now), false
		}

		for i := range states {
			state := &states[i]
			if !state.relevant {
				continue
			}
			def := state.def
			if layer < def.StartLayer {
				continue
			}
			windowIndex := (layer - def.StartLayer) / def.WindowSize
			windowStart := def.StartLayer + windowIndex*def.WindowSize
			if def.TimeoutLayer != 0 && windowStart >= def.TimeoutLayer {
				continue
			}
			if !upgradeWindowComplete(windowStart, def.WindowSize, now) {
				continue
			}

			state.record(layer, windowIndex, windowStart, hdr.Version, now)
		}

		processed++
		if processed%250000 == 0 || time.Since(lastLog) >= 10*time.Second {
			l.Info("upgrade rebuild header scan progress",
				"current_layer", layer,
				"to_layer", now,
				"processed", processed,
				"eta", upgradeRebuildETA(started, processed, layer, minStart),
			)
			lastLog = time.Now()
		}

		parentID := cur.GetMainParent()
		if parentID == blockdag.MaxId {
			if layer > minStart {
				l.Warn("upgrade rebuild stopped before requested start layer", "last_layer", layer, "from_layer", minStart)
				return finalizeUpgradeRebuildScanStates(states, now), false
			}
			break
		}
		next, err := b.bd.ReadBlockById(parentID)
		if err != nil || next == nil {
			l.Warn("upgrade rebuild stopped at missing parent", "block_id", cur.GetID(), "parent_id", parentID, "layer", layer, "error", err)
			return finalizeUpgradeRebuildScanStates(states, now), false
		}
		cur = next
	}

	l.Info("upgrade rebuild header scan finished", "processed", processed, "duration", time.Since(started).String())
	return finalizeUpgradeRebuildScanStates(states, now), true
}

func newUpgradeRebuildScanStates(now uint64, definitions []upgradeRebuildDefinition) []upgradeRebuildScanState {
	states := make([]upgradeRebuildScanState, 0, len(definitions))
	for _, def := range definitions {
		states = append(states, upgradeRebuildScanState{
			def:      def,
			result:   upgradeRebuildResult{Name: def.Name},
			relevant: def.WindowSize != 0 && now >= def.StartLayer && upgradeWindowComplete(def.StartLayer, def.WindowSize, now),
		})
	}
	return states
}

func upgradeRebuildResultsFromScanStates(states []upgradeRebuildScanState) []upgradeRebuildResult {
	results := make([]upgradeRebuildResult, 0, len(states))
	for _, state := range states {
		results = append(results, state.result)
	}
	return results
}

func finalizeUpgradeRebuildScanStates(states []upgradeRebuildScanState, now uint64) []upgradeRebuildResult {
	for i := range states {
		states[i].finalize(now)
		states[i].logDuplicateSummary()
	}
	return upgradeRebuildResultsFromScanStates(states)
}

func (state *upgradeRebuildScanState) record(layer, windowIndex, windowStart uint64, version uint32, now uint64) {
	if !state.initialized {
		state.startWindow(windowIndex, windowStart)
	} else if state.windowIndex != windowIndex {
		state.finalize(now)
		state.startWindow(windowIndex, windowStart)
	}
	recordUpgradeSignal(&state.tally, layer-windowStart, version, state.def.BitMask)
}

func (state *upgradeRebuildScanState) startWindow(windowIndex, windowStart uint64) {
	state.initialized = true
	state.windowIndex = windowIndex
	state.windowStart = windowStart
	state.tally = newUpgradeSignalTally(state.def.WindowSize)
}

func (state *upgradeRebuildScanState) finalize(now uint64) {
	if !state.initialized {
		return
	}
	windowEnd := state.windowStart + state.def.WindowSize - 1
	if state.tally.duplicateLayers > 0 {
		state.duplicateWins++
		state.duplicateLayers += state.tally.duplicateLayers
	}
	locked := applyUpgradeWindowResult(&state.result, now, state.def, state.windowStart, state.tally)
	logUpgradeRebuildWindowTally(state.def, state.windowStart, windowEnd, state.tally, locked)
	state.initialized = false
	state.tally = upgradeSignalTally{}
}

func (state upgradeRebuildScanState) logDuplicateSummary() {
	if state.duplicateWins == 0 {
		return
	}
	l.Warn("upgrade rebuild saw duplicate layer votes",
		"name", state.def.Name,
		"windows", state.duplicateWins,
		"duplicate_layers", state.duplicateLayers,
	)
}

func applyUpgradeWindowResult(result *upgradeRebuildResult, now uint64, def upgradeRebuildDefinition, windowStart uint64, tally upgradeSignalTally) bool {
	if !upgradeWindowLocksIn(def, windowStart, now, tally) {
		return false
	}
	windowEnd := windowStart + def.WindowSize - 1
	if result.LockedIn && result.ThresholdAt <= windowEnd {
		return true
	}
	result.LockedIn = true
	result.AnchorSet = true
	result.ThresholdAt = windowEnd
	result.ActivateAnchor = windowEnd + def.ActivationDelayBlocks
	result.Active = now >= result.ActivateAnchor
	return true
}

func logUpgradeRebuildWindowTally(def upgradeRebuildDefinition, windowStart, windowEnd uint64, tally upgradeSignalTally, locked bool) {
	args := []any{
		"name", def.Name,
		"window_start", windowStart,
		"window_end", windowEnd,
		"headers", tally.headers,
		"unique_layers", tally.uniqueLayers,
		"duplicate_layers", tally.duplicateLayers,
		"yes_headers", tally.yesHeaders,
		"yes_layers", tally.yesLayers,
		"threshold", def.Threshold,
		"window_size", def.WindowSize,
		"bitmask", fmt.Sprintf("0x%x", def.BitMask),
	}
	if locked {
		l.Info("upgrade rebuild window locked in", args...)
		return
	}
	l.Debug("upgrade rebuild window tally", args...)
}

func newUpgradeSignalTally(windowSize uint64) upgradeSignalTally {
	words := int((windowSize + 63) / 64)
	if words == 0 {
		words = 1
	}
	return upgradeSignalTally{
		windowSize:   windowSize,
		seenLayers:   make([]uint64, words),
		yesLayerBits: make([]uint64, words),
	}
}

func recordUpgradeSignal(tally *upgradeSignalTally, offset uint64, version, bitMask uint32) {
	if tally == nil || tally.windowSize == 0 || offset >= tally.windowSize {
		return
	}
	signalled := version&bitMask != 0
	tally.headers++
	if signalled {
		tally.yesHeaders++
	}

	seen := tally.bitIsSet(tally.seenLayers, offset)
	yes := tally.bitIsSet(tally.yesLayerBits, offset)
	if seen {
		tally.duplicateLayers++
		if signalled && !yes {
			tally.setBit(tally.yesLayerBits, offset)
			tally.yesLayers++
		}
		return
	}

	tally.setBit(tally.seenLayers, offset)
	tally.uniqueLayers++
	if signalled {
		tally.setBit(tally.yesLayerBits, offset)
		tally.yesLayers++
	}
}

func (tally upgradeSignalTally) bitIsSet(bits []uint64, offset uint64) bool {
	word := int(offset / 64)
	if word < 0 || word >= len(bits) {
		return false
	}
	return bits[word]&(uint64(1)<<(offset%64)) != 0
}

func (tally upgradeSignalTally) setBit(bits []uint64, offset uint64) {
	word := int(offset / 64)
	if word < 0 || word >= len(bits) {
		return
	}
	bits[word] |= uint64(1) << (offset % 64)
}

func upgradeWindowLocksIn(def upgradeRebuildDefinition, windowStart, now uint64, tally upgradeSignalTally) bool {
	if !upgradeWindowComplete(windowStart, def.WindowSize, now) {
		return false
	}
	return tally.yesLayers >= def.Threshold
}

func upgradeWindowComplete(windowStart, windowSize, now uint64) bool {
	if windowSize == 0 || windowStart > now {
		return false
	}
	return windowSize-1 <= now-windowStart
}

func upgradeRebuildETA(started time.Time, processed uint64, currentLayer uint64, minStart uint64) string {
	if processed == 0 || currentLayer <= minStart {
		return "0s"
	}
	elapsed := time.Since(started)
	if elapsed <= 0 {
		return "unknown"
	}
	rate := float64(processed) / elapsed.Seconds()
	if rate <= 0 {
		return "unknown"
	}
	remaining := float64(currentLayer-minStart) / rate
	return time.Duration(remaining * float64(time.Second)).Round(time.Second).String()
}

func (b *BlockChain) countUpgradeSignalsInRange(tip blockdag.IBlock, from, to, windowSize uint64, mask uint32) upgradeSignalTally {
	if from > to || windowSize == 0 || to-from >= windowSize {
		return upgradeSignalTally{}
	}

	// The observed range may contain only the first layer. Incremental updates
	// later write offsets throughout the declared window, so reserve all of it.
	// The previous prefix-sized bitmap discarded those later votes until a
	// reorg/restart happened to force a longer rescan.
	tally := newUpgradeSignalTally(windowSize)
	for cur := tip; cur != nil; {
		layer := uint64(cur.GetLayer())
		if layer < from {
			break
		}
		if layer <= to {
			hdr := b.GetBlockHeader(cur)
			if hdr != nil {
				recordUpgradeSignal(&tally, layer-from, hdr.Version, mask)
			}
		}

		parentID := cur.GetMainParent()
		if parentID == blockdag.MaxId {
			break
		}
		cur = b.bd.GetBlockById(parentID)
	}

	return tally
}

// evaluateUpgrades recalculates voting windows for all known upgrades and
// updates their state when thresholds are reached. The provided metric is a
// deterministic chain value such as the layer or blue score of the current
// main tip.
func (b *BlockChain) evaluateUpgrades(metric uint64) {
	mainTip := b.bd.GetMainChainTip()
	if mainTip == nil {
		return
	}

	type upgradeSnapshot struct {
		Name                  string
		BitMask               uint32
		Threshold             uint64
		WindowSize            uint64
		ActivationDelayBlocks uint64
		StartLayer            uint64
		TimeoutLayer          uint64
		Active                bool
		LockedIn              bool
		ActivateAnchor        uint64
	}

	snapshots := make([]upgradeSnapshot, 0)
	upgrades.ForEachUpgrade(func(_ string, u *upgrades.UpgradeDefinition) {
		snapshots = append(snapshots, upgradeSnapshot{
			Name:                  u.Name,
			BitMask:               u.BitMask,
			Threshold:             u.Threshold,
			WindowSize:            u.WindowSize,
			ActivationDelayBlocks: u.ActivationDelayBlocks,
			StartLayer:            u.StartLayer,
			TimeoutLayer:          u.TimeoutLayer,
			Active:                u.Active,
			LockedIn:              u.LockedIn,
			ActivateAnchor:        u.ActivateAnchor,
		})
	})

	tipID := mainTip.GetID()
	parentID := mainTip.GetMainParent()

	incremental := false
	b.upgradeCacheMu.Lock()
	if b.upgradeTipID != blockdag.MaxId && parentID == b.upgradeTipID {
		incremental = true
	}
	b.upgradeCacheMu.Unlock()

	var newHeader *types.BlockHeader
	if incremental {
		newHeader = b.GetBlockHeader(mainTip)
		if newHeader == nil {
			incremental = false
		}
	}

	b.upgradeCacheMu.Lock()
	defer b.upgradeCacheMu.Unlock()

	if b.upgradeWindows == nil {
		b.upgradeWindows = make(map[string]upgradeWindowCache)
	}
	if !incremental {
		b.clearUpgradeWindowsLocked()
	}

	for _, u := range snapshots {
		if u.Active {
			delete(b.upgradeWindows, u.Name)
			continue
		}

		if u.LockedIn {
			delete(b.upgradeWindows, u.Name)
			if metric >= u.ActivateAnchor {
				l.Info("upgrade activated", "name", u.Name, "metric", metric)
				if err := upgrades.SetUpgradeActivated(u.Name, u.ActivateAnchor); err != nil {
					l.Warn("failed to activate upgrade", "name", u.Name, "err", err)
				}
			}
			continue
		}

		if metric < u.StartLayer {
			delete(b.upgradeWindows, u.Name)
			continue
		}
		if u.TimeoutLayer != 0 && metric >= u.TimeoutLayer {
			delete(b.upgradeWindows, u.Name)
			continue
		}
		if u.WindowSize == 0 {
			delete(b.upgradeWindows, u.Name)
			continue
		}
		def := upgradeRebuildDefinition{
			Name:                  u.Name,
			BitMask:               u.BitMask,
			Threshold:             u.Threshold,
			WindowSize:            u.WindowSize,
			ActivationDelayBlocks: u.ActivationDelayBlocks,
			StartLayer:            u.StartLayer,
			TimeoutLayer:          u.TimeoutLayer,
		}

		windowIndex := (metric - u.StartLayer) / u.WindowSize
		windowStart := u.StartLayer + windowIndex*u.WindowSize
		windowEnd := windowStart + u.WindowSize - 1

		cache, ok := b.upgradeWindows[u.Name]
		needRecompute := !ok || cache.start != windowStart || cache.end != windowEnd || !incremental

		if needRecompute {
			cache = upgradeWindowCache{
				start: windowStart,
				end:   windowEnd,
				tally: b.countUpgradeSignalsInRange(mainTip, windowStart, metric, u.WindowSize, u.BitMask),
			}
		} else if incremental && newHeader != nil {
			recordUpgradeSignal(&cache.tally, metric-windowStart, newHeader.Version, u.BitMask)
		}

		b.upgradeWindows[u.Name] = cache

		if metric >= windowEnd && upgradeWindowLocksIn(def, windowStart, metric, cache.tally) {
			activateAnchor := windowEnd + u.ActivationDelayBlocks
			if err := upgrades.SetUpgradeLockedIn(u.Name, activateAnchor); err != nil {
				l.Warn("failed to lock in upgrade", "name", u.Name, "err", err)
			} else {
				delete(b.upgradeWindows, u.Name)
			}
		}
	}
	b.upgradeTipID = tipID
}

func (b *BlockChain) clearUpgradeWindowsLocked() {
	if b.upgradeWindows == nil {
		b.upgradeWindows = make(map[string]upgradeWindowCache)
		return
	}
	for name := range b.upgradeWindows {
		delete(b.upgradeWindows, name)
	}
}

func (b *BlockChain) resetUpgradeCache() {
	b.upgradeCacheMu.Lock()
	defer b.upgradeCacheMu.Unlock()

	b.clearUpgradeWindowsLocked()
	b.upgradeTipID = blockdag.MaxId
}
