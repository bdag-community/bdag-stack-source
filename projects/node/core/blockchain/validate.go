// Copyright (c) 2013-2016 The btcsuite developers
// Copyright (c) 2015-2018 The Decred developers
// Use of this source code is governed by an ISC
// license that can be found in the LICENSE file.

package blockchain

import (
	"fmt"
	"math/big"
	"time"

	blockdag "github.com/BlockdagNetworkLabs/bdag/blockdag"
	"github.com/BlockdagNetworkLabs/bdag/consensus/model"

	"github.com/BlockdagNetworkLabs/bdag/core/merkle"
	"github.com/BlockdagNetworkLabs/bdag/core/state"

	"github.com/BlockdagNetworkLabs/bdag/core/types"
	"github.com/BlockdagNetworkLabs/bdag/core/types/pow"
	"github.com/BlockdagNetworkLabs/bdag/params"
	"github.com/ethereum/go-ethereum/common"
	ethtypes "github.com/ethereum/go-ethereum/core/types"
)

func (b *BlockChain) CheckBlockSanity(block *types.SerializedBlock, timeSource model.MedianTimeSource, flags BehaviorFlags, chainParams *params.Params) error {
	return b.checkBlockSanity(block, timeSource, flags, chainParams)
}

// checkBlockSanity performs some preliminary checks on a block to ensure it is
// sane before continuing with block processing.  These checks are context
// free.
//
// The flags do not modify the behavior of this function directly, however they
// are needed to pass along to checkBlockHeaderSanity.
func (b *BlockChain) checkBlockSanity(block *types.SerializedBlock, timeSource model.MedianTimeSource, flags BehaviorFlags, chainParams *params.Params) error {
	msgBlock := block.Block()
	header := &msgBlock.Header

	err := checkBlockHeaderSanity(header, timeSource, flags, chainParams)
	if err != nil {
		return err
	}
	// A block must have at least one parent.
	numPb := len(msgBlock.Parents)
	if numPb == 0 {
		return ruleError(ErrNoParents, "block does not contain "+
			"any parent")
	}
	// A block must not have more parents than the max block payload or
	// else it is certainly over the weight limit.
	if numPb > types.MaxParentsPerBlock {
		str := fmt.Sprintf("block contains too many parents - "+
			"got %d, max %d", numPb, types.MaxParentsPerBlock)
		return ruleError(ErrBlockTooBig, str)
	}
	// Build the block parents merkle tree and ensure the calculated merkle
	// parents root matches the entry in the block header.
	// This also has the effect of caching all
	// of the parents hashes in the block to speed up future hash
	// checks.  The tree here and checks the merkle root
	// after the following checks, but there is no reason not to check the
	// merkle root matches here.
	paMerkles := merkle.BuildParentsMerkleTreeStore(msgBlock.Parents)
	paMerkleRoot := paMerkles[len(paMerkles)-1]
	if header.ParentRoot.Cmp(*paMerkleRoot) != 0 {
		str := fmt.Sprintf("block parents merkle root is invalid - block "+
			"header indicates %v, but calculated value is %v",
			&header.ParentRoot, paMerkleRoot)
		return ruleError(ErrBadParentsMerkleRoot, str)
	}

	// Repeated parents
	parentsSet := blockdag.NewHashSet()
	parentsSet.AddList(msgBlock.Parents)
	if len(msgBlock.Parents) != parentsSet.Size() {
		str := fmt.Sprintf("parents:%v", msgBlock.Parents)
		return ruleError(ErrDuplicateParent, str)
	}
	// A block must not exceed the maximum allowed block payload when
	// serialized.
	//
	// This is a quick and context-free sanity check of the maximum block
	// size according to the wire protocol.  Even though the wire protocol
	// already prevents blocks bigger than this limit, there are other
	// methods of receiving a block that might not have been checked
	// already.  A separate block size is enforced later that takes into
	// account the network-specific block size and the results of block
	// size votes.  Typically that block size is more restrictive than this
	// one.
	serializedSize := msgBlock.SerializeSize()
	if serializedSize > types.MaxBlockPayload {
		str := fmt.Sprintf("serialized block is too big - got %d, "+
			"max %d", serializedSize, types.MaxBlockPayload)
		return ruleError(ErrBlockTooBig, str)
	}
	// The first transaction in a block's regular tree must be a coinbase.
	transactions := block.Transactions()

	// Do some preliminary checks on each regular transaction to ensure they
	// are sane before continuing.
	for _, tx := range transactions {
		err := CheckTransactionSanity(tx, chainParams, b)
		if err != nil {
			return err
		}
	}

	// Build merkle tree and ensure the calculated merkle root matches the
	// entry in the block header.  This also has the effect of caching all
	// of the transaction hashes in the block to speed up future hash
	// checks.  Bitcoind builds the tree here and checks the merkle root
	// after the following checks, but there is no reason not to check the
	// merkle root matches here.
	merkles := merkle.BuildMerkleTreeStore(block.Transactions(), false)
	calculatedMerkleRoot := merkles[len(merkles)-1]
	if header.TxRoot.Cmp(*calculatedMerkleRoot) != 0 {
		str := fmt.Sprintf("block merkle root is invalid - block "+
			"header indicates %v, but calculated value is %v",
			header.TxRoot, calculatedMerkleRoot)
		return ruleError(ErrBadMerkleRoot, str)
	}

	// Check for duplicate transactions.  This check will be fairly quick
	// since the transaction hashes are already cached due to building the
	// merkle trees above.
	existingTxHashes := make(map[common.Hash]struct{})

	for _, tx := range transactions {
		h := tx.Hash()
		if _, exists := existingTxHashes[h]; exists {
			str := fmt.Sprintf("block contains duplicate "+
				"transaction %v", h)
			return ruleError(ErrDuplicateTx, str)
		}
		existingTxHashes[h] = struct{}{}
	}

	return nil
}

