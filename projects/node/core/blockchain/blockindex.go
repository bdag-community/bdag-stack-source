package blockchain

import (
	"fmt"

	blockdag "github.com/BlockdagNetworkLabs/bdag/blockdag"
	"github.com/BlockdagNetworkLabs/bdag/consensus/model"
	"github.com/BlockdagNetworkLabs/bdag/core/types"
	"github.com/BlockdagNetworkLabs/bdag/core/types/pow"
	"github.com/ethereum/go-ethereum/common"
)

// LookupNode returns the block node identified by the provided hash.  It will
// return nil if there is no entry for the hash.
func (b *BlockChain) LookupNode(hash *common.Hash) *BlockNode {
	ib := b.GetBlock(hash)
	if ib == nil {
		return nil
	}
	return b.bd.GetBlockData(ib).(*BlockNode)
}

func (b *BlockChain) LookupNodeById(id uint) *BlockNode {
	ib := b.bd.GetBlockById(id)
	if ib == nil {
		return nil
	}
	return b.bd.GetBlockData(ib).(*BlockNode)
}

func (b *BlockChain) GetBlockNode(ib blockdag.IBlock) *BlockNode {
	if ib == nil {
		return nil
	}
	return b.bd.GetBlockData(ib).(*BlockNode)
}

func (b *BlockChain) getBlockData(hash *common.Hash) blockdag.IBlockData {
	block, err := b.fetchBlockByHash(hash)
	if err != nil {
		log.Error(err.Error())
		return nil
	}
	return NewBlockNode(block)
}

func (b *BlockChain) GetBlock(h *common.Hash) blockdag.IBlock {
	return b.bd.GetBlock(h)
}

func (b *BlockChain) GetBlockByOrder(order uint64) model.Block {
	return b.bd.GetBlockByOrder(uint(order))
}

func (b *BlockChain) GetBlockById(id uint) model.Block {
	return b.bd.GetBlockById(id)
}

func (b *BlockChain) GetMainChainTip() model.Block {
	return b.bd.GetMainChainTip()
}

// ForEachHeaderLayer iterates over block headers on the current main chain
// within the inclusive range [from, to] of layers and invokes fn for each
// header in ascending layer order. If fn is nil or the requested range lies
// beyond the current tip, the function returns immediately.
func (b *BlockChain) ForEachHeaderLayer(from, to uint64, fn func(*types.BlockHeader)) {
	if fn == nil || from > to {
		return
	}

	b.ChainRLock()
	tip := b.bd.GetMainChainTip()
	if tip == nil {
		b.ChainRUnlock()
		return
	}

	tipLayer := uint64(tip.GetLayer())
	if from > tipLayer {
		b.ChainRUnlock()
		return
	}
	if to > tipLayer {
		to = tipLayer
	}

	headers := make([]types.BlockHeader, 0)
	for cur := tip; cur != nil; {
		layer := uint64(cur.GetLayer())
		if layer < from {
			break
		}
		if layer <= to {
			if hdr := b.GetBlockHeader(cur); hdr != nil {
				headers = append(headers, *hdr)
			}
		}

		parentID := cur.GetMainParent()
		if parentID == blockdag.MaxId {
			break
		}
		next := b.bd.GetBlockById(parentID)
		if next == nil {
			break
		}
		cur = next
	}
	b.ChainRUnlock()

	for i := len(headers) - 1; i >= 0; i-- {
		fn(&headers[i])
	}
}

// BlockOrderByHash returns the order of the block with the given hash in the
// chain.
//
// This function is safe for concurrent access.
func (b *BlockChain) GetBlockOrderByHash(hash *common.Hash) (uint, error) {
	ib := b.bd.GetBlock(hash)
	if ib == nil {
		return blockdag.MaxBlockOrder, fmt.Errorf("No block order:%s", hash.String())
	}
	return ib.GetOrder(), nil
}

