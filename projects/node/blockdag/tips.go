package Blockdag

import (
	"fmt"
	"math"

	"github.com/BlockdagNetworkLabs/bdag/core/merkle"
	"github.com/ethereum/go-ethereum/common"
)

// return the terminal blocks, because there maybe more than one, so this is a set.
func (bd *BDAG) GetTips() *HashSet {
	bd.stateLock.Lock()
	defer bd.stateLock.Unlock()

	tips := NewHashSet()
	for k := range bd.tips.GetMap() {
		ib := bd.getBlockById(k)
		tips.AddPair(ib.GetHash(), ib)
	}
	return tips
}

// Acquire the tips array of DAG
func (bd *BDAG) GetTipsList() []IBlock {
	bd.stateLock.Lock()
	defer bd.stateLock.Unlock()

	result := bd.instance.GetTipsList()
	if result != nil {
		return result
	}
	result = []IBlock{}
	for k := range bd.tips.GetMap() {
		result = append(result, bd.getBlockById(k))
	}
	return result
}

func (bd *BDAG) GetValidTips(expectPriority int) []*common.Hash {
	bd.stateLock.Lock()
	defer bd.stateLock.Unlock()
	tips := bd.getValidTips(true)

	result := []*common.Hash{tips[0].GetHash()}
	epNum := MaxPriority
	for k, v := range tips {
		if k == 0 {
			if bd.GetBlockData(v).GetPriority() <= 1 {
				epNum--
			}
			continue
		}
		if bd.GetBlockData(v).GetPriority() > 1 {
			result = append(result, v.GetHash())
			continue
		}
		if epNum <= 0 {
			break
		}
		result = append(result, v.GetHash())
		epNum--
	}
	return result
}

func (bd *BDAG) getValidTips(limit bool) []IBlock {
	temp := bd.tips.Clone()
	mainParent := bd.getMainChainTip()
	temp.Remove(mainParent.GetID())
	var parents []uint
	if temp.Size() > 1 {
		parents = temp.SortHeightList(true)
	} else {
		parents = temp.List()
	}

	tips := make([]IBlock, 1, len(parents)+1)
	tips[0] = mainParent
	tipsM := make(map[common.Hash]struct{}, len(parents))
	rightDist := 0.0
	leftDist := 0.0

	for i := 0; i < len(parents); i++ {
		if mainParent.GetID() == parents[i] {
			continue
		}
		block := bd.getBlockById(parents[i])
		distance := math.Abs(float64(block.GetLayer()) - float64(mainParent.GetLayer()))
		if distance > MaxTipLayerGap {
			continue
		}
		blockHash := block.GetHash()
		_, exist := tipsM[*blockHash]
		if exist {
			continue
		}
		if block.GetLayer() > mainParent.GetLayer() {
			if math.Max(distance, rightDist)+leftDist > MaxTipLayerGap {
				continue
			} else {
				rightDist = math.Max(distance, rightDist)
			}
		} else {
			if math.Max(distance, leftDist)+rightDist > MaxTipLayerGap {
				continue
			} else {
				leftDist = math.Max(leftDist, distance)
			}
		}
		tips = append(tips, block)
		tipsM[*blockHash] = struct{}{}
		if limit && len(tips) >= bd.getMaxParents() {
			break
		}
	}
	return tips
}

// tryGetValidTips mirrors getValidTips exactly, but resolves every block via
// peekBlockById instead of getBlockById: it never triggers a disk-load, and
// returns ok=false the moment any lookup misses the in-memory cache. This
// lets GetGraphState attempt the whole computation under a read lock and
// fall back to the always-correct, write-locked getValidTips only on a
// genuine miss (e.g. a tip that was pruned/evicted from bd.blocks).
//
// Keep this in sync with getValidTips if that logic changes.
func (bd *BDAG) tryGetValidTips(limit bool) ([]IBlock, bool) {
	mainParent, ok := bd.peekBlockById(bd.instance.GetMainChainTipId())
	if !ok {
		return nil, false
	}

	temp := bd.tips.Clone()
	temp.Remove(mainParent.GetID())
	var parents []uint
	if temp.Size() > 1 {
		parents = temp.SortHeightList(true)
	} else {
		parents = temp.List()
	}

	tips := []IBlock{mainParent}
	tipsM := map[string]struct{}{}
	rightDist := 0.0
	leftDist := 0.0

	for i := 0; i < len(parents); i++ {
		if mainParent.GetID() == parents[i] {
			continue
		}
		block, ok := bd.peekBlockById(parents[i])
		if !ok {
			return nil, false
		}
		distance := math.Abs(float64(block.GetLayer()) - float64(mainParent.GetLayer()))
		if distance > MaxTipLayerGap {
			continue
		}
		_, exist := tipsM[block.GetHash().String()]
		if exist {
			continue
		}
		if block.GetLayer() > mainParent.GetLayer() {
			if math.Max(distance, rightDist)+leftDist > MaxTipLayerGap {
				continue
			} else {
				rightDist = math.Max(distance, rightDist)
			}
		} else {
			if math.Max(distance, leftDist)+rightDist > MaxTipLayerGap {
				continue
			} else {
				leftDist = math.Max(leftDist, distance)
			}
		}
		tips = append(tips, block)
		tipsM[block.GetHash().String()] = struct{}{}
		if limit && len(tips) >= bd.getMaxParents() {
			break
		}
	}
	return tips, true
}

