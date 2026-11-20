package Blockdag

import (
	"container/list"
	"context"
	"sync"

	"github.com/ethereum/go-ethereum/common"
)

// This parameter can be set according to the size of TCP package(1500) to ensure the transmission stability of the network
const MaxMainLocatorNum = 32

// Synchronization mode
type SyncMode byte

const (
	DirectMode   SyncMode = 0
	SubDAGMode   SyncMode = 1
	ContinueMode SyncMode = 2
)

type DAGSync struct {
	bd *BDAG

	// The following fields are used to track the graph state being synced to from
	// peers.
	gsMtx sync.Mutex
	gs    *GraphState
}

// CalcSyncBlocks computes which block hashes to serve a peer for the given
// locator/mode. This compatibility entry point discards errors; RPC callers
// use CalcSyncBlocksContext. Both paths bound traversal and output. The first
// pass uses a read lock and does not populate the block cache, but hash/order
// index lookups can still read the database.
//
// On a cache miss, behavior depends on isCurrent -- this node's own
// BlockChain.IsCurrent(), resolved by the *caller* before calling in.
// CalcSyncBlocks must never call back into core/blockchain itself: doing so
// would nest blockdag.BDAG.stateLock inside a BlockChain.chainLock
// acquisition, the reverse of the order maybeAcceptBlock already
// establishes (chainLock, then stateLock), risking deadlock.
//   - isCurrent == true: this node is caught up, so import volume competing
//     for stateLock is naturally low. Retry with write-locked resolution,
//     sharing the remaining request budget with the first attempt.
//   - isCurrent == false: this node is itself mid-catch-up, exactly when
//     stateLock contention is worst and this node's own import should take
//     priority. Return an empty answer instead of
//     contending for the write lock. The requesting peer already handles a
//     short response the same way it handles "nothing new to sync" -- it
//     retries later or asks a different peer, which is normal P2P behavior,
//     not an error condition.
func (ds *DAGSync) CalcSyncBlocks(gs *GraphState, locator []*common.Hash, mode SyncMode, maxHashes uint, isCurrent bool) ([]*common.Hash, *common.Hash) {
	blocks, point, _ := ds.CalcSyncBlocksContext(context.Background(), gs, locator, mode, maxHashes, isCurrent)
	return blocks, point
}

// tryCalcSyncBlocksBounded mirrors calcSyncBlocksBounded, but does not load
// blocks into the cache (index reads may still access disk) and reports ok=false
// the moment any lookup misses the in-memory cache. Keep this in sync with
// calcSyncBlocks if that logic changes. Caller must hold at least
// stateLock.RLock.
func (ds *DAGSync) tryCalcSyncBlocksBounded(b *dagSyncBudget, locator []*common.Hash, mode SyncMode, maxHashes uint) ([]*common.Hash, *common.Hash, bool) {
	if !b.spend(len(locator)) {
		return nil, nil, false
	}
	if mode == DirectMode {
		if len(locator) == 0 {
			return []*common.Hash{}, nil, true
		}
		blocks, ok := ds.sortSyncBlocks(b, locator, true)
		if !ok {
			return nil, nil, false
		}
		return blocks, nil, true
	} else if mode == ContinueMode {
		if len(locator) <= 0 {
			return []*common.Hash{}, nil, true
		}
		point, ok := ds.bd.peekBlock(locator[0])
		if !ok {
			return nil, nil, false
		}
		if point == nil {
			return []*common.Hash{}, nil, true
		}
		if !ds.bd.isOnMainChain(point.GetID()) {
			return []*common.Hash{}, nil, true
		}
		startBlock, ok := ds.bd.peekBlock(locator[1])
		if !ok {
			return nil, nil, false
		}
		if startBlock == nil {
			return []*common.Hash{}, nil, true
		}
		blocks, ok := ds.tryGetBlockChainFromMain(b, startBlock, maxHashes)
		if !ok {
			return nil, nil, false
		}
		return blocks, point.GetHash(), true
	}

	var point IBlock
	for i := len(locator) - 1; i >= 0; i-- {
		if !b.spend(1) {
			return nil, nil, false
		}
		mainBlock, ok := ds.bd.peekBlock(locator[i])
		if !ok {
			return nil, nil, false
		}
		if mainBlock == nil {
			continue
		}
		if !ds.bd.isOnMainChain(mainBlock.GetID()) {
			continue
		}
		point = mainBlock
		break
	}

	if point == nil && len(locator) > 0 {
		p, ok := ds.bd.peekBlock(locator[0])
		if !ok {
			return nil, nil, false
		}
		point = p
		if point != nil {
			for !ds.bd.isOnMainChain(point.GetID()) {
				if !b.spend(1) {
					return nil, nil, false
				}
				if point.GetMainParent() == MaxId {
					break
				}
				next, ok := ds.bd.peekBlockById(point.GetMainParent())
				if !ok {
					return nil, nil, false
				}
				point = next
				if point == nil {
					break
				}
			}
		}
	}

	if point == nil {
		p, ok := ds.bd.peekGenesis()
		if !ok {
			return nil, nil, false
		}
		point = p
	}
	if point == nil {
		// The original assumes genesis always resolves and would panic on
		// point.GetHash() below if it didn't; preserve that assumption
		// (this should be unreachable) rather than papering over it here.
		return nil, nil, false
	}
	blocks, ok := ds.tryGetBlockChainFromMain(b, point, maxHashes)
	if !ok {
		return nil, nil, false
	}
	return blocks, point.GetHash(), true
}