// OrderRange returns a range of block hashes for the given start and end
// orders.  It is inclusive of the start order and exclusive of the end
// order.  The end order will be limited to the current main chain order.
//
// This function is safe for concurrent access.
func (b *BlockChain) OrderRange(startOrder, endOrder uint64) ([]common.Hash, error) {
	// Ensure requested orders are sane.
	if startOrder < 0 {
		return nil, fmt.Errorf("start order of fetch range must not "+
			"be less than zero - got %d", startOrder)
	}
	if endOrder < startOrder {
		return nil, fmt.Errorf("end order of fetch range must not "+
			"be less than the start order - got start %d, end %d",
			startOrder, endOrder)
	}

	// There is nothing to do when the start and end orders are the same,
	// so return now to avoid the chain view lock.
	if startOrder == endOrder {
		return nil, nil
	}

	// Grab a lock on the chain view to prevent it from changing due to a
	// reorg while building the hashes.
	b.ChainLock()
	defer b.ChainUnlock()

	// When the requested start order is after the most recent best chain
	// order, there is nothing to do.
	latestOrder := b.BestSnapshot().GraphState.GetMainOrder()
	if startOrder > uint64(latestOrder) {
		return nil, nil
	}

	// Limit the ending order to the latest order of the chain.
	if endOrder > uint64(latestOrder+1) {
		endOrder = uint64(latestOrder + 1)
	}

	// Fetch as many as are available within the specified range.
	hashes := make([]common.Hash, 0, endOrder-startOrder)
	for i := startOrder; i < endOrder; i++ {
		h, err := b.BlockHashByOrder(i)
		if err != nil {
			log.Error("order not exist", "order", i)
			return nil, err
		}
		hashes = append(hashes, *h)
	}
	return hashes, nil
}

func (b *BlockChain) GetBlockHashByOrder(order uint) *common.Hash {
	return b.bd.GetBlockHashByOrder(order)
}

// dbFetchBlockByOrder uses an existing database transaction to retrieve the
// raw block for the provided order, deserialize it, and return a Block
// with the height set.
func (b *BlockChain) FetchBlockByOrder(order uint64) (*types.SerializedBlock, model.Block, error) {
	// First find the hash associated with the provided order in the index.
	ib := b.bd.GetBlockByOrder(uint(order))
	if ib == nil {
		return nil, nil, fmt.Errorf("No block:order=%d\n", order)
	}
	bn := b.GetBlockNode(ib)
	if bn == nil {
		return nil, nil, fmt.Errorf("No block node:hash=%s\n", ib.GetHash().String())
	}
	return bn.GetBody(), ib, nil
}

// BlockByHeight returns the block at the given height in the main chain.
//
// This function is safe for concurrent access.
func (b *BlockChain) BlockByOrder(blockOrder uint64) (*types.SerializedBlock, error) {
	block, _, err := b.FetchBlockByOrder(blockOrder)
	return block, err
}

// BlockHashByOrder returns the hash of the block at the given order in the
// main chain.
//
// This function is safe for concurrent access.
func (b *BlockChain) BlockHashByOrder(blockOrder uint64) (*common.Hash, error) {
	hash := b.bd.GetBlockHashByOrder(uint(blockOrder))
	if hash == nil {
		return nil, fmt.Errorf("Can't find block")
	}
	return hash, nil
}

// MainChainHasBlock returns whether or not the block with the given hash is in
// the main chain.
//
// This function is safe for concurrent access.
func (b *BlockChain) MainChainHasBlock(hash *common.Hash) bool {
	return b.bd.IsOnMainChain(b.bd.GetBlockId(hash))
}

func (b *BlockChain) GetMainOrder() uint {
	return b.BestSnapshot().GraphState.GetMainOrder()
}

// expect priority
func (b *BlockChain) GetMiningTips(expectPriority int) []*common.Hash {
	return b.Blockdag().GetValidTips(expectPriority)
}

func (b *BlockChain) HasTx(txid *common.Hash) bool {
	return b.indexManager.HasTx(txid)
}

const maxEVMNumberLookupSteps = 64

