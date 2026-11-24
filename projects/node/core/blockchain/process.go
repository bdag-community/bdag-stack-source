// Copyright (c) 2013-2016 The btcsuite developers
// Copyright (c) 2015-2018 The Decred developers
// Use of this source code is governed by an ISC
// license that can be found in the LICENSE file.

package blockchain

import (
	"container/list"
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"

	blockdag "github.com/BlockdagNetworkLabs/bdag/blockdag"

	"github.com/BlockdagNetworkLabs/bdag/core/shutdown"
	"github.com/BlockdagNetworkLabs/bdag/core/state"

	"github.com/BlockdagNetworkLabs/bdag/core/types"
	"github.com/BlockdagNetworkLabs/bdag/core/types/pow"
	l "github.com/BlockdagNetworkLabs/bdag/log"
	gethtypes "github.com/ethereum/go-ethereum/core/types"
)

var (
	errBlockChainShutdown        = errors.New("block chain is shutdown")
	errBlockProcessorUnavailable = errors.New("block processor unavailable")
)

type blockApplyStartTracker interface {
	StartBlockApplyTrack(blockHash string, previousBestHash string, previous shutdown.DatasetState) error
}

func reorgDebugSourcePeer(source *peer.ID) string {
	if source == nil {
		return "none"
	}
	if source.String() == "" {
		return "none"
	}
	return source.String()
}

func reorgDebugBlockSummary(block blockdag.IBlock) string {
	if block == nil {
		return "<nil>"
	}
	return fmt.Sprintf("id=%d hash=%s order=%d height=%d layer=%d ordered=%t",
		block.GetID(),
		block.GetHash().String(),
		block.GetOrder(),
		block.GetHeight(),
		block.GetLayer(),
		block.IsOrdered())
}

func reorgDebugDetachSummary(orders *list.List) []string {
	if orders == nil || orders.Len() == 0 {
		return nil
	}
	summaries := make([]string, 0, orders.Len())
	for e := orders.Front(); e != nil; e = e.Next() {
		order, ok := e.Value.(*blockdag.BlockOrderHelp)
		if !ok || order == nil {
			summaries = append(summaries, "<invalid>")
			continue
		}
		summaries = append(summaries, fmt.Sprintf("oldOrder=%d %s",
			order.OldOrder,
			reorgDebugBlockSummary(order.Block)))
	}
	return summaries
}

func reorgDebugAttachSummary(orders *list.List) []string {
	if orders == nil || orders.Len() == 0 {
		return nil
	}
	summaries := make([]string, 0, orders.Len())
	for e := orders.Front(); e != nil; e = e.Next() {
		block, ok := e.Value.(blockdag.IBlock)
		if !ok {
			summaries = append(summaries, "<invalid>")
			continue
		}
		summaries = append(summaries, reorgDebugBlockSummary(block))
	}
	return summaries
}

// ProcessBlock is the main workhorse for handling insertion of new blocks into
// the block chain.  It includes functionality such as rejecting duplicate
// blocks, ensuring blocks follow all rules, orphan handling, and insertion into
// the block chain along with best chain selection and reorganization.
//
// When no errors occurred during processing, the first return value indicates
// the length of the fork the block extended.  In the case it either exteneded
// the best chain or is now the tip of the best chain due to causing a
// reorganize, the fork length will be 0.  The second return value indicates
// whether or not the block is an orphan, in which case the fork length will
// also be zero as expected, because it, by definition, does not connect ot the
// best chain.
//
// This function is safe for concurrent access.
// return IsOrphan,error
func (b *BlockChain) ProcessBlock(block *types.SerializedBlock, flags BehaviorFlags, source *peer.ID) (blockdag.IBlock, bool, error) {
	return b.ProcessBlockContext(context.Background(), block, flags, source)
}

// ProcessBlockContext queues a block for serialized consensus processing.
// Cancellation is honored while waiting to enter or leave the queue. Once the
// handler marks a message as applying, the atomic state mutation is allowed to
// finish so a canceled caller cannot leave chain or EVM state half-applied.
func (b *BlockChain) ProcessBlockContext(ctx context.Context, block *types.SerializedBlock, flags BehaviorFlags, source *peer.ID) (blockdag.IBlock, bool, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	if b == nil || b.msgChan == nil || b.quit == nil {
		return nil, false, errBlockProcessorUnavailable
	}
	if b.Service.IsShutdown() || (b.consensus != nil && b.IsShutdown()) {
		return nil, false, errBlockChainShutdown
	}
	if processQueueClosed(b.quit) {
		return nil, false, errBlockChainShutdown
	}
	if block == nil {
		return nil, false, errors.New("nil block")
	}
	bh := *block.Hash()
	reservation := &processReservation{}
	if _, loaded := b.processQueueMap.LoadOrStore(bh, reservation); loaded {
		return nil, false, fmt.Errorf("Already exists in the queue:%s", block.Hash().String())
	}
	msg := processMsg{ctx: ctx, block: block, flags: flags, result: make(chan *processResult, 1), source: source, queueKey: bh, reservation: reservation}
	select {
	case b.msgChan <- &msg:
	case <-ctx.Done():
		deleteProcessReservation(b, bh, reservation)
		return nil, false, ctx.Err()
	case <-b.quit:
		deleteProcessReservation(b, bh, reservation)
		return nil, false, errBlockChainShutdown
	}
	select {
	case result := <-msg.result:
		deleteProcessReservation(b, bh, reservation)
		if result == nil {
			return nil, false, errBlockProcessorUnavailable
		}
		return result.block, result.isOrphan, result.err
	case <-ctx.Done():
		releaseProcessReservationAfterCallerExit(b, bh, &msg)
		return nil, false, ctx.Err()
	case <-b.quit:
		releaseProcessReservationAfterCallerExit(b, bh, &msg)
		return nil, false, errBlockChainShutdown
	}
}

