package chaindb

import (
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"

	blockdag "github.com/BlockdagNetworkLabs/bdag/blockdag"
	"github.com/BlockdagNetworkLabs/bdag/common/hash"
	"github.com/BlockdagNetworkLabs/bdag/consensus/model"
	"github.com/BlockdagNetworkLabs/bdag/core/types"
	"github.com/BlockdagNetworkLabs/bdag/database/rawdb"
	"github.com/BlockdagNetworkLabs/bdag/params"
	"github.com/ethereum/go-ethereum/common"
	etypes "github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/ethdb"
	memutil "github.com/shirou/gopsutil/mem"
)

const (
	defaultDiffLayerCacheMB         = 64
	maxCachePercentOfTotalRAM       = 50
	maxMemoryUsagePercent           = 80.0
	maxFlattenRetries               = 3
	estimatedDiffLayerBlockBytes    = 64 * 1024
	maxDiffLayerBlockFlushThreshold = 65536
)

type diffLayer struct {
	db             ethdb.Database
	memory         atomic.Uint64 // Approximate guess as to how much memory we use
	root           common.Hash   // Root hash to which this snapshot diff belongs to
	stale          atomic.Bool   // Signals that the layer became stale (state progressed)
	lock           sync.RWMutex
	cache          uint64
	bestChainState []byte
	blocks         map[common.Hash]*types.SerializedBlock
	dagBlocks      map[uint][]byte      //Iblock data by ID (uint)
	blockidByHash  map[common.Hash]uint //blockID by hash ( Iblock )
	mainchain      map[uint]bool
	blockidByOrder map[uint]uint
	tips           map[uint]bool
	mainTip        uint
	txIdxEntrys    map[common.Hash]*diffTxIdxEntry

	wg   sync.WaitGroup
	quit chan struct{}

	cdb *ChainDB
}

func (dl *diffLayer) handler() {
	timer := time.NewTicker(params.ActiveNetParams.TargetTimePerBlock)
out:
	for {
		select {
		case <-timer.C:
			dl.lock.RLock()
			bcSize := len(dl.blocks)
			dl.lock.RUnlock()

			// Retrieve system memory information. If it fails we
			// simply ignore the error and rely on the other
			// triggers below.
			memStats, _ := memutil.VirtualMemory()

			shouldFlatten := dl.memory.Load() >= dl.cache ||
				bcSize >= dl.blockFlushThreshold() ||
				(memStats != nil && memStats.UsedPercent >= maxMemoryUsagePercent)

			if shouldFlatten {
				var err error
				for attempt := 0; attempt < maxFlattenRetries; attempt++ {
					err = dl.flatten()
					if err == nil {
						break
					}
					log.Error("flatten diff layer failed", "attempt", attempt+1, "err", err)
					time.Sleep(time.Duration(attempt+1) * time.Second)
				}
				if err != nil {
					log.Error("flatten diff layer ultimately failed", "err", err)
				}
			}
		case <-dl.quit:
			break out
		}
	}
	timer.Stop()
	dl.wg.Done()
}

func (dl *diffLayer) close() error {
	log.Info("diff layer close")

	close(dl.quit)
	dl.wg.Wait()
	err := dl.flatten()
	if err != nil {
		return err
	}
	dl.cdb = nil
	return nil
}