// checkBlockHeaderSanity performs some preliminary checks on a block header to
// ensure it is sane before continuing with processing.  These checks are
// context free.
//
// The flags do not modify the behavior of this function directly, however they
// are needed to pass along to checkProofOfWork.
func checkBlockHeaderSanity(header *types.BlockHeader, timeSource model.MedianTimeSource, flags BehaviorFlags, chainParams *params.Params) error {
	instance := pow.GetInstance(header.Pow.GetPowType(), 0, 0, []byte{})
	instance.SetMainHeight(pow.MainHeight(header.BlockHeight))
	instance.SetParams(chainParams.PowConfig)
	if !instance.CheckAvailable() {
		str := fmt.Sprintf("pow type : %d is not available!", header.Pow.GetPowType())
		return ruleError(ErrInValidPowType, str)
	}
	// Ensure the proof of work bits in the block header is in min/max
	// range and the block hash is less than the target value described by
	// the bits.
	err := checkProofOfWork(header, chainParams.PowConfig, flags)
	if err != nil {
		return ruleError(ErrInvalidPow, err.Error())
	}

	// A block timestamp must not have a greater precision than one second.
	// This check is necessary because Go time.Time values support
	// nanosecond precision whereas the consensus rules only apply to
	// seconds and it's much nicer to deal with standard Go time values
	// instead of converting to seconds everywhere.
	if !header.Timestamp.Equal(time.Unix(header.Timestamp.Unix(), 0)) {
		str := fmt.Sprintf("block timestamp of %v has a higher "+
			"precision than one second", header.Timestamp)
		return ruleError(ErrInvalidTime, str)
	}

	// Ensure the block time is not too far in the future.
	maxTimestamp := timeSource.AdjustedTime().Add(time.Second *
		MaxTimeOffsetSeconds)
	if header.Timestamp.After(maxTimestamp) {
		str := fmt.Sprintf("block timestamp of %v is too far in the "+
			"future", header.Timestamp)
		return ruleError(ErrTimeTooNew, str)
	}

	return nil
}

// checkProofOfWork ensures the block header bits which indicate the target
// difficulty is in min/max range and that the block hash is less than the
// target difficulty as claimed.
//
// The flags modify the behavior of this function as follows:
//   - BFNoPoWCheck: The check to ensure the block hash is less than the target
//     difficulty is not performed.
func checkProofOfWork(header *types.BlockHeader, powConfig *pow.PowConfig, flags BehaviorFlags) error {
	// log.Info(fmt.Sprintf("header : %+v and pow %+v",header,header.Pow))
	// The block hash must be less than the claimed target unless the flag
	// to avoid proof of work checks is set.
	if !flags.Has(BFNoPoWCheck) && !params.ActiveNetParams.Params.IsDevelopDiff() {
		header.Pow.SetParams(powConfig)
		header.Pow.SetMainHeight(pow.MainHeight(header.BlockHeight))
		// The block hash must be less than the claimed target.
		// log.Info(fmt.Sprintf("header data ===> %+v\n",header.BlockData()))
		return header.Pow.Verify(header.BlockData(), header.BlockHash(), header.Difficulty)
	}
	return nil
}