func processQueueClosed(quit <-chan struct{}) bool {
	select {
	case <-quit:
		return true
	default:
		return false
	}
}

func releaseProcessReservationAfterCallerExit(b *BlockChain, blockHash any, msg *processMsg) {
	if msg == nil || msg.phase.CompareAndSwap(processMessageQueued, processMessageAbandoned) {
		var reservation *processReservation
		if msg != nil {
			reservation = msg.reservation
		}
		deleteProcessReservation(b, blockHash, reservation)
		return
	}
	go func() {
		select {
		case <-msg.result:
		case <-b.quit:
		}
		deleteProcessReservation(b, blockHash, msg.reservation)
	}()
}

func deleteProcessReservation(b *BlockChain, blockHash any, reservation *processReservation) {
	if b == nil || reservation == nil {
		return
	}
	b.processQueueMap.CompareAndDelete(blockHash, reservation)
}

func (b *BlockChain) handler() {
	log.Trace("BlockChain handler")
out:
	for {
		select {
		case msg, ok := <-b.msgChan:
			if !ok {
				break out
			}
			if msg == nil {
				continue
			}
			// A select may choose a queued message even when quit is already
			// closed. Do not start new consensus work after shutdown; reply to
			// this message and drain the remaining queue instead.
			if processQueueClosed(b.quit) {
				deleteProcessReservation(b, msg.queueKey, msg.reservation)
				replyProcessMessage(msg, &processResult{err: errBlockChainShutdown})
				b.drainProcessQueue(errBlockChainShutdown)
				break out
			}
			if msg.phase.Load() == processMessageAbandoned {
				deleteProcessReservation(b, msg.queueKey, msg.reservation)
				replyProcessMessage(msg, &processResult{err: context.Canceled})
				continue
			}
			if msg.ctx != nil {
				select {
				case <-msg.ctx.Done():
					blockProcessCanceledBeforeApply.Inc(1)
					deleteProcessReservation(b, msg.queueKey, msg.reservation)
					replyProcessMessage(msg, &processResult{err: msg.ctx.Err()})
					continue
				default:
				}
			}
			if !msg.phase.CompareAndSwap(processMessageQueued, processMessageApplying) {
				blockProcessCanceledBeforeApply.Inc(1)
				deleteProcessReservation(b, msg.queueKey, msg.reservation)
				replyProcessMessage(msg, &processResult{err: context.Canceled})
				continue
			}
			start := time.Now()
			ib, isOrphan, err := b.processBlock(msg.block, msg.flags, msg.source)
			blockProcessTimer.Update(time.Since(start))
			if msg.ctx != nil {
				select {
				case <-msg.ctx.Done():
					blockProcessCompletedAfterCallerCancel.Inc(1)
				default:
				}
			}
			replyProcessMessage(msg, &processResult{isOrphan: isOrphan, err: err, block: ib})
		case <-b.quit:
			b.drainProcessQueue(errBlockChainShutdown)
			break out
		}
	}

cleanup:
	for {
		select {
		case msg, ok := <-b.msgChan:
			if !ok {
				break cleanup
			}
			if msg != nil {
				deleteProcessReservation(b, msg.queueKey, msg.reservation)
				replyProcessMessage(msg, &processResult{err: errBlockChainShutdown})
			}
		default:
			break cleanup
		}
	}

	b.wg.Done()
	log.Trace("BlockChain handler done")
}

func (b *BlockChain) drainProcessQueue(err error) {
	for {
		select {
		case msg, ok := <-b.msgChan:
			if !ok {
				return
			}
			if msg != nil {
				deleteProcessReservation(b, msg.queueKey, msg.reservation)
				replyProcessMessage(msg, &processResult{err: err})
			}
		default:
			return
		}
	}
}

func replyProcessMessage(msg *processMsg, result *processResult) {
	if msg == nil || msg.result == nil {
		return
	}
	select {
	case msg.result <- result:
	default:
	}
}

