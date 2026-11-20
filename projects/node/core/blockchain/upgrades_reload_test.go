package blockchain

import (
	"errors"
	"testing"

	"github.com/BlockdagNetworkLabs/bdag/config"
	"github.com/BlockdagNetworkLabs/bdag/consensus/model"
	"github.com/BlockdagNetworkLabs/bdag/core/event"
	"github.com/BlockdagNetworkLabs/bdag/database/chaindb"
	"github.com/BlockdagNetworkLabs/bdag/engine/txscript"
	"github.com/BlockdagNetworkLabs/bdag/params"
	"github.com/BlockdagNetworkLabs/bdag/upgrades"
	ethcommon "github.com/ethereum/go-ethereum/common"
)

// mockConsensus implements model.Consensus with minimal functionality for tests.
type mockConsensus struct {
	db     model.DataBase
	params *params.Params
}

func (m *mockConsensus) Init() error                              { return nil }
func (m *mockConsensus) GenesisHash() *ethcommon.Hash             { return &ethcommon.Hash{} }
func (m *mockConsensus) Config() *config.Config                   { return &config.Config{} }
func (m *mockConsensus) DatabaseContext() model.DataBase          { return m.db }
func (m *mockConsensus) BlockChain() model.BlockChain             { return nil }
func (m *mockConsensus) IndexManager() model.IndexManager         { return nil }
func (m *mockConsensus) Events() *event.Feed                      { return &event.Feed{} }
func (m *mockConsensus) MedianTimeSource() model.MedianTimeSource { return nil }
func (m *mockConsensus) SigCache() *txscript.SigCache             { return nil }
func (m *mockConsensus) Interrupt() <-chan struct{}               { return nil }
func (m *mockConsensus) Params() *params.Params                   { return m.params }
func (m *mockConsensus) Rebuild() error                           { return nil }
func (m *mockConsensus) Shutdown()                                {}

// TestUpgradeStateReload ensures that upgrade state is reloaded from the database
// after a blockchain restart.
func TestUpgradeStateReload(t *testing.T) {
	cfg := &config.Config{DataDir: ""}
	db, err := chaindb.New(cfg)
	if err != nil {
		t.Fatalf("failed to create db: %v", err)
	}
	cons := &mockConsensus{db: db, params: &params.Params{}}

	bc := &BlockChain{consensus: cons}
	bc.resetUpgradeCache()

	oldUpgrades := upgrades.ReplaceUpgrades(map[string]*upgrades.UpgradeDefinition{})
	if err := upgrades.RegisterUpgrade(&upgrades.UpgradeDefinition{Name: "test", BitMask: 0x1, Threshold: 1, WindowSize: 1}); err != nil {
		t.Fatalf("register upgrade: %v", err)
	}
	defer func() { upgrades.ReplaceUpgrades(oldUpgrades) }()

	u := upgrades.GetUpgrades()["test"]
	u.Active = true
	u.LockedIn = true
	u.AnchorSet = true
	u.ActivateAnchor = 42
	u.ThresholdAt = 40

	if err := bc.persistUpgradeState("test", u); err != nil {
		t.Fatalf("persist upgrade state: %v", err)
	}
	// Simulate a restart with a fresh upgrade definition but same DB.
	upgrades.ReplaceUpgrades(map[string]*upgrades.UpgradeDefinition{})
	if err := upgrades.RegisterUpgrade(&upgrades.UpgradeDefinition{Name: "test", BitMask: 0x1, Threshold: 1, WindowSize: 1}); err != nil {
		t.Fatalf("register upgrade: %v", err)
	}
	bc2 := &BlockChain{consensus: cons}
	bc2.resetUpgradeCache()
	if err := bc2.loadUpgradeStates(); err != nil {
		t.Fatalf("load upgrade states: %v", err)
	}

	reloaded := upgrades.GetUpgrades()["test"]
	if !reloaded.Active || !reloaded.LockedIn || !reloaded.AnchorSet || reloaded.ActivateAnchor != 42 || reloaded.ThresholdAt != 40 {
		t.Fatalf("upgrade state not reloaded: %+v", reloaded)
	}
}

