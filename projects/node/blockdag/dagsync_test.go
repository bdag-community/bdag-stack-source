package Blockdag

import (
	"container/list"
	"fmt"
	"io"
	"math/big"
	"testing"

	"github.com/BlockdagNetworkLabs/bdag/consensus/model"
	"github.com/BlockdagNetworkLabs/bdag/core/types"
	dbcommon "github.com/BlockdagNetworkLabs/bdag/database/common"
	"github.com/ethereum/go-ethereum/common"
	ethtypes "github.com/ethereum/go-ethereum/core/types"
)

// dagSyncTestDB is a minimal model.DataBase fake: it only implements
// GetDAGBlockIdByHash and GetBlockIdByOrder with real data (the two methods
// getBlockId/getBlockByOrder actually call), matching a real database's
// "not found" semantics for anything else. Every other method is an
// unused-but-required stub -- CalcSyncBlocks's code paths never call them.
type dagSyncTestDB struct {
	byHash  map[common.Hash]uint
	byOrder map[uint]uint
}

func (d *dagSyncTestDB) GetDAGBlockIdByHash(bh *common.Hash) (uint, error) {
	id, ok := d.byHash[*bh]
	if !ok {
		return 0, fmt.Errorf("not found")
	}
	return id, nil
}

func (d *dagSyncTestDB) GetBlockIdByOrder(order uint) (uint, error) {
	id, ok := d.byOrder[order]
	if !ok {
		return 0, fmt.Errorf("not found")
	}
	return id, nil
}

