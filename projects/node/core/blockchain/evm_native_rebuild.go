package blockchain

import (
	"bytes"
	"errors"
	"fmt"
	"time"

	blockdag "github.com/BlockdagNetworkLabs/bdag/blockdag"
	"github.com/BlockdagNetworkLabs/bdag/consensus/model"
	corestate "github.com/BlockdagNetworkLabs/bdag/core/state"
	qtypes "github.com/BlockdagNetworkLabs/bdag/core/types"
	"github.com/BlockdagNetworkLabs/bdag/core/types/pow"
	"github.com/ethereum/go-ethereum/common"
	gethtypes "github.com/ethereum/go-ethereum/core/types"
)

var (
	errEVMNativeRebuildBusy            = errors.New("EVM native rebuild is busy")
	errEVMNativeRebuildStaleAnchor     = errors.New("EVM native rebuild anchor is stale")
	errEVMNativeRebuildStateCommitment = errors.New("EVM native rebuild state commitment validation failed")
	errEVMNativeRebuildRecordedLineage = errors.New("EVM native rebuild recorded lineage validation failed")
	errEVMNativeRebuildForensicOnly    = errors.New("uncommitted EVM native rebuild is non-authoritative forensic mode only")
	errEVMNativeCatchupUncommitted     = errors.New("EVM native catch-up is blocked before the state commitment activation boundary")
)

type EVMNativeRebuildOptions struct {
	AnchorEVMNumber      uint64
	TargetOrder          uint64
	CommitEvery          uint64
	ProgressEvery        uint64
	RequireCurrentAnchor bool
	// False selects non-authoritative forensic replay and cannot be used with automatic catch-up controls.
	RequireStateCommitment bool
	SkipIfEVMBusy          bool
}

type EVMNativeRebuildResult struct {
	AnchorOrder          uint64
	StartOrder           uint64
	TargetOrder          uint64
	MainTipOrder         uint64
	Processed            uint64
	Replayed             uint64
	ReplayedStored       uint64
	SkippedInvalid       uint64
	SkippedNoAdvance     uint64
	Commits              uint64
	FinalEVMNumber       uint64
	FinalEVMHash         common.Hash
	FinalEVMRoot         common.Hash
	SafeCheckpointNumber uint64
	SafeCheckpointHash   common.Hash
	SafeCheckpointRoot   common.Hash
	SafeCheckpointKnown  bool
	RecordedLineageValid bool
	UncommittedForensic  bool
}

type evmNativeRecordedState struct {
	number uint64
	hash   common.Hash
	root   common.Hash
	status model.BlockStatus
	bytes  []byte
	state  corestate.BlockState
}

func snapshotEVMNativeRecordedState(state model.BlockState) (evmNativeRecordedState, error) {
	if state == nil {
		return evmNativeRecordedState{}, fmt.Errorf("recorded native EVM state is unavailable")
	}
	concrete, ok := state.(*corestate.BlockState)
	if !ok {
		return evmNativeRecordedState{}, fmt.Errorf("recorded native EVM state has unsupported type %T", state)
	}
	encoded, err := concrete.Bytes()
	if err != nil {
		return evmNativeRecordedState{}, fmt.Errorf("serialize recorded native EVM state: %w", err)
	}
	cloned, err := corestate.NewBlockStateFromBytes(encoded)
	if err != nil {
		return evmNativeRecordedState{}, fmt.Errorf("clone recorded native EVM state: %w", err)
	}
	return evmNativeRecordedState{
		number: state.GetEVMNumber(),
		hash:   state.GetEVMHash(),
		root:   state.GetEVMRoot(),
		status: state.GetStatus(),
		bytes:  encoded,
		state:  *cloned,
	}, nil
}

func restoreEVMNativeRecordedState(state model.BlockState, recorded evmNativeRecordedState) error {
	concrete, ok := state.(*corestate.BlockState)
	if !ok {
		return fmt.Errorf("recorded native EVM state has unsupported type %T", state)
	}
	*concrete = recorded.state
	return nil
}