// CheckTransactionSanity performs some preliminary checks on a transaction to
// ensure it is sane.  These checks are context free.
func CheckTransactionSanity(evmTx *ethtypes.Transaction, params *params.Params, bc *BlockChain) error {
	if evmTx.Gas() == 0 {
		return fmt.Errorf("gas limit cannot be zero")
	}

	if evmTx.GasPrice().Cmp(big.NewInt(0)) <= 0 {
		return fmt.Errorf("gas price must be greater than zero")
	}

	return bc.bdagChain.CheckSanity(evmTx)
}

// checkBlockContext peforms several validation checks on the block which depend
// on its position within the block chain.
//
// The flags modify the behavior of this function as follows:
//   - BFFastAdd: The transactions are not checked to see if they are finalized
//     and the somewhat expensive duplication transaction check is not performed.
//
// The flags are also passed to checkBlockHeaderContext.  See its documentation
// for how the flags modify its behavior.
func (b *BlockChain) checkBlockContext(block *types.SerializedBlock, mainParent blockdag.IBlock, flags BehaviorFlags) error {
	// The genesis block is valid by definition.
	if mainParent == nil {
		return nil
	}
	if mainParent.GetHash().Cmp(*block.Block().Parents[0]) != 0 {
		return fmt.Errorf("Main parent (%s) is inconsistent in block (%s)\n", mainParent.GetHash().String(), block.Block().Parents[0].String())
	}
	// prevBlock := mainParent

	// Perform all block header related validation checks.
	err := b.checkBlockHeaderContext(block, mainParent, flags)
	if err != nil {
		return err
	}
	// header := &block.Block().Header
	if !flags.Has(BFFastAdd) {
		// A block must not exceed the maximum allowed size as defined
		// by the network parameters and the current status of any hard
		// fork votes to change it when serialized.
		maxBlockSize, err := b.maxBlockSize()
		if err != nil {
			return err
		}
		//TODO, revisit block size in header
		/*
			serializedSize := int64(block.Block().Header.Size)
		*/
		blockBytes, _ := block.Bytes()
		serializedSize := int64(len(blockBytes))
		if serializedSize > maxBlockSize {
			str := fmt.Sprintf("serialized block is too big - "+
				"got %d, max %d", serializedSize,
				maxBlockSize)
			return ruleError(ErrBlockTooBig, str)
		}

		err = b.checkBlockSubsidy(block)
		if err != nil {
			return err
		}

	}
	return nil
}

func (b *BlockChain) checkBlockSubsidy(block *types.SerializedBlock) error {
	bi := b.bd.GetBlueInfoByHash(block.Block().Parents[0])
	subsidy := uint64(b.subsidyCache.CalcBlockSubsidy(bi, block.Block().Header.Timestamp.Unix()))
	p := params.ActiveNetParams.Params
	totalProportions := float64(p.TotalSubsidyProportions())
	workPro := float64(p.WorkRewardProportion)
	work := uint64(workPro / totalProportions * float64(subsidy))

	reward := block.Block().Header.Reward

	if work != reward {
		str := fmt.Sprintf("block reward is %v which is not the work reward %v",
			reward, subsidy)
		return ruleError(ErrBadRewardValue, str)
	}
	return nil
}