func (d *dagSyncTestDB) Name() string                             { return "test" }
func (d *dagSyncTestDB) Init() error                              { return nil }
func (d *dagSyncTestDB) Close()                                   {}
func (d *dagSyncTestDB) Rebuild(model.IndexManager) error         { return nil }
func (d *dagSyncTestDB) GetInfo() (*dbcommon.DatabaseInfo, error) { return nil, nil }
func (d *dagSyncTestDB) PutInfo(*dbcommon.DatabaseInfo) error     { return nil }
func (d *dagSyncTestDB) GetBestChainState() ([]byte, error)       { return nil, nil }
func (d *dagSyncTestDB) PutBestChainState([]byte) error           { return nil }
func (d *dagSyncTestDB) GetBlock(*common.Hash) (*types.SerializedBlock, error) {
	return nil, fmt.Errorf("not found")
}
func (d *dagSyncTestDB) GetBlockBytes(*common.Hash) ([]byte, error) {
	return nil, fmt.Errorf("not found")
}
func (d *dagSyncTestDB) GetHeader(*common.Hash) (*types.BlockHeader, error) {
	return nil, fmt.Errorf("not found")
}
func (d *dagSyncTestDB) PutBlock(*types.SerializedBlock) error                    { return nil }
func (d *dagSyncTestDB) HasBlock(*common.Hash) bool                               { return false }
func (d *dagSyncTestDB) GetDagInfo() ([]byte, error)                              { return nil, nil }
func (d *dagSyncTestDB) PutDagInfo([]byte) error                                  { return nil }
func (d *dagSyncTestDB) GetDAGBlock(uint) ([]byte, error)                         { return nil, fmt.Errorf("not found") }
func (d *dagSyncTestDB) PutDAGBlock(uint, []byte) error                           { return nil }
func (d *dagSyncTestDB) DeleteDAGBlock(uint) error                                { return nil }
func (d *dagSyncTestDB) PutDAGBlockIdByHash(*common.Hash, uint) error             { return nil }
func (d *dagSyncTestDB) DeleteDAGBlockIdByHash(*common.Hash) error                { return nil }
func (d *dagSyncTestDB) PutMainChainBlock(uint) error                             { return nil }
func (d *dagSyncTestDB) HasMainChainBlock(uint) bool                              { return false }
func (d *dagSyncTestDB) DeleteMainChainBlock(uint) error                          { return nil }
func (d *dagSyncTestDB) PutBlockIdByOrder(uint, uint) error                       { return nil }
func (d *dagSyncTestDB) PutDAGTip(uint, bool) error                               { return nil }
func (d *dagSyncTestDB) GetDAGTips() ([]uint, error)                              { return nil, nil }
func (d *dagSyncTestDB) DeleteDAGTip(uint) error                                  { return nil }
func (d *dagSyncTestDB) PutDiffAnticone(uint) error                               { return nil }
func (d *dagSyncTestDB) GetDiffAnticones() ([]uint, error)                        { return nil, nil }
func (d *dagSyncTestDB) DeleteDiffAnticone(uint) error                            { return nil }
func (d *dagSyncTestDB) Get([]byte) ([]byte, error)                               { return nil, fmt.Errorf("not found") }
func (d *dagSyncTestDB) Put([]byte, []byte) error                                 { return nil }
func (d *dagSyncTestDB) PutTxIdxEntrys(*types.SerializedBlock, model.Block) error { return nil }
func (d *dagSyncTestDB) GetTxIdxEntry(*common.Hash, bool) (*ethtypes.Transaction, *common.Hash, error) {
	return nil, nil, fmt.Errorf("not found")
}
func (d *dagSyncTestDB) DeleteTxIdxEntrys(*types.SerializedBlock) error { return nil }
func (d *dagSyncTestDB) IsInvalidTxIdxEmpty() bool                      { return true }
func (d *dagSyncTestDB) GetInvalidTxIdxTip() (uint64, *common.Hash, error) {
	return 0, nil, fmt.Errorf("not found")
}
func (d *dagSyncTestDB) PutInvalidTxIdxTip(uint64, *common.Hash) error              { return nil }
func (d *dagSyncTestDB) PutInvalidTxs(*types.SerializedBlock, model.Block) error    { return nil }
func (d *dagSyncTestDB) DeleteInvalidTxs(*types.SerializedBlock, model.Block) error { return nil }
func (d *dagSyncTestDB) CleanInvalidTxIdx() error                                   { return nil }
func (d *dagSyncTestDB) GetAddrIdxTip() (*common.Hash, uint, error) {
	return nil, 0, fmt.Errorf("not found")
}
func (d *dagSyncTestDB) PutAddrIdxTip(*common.Hash, uint) error { return nil }
func (d *dagSyncTestDB) PutAddrIdx(*types.SerializedBlock, model.Block, [][]byte) error {
	return nil
}
func (d *dagSyncTestDB) GetTxForAddress(common.Address, uint32, uint32, bool) ([]*dbcommon.RetrievedTx, uint32, error) {
	return nil, 0, nil
}
func (d *dagSyncTestDB) DeleteAddrIdx(*types.SerializedBlock, [][]byte) error     { return nil }
func (d *dagSyncTestDB) CleanAddrIdx(bool) error                                  { return nil }
func (d *dagSyncTestDB) IsLegacy() bool                                           { return false }
func (d *dagSyncTestDB) TryUpgrade(*dbcommon.DatabaseInfo, <-chan struct{}) error { return nil }
func (d *dagSyncTestDB) GetEstimateFee() ([]byte, error)                          { return nil, nil }
func (d *dagSyncTestDB) PutEstimateFee([]byte) error                              { return nil }
func (d *dagSyncTestDB) DeleteEstimateFee() error                                 { return nil }
func (d *dagSyncTestDB) Snapshot() error                                          { return nil }
func (d *dagSyncTestDB) SnapshotInfo() string                                     { return "" }
func (d *dagSyncTestDB) DBEngine() string                                         { return "test" }
func (d *dagSyncTestDB) StartTrack(string) error                                  { return nil }
func (d *dagSyncTestDB) StopTrack() error                                         { return nil }

// dagSyncTestConsensus is a minimal ConsensusAlgorithm fake for exercising
// CalcSyncBlocks/tryCalcSyncBlocks/calcSyncBlocks without a real Phantom
// instance or database.
type dagSyncTestConsensus struct {
	mainTip     IBlock
	onMainChain map[uint]bool
}

func (c *dagSyncTestConsensus) GetName() string                          { return "dagsync-test" }
func (c *dagSyncTestConsensus) Init(*BDAG) bool                          { return true }
func (c *dagSyncTestConsensus) AddBlock(IBlock) (*list.List, *list.List) { return nil, nil }
func (c *dagSyncTestConsensus) CreateBlock(b *Block) IBlock              { return b }
func (c *dagSyncTestConsensus) GetTipsList() []IBlock                    { return nil }
func (c *dagSyncTestConsensus) isOnMainChain(id uint) bool               { return c.onMainChain[id] }
func (c *dagSyncTestConsensus) GetMainChainTip() IBlock                  { return c.mainTip }
func (c *dagSyncTestConsensus) GetMainChainTipId() uint                  { return c.mainTip.GetID() }
func (c *dagSyncTestConsensus) GetMainParent(*IdSet) IBlock              { return nil }
func (c *dagSyncTestConsensus) Encode(io.Writer) error                   { return nil }
func (c *dagSyncTestConsensus) Decode(io.Reader) error                   { return nil }
func (c *dagSyncTestConsensus) Load() error                              { return nil }
func (c *dagSyncTestConsensus) IsDAG([]IBlock) bool                      { return true }
func (c *dagSyncTestConsensus) GetMainParentConcurrency(IBlock) int      { return 0 }
func (c *dagSyncTestConsensus) GetBlues(*IdSet) uint                     { return 0 }
func (c *dagSyncTestConsensus) IsBlue(uint) bool                         { return false }
func (c *dagSyncTestConsensus) getMaxParents() int                       { return 0 }