func validateUpdatedEVMNativeRecordedState(order uint64, state model.BlockState, recorded evmNativeRecordedState, header *gethtypes.Header, requireExact bool) error {
	if state == nil {
		return fmt.Errorf("native state is unavailable after replay order %d", order)
	}
	if header == nil || header.Number == nil {
		return fmt.Errorf("rebuilt EVM header is unavailable after replay order %d", order)
	}
	if state.GetStatus() != recorded.status {
		return fmt.Errorf("native status changed after replay order %d: got=%d want original=%d", order, state.GetStatus(), recorded.status)
	}
	if state.GetEVMNumber() != header.Number.Uint64() ||
		state.GetEVMHash() != header.Hash() ||
		state.GetEVMRoot() != header.Root {
		return fmt.Errorf("native state mismatch after replay order %d: state status=%d number=%d hash=%s root=%s, header number=%d hash=%s root=%s",
			order,
			state.GetStatus(), state.GetEVMNumber(), state.GetEVMHash().String(), state.GetEVMRoot().String(),
			header.Number.Uint64(), header.Hash().String(), header.Root.String())
	}
	if !requireExact {
		return nil
	}
	concrete, ok := state.(*corestate.BlockState)
	if !ok {
		return fmt.Errorf("native state after replay order %d has unsupported type %T", order, state)
	}
	encoded, err := concrete.Bytes()
	if err != nil {
		return fmt.Errorf("serialize native state after replay order %d: %w", order, err)
	}
	if !bytes.Equal(encoded, recorded.bytes) {
		return fmt.Errorf("complete native state changed after authoritative replay order %d", order)
	}
	return nil
}

func validateEVMNativeRecordedLineage(order uint64, recorded evmNativeRecordedState, header *gethtypes.Header) error {
	if header == nil || header.Number == nil {
		return fmt.Errorf("%w at native order %d: rebuilt EVM header is unavailable; recorded status=%d number=%d hash=%s root=%s",
			errEVMNativeRebuildRecordedLineage,
			order,
			recorded.status,
			recorded.number,
			recorded.hash.String(),
			recorded.root.String())
	}
	if header.Number.Uint64() != recorded.number || header.Hash() != recorded.hash || header.Root != recorded.root {
		return fmt.Errorf("%w at native order %d: recorded status=%d number=%d hash=%s root=%s, rebuilt number=%d hash=%s root=%s",
			errEVMNativeRebuildRecordedLineage,
			order,
			recorded.status,
			recorded.number,
			recorded.hash.String(),
			recorded.root.String(),
			header.Number.Uint64(),
			header.Hash().String(),
			header.Root.String())
	}
	return nil
}

func validateEVMNativeRebuildMode(opts EVMNativeRebuildOptions) error {
	if opts.RequireStateCommitment {
		return nil
	}
	if opts.RequireCurrentAnchor || opts.SkipIfEVMBusy {
		return fmt.Errorf("%w: RequireStateCommitment=false cannot be combined with automatic catch-up controls (RequireCurrentAnchor=%t SkipIfEVMBusy=%t)",
			errEVMNativeRebuildForensicOnly,
			opts.RequireCurrentAnchor,
			opts.SkipIfEVMBusy)
	}
	return nil
}

func storedEVMReplayGap(currentNumber, recordedNumber uint64) (first, last uint64, ok bool, err error) {
	if recordedNumber <= currentNumber {
		return 0, 0, false, fmt.Errorf("recorded EVM number %d is not ahead of current head %d", recordedNumber, currentNumber)
	}
	next := currentNumber + 1
	if recordedNumber == next {
		return 0, 0, false, nil
	}
	return next, recordedNumber - 1, true, nil
}