func (dl *diffLayer) flatten() error {
	if dl.cdb.shutdownTracker != nil {
		dl.cdb.shutdownTracker.Wait("difflayer")
		defer dl.cdb.shutdownTracker.Done()
	}

	dl.lock.Lock()
	defer dl.lock.Unlock()

	start := time.Now()
	log.Info("Start flatten diff layer", "memory", dl.memorySize().String())

	batch := dl.db.NewBatchWithSize(dl.flattenBatchSize())
	var flattenErr error

	if len(dl.bestChainState) > 0 {
		if err := rawdb.WriteBestChainState(batch, dl.bestChainState); err != nil {
			flattenErr = errors.Join(flattenErr, err)
		}
	}

	if len(dl.blocks) > 0 {
		for _, v := range dl.blocks {
			if err := rawdb.WriteBlock(batch, v); err != nil {
				flattenErr = errors.Join(flattenErr, err)
			}
		}
		for k := range dl.blocks {
			delete(dl.blocks, k)
		}
	}

	if len(dl.dagBlocks) > 0 {
		for k, v := range dl.dagBlocks {
			if len(v) <= 0 {
				rawdb.DeleteDAGBlock(batch, uint64(k))
			} else {
				if err := rawdb.WriteDAGBlockRaw(batch, k, v); err != nil {
					flattenErr = errors.Join(flattenErr, err)
				}
			}
		}
		for k := range dl.dagBlocks {
			delete(dl.dagBlocks, k)
		}
	}

	if len(dl.blockidByHash) > 0 {
		for k, v := range dl.blockidByHash {
			if v == blockdag.MaxId {
				rawdb.DeleteBlockID(batch, &k)
			} else {
				rawdb.WriteBlockID(batch, &k, uint64(v))
			}
		}
		for k := range dl.blockidByHash {
			delete(dl.blockidByHash, k)
		}
	}

	if len(dl.mainchain) > 0 {
		for k, v := range dl.mainchain {
			if !v {
				rawdb.DeleteMainChain(batch, uint64(k))
			} else {
				if err := rawdb.WriteMainChain(batch, uint64(k)); err != nil {
					flattenErr = errors.Join(flattenErr, err)
				}
			}
		}
		for k := range dl.mainchain {
			delete(dl.mainchain, k)
		}
	}

	if len(dl.blockidByOrder) > 0 {
		for k, v := range dl.blockidByOrder {
			if err := rawdb.WriteBlockOrderSnapshot(batch, uint64(k), uint64(v)); err != nil {
				flattenErr = errors.Join(flattenErr, err)
			}
		}
		for k := range dl.blockidByOrder {
			delete(dl.blockidByOrder, k)
		}
	}

	if len(dl.tips) > 0 {
		tips, err := dl.doGetDAGTips()
		if err != nil {
			return err
		}
		result := []uint64{}
		for i := 0; i < len(tips); i++ {
			result = append(result, uint64(tips[i]))
		}
		if err := rawdb.WriteDAGTips(batch, result); err != nil {
			flattenErr = errors.Join(flattenErr, err)
		}
		for k := range dl.tips {
			delete(dl.tips, k)
		}
	}

	if len(dl.txIdxEntrys) > 0 {
		for _, tide := range dl.txIdxEntrys {
			hash := tide.tx.Hash()
			if tide.add {
				if err := rawdb.WriteTxLookupEntry(batch, &hash, uint64(tide.blockid)); err != nil {
					flattenErr = errors.Join(flattenErr, err)
				}
			} else {
				if err := rawdb.DeleteTxLookupEntry(batch, &hash); err != nil {
					flattenErr = errors.Join(flattenErr, err)
				}
			}
		}
		for k := range dl.txIdxEntrys {
			delete(dl.txIdxEntrys, k)
		}
	}

	if err := batch.Write(); err != nil {
		flattenErr = errors.Join(flattenErr, err)
	}
	dl.memory.Store(0)
	log.Info("End flatten diff layer", "cost", time.Since(start))
	return flattenErr
}

func (dl *diffLayer) GetBestChainState() ([]byte, error) {
	dl.lock.RLock()
	defer dl.lock.RUnlock()

	if len(dl.bestChainState) > 0 {
		return dl.bestChainState, nil
	}
	return rawdb.ReadBestChainState(dl.db), nil
}

func (dl *diffLayer) PutBestChainState(data []byte) error {
	dl.lock.Lock()
	defer dl.lock.Unlock()

	if len(dl.bestChainState) > 0 {
		cur := int64(dl.memory.Load()) - int64(len(dl.bestChainState))
		dl.memory.Store(uint64(cur))
	}
	dl.bestChainState = data
	dl.memory.Add(uint64(len(data)))
	return nil
}

func (dl *diffLayer) GetBlock(hash *common.Hash) (*types.SerializedBlock, error) {
	dl.lock.RLock()
	defer dl.lock.RUnlock()

	if len(dl.blocks) > 0 {
		data, ok := dl.blocks[*hash]
		if ok {
			return data, nil
		}
	}
	return rawdb.ReadBody(dl.db, hash), nil
}

func (dl *diffLayer) GetBlockBytes(hash *common.Hash) ([]byte, error) {
	dl.lock.RLock()
	defer dl.lock.RUnlock()

	if len(dl.blocks) > 0 {
		data, ok := dl.blocks[*hash]
		if ok {
			return data.Bytes()
		}
	}
	return rawdb.ReadBodyRaw(dl.db, hash), nil
}