func (b *BlockChain) processBlock(block *types.SerializedBlock, flags BehaviorFlags, source *peer.ID) (blockdag.IBlock, bool, error) {
	isorphan, err := b.preProcessBlock(block, flags)
	if err != nil || isorphan {
		return nil, isorphan, err
	}
	// The block has passed all context independent checks and appears sane
	// enough to potentially accept it into the block chain.
	acceptStart := time.Now()
	ib, err := b.maybeAcceptBlock(block, flags, source)
	if err != nil {
		return nil, false, err
	}
	acceptDuration := time.Since(acceptStart)
	acceptedBlockValidation.Update(acceptDuration)
	if flags.Has(BFRPCAdd) {
		acceptedBlockRPCValidation.Update(acceptDuration)
	}
	if flags.Has(BFP2PAdd) || flags.Has(BFBroadcast) {
		acceptedBlockP2PValidation.Update(acceptDuration)
	}
	if flags.Has(BFRPCAdd) {
		b.selfAdd.Add(1)
	} else {
		b.selfAdd.Store(0)
	}
	// Accept any orphan blocks that depend on this block (they are no
	// longer orphans) and repeat for those accepted blocks until there are
	// no more.
	err = b.RefreshOrphans()
	if err != nil {
		return ib, false, err
	}
	if source != nil {
		log.Debug("Accepted block", "hash", block.Hash().String(), "age", time.Since(block.Block().Header.Timestamp), "source", source.ShortString())
	} else {
		log.Debug("Accepted block", "hash", block.Hash().String(), "age", time.Since(block.Block().Header.Timestamp))
	}

	return ib, false, nil
}

func (b *BlockChain) preProcessBlock(block *types.SerializedBlock, flags BehaviorFlags) (bool, error) {
	b.ChainRLock()
	defer b.ChainRUnlock()

	blockHash := block.Hash()
	log.Trace("Processing block ", "hash", blockHash)

	// The block must not already exist in the main chain or side chains.
	if b.bd.HasBlock(blockHash) {
		str := fmt.Sprintf("already have block %v", blockHash)
		return false, ruleError(ErrDuplicateBlock, str)
	}

	// The block must not already exist as an orphan.
	if b.IsOrphan(blockHash) {
		str := fmt.Sprintf("already have block (orphan) %v", blockHash)
		return true, ruleError(ErrDuplicateBlock, str)
	}
	// Perform preliminary sanity checks on the block and its transactions.
	err := b.checkBlockSanity(block, b.timeSource, flags, b.params)
	if err != nil {
		return false, err
	}

	// Find the previous checkpoint and perform some additional checks based
	// on the checkpoint.  This provides a few nice properties such as
	// preventing old side chain blocks before the last checkpoint,
	// rejecting easy to mine, but otherwise bogus, blocks that could be
	// used to eat memory, and ensuring expected (versus claimed) proof of
	// work requirements since the previous checkpoint are met.
	blockHeader := &block.Block().Header
	checkpoint, err := b.findPreviousCheckpoint()
	if err != nil {
		return false, err
	}
	checkpointNode := b.GetBlockHeader(checkpoint)
	if checkpointNode != nil {
		// Ensure the block timestamp is after the checkpoint timestamp.
		checkpointTime := time.Unix(checkpointNode.Timestamp.Unix(), 0)
		if blockHeader.Timestamp.Before(checkpointTime) {
			str := fmt.Sprintf("block %v has timestamp %v before "+
				"last checkpoint timestamp %v", blockHash,
				blockHeader.Timestamp, checkpointTime)
			return false, ruleError(ErrCheckpointTimeTooOld, str)
		}

		if !flags.Has(BFFastAdd) {
			// Even though the checks prior to now have already ensured the
			// proof of work exceeds the claimed amount, the claimed amount
			// is a field in the block header which could be forged.  This
			// check ensures the proof of work is at least the minimum
			// expected based on elapsed time since the last checkpoint and
			// maximum adjustment allowed by the retarget rules.
			duration := blockHeader.Timestamp.Sub(checkpointTime)
			requiredTarget := pow.CompactToBig(b.calcEasiestDifficulty(
				checkpointNode.Difficulty, duration, block.Block().Header.Pow))
			currentTarget := pow.CompactToBig(blockHeader.Difficulty)
			if !block.Block().Header.Pow.CompareDiff(currentTarget, requiredTarget) {
				str := fmt.Sprintf("block target difficulty of %064x "+
					"is too low when compared to the previous "+
					"checkpoint", currentTarget)
				return false, ruleError(ErrDifficultyTooLow, str)
			}
		}
	}

	// Handle orphan blocks.
	for _, pb := range block.Block().Parents {
		if !b.bd.HasBlock(pb) {
			log.Trace(fmt.Sprintf("Adding orphan block %s with parent %s", blockHash.String(), pb.String()))
			b.addOrphanBlock(block, flags)

			// The fork length of orphans is unknown since they, by definition, do
			// not connect to the best chain.
			return true, nil
		}
	}
	return false, nil
}