// checkBlockHeaderContext peforms several validation checks on the block
// header which depend on its position within the block chain.
//
// The flags modify the behavior of this function as follows:
//   - BFFastAdd: All checks except those involving comparing the header against
//     the checkpoints are not performed.
//
// This function MUST be called with the chain state lock held (for writes).
func (b *BlockChain) checkBlockHeaderContext(block *types.SerializedBlock, prevNode blockdag.IBlock, flags BehaviorFlags) error {
	// The genesis block is valid by definition.
	if prevNode == nil {
		return nil
	}

	header := &block.Block().Header
	parents := blockdag.NewIdSet()
	for _, v := range block.Block().Parents {
		parents.Add(b.bd.GetBlockId(v))
	}
	blockLayer, ok := b.Blockdag().GetParentsMaxLayer(parents)
	if !ok {
		str := fmt.Sprintf("bad parents:%v", block.Block().Parents)
		return ruleError(ErrMissingParent, str)
	}
	blockLayer += 1

	if !flags.Has(BFFastAdd) {
		if !b.params.IsDevelopDiff() {
			instance := pow.GetInstance(header.Pow.GetPowType(), 0, 0, []byte{})
			instance.SetMainHeight(pow.MainHeight(prevNode.GetHeight() + 1))
			instance.SetParams(b.params.PowConfig)
			// Ensure the difficulty specified in the block header matches
			// the calculated difficulty based on the previous block and
			// difficulty retarget rules.

			expDiff, err := b.calcNextRequiredDifficulty(prevNode,
				header.Timestamp, instance)
			if err != nil {
				return err
			}
			blockDifficulty := header.Difficulty
			if blockDifficulty != expDiff {
				str := fmt.Sprintf("block difficulty of %d is not the"+
					" expected value of %d", blockDifficulty,
					expDiff)
				return ruleError(ErrUnexpectedDifficulty, str)
			}
		}
		// Ensure the timestamp for the block header is after the
		// median time of the last several blocks (medianTimeBlocks).
		medianTime := b.CalcPastMedianTime(prevNode)
		if !header.Timestamp.After(medianTime) {
			str := "block timestamp of %v is not after expected %v"
			str = fmt.Sprintf(str, header.Timestamp.Unix(), medianTime.Unix())
			return ruleError(ErrTimeTooOld, str)
		}
	}

	// checkpoint
	if !b.HasCheckpoints() {
		return nil
	}
	blockHash := header.BlockHash()
	if !b.verifyCheckpoint(uint64(blockLayer), &blockHash) {
		str := fmt.Sprintf("block at layer %d does not match "+
			"checkpoint hash", blockLayer)
		return ruleError(ErrBadCheckpoint, str)
	}

	checkpointNode, err := b.findPreviousCheckpoint()
	if err != nil {
		return err
	}
	if checkpointNode != nil && blockLayer < checkpointNode.GetLayer() {
		str := fmt.Sprintf("block at layer %d forks the main chain "+
			"before the previous checkpoint at layer %d",
			blockLayer, checkpointNode.GetLayer())
		return ruleError(ErrForkTooOld, str)
	}

	return nil
}

// This function MUST be called with the chain state lock held (for writes).
func (b *BlockChain) checkConnectBlock(ib blockdag.IBlock, blockNode *BlockNode) error {
	mainParent := b.bd.GetBlockById(ib.GetMainParent())
	if mainParent == nil {
		return fmt.Errorf("block main parent error: %s", ib.GetHash().String())
	}
	return b.bdagCheckConnectBlock(blockNode, uint64(ib.GetLayer()))
}

// CheckConnectBlockTemplate fully validates that connecting the passed block to
// either the tip of the main chain or its parent does not violate any consensus
// rules, aside from the proof of work requirement.  The block must connect to
// the current tip of the main chain or its parent.
//
// This function is safe for concurrent access.
func (b *BlockChain) CheckConnectBlockTemplate(block *types.SerializedBlock, height uint64) error {
	// Skip the proof of work check as this is just a block template.
	flags := BFNoPoWCheck

	// The block template must build off the current tip of the main chain
	// or its parent.
	// Perform context-free sanity checks on the block and its transactions.
	err := b.checkBlockSanity(block, b.timeSource, flags, b.params)
	if err != nil {
		return err
	}

	newNode := NewBlockNode(block)
	virBlock := b.bd.CreateVirtualBlock(newNode)
	if virBlock == nil {
		return ruleError(ErrPrevBlockNotBest, "tipsNode")
	}
	if uint64(virBlock.GetHeight()) != height {
		return ruleError(ErrPrevBlockNotBest, "tipsNode height")
	}
	mainParent := b.bd.GetBlockById(virBlock.GetMainParent())
	if mainParent == nil {
		return ruleError(ErrPrevBlockNotBest, "main parent")
	}
	virBlock.SetOrder(mainParent.GetOrder() + 1)
	virBlock.GetState().(*state.BlockState).SetDefault(mainParent.GetState().(*state.BlockState))
	err = b.checkBlockContext(block, mainParent, flags)
	if err != nil {
		return err
	}

	b.evmChainLock.Lock()
	defer b.evmChainLock.Unlock()
	err = b.checkConnectBlock(virBlock, newNode)
	if err != nil {
		return err
	}
	if err = b.checkEVMStateGuardCandidate(virBlock, newNode, mainParent.GetState()); err != nil {
		return err
	}
	return nil
}

func (b *BlockChain) CheckStateRoot(block *types.Block) error {
	// Build merkle tree and ensure the calculated merkle root matches the
	// entry in the block header.  This also has the effect of caching all
	// of the hashes in the block to speed up future hash
	// checks.
	calculatedStateRoot := b.CalculateStateRoot(block)
	if block.Header.StateRoot.Cmp(*calculatedStateRoot) != 0 {
		str := fmt.Sprintf("block merkle state root is invalid - block "+
			"header indicates %s, but calculated value is %s",
			block.Header.StateRoot, calculatedStateRoot)
		return ruleError(ErrBadMerkleRoot, str)
	}
	return nil
}