func (b *BlockChain) RebuildEVMNativeState(opts EVMNativeRebuildOptions) (*EVMNativeRebuildResult, error) {
	if b == nil || b.bd == nil || b.bdagChain == nil {
		return nil, fmt.Errorf("blockchain is not initialized")
	}
	if err := validateEVMNativeRebuildMode(opts); err != nil {
		return nil, err
	}
	if !opts.RequireStateCommitment {
		log.Warn("UNCOMMITTED FORENSIC EVM native rebuild enabled; results are non-authoritative and barred from automatic canonical healing",
			"anchorEVMNumber", opts.AnchorEVMNumber,
			"targetOrder", opts.TargetOrder)
	}
	if opts.CommitEvery == 0 {
		opts.CommitEvery = 1_000
	}
	if opts.ProgressEvery == 0 {
		opts.ProgressEvery = 10_000
	}

	b.ChainLock()
	defer b.ChainUnlock()
	if opts.SkipIfEVMBusy {
		if !b.evmChainLock.TryLock() {
			return nil, errEVMNativeRebuildBusy
		}
	} else {
		b.evmChainLock.Lock()
	}
	defer b.evmChainLock.Unlock()

	mainTip := b.bd.GetMainChainTip()
	if mainTip == nil || mainTip.GetState() == nil {
		return nil, fmt.Errorf("native main tip is unavailable")
	}
	mainTipOrder := uint64(mainTip.GetOrder())
	targetOrder := opts.TargetOrder
	if targetOrder == 0 {
		targetOrder = mainTipOrder
	}
	if targetOrder > mainTipOrder {
		return nil, fmt.Errorf("target order %d is beyond native main tip order %d", targetOrder, mainTipOrder)
	}

	anchor := b.getBlockByNumberNoLock(opts.AnchorEVMNumber)
	if anchor == nil || anchor.GetState() == nil {
		return nil, fmt.Errorf("native anchor for EVM block %d is unavailable", opts.AnchorEVMNumber)
	}
	if anchor.GetState().GetEVMNumber() != opts.AnchorEVMNumber {
		return nil, fmt.Errorf("native anchor order %d records EVM number %d, want %d", anchor.GetOrder(), anchor.GetState().GetEVMNumber(), opts.AnchorEVMNumber)
	}
	anchorOrder := uint64(anchor.GetOrder())
	if anchorOrder > targetOrder {
		return nil, fmt.Errorf("anchor order %d is beyond target order %d", anchorOrder, targetOrder)
	}

	result := &EVMNativeRebuildResult{
		AnchorOrder:         anchorOrder,
		StartOrder:          anchorOrder + 1,
		TargetOrder:         targetOrder,
		MainTipOrder:        mainTipOrder,
		UncommittedForensic: !opts.RequireStateCommitment,
	}
	if opts.RequireCurrentAnchor {
		cur := b.bdagChain.GetCurHeader()
		if cur == nil || cur.Number == nil {
			return result, fmt.Errorf("EVM head is unavailable before rebuild")
		}
		curNumber := cur.Number.Uint64()
		if curNumber != opts.AnchorEVMNumber || cur.Hash() != anchor.GetState().GetEVMHash() {
			if curNumber > opts.AnchorEVMNumber {
				if advanced := b.getBlockByNumberNoLock(curNumber); advanced != nil && advanced.GetState() != nil &&
					advanced.GetState().GetEVMHash() == cur.Hash() &&
					advanced.GetState().GetEVMRoot() == cur.Root {
					result.FinalEVMNumber = curNumber
					result.FinalEVMHash = cur.Hash()
					result.FinalEVMRoot = cur.Root
					result.SafeCheckpointNumber, result.SafeCheckpointHash, result.SafeCheckpointRoot, result.SafeCheckpointKnown = b.SafeEVMCheckpoint()
					log.Info("EVM native rebuild skipped because head advanced past requested anchor",
						"anchor.number", opts.AnchorEVMNumber,
						"anchor.hash", anchor.GetState().GetEVMHash().String(),
						"current.number", curNumber,
						"current.hash", cur.Hash().String(),
						"current.order", advanced.GetOrder(),
						"targetOrder", targetOrder)
					return result, nil
				}
			}
			return result, fmt.Errorf("%w: current number=%d hash=%s, anchor number=%d hash=%s",
				errEVMNativeRebuildStaleAnchor,
				curNumber, cur.Hash().String(), opts.AnchorEVMNumber, anchor.GetState().GetEVMHash().String())
		}
	}
	if err := b.bdagChain.SetHeadToExistingEVMState(anchor.GetState()); err != nil {
		return nil, fmt.Errorf("set EVM native rebuild anchor order %d number %d without rewind: %w",
			anchorOrder, opts.AnchorEVMNumber, err)
	}
	cur := b.bdagChain.GetCurHeader()
	if cur == nil || cur.Number == nil {
		return nil, fmt.Errorf("EVM head is unavailable after anchoring")
	}
	if cur.Number.Uint64() != opts.AnchorEVMNumber ||
		cur.Hash() != anchor.GetState().GetEVMHash() ||
		cur.Root != anchor.GetState().GetEVMRoot() {
		return nil, fmt.Errorf("EVM anchor did not land on native anchor order %d: cur number=%d hash=%s root=%s, anchor number=%d hash=%s root=%s",
			anchorOrder,
			cur.Number.Uint64(), cur.Hash().String(), cur.Root.String(),
			opts.AnchorEVMNumber, anchor.GetState().GetEVMHash().String(), anchor.GetState().GetEVMRoot().String())
	}

	started := time.Now()
	lastProgress := started
	lastVerifiedState := anchor.GetState()
	for order := result.StartOrder; order <= targetOrder; order++ {
		select {
		case <-b.consensus.Interrupt():
			return result, fmt.Errorf("EVM native rebuild interrupted at order %d", order)
		default:
		}

		ib := b.bd.GetBlockByOrder(uint(order))
		if ib == nil {
			return result, fmt.Errorf("missing native block at order %d", order)
		}
		if !ib.IsOrdered() {
			if err := b.updateDefaultBlockState(ib); err != nil {
				return result, err
			}
			result.Processed++
			continue
		}
		blockNode := b.GetBlockNode(ib)
		if blockNode == nil || blockNode.GetBody() == nil {
			return result, fmt.Errorf("missing native block body at order %d hash=%s", order, ib.GetHash().String())
		}

		recState := ib.GetState()
		if recState == nil {
			return result, fmt.Errorf("missing native block state at order %d", order)
		}
		recorded, err := snapshotEVMNativeRecordedState(recState)
		if err != nil {
			return result, fmt.Errorf("snapshot native block state at order %d: %w", order, err)
		}
		prevEVMNum := opts.AnchorEVMNumber
		if order > 0 {
			prev := b.bd.GetBlockByOrder(uint(order - 1))
			if prev == nil || prev.GetState() == nil {
				return result, fmt.Errorf("missing native parent state at order %d", order-1)
			}
			prevEVMNum = prev.GetState().GetEVMNumber()
		}
		shouldReplay := !recorded.status.KnownInvalid() && recorded.number > prevEVMNum
		var rebuiltHeader *gethtypes.Header
		lineageValidated := false
		stateCommitmentValidated := false
		if recorded.status.KnownInvalid() {
			rebuiltHeader = b.bdagChain.GetCurHeader()
			result.SkippedInvalid++
		} else if shouldReplay {
			cur := b.bdagChain.GetCurHeader()
			if cur == nil || cur.Number == nil {
				return result, fmt.Errorf("EVM head is unavailable before replay at order %d", order)
			}
			first, last, hasGap, err := storedEVMReplayGap(cur.Number.Uint64(), recorded.number)
			if err != nil {
				return result, fmt.Errorf("EVM replay sequence at order %d (prevEVM=%d): %w", order, prevEVMNum, err)
			}
			if hasGap {
				imported, err := b.bdagChain.ImportStoredCanonicalEVMRangeForState(first, last, &recorded.state)
				if err != nil {
					cause := fmt.Errorf("fill stored EVM gap %d..%d before native order %d: %w", first, last, order, err)
					return result, b.rollbackEVMNativeRebuildToVerifiedState(lastVerifiedState, ib, cause, false)
				}
				result.ReplayedStored += imported
			}
			candidate, err := b.bdagChain.BuildNativeEVMBlockCandidate(blockNode.GetBody(), uint64(ib.GetLayer()))
			if err != nil {
				cause := fmt.Errorf("build native EVM candidate at order %d hash=%s: %w", order, ib.GetHash().String(), err)
				return result, b.rollbackEVMNativeRebuildToVerifiedState(lastVerifiedState, ib, cause, false)
			}
			if err := validateEVMNativeRecordedLineage(order, recorded, candidate.Header()); err != nil {
				return result, b.rollbackEVMNativeRebuildToVerifiedState(lastVerifiedState, ib, err, false)
			}
			lineageValidated = true
			if opts.RequireStateCommitment {
				mainParent := b.bd.GetBlockById(ib.GetMainParent())
				if err := b.validateEVMNativeRebuildStateCommitment(blockNode.GetBody(), mainParent, candidate.Header()); err != nil {
					return result, b.rollbackEVMNativeRebuildToVerifiedState(lastVerifiedState, ib, err, false)
				}
				stateCommitmentValidated = true
			}
			header, err := b.bdagChain.ImportRecordedNativeEVMBlock(candidate, &recorded.state)
			if err != nil {
				cause := fmt.Errorf("import recorded native EVM block at order %d hash=%s: %w", order, ib.GetHash().String(), err)
				return result, b.rollbackEVMNativeRebuildToVerifiedState(lastVerifiedState, ib, cause, false)
			}
			rebuiltHeader = header
			result.Replayed++
		} else {
			head := b.bdagChain.GetCurHeader()
			if head == nil || head.Number == nil {
				return result, fmt.Errorf("EVM head is unavailable at order %d", order)
			}
			if recorded.number != head.Number.Uint64() {
				return result, fmt.Errorf("native order %d records EVM %d but head is %d (prevEVM=%d)",
					order, recorded.number, head.Number.Uint64(), prevEVMNum)
			}
			rebuiltHeader = head
			result.SkippedNoAdvance++
		}

		if rebuiltHeader == nil || rebuiltHeader.Number == nil {
			cause := fmt.Errorf("rebuilt EVM header is unavailable at native order %d", order)
			return result, b.rollbackEVMNativeRebuildToVerifiedState(lastVerifiedState, ib, cause, false)
		}
		if !lineageValidated {
			if err := validateEVMNativeRecordedLineage(order, recorded, rebuiltHeader); err != nil {
				return result, b.rollbackEVMNativeRebuildToVerifiedState(lastVerifiedState, ib, err, false)
			}
		}
		if opts.RequireStateCommitment && !stateCommitmentValidated {
			if !recorded.status.KnownInvalid() {
				mainParent := b.bd.GetBlockById(ib.GetMainParent())
				if err := b.validateEVMNativeRebuildStateCommitment(blockNode.GetBody(), mainParent, rebuiltHeader); err != nil {
					return result, b.rollbackEVMNativeRebuildToVerifiedState(lastVerifiedState, ib, err, true)
				}
			}
		}

		if err := b.updateBlockStateWithEVMHeader(ib, blockNode.GetBody(), rebuiltHeader); err != nil {
			cause := fmt.Errorf("update native state order %d hash=%s: %w", order, ib.GetHash().String(), err)
			commitNative := true
			if restoreErr := restoreEVMNativeRecordedState(recState, recorded); restoreErr != nil {
				cause = errors.Join(cause, fmt.Errorf("restore original native state at order %d: %w", order, restoreErr))
				b.bd.RemoveFromCommit(ib)
				commitNative = false
			}
			return result, b.rollbackEVMNativeRebuildToVerifiedState(lastVerifiedState, ib, cause, commitNative)
		}
		updatedState := ib.GetState()
		updateValidationErr := validateUpdatedEVMNativeRecordedState(order, updatedState, recorded, rebuiltHeader, opts.RequireStateCommitment)
		if updateValidationErr != nil {
			commitNative := true
			if restoreErr := restoreEVMNativeRecordedState(recState, recorded); restoreErr != nil {
				updateValidationErr = errors.Join(updateValidationErr,
					fmt.Errorf("restore original native state at order %d: %w", order, restoreErr))
				b.bd.RemoveFromCommit(ib)
				commitNative = false
			}
			return result, b.rollbackEVMNativeRebuildToVerifiedState(lastVerifiedState, ib, updateValidationErr, commitNative)
		}
		lastVerifiedState = updatedState

		result.Processed++
		if result.Processed%opts.CommitEvery == 0 {
			if err := b.commitRecoveryState(); err != nil {
				return result, fmt.Errorf("commit at order %d: %w", order, err)
			}
			result.Commits++
		}
		if result.Processed == 1 || order == targetOrder || result.Processed%opts.ProgressEvery == 0 || time.Since(lastProgress) >= 30*time.Second {
			head := b.bdagChain.GetCurHeader()
			if head != nil && head.Number != nil {
				log.Info("EVM native rebuild progress",
					"order", order,
					"targetOrder", targetOrder,
					"processed", result.Processed,
					"replayed", result.Replayed,
					"replayedStored", result.ReplayedStored,
					"skippedInvalid", result.SkippedInvalid,
					"skippedNoAdvance", result.SkippedNoAdvance,
					"evmNumber", head.Number.Uint64(),
					"evmHash", head.Hash().String(),
					"elapsed", time.Since(started).Round(time.Second).String())
			}
			lastProgress = time.Now()
		}
	}

	if err := b.commitRecoveryState(); err != nil {
		return result, err
	}
	result.Commits++

	if targetOrder == mainTipOrder {
		if err := b.refreshBestStateForRecoveredTip(); err != nil {
			return result, err
		}
	} else {
		log.Warn("EVM native rebuild ended before native main tip; best chain state was not refreshed", "targetOrder", targetOrder, "mainTipOrder", mainTipOrder)
	}

	head := b.bdagChain.GetCurHeader()
	if head == nil || head.Number == nil {
		return result, fmt.Errorf("EVM head is unavailable after rebuild")
	}
	result.FinalEVMNumber = head.Number.Uint64()
	result.FinalEVMHash = head.Hash()
	result.FinalEVMRoot = head.Root
	result.SafeCheckpointNumber, result.SafeCheckpointHash, result.SafeCheckpointRoot, result.SafeCheckpointKnown = b.SafeEVMCheckpoint()
	result.RecordedLineageValid = true
	if result.UncommittedForensic {
		log.Warn("UNCOMMITTED FORENSIC EVM native rebuild matched recorded EVM lineage without an immutable native header commitment",
			"finalEVMNumber", result.FinalEVMNumber,
			"finalEVMHash", result.FinalEVMHash.String(),
			"targetOrder", targetOrder)
	}
	return result, nil
}