// maybeAcceptBlock potentially accepts a block into the block chain and, if
// accepted, returns the length of the fork the block extended.  It performs
// several validation checks which depend on its position within the block chain
// before adding it.  The block is expected to have already gone through
// ProcessBlock before calling this function with it.  In the case the block
// extends the best chain or is now the tip of the best chain due to causing a
// reorganize, the fork length will be 0.
//
// The flags are also passed to checkBlockContext and connectBestChain.  See
// their documentation for how the flags modify their behavior.
//
// This function MUST be called with the chain state lock held (for writes).
func (b *BlockChain) maybeAcceptBlock(block *types.SerializedBlock, flags BehaviorFlags, source *peer.ID) (blockdag.IBlock, error) {
	if onEnd := l.LogAndMeasureExecutionTime(log, "BlockChain.maybeAcceptBlock"); onEnd != nil {
		defer onEnd()
	}
	// This function should never be called with orphan blocks or the
	// genesis block.
	b.ChainLock()
	defer func() {
		b.flushNotifications()
	}()
	if b.upgradeRebuildUnavailable.Load() {
		b.ChainUnlock()
		return nil, fmt.Errorf("local upgrade state unavailable; reconciliation required")
	}
	if holds := b.localEVMRecovery.Load(); holds != nil && holds.fatal != nil {
		b.ChainUnlock()
		return nil, holds.fatal
	}

	newNode := NewBlockNode(block)
	if !flags.Has(BFFastAdd) {
		mainParent := b.bd.GetBlock(newNode.GetMainParent())
		if mainParent == nil {
			b.ChainUnlock()
			return nil, fmt.Errorf("Can't find main parent")
		}
		// The block must pass all of the validation rules which depend on the
		// position of the block within the block chain.
		err := b.checkBlockContext(block, mainParent, flags)
		if err != nil {
			b.ChainUnlock()
			return nil, err
		}
	}
	// Prune stake nodes which are no longer needed before creating a new
	// node.
	b.pruner.pruneChainIfNeeded()
	// Keep exact EVM anchor evidence protected through durable publication.
	// This lock is always inner to chainLock; notifications run after release.
	b.evmChainLock.Lock()
	evmLocked := true
	unlockEVM := func() {
		if evmLocked {
			b.evmChainLock.Unlock()
			evmLocked = false
		}
	}
	defer unlockEVM()
	boundary, boundaryErr := b.captureLocalReorgBoundary(b.bdagChain, block.Hash())
	if boundaryErr != nil {
		local := &LocalEVMStateUnavailableError{Candidate: *block.Hash(), Fatal: true, Cause: boundaryErr}
		b.holdLocalEVMRecovery(local)
		unlockEVM()
		b.ChainUnlock()
		return nil, local
	}

	//dag
	previousMainTipHash := ""
	previousDatasetState := shutdown.DatasetState{}
	// Retain the exact selected-parent object while ChainLock is held.  The
	// final-state upgrade marker shortcut needs this pre-AddBlock tip to prove
	// that a reordered suffix is strictly after every irreversible threshold.
	previousMainTip := b.bd.GetMainChainTip()
	if previousMainTip != nil {
		previousMainTipHash = previousMainTip.GetHash().String()
		previousDatasetState.NativeOrder = uint64(previousMainTip.GetOrder())
		previousDatasetState.NativeHash = previousMainTipHash
		if previousState := previousMainTip.GetState(); previousState != nil {
			previousDatasetState.EVMNumber = previousState.GetEVMNumber()
			previousDatasetState.EVMHash = previousState.GetEVMHash().String()
			previousDatasetState.EVMRoot = previousState.GetEVMRoot().String()
		}
	}
	newOrders, oldOrders, ib, isMainChainTipChange := b.bd.AddBlock(newNode)
	if ib == nil {
		unlockEVM()
		b.ChainUnlock()
		return nil, fmt.Errorf("Irreparable error![%s]", newNode.GetHash().String())
	}
	if err := b.preflightLocalEVMReorg(b.bdagChain, ib, newOrders, oldOrders, boundary); err != nil {
		unlockEVM()
		b.ChainUnlock()
		return nil, err
	}
	// Insert the block into the database if it's not already there.  Even
	// though it is possible the block will ultimately fail to connect, it
	// has already passed all proof-of-work and validity tests which means
	// it would be prohibitively expensive for an attacker to fill up the
	// disk with a bunch of blocks that fail to connect.  This is necessary
	// since it allows block download to be decoupled from the much more
	// expensive connection logic.  It also has some other nice properties
	// such as making blocks that never become part of the main chain or
	// blocks that fail to connect available for further analysis.
	//
	// Also, store the associated block index entry.
	if !b.DB().HasBlock(block.Hash()) {
		err := block.AssertImmutability()
		if err != nil {
			panic(err.Error())
		}
		err = dbMaybeStoreBlock(b.DB(), block)
		if err != nil {
			panic(err.Error())
		}
	}

	reorgOccurred := oldOrders.Len() > 0
	if reorgOccurred {
		selectedMainTipHash := ""
		if selectedMainTip := b.bd.GetMainChainTip(); selectedMainTip != nil {
			selectedMainTipHash = selectedMainTip.GetHash().String()
		}
		sourceStatus, sourceStatusKnown := b.peerReorgDebugStatus(source)
		log.Info("[DEBUG-reorg-source] DAG reorg candidate selected",
			"sourcePeer", reorgDebugSourcePeer(source),
			"sourceStatusKnown", sourceStatusKnown,
			"sourceProtocol", sourceStatus.ProtocolVersion,
			"sourceCommitmentStatus", sourceStatus.CommitmentStatus,
			"sourceForkStatus", sourceStatus.ForkStatus,
			"sourceForkConfidence", sourceStatus.ForkConfidence,
			"sourceForkReason", sourceStatus.ForkReason,
			"sourceAddress", sourceStatus.Address,
			"sourcePeerMainOrder", sourceStatus.PeerMainOrder,
			"sourceLocalMainOrder", sourceStatus.LocalMainOrder,
			"sourceEVMCheckpointNumber", sourceStatus.EVMCheckpointNumber,
			"sourceEVMCheckpointHash", sourceStatus.EVMCheckpointHash,
			"sourceEVMCheckpointRoot", sourceStatus.EVMCheckpointRoot,
			"flags", uint32(flags),
			"processedBlock", block.Hash().String(),
			"acceptedBlock", reorgDebugBlockSummary(ib),
			"previousMainTip", previousMainTipHash,
			"selectedMainTip", selectedMainTipHash,
			"oldLen", oldOrders.Len(),
			"newLen", newOrders.Len(),
			"oldOrders", reorgDebugDetachSummary(oldOrders),
			"newOrders", reorgDebugAttachSummary(newOrders))
	}

	err := startBlockApplyTrack(b.DB(), ib.GetHash().String(), previousMainTipHash, previousDatasetState)
	if err != nil {
		panic(err.Error())
	}
	connectedBlocks := list.New()
	// Connect the passed block to the chain while respecting proper chain
	// selection according to the chain with the most proof of work.  This
	// also handles validation of the transaction scripts.
	evmCheckpoint, _ := b.peerEVMCheckpoint(source)
	_, err = b.connectDagChain(ib, newNode, newOrders, oldOrders, connectedBlocks, evmCheckpoint)
	var governanceErr error
	var evmCheckpointErr error
	var evmStateGuardErr error
	if isGovernanceAdmissibilityRuleError(err) {
		governanceErr = err
	} else if isEVMCheckpointRuleError(err) {
		evmCheckpointErr = err
	} else if isEVMStateGuardRuleError(err) || isEVMStateGuardRecoveryError(err) {
		evmStateGuardErr = err
	} else if err != nil {
		if b.hasLocalEVMRecovery() {
			local := b.failLocalEVMApply(*ib.GetHash(), err)
			unlockEVM()
			b.ChainUnlock()
			return ib, local
		}
		panic(err.Error())
	}
	if evmStateGuardErr != nil {
		err = b.abortEVMStateGuardNativeCandidate(ib, evmStateGuardErr)
		if b.hasLocalEVMRecovery() || isEVMStateGuardRecoveryError(err) {
			// DAG rollback alone does not restore touched native indexes or
			// an EVM head rewound to a common ancestor. Retain the marker.
			local := b.failLocalEVMApply(*ib.GetHash(), err)
			unlockEVM()
			b.ChainUnlock()
			return ib, local
		}
		stopErr := b.DB().StopTrack()
		b.publishCommittedEVMHead()
		unlockEVM()
		b.ChainUnlock()
		if stopErr != nil {
			panic(stopErr.Error())
		}
		return ib, err
	}
	err = b.updateBestState(ib, block, newOrders)
	if err != nil {
		if b.hasLocalEVMRecovery() {
			local := b.failLocalEVMApply(*ib.GetHash(), err)
			unlockEVM()
			b.ChainUnlock()
			return ib, local
		}
		panic(err.Error())
	}
	b.evaluateUpgrades(uint64(b.bd.GetMainChainTip().GetLayer()))
	completed, completionErr := b.completeLocalGovernanceRejections(b.bdagChain, governanceErr, b.DB().StopTrack)
	if completionErr != nil {
		unlockEVM()
		b.ChainUnlock()
		return ib, completionErr
	}
	if !completed {
		err = b.DB().StopTrack()
		if err != nil {
			panic(err.Error())
		}
		b.publishCommittedEVMHead()
	}
	if governanceErr == nil && evmCheckpointErr == nil && evmStateGuardErr == nil {
		b.clearAppliedLocalEVMRecovery(*ib.GetHash(), b.bdagChain)
	}
	unlockEVM()
	tipHeight := uint64(b.bd.GetMainChainTip().GetHeight())
	markerHeight := b.upgradeRebuildTipHeight.Load()
	checkpointDue := tipHeight >= markerHeight && tipHeight-markerHeight >= 2048
	if (reorgOccurred || checkpointDue) && governanceErr == nil && evmCheckpointErr == nil && evmStateGuardErr == nil {
		reason := "forward"
		var prior blockdag.IBlock
		if reorgOccurred {
			reason, prior = "reorg", previousMainTip
		}
		if err := b.reconcileUpgradesHeld(reason, prior, false); err != nil {
			l.Error("upgrade reconciliation failed after committed block", "reason", reason, "err", err)
		}
	}
	b.ChainUnlock()
	if governanceErr != nil {
		return ib, governanceErr
	}
	if evmCheckpointErr != nil {
		return ib, evmCheckpointErr
	}
	if evmStateGuardErr != nil {
		return ib, evmStateGuardErr
	}
	if connectedBlocks.Len() > 0 {
		start := time.Now()
		for e := connectedBlocks.Front(); e != nil; e = e.Next() {
			b.sendNotification(BlockConnected, e.Value)
		}
		blockConnectedNotifications.Update(time.Since(start))
	}

	if flags.Has(BFP2PAdd) || flags.Has(BFBroadcast) {
		b.progressLogger.LogBlockOrder(ib.GetOrder(), block)
	}
	// Notify the caller that the new block was accepted into the block
	// chain.  The caller would typically want to react by relaying the
	// inventory to other peers.

	//receiver is in notifymgr.go => (handleNotifyMsg)
	b.sendNotification(BlockAccepted, &BlockAcceptedNotifyData{
		IsMainChainTipChange: isMainChainTipChange,
		Block:                block,
		Flags:                flags,
		Height:               uint64(ib.GetHeight()),
		Source:               source,
		AcceptedAt:           time.Now(),
	})
	b.signalEVMNativeCatchup()
	return ib, nil
}