// errDB wraps a DataBase and forces Get to return a predefined error.
type errDB struct {
	model.DataBase
	err error
}

func (e *errDB) Get(key []byte) ([]byte, error) { return nil, e.err }

// TestUpgradeStateDBError ensures database errors are surfaced.
func TestUpgradeStateDBError(t *testing.T) {
	cfg := &config.Config{DataDir: ""}
	db, err := chaindb.New(cfg)
	if err != nil {
		t.Fatalf("failed to create db: %v", err)
	}
	injected := errors.New("db fail")
	cons := &mockConsensus{db: &errDB{DataBase: db, err: injected}, params: &params.Params{}}

	bc := &BlockChain{consensus: cons}
	bc.resetUpgradeCache()

	oldUpgrades := upgrades.ReplaceUpgrades(map[string]*upgrades.UpgradeDefinition{})
	if err := upgrades.RegisterUpgrade(&upgrades.UpgradeDefinition{Name: "test", BitMask: 0x1, Threshold: 1, WindowSize: 1}); err != nil {
		t.Fatalf("register upgrade: %v", err)
	}
	defer func() { upgrades.ReplaceUpgrades(oldUpgrades) }()

	err = bc.loadUpgradeStates()
	if err == nil || !errors.Is(err, injected) {
		t.Fatalf("expected error %v, got %v", injected, err)
	}
}

// TestUpgradeStateMissingDefinition ensures an error is returned when the
// database contains upgrade state for a name without a corresponding
// definition.
func TestUpgradeStateMissingDefinition(t *testing.T) {
	cfg := &config.Config{DataDir: ""}
	db, err := chaindb.New(cfg)
	if err != nil {
		t.Fatalf("failed to create db: %v", err)
	}
	cons := &mockConsensus{db: db, params: &params.Params{}}

	bc := &BlockChain{consensus: cons}
	bc.resetUpgradeCache()

	oldUpgrades := upgrades.ReplaceUpgrades(map[string]*upgrades.UpgradeDefinition{})
	if err := upgrades.RegisterUpgrade(&upgrades.UpgradeDefinition{Name: "test", BitMask: 0x1, Threshold: 1, WindowSize: 1}); err != nil {
		t.Fatalf("register upgrade: %v", err)
	}
	defer func() { upgrades.ReplaceUpgrades(oldUpgrades) }()

	u := upgrades.GetUpgrades()["test"]
	u.Active = true
	if err := bc.persistUpgradeState("test", u); err != nil {
		t.Fatalf("persist upgrade state: %v", err)
	}

	// Simulate restart without re-registering the upgrade definition.
	upgrades.ReplaceUpgrades(map[string]*upgrades.UpgradeDefinition{})
	bc2 := &BlockChain{consensus: cons}
	bc2.resetUpgradeCache()
	err = bc2.loadUpgradeStates()
	if err == nil {
		t.Fatalf("expected error for missing upgrade definition")
	}
}

func TestRebuildUpgradeResultsUsesOnlyCompleteWindows(t *testing.T) {
	def := upgradeRebuildDefinition{
		Name:                  "test",
		BitMask:               0x1,
		Threshold:             3,
		WindowSize:            4,
		ActivationDelayBlocks: 2,
		StartLayer:            0,
	}

	partial := newUpgradeSignalTally(def.WindowSize)
	for _, offset := range []uint64{0, 1, 2} {
		recordUpgradeSignal(&partial, offset, VBTopBits|0x1, def.BitMask)
	}
	result := upgradeRebuildResult{Name: def.Name}
	if applyUpgradeWindowResult(&result, 6, def, 4, partial) || result.LockedIn {
		t.Fatalf("partial window should not lock in upgrade: %+v", result)
	}

	complete := partial
	recordUpgradeSignal(&complete, 3, VBTopBits, def.BitMask)
	result = upgradeRebuildResult{Name: def.Name}
	if !applyUpgradeWindowResult(&result, 7, def, 4, complete) ||
		!result.LockedIn || !result.AnchorSet || result.ThresholdAt != 7 || result.ActivateAnchor != 9 || result.Active {
		t.Fatalf("complete threshold window should lock in with delayed activation: %+v", result)
	}

	result = upgradeRebuildResult{Name: def.Name}
	if !applyUpgradeWindowResult(&result, 9, def, 4, complete) || !result.Active {
		t.Fatalf("upgrade should be active after activation anchor: %+v", result)
	}
}