// GetBlockByNumber returns the first native main-chain block that records the
// transition to number. The chain read lock keeps the tip, order index, and
// recorded EVM metadata in one coherent view. This legacy API has no
// cancellation mechanism, including while acquiring that lock. Once acquired,
// the lookup is strictly bounded by maxEVMNumberLookupSteps plus constant
// validation reads.
func (b *BlockChain) GetBlockByNumber(number uint64) blockdag.IBlock {
	if number == 0 {
		return b.Blockdag().GetBlockByOrder(0)
	}
	b.ChainRLock()
	defer b.ChainRUnlock()
	return b.getBlockByNumberNoLock(number)
}

// GetBlockByNumberNoLock is the bounded EVM-number lookup for callers that
// already hold the native chain lock. It must not be used without ChainLock or
// ChainRLock because the order index and recorded EVM metadata must stay in one
// coherent view.
func (b *BlockChain) GetBlockByNumberNoLock(number uint64) blockdag.IBlock {
	return b.getBlockByNumberNoLock(number)
}

func (b *BlockChain) getBlockByNumberNoLock(number uint64) blockdag.IBlock {
	if number == 0 {
		return b.Blockdag().GetBlockByOrder(0)
	}
	mainTip := b.Blockdag().GetMainChainTip()
	if mainTip == nil || mainTip.GetState() == nil {
		return nil
	}
	tipOrder := uint64(mainTip.GetOrder())
	tipEVMNumber := mainTip.GetState().GetEVMNumber()
	if number > tipOrder || number > tipEVMNumber {
		return nil
	}

	order, ok := findEVMTransitionOrder(number, tipOrder, func(order uint64) (uint64, bool) {
		if order == tipOrder {
			return tipEVMNumber, true
		}
		ib := b.Blockdag().GetBlockByOrder(uint(order))
		if ib == nil || ib.GetState() == nil {
			return 0, false
		}
		return ib.GetState().GetEVMNumber(), true
	})
	if !ok {
		return nil
	}
	if order == tipOrder {
		return mainTip
	}
	candidate := b.Blockdag().GetBlockByOrder(uint(order))
	if candidate == nil || candidate.GetState() == nil ||
		candidate.GetState().GetEVMNumber() != number {
		return nil
	}
	return candidate
}

// findEVMTransitionOrder uses the native order index and the expected monotonic
// EVM metadata to locate the beginning of a number plateau. A candidate is
// valid only when its immediate predecessor records number-1, so discontinuous
// metadata cannot produce a false native anchor.
func findEVMTransitionOrder(number, tipOrder uint64, evmNumberAt func(uint64) (uint64, bool)) (uint64, bool) {
	if number == 0 {
		return 0, true
	}
	if number > tipOrder || evmNumberAt == nil {
		return 0, false
	}

	low, high := number, tipOrder
	for steps := 0; low < high; steps++ {
		if steps == maxEVMNumberLookupSteps {
			return 0, false
		}
		mid := low + (high-low)/2
		midNumber, ok := evmNumberAt(mid)
		if !ok {
			return 0, false
		}
		if midNumber < number {
			low = mid + 1
		} else {
			high = mid
		}
	}

	candidateNumber, ok := evmNumberAt(low)
	if !ok || candidateNumber != number {
		return 0, false
	}
	previousNumber, ok := evmNumberAt(low - 1)
	if !ok || previousNumber != number-1 {
		return 0, false
	}
	return low, true
}

// BlockByHash returns the block from the main chain with the given hash.
//
// This function is safe for concurrent access.
func (b *BlockChain) BlockByHash(hash *common.Hash) (*types.SerializedBlock, error) {
	b.ChainRLock()
	defer b.ChainRUnlock()

	return b.fetchMainChainBlockByHash(hash)
}