func (dl *diffLayer) GetHeader(hash *common.Hash) (*types.BlockHeader, error) {
	dl.lock.RLock()
	defer dl.lock.RUnlock()

	if len(dl.blocks) > 0 {
		data, ok := dl.blocks[*hash]
		if ok {
			return &data.Block().Header, nil
		}
	}
	return rawdb.ReadHeader(dl.db, hash), nil
}

func (dl *diffLayer) PutBlock(block *types.SerializedBlock) error {
	dl.lock.Lock()
	defer dl.lock.Unlock()

	if dl.blocks == nil {
		dl.blocks = map[common.Hash]*types.SerializedBlock{}
	}
	dl.blocks[*block.Hash()] = block

	bbs, err := block.Bytes()
	if err != nil {
		return err
	}
	dl.memory.Add(uint64(len(bbs)) + common.HashLength)
	return nil
}

func (dl *diffLayer) HasBlock(hash *common.Hash) bool {
	dl.lock.RLock()
	defer dl.lock.RUnlock()

	if len(dl.blocks) > 0 {
		_, ok := dl.blocks[*hash]
		if ok {
			return true
		}
	}
	return rawdb.HasHeader(dl.db, hash)
}

func (dl *diffLayer) GetDAGBlock(blockID uint) ([]byte, error) {
	dl.lock.RLock()
	defer dl.lock.RUnlock()

	if len(dl.dagBlocks) > 0 {
		data, ok := dl.dagBlocks[blockID]
		if ok {
			if len(data) > 0 {
				return data, nil
			} else {
				return nil, nil
			}
		}
	}
	return rawdb.ReadDAGBlockBaw(dl.db, uint64(blockID)), nil
}

func (dl *diffLayer) PutDAGBlock(blockID uint, data []byte) error {
	dl.lock.Lock()
	defer dl.lock.Unlock()

	if dl.dagBlocks == nil {
		dl.dagBlocks = map[uint][]byte{}
	}
	dl.dagBlocks[blockID] = data

	dl.memory.Add(uint64(len(data)) + uint64(unsafe.Sizeof(blockID)))
	return nil
}

func (dl *diffLayer) DeleteDAGBlock(blockID uint) error {
	dl.lock.Lock()
	defer dl.lock.Unlock()

	if dl.dagBlocks == nil {
		dl.dagBlocks = map[uint][]byte{}
	}
	dl.dagBlocks[blockID] = nil

	dl.memory.Add(uint64(unsafe.Sizeof(blockID)))
	return nil
}

func (dl *diffLayer) GetDAGBlockIdByHash(bh *common.Hash) (uint, error) {
	dl.lock.RLock()
	defer dl.lock.RUnlock()

	if len(dl.dagBlocks) > 0 {
		data, ok := dl.blockidByHash[*bh]
		if ok {
			if data != blockdag.MaxId {
				return data, nil
			} else {
				return blockdag.MaxId, nil
			}
		}
	}

	blockID := rawdb.ReadBlockID(dl.db, bh)
	if blockID == nil {
		return blockdag.MaxId, fmt.Errorf("No blockID:%s", bh.String())
	}
	return uint(*blockID), nil
}

func (dl *diffLayer) PutDAGBlockIdByHash(bh *common.Hash, id uint) error {
	dl.lock.Lock()
	defer dl.lock.Unlock()

	if dl.blockidByHash == nil {
		dl.blockidByHash = map[common.Hash]uint{}
	}
	dl.blockidByHash[*bh] = id

	dl.memory.Add(uint64(unsafe.Sizeof(id)) + common.HashLength)
	return nil
}

func (dl *diffLayer) DeleteDAGBlockIdByHash(bh *common.Hash) error {
	dl.lock.Lock()
	defer dl.lock.Unlock()

	if dl.blockidByHash == nil {
		dl.blockidByHash = map[common.Hash]uint{}
	}
	dl.blockidByHash[*bh] = blockdag.MaxId

	dl.memory.Add(uint64(unsafe.Sizeof(blockdag.MaxId)) + common.HashLength)
	return nil
}

func (dl *diffLayer) PutMainChainBlock(blockID uint) error {
	dl.lock.Lock()
	defer dl.lock.Unlock()

	if dl.mainchain == nil {
		dl.mainchain = map[uint]bool{}
	}
	dl.mainchain[blockID] = true

	dl.memory.Add(uint64(unsafe.Sizeof(blockID)) + 1)
	return nil
}