func TestUpgradeSignalTallyTracksDuplicateLayers(t *testing.T) {
	tally := newUpgradeSignalTally(4)
	for _, vote := range []struct {
		offset  uint64
		version uint32
	}{
		{offset: 0, version: VBTopBits | 0x1},
		{offset: 0, version: VBTopBits},
		{offset: 1, version: VBTopBits},
		{offset: 2, version: VBTopBits | 0x1},
		{offset: 2, version: VBTopBits | 0x1},
		{offset: 3, version: VBTopBits},
	} {
		recordUpgradeSignal(&tally, vote.offset, vote.version, 0x1)
	}

	if tally.headers != 6 || tally.yesHeaders != 3 {
		t.Fatalf("header tally mismatch: %+v", tally)
	}
	if tally.uniqueLayers != 4 || tally.duplicateLayers != 2 || tally.yesLayers != 2 {
		t.Fatalf("layer tally mismatch: %+v", tally)
	}
}

func TestRebuildUpgradeResultsUsesLayerVotesNotHeaderTotal(t *testing.T) {
	def := upgradeRebuildDefinition{
		Name:                  "test",
		BitMask:               0x1,
		Threshold:             3,
		WindowSize:            4,
		ActivationDelayBlocks: 2,
		StartLayer:            0,
	}

	thresholdTally := newUpgradeSignalTally(def.WindowSize)
	for _, vote := range []struct {
		offset  uint64
		version uint32
	}{
		{offset: 0, version: VBTopBits | 0x1},
		{offset: 0, version: VBTopBits | 0x1},
		{offset: 1, version: VBTopBits | 0x1},
		{offset: 2, version: VBTopBits},
		{offset: 3, version: VBTopBits | 0x1},
	} {
		recordUpgradeSignal(&thresholdTally, vote.offset, vote.version, 0x1)
	}
	result := upgradeRebuildResult{Name: def.Name}
	if !applyUpgradeWindowResult(&result, 3, def, 0, thresholdTally) || !result.LockedIn || !result.AnchorSet {
		t.Fatalf("duplicate headers should not prevent lock-in once enough layers signal: %+v tally=%+v", result, thresholdTally)
	}

	duplicateOnlyTally := newUpgradeSignalTally(def.WindowSize)
	for _, vote := range []struct {
		offset  uint64
		version uint32
	}{
		{offset: 0, version: VBTopBits | 0x1},
		{offset: 0, version: VBTopBits | 0x1},
		{offset: 0, version: VBTopBits | 0x1},
		{offset: 1, version: VBTopBits | 0x1},
		{offset: 2, version: VBTopBits},
		{offset: 3, version: VBTopBits},
	} {
		recordUpgradeSignal(&duplicateOnlyTally, vote.offset, vote.version, 0x1)
	}
	result = upgradeRebuildResult{Name: def.Name}
	if applyUpgradeWindowResult(&result, 3, def, 0, duplicateOnlyTally) || result.LockedIn {
		t.Fatalf("duplicate headers alone must not inflate layer votes into lock-in: %+v tally=%+v", result, duplicateOnlyTally)
	}
}