// fetchMainChainBlockByHash returns the block from the main chain with the
// given hash.  It first attempts to use cache and then falls back to loading it
// from the database.
//
// An error is returned if the block is either not found or not in the main
// chain.
//
// This function is safe for concurrent access.
func (b *BlockChain) fetchMainChainBlockByHash(hash *common.Hash) (*types.SerializedBlock, error) {
	if !b.MainChainHasBlock(hash) {
		return nil, fmt.Errorf("No block in main chain")
	}
	block, err := b.fetchBlockByHash(hash)
	return block, err
}

// HeaderByHash returns the block header identified by the given hash or an
// error if it doesn't exist.  Note that this will return headers from both the
// main chain and any side chains.
//
// This function is safe for concurrent access.
func (b *BlockChain) HeaderByHash(hash *common.Hash) (types.BlockHeader, error) {
	block, err := b.fetchBlockByHash(hash)
	if err != nil || block == nil {
		return types.BlockHeader{}, fmt.Errorf("block %s is not known", hash)
	}

	return block.Block().Header, nil
}

// FetchBlockByHash searches the internal chain block stores and the database
// in an attempt to find the requested block.
//
// This function differs from BlockByHash in that this one also returns blocks
// that are not part of the main chain (if they are known).
//
// This function is safe for concurrent access.
func (b *BlockChain) FetchBlockByHash(hash *common.Hash) (*types.SerializedBlock, error) {
	return b.fetchBlockByHash(hash)
}

func (b *BlockChain) FetchBlockBytesByHash(hash *common.Hash) ([]byte, error) {
	return b.fetchBlockBytesByHash(hash)
}

// fetchBlockByHash returns the block with the given hash from all known sources
// such as the internal caches and the database.
//
// This function is safe for concurrent access.
func (b *BlockChain) fetchBlockByHash(hash *common.Hash) (*types.SerializedBlock, error) {
	// Check orphan cache.
	block := b.GetOrphan(hash)
	if block != nil {
		return block, nil
	}
	// Load the block from the database.
	block, err := dbFetchBlockByHash(b.DB(), hash)
	if err == nil && block != nil {
		return block, nil
	}
	return nil, fmt.Errorf("unable to find block %v db", hash)
}

func (b *BlockChain) fetchBlockBytesByHash(hash *common.Hash) ([]byte, error) {
	// Check orphan cache.
	block := b.GetOrphan(hash)
	if block != nil {
		return block.Bytes()
	}
	return b.DB().GetBlockBytes(hash)
}

func (b *BlockChain) fetchHeaderByHash(hash *common.Hash) (*types.BlockHeader, error) {
	// Check orphan cache.
	block := b.GetOrphan(hash)
	if block != nil {
		return &block.Block().Header, nil
	}

	header, err := dbFetchHeaderByHash(b.DB(), hash)
	if err == nil && header != nil {
		return header, nil
	}
	return nil, fmt.Errorf("unable to find block header %v db %v", hash, err)
}

func (b *BlockChain) GetBlockHeader(block model.Block) *types.BlockHeader {
	ib, ok := block.(blockdag.IBlock)
	if ib == nil || !ok {
		return nil
	}
	if ib.GetData() != nil {
		bn, ok := ib.GetData().(*BlockNode)
		if !ok {
			log.Error("block data type error", "hash", ib.GetHash().String())
			return nil
		}
		return bn.GetHeader()
	}
	header, err := b.fetchHeaderByHash(ib.GetHash())
	if err != nil {
		log.Error(err.Error())
		return nil
	}
	return header
}

func (b *BlockChain) ForeachBlueBlocks(start model.Block, depth uint, powType pow.PowType, fn func(block model.Block, header *types.BlockHeader) error) error {
	return b.bd.Foreach(start.(blockdag.IBlock), depth, blockdag.Blue, func(block blockdag.IBlock) (bool, error) {
		blockHeader := b.GetBlockHeader(block)
		if blockHeader == nil {
			return false, fmt.Errorf("No blockHeader:%s", block.GetHash().String())
		}
		if blockHeader.Pow.GetPowType() != powType {
			return false, nil
		}
		return true, fn(block, blockHeader)
	})
}
