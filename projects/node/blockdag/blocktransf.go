package Blockdag

import (
	"fmt"
	"sort"
	"time"

	"github.com/ethereum/go-ethereum/common"
)

type FilterType byte

const (
	// all
	All FilterType = 0

	// only blocks in blue set
	Blue FilterType = 1

	// only blocks in red set
	Red FilterType = 2
)

// Is there a block in DAG?
func (bd *BDAG) HasBlock(h *common.Hash) bool {
	return bd.GetBlockId(h) != MaxId
}

func (bd *BDAG) HasBlocks(hs []*common.Hash) bool {
	if len(hs) <= 0 {
		return false
	}
	// hasBlock only reaches getBlockId, which is a pure read (snapshot
	// check + DB lookup) -- safe to hold for reads only.
	bd.stateLock.RLock()
	defer bd.stateLock.RUnlock()
	for _, h := range hs {
		if !bd.hasBlock(h) {
			return false
		}
	}
	return true
}

func (bd *BDAG) hasBlock(h *common.Hash) bool {
	return bd.getBlockId(h) != MaxId
}

// Is there a block in DAG?
func (bd *BDAG) hasBlockById(id uint) bool {
	return bd.getBlockById(id) != nil
}

// Is there a block in DAG?
func (bd *BDAG) HasBlockById(id uint) bool {
	// Delegates to GetBlockById, which already does the RLock-fast-path /
	// Lock-on-miss dance -- avoids a second, cruder full-Lock code path
	// for what is semantically the same lookup.
	return bd.GetBlockById(id) != nil
}

// Is there some block in DAG?
func (bd *BDAG) hasBlocks(ids []uint) bool {
	for _, id := range ids {
		if !bd.hasBlockById(id) {
			return false
		}
	}
	return true
}

// Acquire one block by hash
func (bd *BDAG) GetBlock(h *common.Hash) IBlock {
	// Delegates to the now-RLock-friendly GetBlockId/GetBlockById rather
	// than holding a single write lock across both lookups. Safe because
	// blocks are never removed from bd.blocks once loaded -- an id found
	// a moment ago in GetBlockId is still valid by the time GetBlockById
	// runs.
	return bd.GetBlockById(bd.GetBlockId(h))
}

// Acquire one block by hash
// Be careful, this is inefficient and cannot be called frequently
func (bd *BDAG) getBlock(h *common.Hash) IBlock {
	return bd.getBlockById(bd.getBlockId(h))
}

// peekBlock mirrors getBlock using peekBlockById so a cache miss never
// triggers a disk load. getBlockId is a pure read (no mutation), so the
// hash lookup itself needs no peek treatment. A hash that doesn't resolve
// to any id (MaxId) is a definite "unknown block", not a cache-miss
// ambiguity -- distinguished explicitly here rather than delegating a
// MaxId id into peekBlockById, whose (nil, false) return for MaxId means
// "ambiguous" for its own (different) callers.
func (bd *BDAG) peekBlock(h *common.Hash) (IBlock, bool) {
	id := bd.getBlockId(h)
	if id == MaxId {
		return nil, true
	}
	return bd.peekBlockById(id)
}

func (bd *BDAG) GetBlockId(h *common.Hash) uint {
	// getBlockId is a pure read (lastSnapshot check + DB lookup, no
	// mutation of bd state) -- safe under a read lock.
	bd.stateLock.RLock()
	defer bd.stateLock.RUnlock()

	return bd.getBlockId(h)
}

func (bd *BDAG) getBlockId(h *common.Hash) uint {
	if h == nil {
		return MaxId
	}
	if bd.lastSnapshot.block != nil {
		if bd.lastSnapshot.block.GetHash().Cmp(*h) == 0 {
			return bd.lastSnapshot.block.GetID()
		}
	}
	bid, err := DBGetBlockIdByHash(bd.db, h)
	if err != nil {
		return MaxId
	}
	return bid
}

// Acquire one block by hash
func (bd *BDAG) GetBlockById(id uint) IBlock {
	if id == MaxId {
		return nil
	}

	// Fast path: most lookups hit the in-memory cache. Slow path (cache
	// miss): getBlockById can populate bd.blocks via loadBlock, which
	// mutates shared state, so it needs the write lock -- and it re-checks
	// bd.blocks itself, so a concurrent loader racing us between the two
	// lock acquisitions is still handled correctly. See peekThenLoad.
	return peekThenLoad(&bd.stateLock, func() (IBlock, bool) {
		block, ok := bd.blocks[id]
		return block, ok
	}, func() IBlock {
		return bd.getBlockById(id)
	})
}