func (dl *diffLayer) HasMainChainBlock(blockID uint) bool {
	dl.lock.RLock()
	defer dl.lock.RUnlock()

	if len(dl.mainchain) > 0 {
		data, ok := dl.mainchain[blockID]
		if ok {
			return data
		}
	}
	return rawdb.ReadMainChain(dl.db, uint64(blockID))
}

func (dl *diffLayer) DeleteMainChainBlock(blockID uint) error {
	dl.lock.Lock()
	defer dl.lock.Unlock()

	if dl.mainchain == nil {
		dl.mainchain = map[uint]bool{}
	}
	dl.mainchain[blockID] = false

	dl.memory.Add(uint64(unsafe.Sizeof(blockID)) + 1)
	return nil
}

func (dl *diffLayer) PutBlockIdByOrder(order uint, id uint) error {
	dl.lock.Lock()
	defer dl.lock.Unlock()

	if dl.blockidByOrder == nil {
		dl.blockidByOrder = map[uint]uint{}
	}
	dl.blockidByOrder[order] = id

	dl.memory.Add(uint64(unsafe.Sizeof(order)) * 2)
	return nil
}

func (dl *diffLayer) GetBlockIdByOrder(order uint) (uint, error) {
	dl.lock.RLock()
	defer dl.lock.RUnlock()

	if len(dl.blockidByOrder) > 0 {
		data, ok := dl.blockidByOrder[order]
		if ok {
			return data, nil
		}
	}

	id := rawdb.ReadBlockOrderSnapshot(dl.db, uint64(order))
	if id == nil {
		return blockdag.MaxId, nil
	}
	return uint(*id), nil
}

func (dl *diffLayer) PutDAGTip(id uint, isMain bool) error {
	dl.lock.Lock()
	defer dl.lock.Unlock()

	num := 0
	if dl.tips == nil {
		dl.tips = map[uint]bool{}
	} else {
		num = len(dl.tips)
	}
	dl.tips[id] = true
	if isMain {
		dl.mainTip = id
	}

	dl.memory.Add(uint64(5 * (len(dl.tips) - num)))
	return nil
}

func (dl *diffLayer) GetDAGTips() ([]uint, error) {
	dl.lock.Lock()
	defer dl.lock.Unlock()

	return dl.doGetDAGTips()
}

func (dl *diffLayer) doGetDAGTips() ([]uint, error) {
	tips := rawdb.ReadDAGTips(dl.db)
	result := []uint{}
	resultSet := blockdag.NewIdSet()
	for i := 0; i < len(tips); i++ {
		resultSet.Add(uint(tips[i]))
	}

	result = append(result, dl.mainTip)
	if len(dl.tips) > 0 {
		for k, v := range dl.tips {
			if k == dl.mainTip {
				resultSet.Remove(k)
				continue
			}
			if v {
				resultSet.Add(k)
			} else {
				resultSet.Remove(k)
			}
		}
	}

	result = append(result, resultSet.List()...)

	if len(result) <= 0 {
		return nil, fmt.Errorf("No tips")
	}
	if result[0] == blockdag.MaxId {
		return nil, fmt.Errorf("Can't find main tip")
	}

	return result, nil
}

func (dl *diffLayer) DeleteDAGTip(id uint) error {
	dl.lock.Lock()
	defer dl.lock.Unlock()

	num := 0
	if dl.tips == nil {
		dl.tips = map[uint]bool{}
	} else {
		num = len(dl.tips)
	}
	dl.tips[id] = false
	if dl.mainTip == id {
		dl.mainTip = blockdag.MaxId
	}

	dl.memory.Add(uint64(5 * (len(dl.tips) - num)))
	return nil
}

type diffTxIdxEntry struct {
	tx        *etypes.Transaction
	blockid   uint
	blockhash *common.Hash
	add       bool
}

func (dl *diffLayer) PutTxIdxEntrys(sblock *types.SerializedBlock, block model.Block) error {
	dl.lock.Lock()
	defer dl.lock.Unlock()

	if dl.txIdxEntrys == nil {
		dl.txIdxEntrys = map[common.Hash]*diffTxIdxEntry{}
	}
	for _, tx := range sblock.Transactions() {
		dtie := &diffTxIdxEntry{
			tx:        tx,
			blockid:   block.GetID(),
			blockhash: sblock.Hash(),
			add:       true,
		}
		dl.txIdxEntrys[tx.Hash()] = dtie
		dl.memory.Add(uint64(unsafe.Sizeof(dtie)) + common.HashLength)
	}
	return nil
}