func (b *BlockChain) abortEVMStateGuardNativeCandidate(ib blockdag.IBlock, guardErr error) error {
	if !isEVMStateGuardRuleError(guardErr) && !isEVMStateGuardRecoveryError(guardErr) {
		return guardErr
	}
	// Local recovery failures also require rollback, without classifying a peer
	// block as invalid. Keep the recovery marker and fail closed afterward.
	recoveryFailure := func(cause error) error {
		if !isEVMStateGuardRuleError(guardErr) {
			return evmStateGuardRecoveryError{recoveryErr: errors.Join(guardErr, cause)}
		}
		return b.evmStateGuardRecoveryError(ib, guardErr, cause)
	}
	if b == nil || b.bd == nil {
		return recoveryFailure(fmt.Errorf("native DAG rollback unavailable"))
	}
	if rollbackErr := b.bd.Rollback(); rollbackErr != nil {
		blockHash, order := evmStateGuardBlockID(ib)
		log.Error("EVM state guard failed to roll back native candidate",
			"block", blockHash,
			"order", order,
			"err", rollbackErr)
		return recoveryFailure(fmt.Errorf("native DAG rollback failed: %w", rollbackErr))
	}
	blockHash, order := evmStateGuardBlockID(ib)
	log.Warn("EVM state guard rolled back native candidate",
		"block", blockHash,
		"order", order,
		"err", guardErr)
	return guardErr
}

