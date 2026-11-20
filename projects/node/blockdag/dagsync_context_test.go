package Blockdag

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
)

type dagSyncObservedDB struct {
	*dagSyncTestDB
	reads, loads int
	afterRead    func()
}

func (d *dagSyncObservedDB) GetDAGBlockIdByHash(hash *common.Hash) (uint, error) {
	d.reads++
	if d.afterRead != nil {
		d.afterRead()
	}
	return d.dagSyncTestDB.GetDAGBlockIdByHash(hash)
}

func (d *dagSyncObservedDB) GetDAGBlock(id uint) ([]byte, error) {
	d.loads++
	return d.dagSyncTestDB.GetDAGBlock(id)
}

func TestDAGSyncContextValidation(t *testing.T) {
	ds, blocks := newDagSyncTestFixture()
	h := blocks[0].GetHash()
	for _, tc := range []struct {
		name    string
		locator []*common.Hash
		mode    SyncMode
		limit   uint
	}{
		{"mode", nil, SyncMode(255), 1},
		{"output-limit", nil, SubDAGMode, MaxDAGSyncHashes + 1},
		{"locator-limit", make([]*common.Hash, MaxDAGSyncLocatorHashes+1), SubDAGMode, 1},
		{"nil-locator", []*common.Hash{nil}, DirectMode, 1},
		{"continue-empty", nil, ContinueMode, 1},
		{"continue-short", []*common.Hash{h}, ContinueMode, 1},
		{"continue-long", []*common.Hash{h, h, h}, ContinueMode, 1},
		{"direct-output", []*common.Hash{h, h}, DirectMode, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ds.bd.stateLock.Lock()
			defer ds.bd.stateLock.Unlock()
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			got, point, err := ds.CalcSyncBlocksContext(ctx, nil, tc.locator, tc.mode, tc.limit, true)
			if err == nil || errors.Is(err, context.DeadlineExceeded) || got != nil || point != nil {
				t.Fatalf("validation reached locked DAG or returned data: %v %v %v", got, point, err)
			}
		})
	}
}

func TestDAGSyncContextLockCancellation(t *testing.T) {
	t.Run("read-lock", func(t *testing.T) {
		ds, blocks := newDagSyncTestFixture()
		ds.bd.stateLock.Lock()
		defer ds.bd.stateLock.Unlock()
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
		defer cancel()
		got, point, err := ds.CalcSyncBlocksContext(ctx, nil, []*common.Hash{blocks[0].GetHash()}, SubDAGMode, 2, true)
		if !errors.Is(err, context.DeadlineExceeded) || got != nil || point != nil {
			t.Fatalf("%v %v %v", got, point, err)
		}
	})
	t.Run("write-lock", func(t *testing.T) {
		ds, blocks := newDagSyncTestFixture()
		delete(ds.bd.blocks, 1)
		ds.bd.stateLock.RLock()
		defer ds.bd.stateLock.RUnlock()
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
		defer cancel()
		got, point, err := ds.CalcSyncBlocksContext(ctx, nil, []*common.Hash{blocks[0].GetHash()}, SubDAGMode, 2, true)
		if !errors.Is(err, context.DeadlineExceeded) || got != nil || point != nil {
			t.Fatalf("%v %v %v", got, point, err)
		}
	})
	t.Run("after-acquisition", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		unlocks := 0
		err := lockDAGSyncContext(ctx, func() bool { cancel(); return true }, func() { unlocks++ })
		if !errors.Is(err, context.Canceled) || unlocks != 1 {
			t.Fatalf("err=%v unlocks=%d", err, unlocks)
		}
	})
}

func TestDAGSyncContextCachedReader(t *testing.T) {
	ds, blocks := newDagSyncTestFixture()
	ds.bd.stateLock.RLock()
	defer ds.bd.stateLock.RUnlock()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	got, point, err := ds.CalcSyncBlocksContext(ctx, nil, []*common.Hash{blocks[0].GetHash()}, SubDAGMode, 2, true)
	if err != nil {
		t.Fatal(err)
	}
	hashesEqual(t, got, []*common.Hash{blocks[1].GetHash(), blocks[2].GetHash()})
	pointEqual(t, point, blocks[0].GetHash())
}

func TestDAGSyncContextCurrentFallback(t *testing.T) {
	for _, current := range []bool{false, true} {
		name := "decline"
		if current {
			name = "load"
		}
		t.Run(name, func(t *testing.T) {
			ds, blocks := newDagSyncTestFixture()
			db := &dagSyncObservedDB{dagSyncTestDB: ds.bd.db.(*dagSyncTestDB)}
			ds.bd.db = db
			delete(ds.bd.blocks, 1)
			_, _, err := ds.CalcSyncBlocksContext(context.Background(), nil, []*common.Hash{blocks[0].GetHash()}, SubDAGMode, 2, current)
			if err != nil {
				t.Fatalf("current=%v loads=%d err=%v", current, db.loads, err)
			}
			if !current && db.loads != 0 {
				t.Fatalf("non-current node attempted disk load: loads=%d", db.loads)
			}
			// The target requires a real Phantom before readBlock reaches the
			// database. TestDAGSyncContextLockCancellation/write-lock proves
			// the current-node path still enters the write-locked fallback.
		})
	}
}