func (b *BlockChain) requiresEVMNativeStateCommitment(order uint64) bool {
	return b != nil && b.evmStateGuard != nil && b.evmStateGuard.commitmentActive(order)
}

func (b *BlockChain) validateEVMNativeRebuildStateCommitment(block *qtypes.SerializedBlock, mainParent blockdag.IBlock, header *gethtypes.Header) error {
	if block == nil || block.Block() == nil {
		return fmt.Errorf("%w: native block body is unavailable", errEVMNativeRebuildStateCommitment)
	}
	if mainParent == nil || mainParent.GetState() == nil {
		return fmt.Errorf("%w: native main parent is unavailable", errEVMNativeRebuildStateCommitment)
	}
	if header == nil || header.Number == nil {
		return fmt.Errorf("%w: rebuilt EVM header is unavailable", errEVMNativeRebuildStateCommitment)
	}

	expected, err := b.CalculateEVMStateCommitmentRoot(
		cloneBlockForEVMStateCommitment(block.Block()),
		mainParent,
		header,
	)
	if err != nil {
		return fmt.Errorf("%w: calculate expected root: %v", errEVMNativeRebuildStateCommitment, err)
	}
	if got := block.Block().Header.StateRoot; got != *expected {
		return fmt.Errorf("%w: native state root=%s expected=%s evm.number=%d evm.hash=%s evm.root=%s",
			errEVMNativeRebuildStateCommitment,
			got.String(),
			expected.String(),
			header.Number.Uint64(),
			header.Hash().String(),
			header.Root.String())
	}
	return nil
}