func (dl *diffLayer) GetTxIdxEntry(id *common.Hash, verbose bool) (*etypes.Transaction, *common.Hash, error) {
	dl.lock.Lock()

	if len(dl.txIdxEntrys) > 0 {
		dtie, ok := dl.txIdxEntrys[*id]
		if ok {
			if dtie.add {
				dl.lock.Unlock()
				return dtie.tx, dtie.blockhash, nil
			} else {
				dl.lock.Unlock()
				return nil, nil, nil
			}
		}
	}

	dl.lock.Unlock()

	if !verbose {
		blockID := rawdb.ReadTxLookupEntry(dl.db, id)
		if blockID == nil {
			return nil, nil, nil
		}
		blockhash, err := blockdag.DBGetDAGBlockHashByID(dl.cdb, *blockID)
		if err != nil {
			return nil, nil, err
		}
		return nil, blockhash, nil
	}
	tx, _, blockhash, _ := rawdb.ReadTransaction(dl.db, id)
	return tx, blockhash, nil
}

func (dl *diffLayer) DeleteTxIdxEntrys(block *types.SerializedBlock) error {
	for _, tx := range block.Transactions() {
		hash := tx.Hash()
		_, blockHash, _ := dl.GetTxIdxEntry(&hash, false)
		if blockHash != nil && blockHash.Cmp(*block.Hash()) != 0 {
			continue
		}

		dl.lock.Lock()
		if dl.txIdxEntrys == nil {
			dl.txIdxEntrys = map[common.Hash]*diffTxIdxEntry{}
		}
		dtie := &diffTxIdxEntry{
			tx:        tx,
			blockid:   blockdag.MaxId,
			blockhash: block.Hash(),
			add:       false,
		}
		dl.txIdxEntrys[tx.Hash()] = dtie
		// dl.memory.Add(uint64(13)) //need to look this

		dl.memory.Add(uint64(unsafe.Sizeof(dtie)) + common.HashLength)
		dl.lock.Unlock()
	}
	return nil
}

func (dl *diffLayer) memorySize() common.StorageSize {
	return common.StorageSize(dl.memory.Load())
}

func (dl *diffLayer) objects() int {
	dl.lock.RLock()
	defer dl.lock.RUnlock()

	return /**len(dl.spendJournal) + len(dl.utxo) + len(dl.tokenState)**/ +len(dl.blocks) + len(dl.dagBlocks) +
		len(dl.blockidByHash) + len(dl.mainchain) + len(dl.blockidByOrder) + len(dl.tips) + len(dl.txIdxEntrys)
}

func (dl *diffLayer) blockFlushThreshold() int {
	threshold := int(dl.cache / estimatedDiffLayerBlockBytes)
	if threshold < blockdag.MinBlockDataCache {
		return blockdag.MinBlockDataCache
	}
	if threshold > maxDiffLayerBlockFlushThreshold {
		return maxDiffLayerBlockFlushThreshold
	}
	return threshold
}

func (dl *diffLayer) flattenBatchSize() int {
	size := dl.memory.Load()
	if size < 4*1024*1024 {
		return 4 * 1024 * 1024
	}
	if size > 256*1024*1024 {
		return 256 * 1024 * 1024
	}
	return int(size)
}

func newDiffLayer(cdb *ChainDB, cache int) *diffLayer {
	if cache <= 0 {
		cache = defaultDiffLayerCacheMB
	}
	if vm, err := memutil.VirtualMemory(); err == nil {
		maxCache := int((vm.Total / 1024 / 1024) * maxCachePercentOfTotalRAM / 100)
		if cache > maxCache {
			cache = maxCache
		}
	}

	dl := &diffLayer{
		cdb:   cdb,
		db:    cdb.db,
		root:  hash.ZeroHash.ToEVMHash(),
		cache: uint64(cache) * 1024 * 1024,
		quit:  make(chan struct{}),
	}
	dl.stale.Store(false)
	dl.memory.Store(0)
	dl.wg.Add(1)
	go dl.handler()
	return dl
}