func TestDAGSyncContextCancellationBetweenReads(t *testing.T) {
	ds, blocks := newDagSyncTestFixture()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	db := &dagSyncObservedDB{dagSyncTestDB: ds.bd.db.(*dagSyncTestDB), afterRead: cancel}
	ds.bd.db = db
	got, point, err := ds.CalcSyncBlocksContext(ctx, nil, []*common.Hash{blocks[1].GetHash(), blocks[2].GetHash()}, DirectMode, 2, true)
	if !errors.Is(err, context.Canceled) || got != nil || point != nil || db.reads != 1 {
		t.Fatalf("reads=%d blocks=%v point=%v err=%v", db.reads, got, point, err)
	}
}

func TestDAGSyncContextTraversalBudget(t *testing.T) {
	t.Run("sparse-orders", func(t *testing.T) {
		ds, blocks := newDagSyncTestFixture()
		blocks[2].SetOrder(dagSyncWorkLimit + 10)
		got, point, err := ds.CalcSyncBlocksContext(context.Background(), nil, []*common.Hash{blocks[0].GetHash()}, SubDAGMode, 3, true)
		if !errors.Is(err, ErrDAGSyncWorkLimit) || got != nil || point != nil {
			t.Fatalf("partial response survived: %v %v %v", got, point, err)
		}
	})
	t.Run("parent-cycle", func(t *testing.T) {
		ds, blocks := newDagSyncTestFixture()
		ds.bd.instance.(*dagSyncTestConsensus).onMainChain = map[uint]bool{0: true}
		blocks[2].mainParent = 2
		got, point, err := ds.CalcSyncBlocksContext(context.Background(), nil, []*common.Hash{blocks[2].GetHash()}, SubDAGMode, 3, true)
		if !errors.Is(err, ErrDAGSyncWorkLimit) || got != nil || point != nil {
			t.Fatalf("%v %v %v", got, point, err)
		}
	})
	t.Run("shared-retry", func(t *testing.T) {
		ds, blocks := newDagSyncTestFixture()
		delete(ds.bd.blocks, 1)
		b := newDAGSyncBudget(context.Background())
		b.left = 3
		loc := []*common.Hash{blocks[1].GetHash(), blocks[2].GetHash()}
		if _, _, cached := ds.tryCalcSyncBlocksBounded(b, loc, DirectMode, 2); cached {
			t.Fatal("expected cache miss")
		}
		ds.calcSyncBlocksBounded(b, loc, DirectMode, 2)
		if !errors.Is(b.err, ErrDAGSyncWorkLimit) {
			t.Fatal("fallback reset request budget")
		}
	})
}

func TestDAGSyncContextAnticoneBudget(t *testing.T) {
	for _, size := range []uint{4, 5} {
		name := "boundary"
		if size == 5 {
			name = "overflow"
		}
		t.Run(name, func(t *testing.T) {
			ds, blocks := newDagSyncTestFixture()
			ids := NewIdSet()
			for id := uint(3); id < 3+size; id++ {
				ids.Add(id)
				ds.bd.blocks[id] = newDagSyncTestBlock(id, uint64(id), 2)
			}
			main := NewIdSet()
			main.AddPair(2, true)
			ds.bd.instance = &Phantom{bd: ds.bd, mainChain: &MainChain{bd: ds.bd, tip: 2, commitBlocks: main}, diffAnticone: ids}
			got, point, err := ds.CalcSyncBlocksContext(context.Background(), nil, []*common.Hash{blocks[2].GetHash()}, SubDAGMode, 1, true)
			if size == 5 {
				if !errors.Is(err, ErrDAGSyncWorkLimit) || got != nil || point != nil {
					t.Fatalf("%v %v %v", got, point, err)
				}
			} else {
				if err != nil || len(got) != 1 || point == nil {
					t.Fatalf("%v %v %v", got, point, err)
				}
			}
		})
	}
}

func TestDAGSyncContextZeroOutput(t *testing.T) {
	ds, blocks := newDagSyncTestFixture()
	got, point, err := ds.CalcSyncBlocksContext(nil, nil, []*common.Hash{blocks[0].GetHash()}, SubDAGMode, 0, true)
	if err != nil || got != nil || point != nil {
		t.Fatalf("%v %v %v", got, point, err)
	}
}