// peekBlockById returns the in-memory-cached block for id, if any, without
// ever triggering the loadBlock disk-load/mutation path that getBlockById
// can take on a cache miss. Safe to call under either stateLock.RLock or
// stateLock.Lock. Used by read-mostly callers (e.g. GetGraphState) that want
// an optimistic lock-free fast path and can fall back to the write-locked
// getBlockById when a lookup misses.
func (bd *BDAG) peekBlockById(id uint) (IBlock, bool) {
	if id == MaxId {
		return nil, false
	}
	block, ok := bd.blocks[id]
	return block, ok
}

// Acquire one block by id
func (bd *BDAG) getBlockById(id uint) IBlock {
	if id == MaxId {
		return nil
	}
	block, ok := bd.blocks[id]
	if !ok {
		b, err := bd.loadBlock(id)
		if err != nil {
			log.Warn("get block", "error", err.Error(), "blockID", id)
		}
		return b
	}
	return block
}

func (bd *BDAG) HasLoadedBlock(id uint) bool {
	// hasLoadedBlock is a plain map read with no cache-population
	// side effect (unlike getBlockById) -- safe under a read lock.
	bd.stateLock.RLock()
	defer bd.stateLock.RUnlock()
	return bd.hasLoadedBlock(id)
}

func (bd *BDAG) hasLoadedBlock(id uint) bool {
	if id == MaxId {
		return false
	}
	_, ok := bd.blocks[id]
	return ok
}

// Obtain block hash by global order
func (bd *BDAG) GetBlockHashByOrder(order uint) *common.Hash {
	bd.stateLock.Lock()
	defer bd.stateLock.Unlock()

	ib := bd.getBlockByOrder(order)
	if ib != nil {
		return ib.GetHash()
	}
	return nil
}

func (bd *BDAG) GetBlockByOrder(order uint) IBlock {
	bd.stateLock.Lock()
	defer bd.stateLock.Unlock()

	return bd.getBlockByOrder(order)
}

func (bd *BDAG) getBlockByOrder(order uint) IBlock {
	if order >= MaxBlockOrder {
		return nil
	}
	id, ok := bd.commitOrder[order]
	if ok {
		return bd.getBlockById(id)
	}
	id, err := DBGetBlockIdByOrder(bd.db, order)
	if err != nil {
		log.Error(err.Error())
		return nil
	}
	return bd.getBlockById(id)
}

// peekBlockByOrder mirrors getBlockByOrder using peekBlockById so a cache
// miss never triggers a disk load. The order->id resolution itself
// (bd.commitOrder map read, or a DBGetBlockIdByOrder read) is a pure read
// with no mutation risk, so only the final id->block step needs the peek
// treatment. Return value mirrors peekBlock's convention: (nil, true) means
// "definitely doesn't exist", (_, false) means "ambiguous, cache miss".
func (bd *BDAG) peekBlockByOrder(order uint) (IBlock, bool) {
	if order >= MaxBlockOrder {
		return nil, true
	}
	id, ok := bd.commitOrder[order]
	if !ok {
		var err error
		id, err = DBGetBlockIdByOrder(bd.db, order)
		if err != nil {
			return nil, true
		}
	}
	if id == MaxId {
		return nil, true
	}
	return bd.peekBlockById(id)
}

// Return the last added block
func (bd *BDAG) GetLastBlock() IBlock {
	bd.stateLock.Lock()
	defer bd.stateLock.Unlock()

	return bd.getBlockById(bd.getLastBlockID())
}

func (bd *BDAG) GetLastBlockID() uint {
	bd.stateLock.Lock()
	defer bd.stateLock.Unlock()
	return bd.getLastBlockID()
}

func (bd *BDAG) getLastBlockID() uint {
	if bd.blockTotal <= 0 {
		return 0
	}
	return bd.blockTotal - 1
}

// This function need a stable sequence,so call it before sorting the DAG.
// If the h is invalid,the function will become a little inefficient.
func (bd *BDAG) GetPrevious(id uint) (uint, error) {
	bd.stateLock.Lock()
	defer bd.stateLock.Unlock()

	if id == 0 {
		return 0, fmt.Errorf("no pre")
	}
	b := bd.getBlockById(id)
	if b == nil {
		return 0, fmt.Errorf("no pre")
	}
	if b.GetOrder() == 0 {
		return 0, fmt.Errorf("no pre")
	}
	// TODO
	ib := bd.getBlockByOrder(b.GetOrder() - 1)
	if ib != nil {
		return ib.GetID(), nil
	}
	return 0, fmt.Errorf("no pre")
}

func (bd *BDAG) GetBlockHash(id uint) *common.Hash {
	bd.stateLock.Lock()
	defer bd.stateLock.Unlock()

	ib := bd.getBlockById(id)
	if ib != nil {
		return ib.GetHash()
	}
	return nil
}