// build merkle tree form current DAG tips
func (bd *BDAG) BuildMerkleTreeStoreFromTips() []*common.Hash {
	parents := bd.GetTips().SortList(false)
	return merkle.BuildParentsMerkleTreeStore(parents)
}

// Refresh the dag tip with new block,it will cause changes in tips set.
func (bd *BDAG) updateTips(b IBlock) {
	if b.HasChildren() {
		log.Warn(fmt.Sprintf("Cannot add illegal tip:%s", b.GetHash()))
		return
	}
	if bd.tips == nil {
		bd.tips = NewIdSet()
		bd.tips.AddPair(b.GetID(), b)
		return
	}
	for k, v := range bd.tips.GetMap() {
		block := v.(IBlock)
		if block.HasChildren() {
			bd.tips.Remove(k)
		}
	}
	bd.tips.AddPair(b.GetID(), b)
}

func (bd *BDAG) optimizeTips(load bool) {
	disTipsCount := 0
	for {
		disTips := bd.getDiscardedTips(load)
		if len(disTips) <= 0 {
			break
		}
		for _, v := range disTips {
			err := bd.removeTip(v)
			if err != nil {
				log.Error(err.Error())
			} else {
				disTipsCount++
				log.Trace(fmt.Sprintf("Remove discarded tip:%d(%s)", v.GetID(), v.GetHash().String()))
			}
		}
	}
	if disTipsCount > 0 {
		log.Trace(fmt.Sprintf("Remove discarded tips:%d", disTipsCount))
	}
}

func (bd *BDAG) removeTip(b IBlock) error {
	bd.tips.Remove(b.GetID())
	err := DBDelDAGBlock(bd.db, b.GetID())
	if err != nil {
		return err
	}
	err = DBDelBlockIdByHash(bd.db, b.GetHash())
	if err != nil {
		return err
	}
	err = DBDelDAGTip(bd.db, b.GetID())
	if err != nil {
		return err
	}
	parents := bd.getParents(b)
	for _, v := range parents.GetMap() {
		block := v.(IBlock)
		block.RemoveChild(b.GetID())
		if !block.HasChildren() {
			bd.tips.AddPair(block.GetID(), block)
			err = DBPutDAGTip(bd.db, block.GetID(), block.GetID() == bd.instance.GetMainChainTipId())
			if err != nil {
				return err
			}
		}
		err = DBPutDAGBlock(bd.db, block)
		if err != nil {
			return err
		}
	}
	if err != nil {
		return err
	}
	delete(bd.blocks, b.GetID())

	ph, ok := bd.instance.(*Phantom)
	if !ok {
		return fmt.Errorf("BDAG instance error")
	}
	ph.diffAnticone.Remove(b.GetID())
	if ph.virtualBlock.HasParents() {
		ph.virtualBlock.RemoveParent(b.GetID())
	}
	return DBDelDiffAnticone(bd.db, b.GetID())
}

func (bd *BDAG) getDiscardedTips(load bool) []IBlock {
	mainTip := bd.getMainChainTip()
	var result []IBlock
	for _, v := range bd.tips.GetMap() {
		block := v.(IBlock)
		if !load {
			if block.GetID()+uint(bd.tipsDisLimit) >= mainTip.GetID() {
				continue
			}
		}
		if bd.CanPrune(block, mainTip) {
			if result == nil {
				result = []IBlock{}
			}
			result = append(result, block)
		}
	}
	return result
}

func (bd *BDAG) SetTipsDisLimit(limit int64) {
	bd.stateLock.Lock()
	defer bd.stateLock.Unlock()
	bd.tipsDisLimit = limit
}

func (bd *BDAG) GetTipsDisLimit() int64 {
	bd.stateLock.Lock()
	defer bd.stateLock.Unlock()
	return bd.tipsDisLimit
}

func (bd *BDAG) GetTipsSet() *IdSet {
	return bd.tips
}

func (bd *BDAG) CanPrune(block IBlock, mainTip IBlock) bool {
	if block.GetID() == mainTip.GetID() {
		return false
	}
	if block.IsOrdered() {
		return false
	}
	gap := int64(mainTip.GetHeight()) - int64(block.GetHeight())
	if gap <= bd.tipsDisLimit {
		return false
	}
	return true
}