func startBlockApplyTrack(db interface {
	StartTrack(info string) error
}, blockHash string, previousBestHash string, previous shutdown.DatasetState) error {
	if tracker, ok := db.(blockApplyStartTracker); ok {
		return tracker.StartBlockApplyTrack(blockHash, previousBestHash, previous)
	}
	return db.StartTrack(blockHash)
}

func (b *BlockChain) FastAcceptBlock(block *types.SerializedBlock, flags BehaviorFlags) (blockdag.IBlock, error) {
	return b.maybeAcceptBlock(block, flags, nil)
}

// connectBestChain handles connecting the passed block to the chain while
// respecting proper chain selection according to the chain with the most
// proof of work.  In the typical case, the new block simply extends the main
// chain.  However, it may also be extending (or creating) a side chain (fork)
// which may or may not end up becoming the main chain depending on which fork
// cumulatively has the most proof of work.  It returns the resulting fork
// length, that is to say the number of blocks to the fork point from the main
// chain, which will be zero if the block ends up on the main chain (either
// due to extending the main chain or causing a reorganization to become the
// main chain).
//
// The flags modify the behavior of this function as follows:
//   - BFFastAdd: Avoids several expensive transaction validation operations.
//     This is useful when using checkpoints.
//
// This function MUST be called with the chain state lock held (for writes).
func (b *BlockChain) connectDagChain(ib blockdag.IBlock, block *BlockNode, newOrders *list.List, oldOrders *list.List, connectedBlocks *list.List, evmCheckpoint *EVMCheckpoint) (bool, error) {
	if oldOrders.Len() <= 0 {
		newOr := []blockdag.IBlock{}
		for e := newOrders.Front(); e != nil; e = e.Next() {
			nodeBlock := e.Value.(blockdag.IBlock)
			newOr = append(newOr, nodeBlock)
		}
		if len(newOr) <= 0 {
			newOr = append(newOr, ib)
		}
		var sb *BlockNode
		var err error
		var governanceErr error
		isEVMInit := false
		evmBatchStartState := b.firstOrderedParentState(newOr)
		for _, nodeBlock := range newOr {

			if nodeBlock.GetID() == ib.GetID() {
				sb = block
			} else {
				sb = b.GetBlockNode(nodeBlock)
				if sb == nil {
					return false, fmt.Errorf("No block data:%s", nodeBlock.GetHash().String())
				}
			}
			if !nodeBlock.IsOrdered() {
				er := b.updateDefaultBlockState(nodeBlock)
				if er != nil {
					log.Error(er.Error())
				}
				continue
			}
			if sb == nil {
				return false, fmt.Errorf("no block: %s, id: %d", nodeBlock.GetHash().String(), nodeBlock.GetID())
			}

			if !isEVMInit {
				isEVMInit = true
				err = b.prepareEVMEnvironment(nodeBlock)
				if err != nil {
					return false, err
				}
			}

			if err = b.checkConnectBlock(nodeBlock, sb); err != nil {
				if b.enforcingEVMStateGuard(nodeBlock) && !isGovernanceAdmissibilityRuleError(err) {
					guardErr := b.handleEVMStateGuardError(nodeBlock, fmt.Errorf("dry-connect EVM block: %w", err))
					guardErr = b.rejectEVMStateGuardCandidate(evmBatchStartState, nodeBlock, guardErr)
					log.Warn(guardErr.Error(), "block", nodeBlock.GetHash().String(), "order", nodeBlock.GetOrder())
					return false, guardErr
				} else {
					b.bd.InvalidBlock(nodeBlock)
					governanceErr = b.bindLocalGovernanceRejection(governanceErr, nodeBlock, err)
					log.Warn(err.Error(), "block", nodeBlock.GetHash().String(), "order", nodeBlock.GetOrder())
				}
			} else if err = b.checkEVMStateGuardCandidateWithBuiltBlock(nodeBlock, sb); err != nil {
				if isEVMStateGuardRuleError(err) {
					err = b.rejectEVMStateGuardCandidate(evmBatchStartState, nodeBlock, err)
					log.Warn(err.Error(), "block", nodeBlock.GetHash().String(), "order", nodeBlock.GetOrder())
					return false, err
				} else {
					b.bd.InvalidBlock(nodeBlock)
				}
				log.Warn(err.Error(), "block", nodeBlock.GetHash().String(), "order", nodeBlock.GetOrder())
			}
			if err = b.connectBlock(nodeBlock, sb, connectedBlocks, evmCheckpoint); err != nil {
				if isEVMStateGuardRuleError(err) {
					err = b.rejectEVMStateGuardCandidate(evmBatchStartState, nodeBlock, err)
					log.Warn(err.Error(), "block", nodeBlock.GetHash().String(), "order", nodeBlock.GetOrder())
					return false, err
				}
				b.bd.InvalidBlock(nodeBlock)
				if er := b.updateDefaultBlockState(nodeBlock); er != nil {
					log.Error(er.Error())
				}
				return false, err
			}
			if !nodeBlock.GetState().GetStatus().KnownInvalid() {
				b.bd.ValidBlock(nodeBlock)
			}
			er := b.updateBlockState(nodeBlock, sb.GetBody())
			if er != nil {
				log.Error(er.Error())
			}

			log.Debug("Block connected to the main chain", "hash", nodeBlock.GetHash(), "order", nodeBlock.GetOrder())
		}
		if governanceErr != nil {
			return true, governanceErr
		}
		return true, nil
	}

	// We're extending (or creating) a side chain and the cumulative work
	// for this new side chain is more than the old best chain, so this side
	// chain needs to become the main chain.  In order to accomplish that,
	// find the common ancestor of both sides of the fork, disconnect the
	// blocks that form the (now) old fork from the main chain, and attach
	// the blocks that form the new chain to the main chain starting at the
	// common ancenstor (the point where the chain forked).

	// Reorganize the chain.
	log.Info(fmt.Sprintf("Start DAG REORGANIZE: Block %v is causing a reorganize.", ib.GetHash()))
	err := b.reorganizeChain(ib, oldOrders, newOrders, block, connectedBlocks)
	if err != nil {
		return false, err
	}
	return true, nil
}