func (bd *BDAG) GetMainAncestor(block IBlock, height int64) IBlock {
	if height < 0 || height > int64(block.GetHeight()) {
		return nil
	}

	ib := block

	for ib != nil && int64(ib.GetHeight()) != height {
		if !ib.HasParents() {
			ib = nil
			break
		}
		ib = bd.GetBlockById(ib.GetMainParent())
	}
	return ib
}

func (bd *BDAG) RelativeMainAncestor(block IBlock, distance int64) IBlock {
	return bd.GetMainAncestor(block, int64(block.GetHeight())-distance)
}

func (bd *BDAG) ValidBlock(block IBlock) {
	bd.stateLock.Lock()
	defer bd.stateLock.Unlock()

	block.GetState().Valid()
	bd.commitBlock.AddPair(block.GetID(), block)
}

func (bd *BDAG) InvalidBlock(block IBlock) {
	bd.stateLock.Lock()
	defer bd.stateLock.Unlock()

	block.GetState().Invalid()
	bd.commitBlock.AddPair(block.GetID(), block)
}

// GetIdSet
func (bd *BDAG) GetIdSet(hs []*common.Hash) *IdSet {
	result := NewIdSet()

	for _, v := range hs {
		if bd.lastSnapshot.block != nil {
			if bd.lastSnapshot.block.GetHash().Cmp(*v) == 0 {
				result.Add(bd.lastSnapshot.block.GetID())
				continue
			}
		}
		bid, err := DBGetBlockIdByHash(bd.db, v)
		if err == nil {
			result.Add(uint(bid))
		} else {
			return nil
		}
	}
	return result
}

// Sort block by id
func (bd *BDAG) sortBlock(src []*common.Hash) []*common.Hash {

	if len(src) <= 1 {
		return src
	}
	srcBlockS := BlockSlice{}
	for i := 0; i < len(src); i++ {
		ib := bd.getBlock(src[i])
		if ib != nil {
			srcBlockS = append(srcBlockS, ib)
		}
	}
	if len(srcBlockS) >= 2 {
		sort.Sort(srcBlockS)
	}
	result := []*common.Hash{}
	for i := 0; i < len(srcBlockS); i++ {
		result = append(result, srcBlockS[i].GetHash())
	}
	return result
}

// trySortBlock mirrors sortBlock using peekBlock so a cache miss bails with
// ok=false instead of ever loading from disk. Unlike sortBlock (which
// silently skips a hash it can't resolve, since "doesn't exist" and "not
// cached yet" look the same to a normal lookup), this must bail on any
// ambiguous miss -- it can't tell those two cases apart from a peek alone,
// and guessing wrong would silently drop a block that does exist.
func (bd *BDAG) trySortBlock(src []*common.Hash) ([]*common.Hash, bool) {
	if len(src) <= 1 {
		return src, true
	}
	srcBlockS := BlockSlice{}
	for i := 0; i < len(src); i++ {
		ib, ok := bd.peekBlock(src[i])
		if !ok {
			return nil, false
		}
		if ib != nil {
			srcBlockS = append(srcBlockS, ib)
		}
	}
	if len(srcBlockS) >= 2 {
		sort.Sort(srcBlockS)
	}
	result := []*common.Hash{}
	for i := 0; i < len(srcBlockS); i++ {
		result = append(result, srcBlockS[i].GetHash())
	}
	return result, true
}

// Sort block by id
func (bd *BDAG) SortBlock(src []*common.Hash) []*common.Hash {
	bd.stateLock.Lock()
	defer bd.stateLock.Unlock()

	return bd.sortBlock(src)
}

func (bd *BDAG) LocateBlocks(gs *GraphState, maxHashes uint) []*common.Hash {
	bd.stateLock.Lock()
	defer bd.stateLock.Unlock()
	return bd.locateBlocks(gs, maxHashes)
}