func TestUpgradeSignalTallyUsesBoundedBitsets(t *testing.T) {
	tally := newUpgradeSignalTally(2000)
	if len(tally.seenLayers) != 32 || len(tally.yesLayerBits) != 32 {
		t.Fatalf("unexpected bitset allocation: seen=%d yes=%d", len(tally.seenLayers), len(tally.yesLayerBits))
	}

	recordUpgradeSignal(&tally, 0, VBTopBits|0x1, 0x1)
	recordUpgradeSignal(&tally, 1999, VBTopBits|0x1, 0x1)
	recordUpgradeSignal(&tally, 2000, VBTopBits|0x1, 0x1)

	if tally.headers != 2 || tally.uniqueLayers != 2 || tally.yesLayers != 2 {
		t.Fatalf("out-of-window signal should be ignored: %+v", tally)
	}
}

func TestUpgradeRebuildScanStateKeepsEarliestLockedWindow(t *testing.T) {
	def := upgradeRebuildDefinition{
		Name:                  "test",
		BitMask:               0x1,
		Threshold:             2,
		WindowSize:            4,
		ActivationDelayBlocks: 2,
		StartLayer:            0,
	}
	state := upgradeRebuildScanState{
		def:      def,
		result:   upgradeRebuildResult{Name: def.Name},
		relevant: true,
	}

	state.record(7, 1, 4, VBTopBits|0x1, 9)
	state.record(6, 1, 4, VBTopBits|0x1, 9)
	state.record(3, 0, 0, VBTopBits|0x1, 9)
	state.record(2, 0, 0, VBTopBits|0x1, 9)
	state.finalize(9)

	if !state.result.LockedIn || state.result.ThresholdAt != 3 || state.result.ActivateAnchor != 5 || !state.result.Active {
		t.Fatalf("backward scan should retain earliest locked window: %+v", state.result)
	}
}

func TestUpgradeRebuildStateMatchesVotingCheckpointAndDefinitions(t *testing.T) {
	oldUpgrades := upgrades.ReplaceUpgrades(map[string]*upgrades.UpgradeDefinition{})
	upgrades.RegisterPersist(nil)
	defer func() { upgrades.ReplaceUpgrades(oldUpgrades) }()

	if err := upgrades.RegisterUpgrade(&upgrades.UpgradeDefinition{
		Name:       "test",
		BitMask:    0x1,
		Threshold:  3,
		WindowSize: 4,
		StartLayer: 20,
	}); err != nil {
		t.Fatalf("register upgrade: %v", err)
	}

	definitionsHash := upgradeDefinitionsHash()
	stored := upgradeRebuildState{
		Version:         upgradeRebuildStateVersion,
		TipID:           10,
		TipLayer:        21,
		TipHash:         ethcommon.HexToHash("0x01"),
		DefinitionsHash: definitionsHash,
	}

	if !upgradeRebuildStateMatches(stored, stored) {
		t.Fatalf("expected identical rebuild states to match")
	}

	sameCheckpoint := stored
	sameCheckpoint.TipID++
	sameCheckpoint.TipLayer = 22
	sameCheckpoint.TipHash = ethcommon.HexToHash("0x02")
	if !upgradeRebuildStateMatches(stored, sameCheckpoint) {
		t.Fatalf("expected tip changes inside the same incomplete voting window to match")
	}

	crossedCheckpoint := stored
	crossedCheckpoint.TipLayer = 23
	crossedCheckpoint.TipHash = ethcommon.HexToHash("0x03")
	if upgradeRebuildStateMatches(stored, crossedCheckpoint) {
		t.Fatalf("expected a newly completed voting window to invalidate rebuild state")
	}

	changedDefinitions := stored
	changedDefinitions.DefinitionsHash = ethcommon.HexToHash("0x03")
	if upgradeRebuildStateMatches(changedDefinitions, stored) {
		t.Fatalf("expected definition hash mismatch to invalidate rebuild state")
	}
}