func (b *BlockChain) rollbackEVMNativeRebuildToVerifiedState(anchor model.BlockState, ib blockdag.IBlock, cause error, commitNative bool) error {
	if anchor == nil {
		return fmt.Errorf("%w: no verified EVM state is available for rollback", cause)
	}
	if err := b.bdagChain.SetHeadToExistingEVMState(anchor); err != nil {
		return fmt.Errorf("%w: restore last verified EVM state at native order %d EVM %d hash=%s: %v",
			cause,
			anchor.GetOrder(),
			anchor.GetEVMNumber(),
			anchor.GetEVMHash().String(),
			err)
	}
	var commitErr error
	if commitNative {
		commitErr = b.commitRecoveryState()
	} else {
		commitErr = b.bdagChain.CommitEVMHeadState()
	}
	if commitErr != nil {
		return fmt.Errorf("%w: restored last verified EVM state at native order %d but could not make it durable: %v",
			cause,
			anchor.GetOrder(),
			commitErr)
	}
	if ib == nil || ib.GetHash() == nil {
		return cause
	}
	return fmt.Errorf("%w at native order %d hash=%s", cause, ib.GetOrder(), ib.GetHash().String())
}

func (b *BlockChain) refreshBestStateForRecoveredTip() error {
	mainTip := b.bd.GetMainChainTip()
	if mainTip == nil || mainTip.GetState() == nil {
		return fmt.Errorf("native main tip is unavailable")
	}
	mainTipNode := b.GetBlockNode(mainTip)
	if mainTipNode == nil {
		return fmt.Errorf("native main tip node is unavailable")
	}
	block, err := dbFetchBlockByHash(b.DB(), mainTip.GetHash())
	if err != nil {
		return err
	}
	lastState := b.BestSnapshot()
	if lastState == nil {
		return fmt.Errorf("best state snapshot is unavailable")
	}
	numTxns := uint64(len(block.Block().Transactions))
	blockSize := uint64(block.Block().SerializeSize())
	state := newBestState(
		mainTip.GetHash(),
		mainTipNode.Difficulty(),
		blockSize,
		numTxns,
		b.CalcPastMedianTime(mainTip),
		lastState.TotalTxns,
		mainTip.GetState().GetWeight(),
		b.bd.GetGraphState(),
		lastState.TokenTipHash,
		*mainTip.GetState().Root(),
	)
	if err := dbPutBestState(b.DB(), state, pow.CalcWork(mainTipNode.Difficulty(), mainTipNode.Pow().GetPowType())); err != nil {
		return err
	}
	if err := b.indexManager.UpdateMainTip(mainTip.GetHash(), uint64(mainTip.GetOrder())); err != nil {
		return err
	}
	b.stateLock.Lock()
	b.stateSnapshot = state
	b.stateLock.Unlock()
	return nil
}
