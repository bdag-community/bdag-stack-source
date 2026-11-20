package Blockdag

import (
	"context"
	"errors"
	"sort"
	"time"

	"github.com/ethereum/go-ethereum/common"
)

const (
	MaxDAGSyncHashes        = 2000
	MaxDAGSyncLocatorHashes = 2000
	dagSyncWorkLimit        = 4 * (MaxDAGSyncHashes + MaxDAGSyncLocatorHashes)
)

var ErrDAGSyncWorkLimit = errors.New("DAG sync work limit exceeded")

type dagSyncBudget struct {
	ctx  context.Context
	left int
	err  error
}

func newDAGSyncBudget(ctx context.Context) *dagSyncBudget {
	if ctx == nil {
		ctx = context.Background()
	}
	return &dagSyncBudget{ctx: ctx, left: dagSyncWorkLimit}
}

func (b *dagSyncBudget) spend(n int) bool {
	if b.err != nil {
		return false
	}
	if b.err = b.ctx.Err(); b.err != nil {
		return false
	}
	if n < 0 || n > b.left {
		b.err = ErrDAGSyncWorkLimit
		return false
	}
	b.left -= n
	return true
}

// Both cache and disk attempts share one traversal budget. Cached requests keep
// the read-lock fast path; only a current node may retry a miss with a write lock.
// Cancellation cannot interrupt a storage read already inside the database API.
func (ds *DAGSync) CalcSyncBlocksContext(ctx context.Context, gs *GraphState, locator []*common.Hash,
	mode SyncMode, maxHashes uint, isCurrent bool) (blocks []*common.Hash, point *common.Hash, err error) {
	b := newDAGSyncBudget(ctx)
	if ds == nil || ds.bd == nil {
		return nil, nil, errors.New("DAG sync is unavailable")
	}
	if maxHashes > MaxDAGSyncHashes || len(locator) > MaxDAGSyncLocatorHashes {
		return nil, nil, ErrDAGSyncWorkLimit
	}
	if mode != DirectMode && mode != SubDAGMode && mode != ContinueMode {
		return nil, nil, errors.New("unknown DAG sync mode")
	}
	if mode == ContinueMode && len(locator) != 2 {
		return nil, nil, errors.New("continue DAG sync requires exactly two locators")
	}
	for _, hash := range locator {
		if hash == nil {
			return nil, nil, errors.New("nil DAG sync locator")
		}
	}
	if mode == DirectMode && uint(len(locator)) > maxHashes {
		return nil, nil, ErrDAGSyncWorkLimit
	}
	if !b.spend(0) || maxHashes == 0 {
		return nil, nil, b.err
	}
	defer func() {
		b.spend(0)
		if b.err != nil {
			blocks, point, err = nil, nil, b.err
		}
	}()
	if err := lockDAGSyncContext(b.ctx, ds.bd.stateLock.TryRLock, ds.bd.stateLock.RUnlock); err != nil {
		return nil, nil, err
	}
	blocks, point, cached := ds.tryCalcSyncBlocksBounded(b, locator, mode, maxHashes)
	ds.bd.stateLock.RUnlock()
	if cached || b.err != nil {
		return blocks, point, b.err
	}
	if !isCurrent {
		return nil, nil, nil
	}
	if err := lockDAGSyncContext(b.ctx, ds.bd.stateLock.TryLock, ds.bd.stateLock.Unlock); err != nil {
		return nil, nil, err
	}
	defer ds.bd.stateLock.Unlock()
	blocks, point = ds.calcSyncBlocksBounded(b, locator, mode, maxHashes)
	return blocks, point, b.err
}

func (ds *DAGSync) sortSyncBlocks(b *dagSyncBudget, locator []*common.Hash, cached bool) ([]*common.Hash, bool) {
	// Preserve the legacy single-hash echo; larger lists resolve known blocks.
	if len(locator) <= 1 {
		return locator, b.spend(0)
	}
	blocks := make(BlockSlice, 0, len(locator))
	for _, hash := range locator {
		if !b.spend(1) {
			return nil, false
		}
		var block IBlock
		if cached {
			var ok bool
			block, ok = ds.bd.peekBlock(hash)
			if !ok {
				return nil, false
			}
		} else {
			block = ds.bd.getBlock(hash)
		}
		if block != nil {
			blocks = append(blocks, block)
		}
	}
	if !b.spend(0) {
		return nil, false
	}
	sort.Sort(blocks)
	result := make([]*common.Hash, 0, len(blocks))
	for _, block := range blocks {
		result = append(result, block.GetHash())
	}
	return result, b.spend(0)
}

func lockDAGSyncContext(ctx context.Context, try func() bool, unlock func()) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if try() {
		if err := ctx.Err(); err != nil {
			unlock()
			return err
		}
		return nil
	}
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if try() {
				if err := ctx.Err(); err != nil {
					unlock()
					return err
				}
				return nil
			}
		}
	}
}

func (b *dagSyncBudget) anticone(ids *IdSet, maxHashes uint) ([]uint, bool) {
	// Check cardinality before allocating or sorting a caller-triggered view.
	if ids.Size() > int(maxHashes)*4 {
		b.err = ErrDAGSyncWorkLimit
		return nil, false
	}
	if !b.spend(ids.Size()) {
		return nil, false
	}
	result := ids.SortList(false)
	return result, b.spend(0)
}
