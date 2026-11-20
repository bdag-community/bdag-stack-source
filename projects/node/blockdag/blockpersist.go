package Blockdag

import (
	"bytes"
	"fmt"
	"io"
	"time"

	"github.com/BlockdagNetworkLabs/bdag/consensus/model"
	s "github.com/BlockdagNetworkLabs/bdag/core/serialization"
	"github.com/ethereum/go-ethereum/common"

	"github.com/BlockdagNetworkLabs/bdag/params"
)

// Load from database
func (bd *BDAG) Load(blockTotal uint, genesis *common.Hash) error {
	serializedData, err := bd.db.GetDagInfo()
	if err != nil {
		return err
	}
	err = bd.Decode(bytes.NewReader(serializedData))
	if err != nil {
		return err
	}
	bd.genesis = *genesis
	bd.blockTotal = blockTotal
	bd.blocks = map[uint]IBlock{}
	bd.tips = NewIdSet()
	return bd.instance.Load()
}

func (bd *BDAG) Encode(w io.Writer) error {
	dagTypeIndex := GetDAGTypeIndex(bd.instance.GetName())
	err := s.WriteElements(w, dagTypeIndex)
	if err != nil {
		return err
	}
	return bd.instance.Encode(w)
}

// decode
func (bd *BDAG) Decode(r io.Reader) error {
	var dagTypeIndex byte
	err := s.ReadElements(r, &dagTypeIndex)
	if err != nil {
		return err
	}
	if GetDAGTypeIndex(bd.instance.GetName()) != dagTypeIndex {
		return fmt.Errorf("The dag type is %s, but read is %s", bd.instance.GetName(), GetDAGTypeByIndex(dagTypeIndex))
	}
	return bd.instance.Decode(r)
}

func (bd *BDAG) GetBlockData(ib IBlock) IBlockData {
	bd.blockDataLock.Lock()
	defer bd.blockDataLock.Unlock()
	if ib.GetID() < bd.blockTotal {
		bd.blockDataCache[ib.GetID()] = time.Now()
	}
	if ib.IsLoaded() {
		return ib.GetData()
	}
	// load
	data := bd.getBlockData(ib.GetHash())
	if data == nil {
		panic(fmt.Errorf("Can't load block data:%s", ib.GetHash().String()))
	}
	ib.SetData(data)
	return ib.GetData()
}

func (bd *BDAG) updateBlockDataCache() {
	cacheSize := bd.GetBlockDataCacheSize()
	if cacheSize <= bd.minBDCacheSize {
		return
	}
	maxLife := params.ActiveNetParams.TargetTimePerBlock
	need := cacheSize - bd.minBDCacheSize
	blockDataCache := []uint{}
	bd.blockDataLock.Lock()
	for k, t := range bd.blockDataCache {
		if time.Since(t) < maxLife {
			continue
		}
		blockDataCache = append(blockDataCache, k)
		need--
		if need <= 0 {
			break
		}
	}
	bd.blockDataLock.Unlock()
	if len(blockDataCache) <= 0 {
		return
	}
	for _, k := range blockDataCache {
		if !bd.HasLoadedBlock(k) {
			bd.blockDataLock.Lock()
			delete(bd.blockDataCache, k)
			bd.blockDataLock.Unlock()
			continue
		}
		ib := bd.GetBlockById(k)
		if ib != nil {
			ib.SetData(nil)
		}
		bd.blockDataLock.Lock()
		delete(bd.blockDataCache, k)
		bd.blockDataLock.Unlock()
	}
	log.Debug("BDAG block data cache release", "num", len(blockDataCache))
}

func (bd *BDAG) LoadBlockDataSet(sets *IdSet) {
	if sets == nil {
		return
	}
	for _, v := range sets.GetMap() {
		bd.GetBlockData(v.(IBlock))
	}
}

func (bd *BDAG) GetBlockDataCacheSize() uint64 {
	bd.blockDataLock.Lock()
	defer bd.blockDataLock.Unlock()
	return uint64(len(bd.blockDataCache))
}

func (bd *BDAG) GetMinBlockDataCacheSize() uint64 {
	bd.blockDataLock.Lock()
	defer bd.blockDataLock.Unlock()
	return bd.minBDCacheSize
}

// ReadBlockById reads historical metadata without populating the live DAG
// cache or linking ancestors into an ever-growing object graph. Callers must
// not mutate the result, which may be an existing cache entry.
func (bd *BDAG) ReadBlockById(id uint) (IBlock, error) {
	if id == MaxId {
		return nil, fmt.Errorf("invalid block ID")
	}
	bd.stateLock.RLock()
	defer bd.stateLock.RUnlock()
	if block, ok := bd.blocks[id]; ok {
		return block, nil
	}
	return bd.readBlock(id)
}