func TestUpgradeDefinitionsHashChangesOnDefinitionEdit(t *testing.T) {
	oldUpgrades := upgrades.ReplaceUpgrades(map[string]*upgrades.UpgradeDefinition{})
	upgrades.RegisterPersist(nil)
	defer func() { upgrades.ReplaceUpgrades(oldUpgrades) }()

	if err := upgrades.RegisterUpgrade(&upgrades.UpgradeDefinition{Name: "test", BitMask: 0x1, Threshold: 1, WindowSize: 4}); err != nil {
		t.Fatalf("register upgrade: %v", err)
	}
	first := upgradeDefinitionsHash()

	upgrades.ReplaceUpgrades(map[string]*upgrades.UpgradeDefinition{})
	if err := upgrades.RegisterUpgrade(&upgrades.UpgradeDefinition{Name: "test", BitMask: 0x1, Threshold: 2, WindowSize: 4}); err != nil {
		t.Fatalf("register upgrade: %v", err)
	}
	second := upgradeDefinitionsHash()

	if first == second {
		t.Fatalf("expected static definition changes to alter rebuild marker hash")
	}
}

func TestUpgradeRebuildMarkerRequiresPerUpgradeState(t *testing.T) {
	cfg := &config.Config{DataDir: ""}
	db, err := chaindb.New(cfg)
	if err != nil {
		t.Fatalf("failed to create db: %v", err)
	}
	cons := &mockConsensus{db: db, params: &params.Params{}}
	bc := &BlockChain{consensus: cons}
	bc.resetUpgradeCache()

	oldUpgrades := upgrades.ReplaceUpgrades(map[string]*upgrades.UpgradeDefinition{})
	upgrades.RegisterPersist(nil)
	defer func() { upgrades.ReplaceUpgrades(oldUpgrades) }()

	if err := upgrades.RegisterUpgrade(&upgrades.UpgradeDefinition{Name: "test", BitMask: 0x1, Threshold: 1, WindowSize: 1}); err != nil {
		t.Fatalf("register upgrade: %v", err)
	}

	ok, err := bc.hasPersistedStateForAllUpgrades()
	if err != nil {
		t.Fatalf("has persisted state: %v", err)
	}
	if ok {
		t.Fatalf("expected missing per-upgrade state to reject rebuild marker")
	}

	if err := bc.persistUpgradeState("test", upgrades.GetUpgrades()["test"]); err != nil {
		t.Fatalf("persist upgrade state: %v", err)
	}
	ok, err = bc.hasPersistedStateForAllUpgrades()
	if err != nil {
		t.Fatalf("has persisted state after persist: %v", err)
	}
	if !ok {
		t.Fatalf("expected persisted per-upgrade state to satisfy marker validation")
	}
}

func TestUpgradeRebuildStateRoundTrip(t *testing.T) {
	cfg := &config.Config{DataDir: ""}
	db, err := chaindb.New(cfg)
	if err != nil {
		t.Fatalf("failed to create db: %v", err)
	}
	cons := &mockConsensus{db: db, params: &params.Params{}}
	bc := &BlockChain{consensus: cons}

	state := upgradeRebuildState{
		Version:         upgradeRebuildStateVersion,
		TipID:           10,
		TipLayer:        20,
		TipHash:         ethcommon.HexToHash("0x01"),
		DefinitionsHash: ethcommon.HexToHash("0x02"),
	}
	if err := bc.persistUpgradeRebuildState(state); err != nil {
		t.Fatalf("persist rebuild state: %v", err)
	}

	loaded, found, err := bc.loadUpgradeRebuildState()
	if err != nil {
		t.Fatalf("load rebuild state: %v", err)
	}
	if !found {
		t.Fatalf("expected rebuild state to be found")
	}
	if !upgradeRebuildStateMatches(*loaded, state) {
		t.Fatalf("loaded rebuild state mismatch: got %+v want %+v", *loaded, state)
	}
}