// calcSyncBlocks is the original, always-correct implementation: it can
// trigger a disk load on a cache miss via the underlying getBlockById, so it
// requires the caller to hold stateLock.Lock() (see CalcSyncBlocks).
func (ds *DAGSync) calcSyncBlocks(locator []*common.Hash, mode SyncMode, maxHashes uint) ([]*common.Hash, *common.Hash) {
	return ds.calcSyncBlocksBounded(newDAGSyncBudget(context.Background()), locator, mode, maxHashes)
}

func (ds *DAGSync) calcSyncBlocksBounded(b *dagSyncBudget, locator []*common.Hash, mode SyncMode, maxHashes uint) ([]*common.Hash, *common.Hash) {
	if !b.spend(len(locator)) {
		return nil, nil
	}
	if mode == DirectMode {
		result := []*common.Hash{}
		if len(locator) == 0 {
			return result, nil
		}
		blocks, _ := ds.sortSyncBlocks(b, locator, false)
		return blocks, nil
	} else if mode == ContinueMode {
		result := []*common.Hash{}
		if len(locator) <= 0 {
			return result, nil
		}
		point := ds.bd.getBlock(locator[0])
		if point == nil {
			return result, nil
		}
		if !ds.bd.isOnMainChain(point.GetID()) {
			return result, nil
		}
		startBlock := ds.bd.getBlock(locator[1])
		if startBlock == nil {
			return result, nil
		}
		return ds.getBlockChainFromMain(b, startBlock, maxHashes), point.GetHash()
	}

	var point IBlock
	for i := len(locator) - 1; i >= 0; i-- {
		if !b.spend(1) {
			return nil, nil
		}
		mainBlock := ds.bd.getBlock(locator[i])
		if mainBlock == nil {
			continue
		}
		if !ds.bd.isOnMainChain(mainBlock.GetID()) {
			continue
		}
		point = mainBlock
		break
	}

	if point == nil && len(locator) > 0 {
		point = ds.bd.getBlock(locator[0])
		if point != nil {
			for !ds.bd.isOnMainChain(point.GetID()) {
				if !b.spend(1) {
					return nil, nil
				}
				if point.GetMainParent() == MaxId {
					break
				}
				point = ds.bd.getBlockById(point.GetMainParent())
				if point == nil {
					break
				}
			}
		}

	}

	if point == nil {
		point = ds.bd.getGenesis()
	}
	if point == nil {
		return nil, nil
	}
	return ds.getBlockChainFromMain(b, point, maxHashes), point.GetHash()
}

// GetMainLocator
func (ds *DAGSync) GetMainLocator(point *common.Hash) []*common.Hash {
	ds.bd.stateLock.Lock()
	defer ds.bd.stateLock.Unlock()

	var endBlock IBlock
	if point != nil {
		endBlock = ds.bd.getBlock(point)
	}
	if endBlock != nil {
		for !ds.bd.isOnMainChain(endBlock.GetID()) {
			if endBlock.GetMainParent() == MaxId {
				break
			}
			endBlock = ds.bd.getBlockById(endBlock.GetMainParent())
			if endBlock == nil {
				break
			}
		}
	}
	if endBlock == nil {
		endBlock = ds.bd.getGenesis()
	}
	startBlock := ds.bd.getMainChainTip()
	locator := list.New()
	cur := startBlock
	const DefaultMainLocatorNum = 10
	for i := 0; i < DefaultMainLocatorNum; i++ {
		if cur.GetID() == 0 ||
			cur.GetMainParent() == MaxId ||
			cur.GetID() <= endBlock.GetID() {
			break
		}
		locator.PushFront(cur.GetHash())
		cur = ds.bd.getBlockById(cur.GetMainParent())
		if cur == nil {
			break
		}
	}
	if cur.GetID() > endBlock.GetID() {
		halfStart := cur.GetID()
		halfEnd := endBlock.GetID()
		hlocator := []*common.Hash{}
		for locator.Len()+len(hlocator)+1 < MaxMainLocatorNum {
			//for {
			nextID := (halfStart - halfEnd) / 2
			if nextID <= 0 {
				break
			}
			nextID += halfEnd
			if nextID == halfStart ||
				nextID == halfEnd {
				break
			}
			if !ds.bd.isOnMainChain(nextID) {
				halfEnd++
				continue
			}
			ib := ds.bd.getBlockById(nextID)
			if ib == nil {
				halfEnd++
				continue
			}
			hlocator = append(hlocator, ib.GetHash())
			halfEnd = nextID
		}
		if len(hlocator) > 0 {
			for i := len(hlocator) - 1; i >= 0; i-- {
				locator.PushFront(hlocator[i])
			}
		}
	}
	result := []*common.Hash{endBlock.GetHash()}
	for i := locator.Front(); i != nil; i = i.Next() {
		result = append(result, i.Value.(*common.Hash))
	}
	return result
}