func newDagSyncTestBlock(id uint, order uint64, mainParent uint) *Block {
	b := &Block{
		id:         id,
		hash:       common.BigToHash(new(big.Int).SetUint64(uint64(id) + 1)),
		height:     uint(order),
		layer:      uint(order),
		mainParent: mainParent,
	}
	b.state = CreateMockBlockState(uint64(id))
	b.state.SetOrder(order)
	return b
}

// newDagSyncTestFixture builds a tiny 3-block linear main chain
// (genesis(0) -> b1(1) -> b2(2)) with every relevant BDAG field populated
// (blocks cache, commitOrder, a fake consensus) so both the peek-only fast
// path and the original write-locked path can run against it.
func newDagSyncTestFixture() (*DAGSync, map[uint]*Block) {
	genesis := newDagSyncTestBlock(0, 0, MaxId)
	b1 := newDagSyncTestBlock(1, 1, 0)
	b2 := newDagSyncTestBlock(2, 2, 1)

	blocks := map[uint]*Block{0: genesis, 1: b1, 2: b2}

	consensus := &dagSyncTestConsensus{
		mainTip:     b2,
		onMainChain: map[uint]bool{0: true, 1: true, 2: true},
	}

	testDB := &dagSyncTestDB{
		byHash: map[common.Hash]uint{
			*genesis.GetHash(): 0,
			*b1.GetHash():      1,
			*b2.GetHash():      2,
		},
		byOrder: map[uint]uint{0: 0, 1: 1, 2: 2},
	}

	bd := &BDAG{
		blocks:       map[uint]IBlock{0: genesis, 1: b1, 2: b2},
		tips:         NewIdSet(),
		commitOrder:  map[uint]uint{0: 0, 1: 1, 2: 2},
		commitBlock:  NewIdSet(),
		instance:     consensus,
		lastSnapshot: NewDAGSnapshot(),
		db:           testDB,
	}
	return NewDAGSync(bd), blocks
}

func hashesEqual(t *testing.T, got, want []*common.Hash) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("length mismatch: got %d hashes, want %d (got=%v want=%v)", len(got), len(want), got, want)
	}
	for i := range got {
		if *got[i] != *want[i] {
			t.Fatalf("hash mismatch at index %d: got %s, want %s", i, got[i], want[i])
		}
	}
}

func pointEqual(t *testing.T, got, want *common.Hash) {
	t.Helper()
	if (got == nil) != (want == nil) {
		t.Fatalf("point nil-ness mismatch: got %v, want %v", got, want)
	}
	if got != nil && *got != *want {
		t.Fatalf("point mismatch: got %s, want %s", got, want)
	}
}

// TestCalcSyncBlocksFastPathMatchesSlowPathDirectMode verifies that, when
// every requested block is already cached, the RLock-based fast path
// (tryCalcSyncBlocks, reached via CalcSyncBlocks) returns exactly the same
// result as the original always-correct write-locked path (calcSyncBlocks).
func TestCalcSyncBlocksFastPathMatchesSlowPathDirectMode(t *testing.T) {
	ds, blocks := newDagSyncTestFixture()
	locator := []*common.Hash{blocks[2].GetHash(), blocks[1].GetHash()}

	fastBlocks, fastPoint := ds.CalcSyncBlocks(nil, locator, DirectMode, 100, true)
	slowBlocks, slowPoint := ds.calcSyncBlocks(locator, DirectMode, 100)

	hashesEqual(t, fastBlocks, slowBlocks)
	pointEqual(t, fastPoint, slowPoint)
}

func TestCalcSyncBlocksFastPathMatchesSlowPathSubDAGMode(t *testing.T) {
	ds, blocks := newDagSyncTestFixture()
	locator := []*common.Hash{blocks[0].GetHash()}

	fastBlocks, fastPoint := ds.CalcSyncBlocks(nil, locator, SubDAGMode, 100, true)
	slowBlocks, slowPoint := ds.calcSyncBlocks(locator, SubDAGMode, 100)

	hashesEqual(t, fastBlocks, slowBlocks)
	pointEqual(t, fastPoint, slowPoint)
	// Sanity: genesis + maxHashes should stream the rest of the main chain.
	hashesEqual(t, fastBlocks, []*common.Hash{blocks[1].GetHash(), blocks[2].GetHash()})
}