// connectBlock handles connecting the passed node/block to the end of the main
// (best) chain.

// This function MUST be called with the chain state lock held (for writes).
func (b *BlockChain) connectBlock(node blockdag.IBlock, blockNode *BlockNode, connectedBlocks *list.List, evmCheckpoint *EVMCheckpoint) error {
	block := blockNode.GetBody()
	if !node.GetState().GetStatus().KnownInvalid() {
		_, err := b.bdagConnectBlock(blockNode, evmCheckpoint)
		if err != nil {
			return err
		}

		// if upgrades.IsUpgradeActive(upgrades.UpgradeHeaderStatus.Name, uint64(block.Block().Header.Timestamp.Unix())) {
		// 	block.SetIsBlue(true)
		// }

		// Allow the index manager to call each of the currently active
		// optional indexes with the block being connected so they can
		// update themselves accordingly.
		err = b.indexManager.ConnectBlock(block, node)
		if err != nil {
			return fmt.Errorf("%v. (Attempt to execute --droptxindex)", err)
		}
	} else {
		// Atomically insert info into the database.
		err := b.indexManager.ConnectBlock(block, node)
		if err != nil {
			return err
		}
		return nil
	}
	connectedBlocks.PushBack([]any{block, b.bd.IsOnMainChain(node.GetID()), node})
	return nil
}

// disconnectBlock handles disconnecting the passed node/block from the end of
// the main (best) chain.
//
// This function MUST be called with the chain state lock held (for writes).
func (b *BlockChain) disconnectBlock(ib blockdag.IBlock, block *types.SerializedBlock) error {
	err := b.indexManager.DisconnectBlock(block, ib)
	if err != nil {
		return fmt.Errorf("%v. (Attempt to execute --droptxindex)", err)
	}

	b.sendNotification(BlockDisconnected, []any{block, ib})
	return nil
}