func (ds *DAGSync) getBlockChainFromMain(b *dagSyncBudget, point IBlock, maxHashes uint) []*common.Hash {
	mainTip := ds.bd.getMainChainTip()
	result := []*common.Hash{}
	if point == nil || mainTip == nil || maxHashes == 0 {
		return result
	}
	for i := point.GetOrder(); i < mainTip.GetOrder(); {
		i++
		if !b.spend(1) {
			return nil
		}
		block := ds.bd.getBlockByOrder(i)
		if block == nil {
			continue
		}
		result = append(result, block.GetHash())
		if uint(len(result)) >= maxHashes {
			break
		}
	}
	rlen := len(result)
	if uint(rlen) < maxHashes {
		pdb, ok := ds.bd.GetInstance().(*Phantom)
		if ok {
			if !pdb.GetDiffAnticone().IsEmpty() {
				da, ok := b.anticone(pdb.GetDiffAnticone(), maxHashes)
				if !ok {
					return nil
				}
				for _, id := range da {
					if !b.spend(1) {
						return nil
					}
					block := ds.bd.getBlockById(id)
					if block == nil {
						continue
					}
					if ds.bd.CanPrune(block, mainTip) {
						continue
					}
					result = append(result, block.GetHash())
					if uint(len(result)) >= maxHashes {
						break
					}
				}
			}
		}
	}
	return result
}

// tryGetBlockChainFromMain mirrors getBlockChainFromMain using peek-only
// lookups so a cache miss bails with ok=false instead of loading from disk.
// Sparse order ranges consume work even when no block is returned. Index
// reads can access disk; block-cache population is reserved for the slow path.
func (ds *DAGSync) tryGetBlockChainFromMain(b *dagSyncBudget, point IBlock, maxHashes uint) ([]*common.Hash, bool) {
	mainTip, ok := ds.bd.peekBlockById(ds.bd.instance.GetMainChainTipId())
	if !ok {
		return nil, false
	}
	if point == nil || mainTip == nil || maxHashes == 0 {
		return []*common.Hash{}, true
	}
	result := []*common.Hash{}
	for i := point.GetOrder(); i < mainTip.GetOrder(); {
		i++
		if !b.spend(1) {
			return nil, false
		}
		block, ok := ds.bd.peekBlockByOrder(i)
		if !ok {
			return nil, false
		}
		if block == nil {
			continue
		}
		result = append(result, block.GetHash())
		if uint(len(result)) >= maxHashes {
			break
		}
	}
	rlen := len(result)
	if uint(rlen) < maxHashes {
		pdb, ok := ds.bd.GetInstance().(*Phantom)
		if ok {
			if !pdb.GetDiffAnticone().IsEmpty() {
				da, ok := b.anticone(pdb.GetDiffAnticone(), maxHashes)
				if !ok {
					return nil, false
				}
				for _, id := range da {
					if !b.spend(1) {
						return nil, false
					}
					block, ok := ds.bd.peekBlockById(id)
					if !ok {
						return nil, false
					}
					if block == nil {
						continue
					}
					if ds.bd.CanPrune(block, mainTip) {
						continue
					}
					result = append(result, block.GetHash())
					if uint(len(result)) >= maxHashes {
						break
					}
				}
			}
		}
	}
	return result, true
}

func (ds *DAGSync) SetGraphState(gs *GraphState) {
	ds.gsMtx.Lock()
	defer ds.gsMtx.Unlock()

	ds.gs = gs
}

// NewDAGSync
func NewDAGSync(bd *BDAG) *DAGSync {
	return &DAGSync{bd: bd}
}