// readBlock decodes one detached block. The caller holds stateLock.
func (bd *BDAG) readBlock(id uint) (IBlock, error) {
	ph, ok := bd.instance.(*Phantom)
	if !ok {
		return nil, fmt.Errorf("BDAG instance error")
	}
	block := Block{id: id}
	ib := ph.CreateBlock(&block)
	err := DBGetDAGBlock(bd.db, ib)
	if err != nil {
		return nil, err
	}
	if id == 0 && ib.GetHash().Cmp(*ph.bd.GetGenesisHash()) != 0 {
		return nil, fmt.Errorf("genesis data mismatch")
	}
	return ib, nil
}

func (bd *BDAG) loadBlock(id uint) (IBlock, error) {
	ib, err := bd.readBlock(id)
	if err != nil {
		return nil, err
	}
	if ib.HasParents() {
		parentIDs := ib.GetParents().List()
		for _, pid := range parentIDs {
			parent, ok := bd.blocks[pid]
			if ok {
				parent.AttachChild(ib)
				ib.AttachParent(parent)
			}
		}
	}
	if ib.HasChildren() {
		childrenIDs := ib.GetChildren().List()
		for _, pid := range childrenIDs {
			child, ok := bd.blocks[pid]
			if ok {
				child.AttachParent(ib)
				ib.AttachChild(child)
			}
		}
	}
	bd.blocks[ib.GetID()] = ib
	return ib, nil
}

func (bd *BDAG) GetParents(ib IBlock) *IdSet {
	bd.stateLock.Lock()
	defer bd.stateLock.Unlock()
	return bd.getParents(ib)
}

// get parents from block if it is not loaded, it will be loaded
func (bd *BDAG) getParents(ib IBlock) *IdSet {
	if !ib.HasParents() {
		return ib.GetParents()
	}
	parents := ib.GetParents()
	parentIDs := parents.List()
	for _, pid := range parentIDs {
		if parents.IsDataEmpty(pid) {
			ib.AttachParent(bd.getBlockById(pid))
		}
	}
	return parents
}

func (bd *BDAG) GetChildren(ib IBlock) *IdSet {
	bd.stateLock.Lock()
	defer bd.stateLock.Unlock()
	return bd.getChildren(ib)
}

// get children from block if it is not loaded, it will be loaded
func (bd *BDAG) getChildren(ib IBlock) *IdSet {
	if !ib.HasChildren() {
		return ib.GetChildren()
	}
	children := ib.GetChildren()
	childIDs := children.List()
	for _, cid := range childIDs {
		if children.IsDataEmpty(cid) {
			ib.AttachChild(bd.getBlockById(cid))
		}
	}
	return children
}

func (bd *BDAG) GetBlockCacheSize() uint64 {
	bd.stateLock.Lock()
	defer bd.stateLock.Unlock()
	return uint64(len(bd.blocks))
}

func (bd *BDAG) GetMinBlockCacheSize() uint64 {
	bd.stateLock.Lock()
	defer bd.stateLock.Unlock()
	return bd.minCacheSize
}

func (bd *BDAG) updateBlockCache() {
	cacheSize := bd.GetBlockCacheSize()
	if cacheSize <= bd.minCacheSize {
		return
	}
	mainTip := bd.GetMainChainTip()
	need := cacheSize - bd.minCacheSize
	deletes := []IBlock{}

	bd.stateLock.Lock()
	defer bd.stateLock.Unlock()
	for k, v := range bd.blocks {
		if k == 0 ||
			k == mainTip.GetID() ||
			bd.tips.Has(k) ||
			v.GetHash().Cmp(*bd.GetGenesisHash()) == 0 ||
			bd.commitBlock.Has(k) {
			continue
		}
		if v.GetHeight()+uint(bd.minCacheSize) < mainTip.GetHeight() {
			deletes = append(deletes, v)
			need--
			if need <= 0 {
				break
			}
		}
	}
	for _, b := range deletes {
		bd.unloadBlock(b)
	}
	if len(deletes) > 0 {
		log.Debug("BDAG block cache release", "num", len(deletes))
	}
}

func (bd *BDAG) unloadBlock(ib IBlock) {
	delete(bd.blocks, ib.GetID())
	if ib.HasParents() {
		for _, parent := range ib.GetParents().GetMap() {
			if parent == nil {
				continue
			}
			pib, ok := parent.(IBlock)
			if !ok {
				continue
			}
			pib.DetachChild(ib)
		}
	}

	if ib.HasChildren() {
		for _, child := range ib.GetChildren().GetMap() {
			if child == nil {
				continue
			}
			cib, ok := child.(IBlock)
			if !ok {
				continue
			}
			cib.DetachParent(ib)
		}
	}

}

func (bd *BDAG) SetCacheSize(dag uint64, data uint64) {
	bd.stateLock.Lock()
	defer bd.stateLock.Unlock()
	if dag > MinBlockPruneSize {
		bd.minCacheSize = dag
	}
	if data > 0 {
		bd.minBDCacheSize = data
	}
}

func (bd *BDAG) DB() model.DataBase {
	return bd.db
}