// Locate all eligible block by current graph state.
func (bd *BDAG) locateBlocks(gs *GraphState, maxHashes uint) []*common.Hash {
	if gs.IsExcellent(bd.getGraphState()) {
		return nil
	}
	fs := NewHashSet()
	tips := bd.getValidTips(false)
	queue := make([]IBlock, 0, len(tips))
	queue = append(queue, tips...)
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]

		if fs.Has(cur.GetHash()) {
			continue
		}
		if gs.GetTips().Has(cur.GetHash()) || cur.GetID() == 0 {
			continue
		}
		needRec := true
		if cur.HasChildren() {
			for _, v := range bd.getChildren(cur).GetMap() {
				ib := v.(IBlock)
				if gs.GetTips().Has(ib.GetHash()) || !fs.Has(ib.GetHash()) && ib.IsOrdered() {
					needRec = false
					break
				}
			}
		}
		if needRec {
			fs.AddPair(cur.GetHash(), cur)
			if cur.HasParents() {
				for _, v := range bd.getParents(cur).GetMap() {
					value := v.(IBlock)
					ib := value
					if fs.Has(ib.GetHash()) {
						continue
					}
					queue = append(queue, ib)

				}
			}
		}
	}

	fsSlice := make(BlockSlice, 0, len(fs.GetMap()))
	for _, v := range fs.GetMap() {
		value := v.(IBlock)
		ib := value
		if gs.GetTips().Has(ib.GetHash()) {
			continue
		}
		if ib.HasChildren() {
			need := true
			for _, v := range bd.getChildren(ib).GetMap() {
				ib := v.(IBlock)
				if gs.GetTips().Has(ib.GetHash()) {
					need = false
					break
				}
			}
			if !need {
				continue
			}
		}
		if !ib.IsOrdered() {
			continue
		}
		fsSlice = append(fsSlice, ib)
	}

	resultCap := len(fsSlice)
	if maxHashes > 0 && int(maxHashes) < resultCap {
		resultCap = int(maxHashes)
	}
	result := make([]*common.Hash, 0, resultCap)
	if len(fsSlice) >= 2 {
		sort.Sort(fsSlice)
	}
	for i := 0; i < len(fsSlice); i++ {
		if maxHashes > 0 && i >= int(maxHashes) {
			break
		}
		result = append(result, fsSlice[i].GetHash())
	}
	return result
}

// Fuzzy and quick locate all eligible block by current graph state.
// And maxHashes param cannot be empty.
func (bd *BDAG) locateBlocksFuzzy(gs *GraphState, maxHashes uint) []*common.Hash {
	if gs.IsExcellent(bd.getGraphState()) {
		return nil
	}
	curID := MaxId
	for _, k := range gs.tips {
		gst := bd.getBlock(&k)
		if gst == nil || !gst.IsOrdered() {
			continue
		}
		if gst.GetID() < curID {
			curID = gst.GetID()
		}
	}
	fsSlice := BlockSlice{}
	mainTipID := bd.instance.GetMainChainTipId()
	for ; curID <= mainTipID; curID++ {
		ib := bd.getBlockById(curID)
		if ib == nil {
			continue
		}
		if gs.GetTips().Has(ib.GetHash()) {
			continue
		}
		if !ib.IsOrdered() {
			continue
		}
		fsSlice = append(fsSlice, ib)
		if uint(len(fsSlice)) >= maxHashes {
			break
		}
	}

	result := []*common.Hash{}
	if len(fsSlice) >= 2 {
		sort.Sort(fsSlice)
	}
	for i := 0; i < len(fsSlice); i++ {
		result = append(result, fsSlice[i].GetHash())
	}
	return result
}

// Return the layer of block,it is stable.
// You can imagine that this is the main chain.
func (bd *BDAG) GetLayer(id uint) uint {
	return bd.GetBlockById(id).GetLayer()
}

func (bd *BDAG) Foreach(start IBlock, depth uint, filter FilterType, fn func(block IBlock) (bool, error)) error {
	if _, ok := bd.instance.(*Phantom); !ok {
		return fmt.Errorf("Not support Foreach:%s", bd.instance.GetName())
	}
	count := uint(0)
	cost := time.Now()
	cur := start
main:
	for count < depth && cur != nil {
		if cur.GetID() != start.GetID() {
			ret, err := fn(cur)
			if err != nil {
				return err
			}
			if ret {
				count++
				if count >= depth {
					break
				}
			}
		}
		if cur.GetID() == 0 {
			break
		}
		das := cur.(*PhantomBlock).GetDiffAnticoneList(filter)
		if len(das) > 0 {
			for i := len(das) - 1; i >= 0; i-- {
				if das[i] == MaxId {
					continue
				}
				block := bd.GetBlockById(das[i])
				if block == nil {
					return fmt.Errorf("No block:id=%d", das[i])
				}
				pBlock := block.(*PhantomBlock)
				ret, err := fn(pBlock)
				if err != nil {
					return err
				}
				if ret {
					count++
					if count >= depth {
						break main
					}
				}
			}
		}

		mp := bd.GetBlockById(cur.GetMainParent())
		if mp == nil {
			return fmt.Errorf("No block:id=%d", cur.GetMainParent())
		}
		cur = mp
	}
	if count == 0 {
		return nil
	}
	log.Trace("Foreach dag blocks", "count", count, "depth", depth, "time", time.Since(cost))
	return nil
}