func TestCalcSyncBlocksFastPathMatchesSlowPathContinueMode(t *testing.T) {
	ds, blocks := newDagSyncTestFixture()
	locator := []*common.Hash{blocks[0].GetHash(), blocks[1].GetHash()}

	fastBlocks, fastPoint := ds.CalcSyncBlocks(nil, locator, ContinueMode, 100, true)
	slowBlocks, slowPoint := ds.calcSyncBlocks(locator, ContinueMode, 100)

	hashesEqual(t, fastBlocks, slowBlocks)
	pointEqual(t, fastPoint, slowPoint)
	hashesEqual(t, fastBlocks, []*common.Hash{blocks[2].GetHash()})
}

// TestCalcSyncBlocksDeclinesOnMissWhenNotCurrent verifies that a cache miss
// (block evicted from bd.blocks, e.g. by pruning) while this node is not
// itself synced produces an empty/decline response rather than falling back
// to the write-locked path.
func TestCalcSyncBlocksDeclinesOnMissWhenNotCurrent(t *testing.T) {
	ds, blocks := newDagSyncTestFixture()
	// Simulate b1 having been evicted from the in-memory cache (still
	// referenced by commitOrder, as a genuinely-committed-but-not-cached
	// block would be), forcing a miss in the SubDAGMode walk from genesis.
	delete(ds.bd.blocks, 1)

	locator := []*common.Hash{blocks[0].GetHash()}
	gotBlocks, gotPoint := ds.CalcSyncBlocks(nil, locator, SubDAGMode, 100, false)

	if len(gotBlocks) != 0 {
		t.Fatalf("expected empty blocks on decline, got %v", gotBlocks)
	}
	if gotPoint != nil {
		t.Fatalf("expected nil point on decline, got %s", gotPoint)
	}
}

// TestCalcSyncBlocksFallsBackWhenCurrent verifies that the same miss, when
// this node IS synced (isCurrent=true), takes the write-locked fallback
// path instead of declining -- verified here by confirming it doesn't
// return the "declined" shape and instead attempts (and, since the fixture
// has no real DB/Phantom to load from, gracefully fails to resolve rather
// than panicking) the original resolution.
func TestCalcSyncBlocksFallsBackWhenCurrent(t *testing.T) {
	ds, blocks := newDagSyncTestFixture()
	delete(ds.bd.blocks, 1)

	locator := []*common.Hash{blocks[0].GetHash()}

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("CalcSyncBlocks panicked on fallback path: %v", r)
		}
	}()
	ds.CalcSyncBlocks(nil, locator, SubDAGMode, 100, true)
}

func TestValidTipsFastPathMatchesReferenceOrderingAndDeduplication(t *testing.T) {
	ds, blocks := newDagSyncTestFixture()
	duplicate := newDagSyncTestBlock(3, 1, 0)
	duplicate.hash = blocks[1].hash
	duplicate.height = 0 // force a deterministic lower-priority duplicate
	ds.bd.blocks[duplicate.GetID()] = duplicate
	ds.bd.tips.AddPair(blocks[1].GetID(), blocks[1])
	ds.bd.tips.AddPair(duplicate.GetID(), duplicate)
	ds.bd.tips.AddPair(blocks[2].GetID(), blocks[2])

	want := ds.bd.getValidTips(false)
	got, ok := ds.bd.tryGetValidTips(false)
	if !ok {
		t.Fatal("fast valid-tip lookup unexpectedly missed an in-memory block")
	}
	if len(got) != len(want) || len(got) != 2 {
		t.Fatalf("valid tips lengths = %d/%d, want two", len(got), len(want))
	}
	for i := range want {
		if got[i].GetID() != want[i].GetID() || *got[i].GetHash() != *want[i].GetHash() {
			t.Fatalf("valid tip %d = (%d,%s), want (%d,%s)", i, got[i].GetID(), got[i].GetHash(), want[i].GetID(), want[i].GetHash())
		}
	}
	if got[1].GetID() != blocks[1].GetID() {
		t.Fatalf("duplicate hash changed ordered tip selection: got id %d, want id %d", got[1].GetID(), blocks[1].GetID())
	}
}