func (b *BlockChain) updateBestState(ib blockdag.IBlock, block *types.SerializedBlock, attachNodes *list.List) error {
	err := b.bd.Commit()
	if err != nil {
		return err
	}
	// Must be end node of sequence in dag
	// Generate a new best state snapshot that will be used to update the
	// database and later memory if all database updates are successful.
	lastState := b.BestSnapshot()

	// Calculate the number of transactions that would be added by adding
	// this block.
	numTxns := uint64(len(block.Block().Transactions))

	blockSize := uint64(block.Block().SerializeSize())

	mainTip := b.bd.GetMainChainTip()
	mainTipNode := b.GetBlockNode(mainTip)
	if mainTipNode == nil {
		return fmt.Errorf("No main tip node\n")
	}
	state := newBestState(mainTip.GetHash(), mainTipNode.Difficulty(), blockSize, numTxns, b.CalcPastMedianTime(mainTip), lastState.TotalTxns+numTxns,
		b.bd.GetMainChainTip().GetState().GetWeight(), b.bd.GetGraphState(), nil, *mainTip.GetState().Root())

	// Atomically insert info into the database.
	// Update best block state.
	err = dbPutBestState(b.DB(), state, pow.CalcWork(mainTipNode.Difficulty(), mainTipNode.Pow().GetPowType()))
	if err != nil {
		return err
	}
	err = b.indexManager.UpdateMainTip(mainTip.GetHash(), uint64(mainTip.GetOrder()))
	if err != nil {
		return err
	}
	// Update the state for the best block.  Notice how this replaces the
	// entire struct instead of updating the existing one.  This effectively
	// allows the old version to act as a snapshot which callers can use
	// freely without needing to hold a lock for the duration.  See the
	// comments on the state variable for more details.
	b.stateLock.Lock()
	b.stateSnapshot = state
	b.stateLock.Unlock()

	return nil
}

func (b *BlockChain) updateBlockState(ib blockdag.IBlock, block *types.SerializedBlock) error {
	return b.updateBlockStateWithEVMHeader(ib, block, b.bdagChain.GetCurHeader())
}

func (b *BlockChain) updateBlockStateWithEVMHeader(ib blockdag.IBlock, block *types.SerializedBlock, evmHeader *gethtypes.Header) error {
	if !ib.IsOrdered() {
		return b.updateDefaultBlockState(ib)
	}
	if ib.GetState() == nil ||
		ib.GetOrder() <= 0 {
		return fmt.Errorf("block state is nill:%d %s", ib.GetID(), ib.GetHash().String())
	}
	bs, ok := ib.GetState().(*state.BlockState)
	if !ok {
		return fmt.Errorf("block state is nill:%d %s", ib.GetID(), ib.GetHash().String())
	}
	b.UpdateWeight(ib)
	prev := b.bd.GetBlockByOrder(ib.GetOrder() - 1)
	if prev == nil {
		return fmt.Errorf("No prev block:%d %s", ib.GetID(), ib.GetHash().String())
	}
	bs.Update(block, prev.GetState().(*state.BlockState), evmHeader)
	b.Blockdag().AddToCommit(ib)
	return nil
}

func (b *BlockChain) updateDefaultBlockState(ib blockdag.IBlock) error {
	if ib.GetState() == nil {
		return fmt.Errorf("block state is nill:%d %s", ib.GetID(), ib.GetHash().String())
	}
	bs, ok := ib.GetState().(*state.BlockState)
	if !ok {
		return fmt.Errorf("block state is nill:%d %s", ib.GetID(), ib.GetHash().String())
	}
	mp := b.bd.GetBlockById(ib.GetMainParent())
	if mp == nil {
		return fmt.Errorf("No main parent:%d %s", ib.GetID(), ib.GetHash().String())
	}
	bs.SetDefault(mp.GetState().(*state.BlockState))
	b.Blockdag().AddToCommit(ib)
	return nil
}

func (b *BlockChain) prepareEVMEnvironment(block blockdag.IBlock) error {
	prev := b.bd.GetBlockByOrder(block.GetOrder() - 1)
	if prev == nil {
		return fmt.Errorf("No dag block:%s,id:%d\n", block.GetHash().String(), block.GetID())
	}
	_, err := b.bdagChain.PrepareEnvironment(prev.GetState())
	if err != nil {
		return err
	}
	return nil
}

func (b *BlockChain) UpdateWeight(ib blockdag.IBlock) {
	if ib.GetID() != blockdag.GenesisId {
		pb := ib.(*blockdag.PhantomBlock)
		tp := b.bd.GetBlockById(pb.GetMainParent())
		pb.GetState().SetWeight(tp.GetState().GetWeight())
		pb.GetState().SetWeight(pb.GetState().GetWeight() + uint64(b.CalcWeight(pb, b.bd.GetBlueInfo(pb))))
		if pb.GetBlueDiffAnticoneSize() > 0 {
			for k := range pb.GetBlueDiffAnticone().GetMap() {
				bdpb := b.bd.GetBlockById(k)
				pb.GetState().SetWeight(pb.GetState().GetWeight() + uint64(b.CalcWeight(bdpb, b.bd.GetBlueInfo(bdpb))))
			}
		}
		b.bd.AddToCommit(ib)
	}
}

func (b *BlockChain) GetSelfAdd() int64 {
	return b.selfAdd.Load()
}

type processMsg struct {
	ctx         context.Context
	block       *types.SerializedBlock
	flags       BehaviorFlags
	result      chan *processResult
	source      *peer.ID
	queueKey    any
	reservation *processReservation
	phase       atomic.Uint32
}

// Non-zero size is intentional: Go may assign identical addresses to distinct
// zero-sized allocations, which would defeat pointer identity in CompareAndDelete.
type processReservation struct{ token byte }

const (
	processMessageQueued uint32 = iota
	processMessageApplying
	processMessageAbandoned
)

type processResult struct {
	block    blockdag.IBlock
	isOrphan bool
	err      error
}
