package blockchain

import (
	"bytes"
	"container/list"
	"errors"
	"fmt"

	blockdag "github.com/BlockdagNetworkLabs/bdag/blockdag"
	"github.com/BlockdagNetworkLabs/bdag/consensus/model"
	"github.com/BlockdagNetworkLabs/bdag/core/state"
	"github.com/BlockdagNetworkLabs/bdag/database/rawdb"
	ethcommon "github.com/ethereum/go-ethereum/common"
	gethtypes "github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/ethdb"
)

// LocalEVMStateUnavailableError reports missing LOCAL execution state, not a
// consensus violation. A valid peer/candidate must remain eligible for retry.
type LocalEVMStateUnavailableError struct {
	Candidate    ethcommon.Hash
	AnchorNumber uint64
	AnchorHash   ethcommon.Hash
	AnchorRoot   ethcommon.Hash
	Fatal        bool
	Cause        error
	anchor       *state.BlockState // Immutable original missing-state identity.
}

func (e *LocalEVMStateUnavailableError) Error() string {
	return fmt.Sprintf("local EVM state recovery required: candidate=%s anchor.number=%d anchor.hash=%s anchor.root=%s fatal=%t: %v",
		e.Candidate, e.AnchorNumber, e.AnchorHash, e.AnchorRoot, e.Fatal, e.Cause)
}

// Deliberately do not unwrap this into a RuleError supplied by another layer.
func IsLocalEVMStateUnavailable(err error) bool {
	var local *LocalEVMStateUnavailableError
	return errors.As(err, &local)
}

type localEVMRecoveryHolds struct {
	pending map[ethcommon.Hash]*LocalEVMStateUnavailableError
	fatal   *LocalEVMStateUnavailableError
}

// LocalEVMRecoveryStatus is independent of chain validity and currentness.
// Readers never take chainLock and cannot clear this mining-only hold.
func (b *BlockChain) LocalEVMRecoveryStatus() (held bool, reason string) {
	if b == nil {
		return false, ""
	}
	holds := b.localEVMRecovery.Load()
	if holds == nil {
		return false, ""
	}
	if holds.fatal != nil {
		return true, holds.fatal.Error()
	}
	if len(holds.pending) != 0 {
		return true, fmt.Sprintf("local EVM state unavailable for %d deferred candidate(s); restore the exact anchor state on a verified copy and retry", len(holds.pending))
	}
	return false, ""
}

// All writers hold chainLock. Each publication is immutable for RPC readers.
func (b *BlockChain) holdLocalEVMRecovery(err *LocalEVMStateUnavailableError) {
	next := &localEVMRecoveryHolds{pending: make(map[ethcommon.Hash]*LocalEVMStateUnavailableError)}
	if previous := b.localEVMRecovery.Load(); previous != nil {
		for key, value := range previous.pending {
			next.pending[key] = value
		}
		next.fatal = previous.fatal
	}
	next.pending[err.Candidate] = err
	if err.Fatal {
		next.fatal = err
	}
	b.localEVMRecovery.Store(next)
}

func (b *BlockChain) clearAppliedLocalEVMRecovery(candidate ethcommon.Hash, reader localEVMStateReader) {
	previous := b.localEVMRecovery.Load()
	if previous == nil || previous.fatal != nil || previous.pending[candidate] == nil {
		return
	}
	committed := b.committedEVMHead.Load()
	actual, err := readLocalEVMTuple(reader)
	if err != nil || committed == nil || !committed.valid || actual != (localEVMTuple{committed.number, committed.hash, committed.root}) {
		return
	}
	block := b.bd.GetBlock(&candidate)
	if block == nil || block.GetState() == nil || block.GetState().GetStatus().KnownInvalid() {
		return
	}
	b.removeLocalEVMRecovery(candidate)
}

func (b *BlockChain) removeLocalEVMRecovery(candidate ethcommon.Hash) {
	previous := b.localEVMRecovery.Load()
	if previous == nil || previous.fatal != nil {
		return
	}
	next := &localEVMRecoveryHolds{pending: make(map[ethcommon.Hash]*LocalEVMStateUnavailableError)}
	for key, value := range previous.pending {
		if key != candidate {
			next.pending[key] = value
		}
	}
	b.localEVMRecovery.Store(next)
}

// This provenance is created only where checkConnectBlock rejects the actual
// nodeBlock. A batch-level error must never be attributed to its triggering ib.
type localEVMGovernanceRejections struct {
	cause      error
	candidates []ethcommon.Hash
}

func (e *localEVMGovernanceRejections) Error() string { return e.cause.Error() }
func (e *localEVMGovernanceRejections) Unwrap() error { return e.cause }

func (b *BlockChain) bindLocalGovernanceRejection(batch error, block blockdag.IBlock, cause error) error {
	if !isGovernanceAdmissibilityRuleError(cause) || isEVMStateGuardRecoveryError(cause) || IsLocalEVMStateUnavailable(cause) {
		return batch
	}
	if batch == nil {
		batch = cause
	}
	holds := b.localEVMRecovery.Load()
	if block == nil || holds == nil || holds.fatal != nil || holds.pending[*block.GetHash()] == nil {
		return batch
	}
	var bound *localEVMGovernanceRejections
	if !errors.As(batch, &bound) {
		bound = &localEVMGovernanceRejections{cause: batch}
	}
	for _, candidate := range bound.candidates {
		if candidate == *block.GetHash() {
			return bound
		}
	}
	bound.candidates = append(bound.candidates, *block.GetHash())
	return bound
}

func (b *BlockChain) hasLocalEVMRecovery() bool {
	holds := b.localEVMRecovery.Load()
	return holds != nil && (holds.fatal != nil || len(holds.pending) != 0)
}

func (b *BlockChain) failLocalEVMApply(candidate ethcommon.Hash, cause error) error {
	local := &LocalEVMStateUnavailableError{Candidate: candidate}
	if holds := b.localEVMRecovery.Load(); holds != nil && holds.pending[candidate] != nil {
		*local = *holds.pending[candidate]
	}
	local.Fatal = true
	local.Cause = fmt.Errorf("application recovery is unproven; retain the operation marker and recover on a verified copy: %w", cause)
	b.holdLocalEVMRecovery(local)
	return local
}

// This is NOT an abort/rollback verifier. It is used only after the unchanged
// successful native apply/index/Commit/UpdateMainTip path. It independently
// checks that the resulting committed database view agrees with native memory
// and the exact accessible EVM state before completing the operation marker.
func (b *BlockChain) verifyLocalGovernanceCommit(reader localEVMStateReader, candidates []ethcommon.Hash) error {
	tip := b.bd.GetMainChainTip()
	if tip == nil || tip.GetState() == nil || tip.GetState().Root() == nil {
		return fmt.Errorf("committed native tip is unavailable")
	}
	durable, ok := b.DB().(interface{ DB() ethdb.Database })
	if !ok || durable.DB() == nil {
		return fmt.Errorf("raw durable native database proof is unavailable")
	}
	rawTips := rawdb.ReadDAGTips(durable.DB())
	if len(rawTips) == 0 || rawTips[0] != uint64(tip.GetID()) {
		return fmt.Errorf("raw durable native tip differs from the accepted database view")
	}
	tips, err := b.DB().GetDAGTips()
	if err != nil || len(tips) == 0 || tips[0] != tip.GetID() {
		return fmt.Errorf("persisted native main tip does not match")
	}
	verifyBlock := func(block blockdag.IBlock) error {
		data, err := b.DB().GetDAGBlock(block.GetID())
		if err != nil || !bytes.Equal(data, block.Bytes()) {
			return fmt.Errorf("persisted native block/state differs for %s", block.GetHash())
		}
		if !bytes.Equal(data, rawdb.ReadDAGBlockBaw(durable.DB(), uint64(block.GetID()))) {
			return fmt.Errorf("raw durable native block/state differs for %s", block.GetHash())
		}
		id, err := b.DB().GetDAGBlockIdByHash(block.GetHash())
		if err != nil || id != block.GetID() {
			return fmt.Errorf("persisted native hash index differs for %s", block.GetHash())
		}
		rawID := rawdb.ReadBlockID(durable.DB(), block.GetHash())
		if rawID == nil || *rawID != uint64(block.GetID()) {
			return fmt.Errorf("raw durable native hash index differs for %s", block.GetHash())
		}
		if block.IsOrdered() {
			id, err = b.DB().GetBlockIdByOrder(block.GetOrder())
			if err != nil || id != block.GetID() || block.GetState().GetOrder() != uint64(block.GetOrder()) {
				return fmt.Errorf("persisted native order differs for %s", block.GetHash())
			}
			rawOrderID := rawdb.ReadBlockOrderSnapshot(durable.DB(), uint64(block.GetOrder()))
			if rawOrderID == nil || *rawOrderID != uint64(block.GetID()) {
				return fmt.Errorf("raw durable native order differs for %s", block.GetHash())
			}
		}
		return nil
	}
	if err := verifyBlock(tip); err != nil {
		return err
	}
	data, err := b.DB().GetBestChainState()
	if err != nil {
		return err
	}
	if !bytes.Equal(data, rawdb.ReadBestChainState(durable.DB())) {
		return fmt.Errorf("raw durable native best state differs from the accepted database view")
	}
	best, err := DeserializeBestChainState(data)
	snapshot := b.BestSnapshot()
	if err != nil || best.hash != *tip.GetHash() || best.total != uint64(b.bd.GetBlockTotal()) ||
		snapshot == nil || snapshot.GraphState == nil || snapshot.Hash != *tip.GetHash() ||
		snapshot.GraphState.GetTotal() != b.bd.GetBlockTotal() || snapshot.StateRoot != *tip.GetState().Root() {
		return fmt.Errorf("persisted native best-state/hash/total/root differs")
	}
	actual, err := readLocalEVMTuple(reader)
	if err != nil || actual != (localEVMTuple{tip.GetState().GetEVMNumber(), tip.GetState().GetEVMHash(), tip.GetState().GetEVMRoot()}) {
		return fmt.Errorf("committed native and actual EVM boundary differ")
	}
	if _, err := reader.EVMHeaderForState(tip.GetState()); err != nil {
		return fmt.Errorf("committed EVM state is unavailable: %w", err)
	}
	holds := b.localEVMRecovery.Load()
	for _, candidate := range candidates {
		if holds == nil || holds.fatal != nil || holds.pending[candidate] == nil || holds.pending[candidate].anchor == nil {
			return fmt.Errorf("original candidate recovery identity is unavailable")
		}
		if _, err := reader.EVMHeaderForState(holds.pending[candidate].anchor); err != nil {
			return fmt.Errorf("original candidate anchor state is not restored: %w", err)
		}
		block := b.bd.GetBlock(&candidate)
		if block == nil || block.GetState() == nil || !block.GetState().GetStatus().KnownInvalid() {
			return fmt.Errorf("proven governance rejection is not committed for %s", candidate)
		}
		if err := verifyBlock(block); err != nil {
			return err
		}
	}
	return nil
}

func (b *BlockChain) completeLocalGovernanceRejections(reader localEVMStateReader, rejection error, stopTrack func() error) (bool, error) {
	var bound *localEVMGovernanceRejections
	if !errors.As(rejection, &bound) || len(bound.candidates) == 0 {
		return false, nil
	}
	if !isGovernanceAdmissibilityRuleError(rejection) || isEVMCheckpointRuleError(rejection) ||
		isEVMStateGuardRecoveryError(rejection) || IsLocalEVMStateUnavailable(rejection) {
		return true, b.failLocalEVMApply(bound.candidates[0], fmt.Errorf("ambiguous rejection provenance"))
	}
	if err := b.verifyLocalGovernanceCommit(reader, bound.candidates); err != nil {
		return true, b.failLocalEVMApply(bound.candidates[0], err)
	}
	if err := stopTrack(); err != nil {
		return true, b.failLocalEVMApply(bound.candidates[0], err)
	}
	b.publishCommittedEVMHead() // Keep native KnownInvalid semantics unchanged.
	for _, candidate := range bound.candidates {
		b.removeLocalEVMRecovery(candidate)
	}
	return true, nil
}

type localEVMStateReader interface {
	EVMHeaderForState(model.BlockState) (*gethtypes.Header, error)
	GetCurHeader() *gethtypes.Header
}

type localEVMTuple struct {
	number     uint64
	hash, root ethcommon.Hash
}

func readLocalEVMTuple(reader localEVMStateReader) (localEVMTuple, error) {
	if reader == nil {
		return localEVMTuple{}, fmt.Errorf("EVM reader is unavailable")
	}
	head := reader.GetCurHeader()
	if head == nil || head.Number == nil {
		return localEVMTuple{}, fmt.Errorf("current EVM header is unavailable")
	}
	return localEVMTuple{head.Number.Uint64(), head.Hash(), head.Root}, nil
}

type localReorgBoundary struct {
	tipHash                ethcommon.Hash
	tipID, order, total    uint
	nativeRoot             ethcommon.Hash
	nativeEVM              localEVMTuple
	actualEVM              localEVMTuple
	best                   *BestState
	bestBytes, durableBest []byte
	committed              *committedNativeEVMHead
	candidateStored        bool
}

// This copy is made before AddBlock, while chainLock -> evmChainLock are held.
func (b *BlockChain) captureLocalReorgBoundary(reader localEVMStateReader, candidate *ethcommon.Hash) (*localReorgBoundary, error) {
	tip := b.bd.GetMainChainTip()
	if tip == nil || tip.GetState() == nil || tip.GetState().Root() == nil {
		return nil, fmt.Errorf("native main-tip boundary is unavailable")
	}
	actual, err := readLocalEVMTuple(reader)
	if err != nil {
		return nil, err
	}
	bestBytes, err := b.DB().GetBestChainState()
	if err != nil {
		return nil, fmt.Errorf("read native best-state boundary: %w", err)
	}
	state := tip.GetState()
	snapshot := &localReorgBoundary{
		tipHash: *tip.GetHash(), tipID: tip.GetID(), order: tip.GetOrder(), total: b.bd.GetBlockTotal(),
		nativeRoot: *state.Root(), nativeEVM: localEVMTuple{state.GetEVMNumber(), state.GetEVMHash(), state.GetEVMRoot()},
		actualEVM: actual, best: b.BestSnapshot(), bestBytes: append([]byte(nil), bestBytes...),
		committed: b.committedEVMHead.Load(), candidateStored: b.DB().HasBlock(candidate),
	}
	if db, ok := b.DB().(interface{ DB() ethdb.Database }); ok {
		snapshot.durableBest = append([]byte(nil), rawdb.ReadBestChainState(db.DB())...)
	}
	return snapshot, nil
}

// Shared by the preflight and actual reorganization; never choose different
// anchors on the two paths or infer a native order from the DAG total.
func (b *BlockChain) evmReorgAnchor(attach *list.List) (model.BlockState, blockdag.IBlock, error) {
	for entry := attach.Front(); entry != nil; entry = entry.Next() {
		block, ok := entry.Value.(blockdag.IBlock)
		if !ok || block == nil {
			return nil, nil, fmt.Errorf("invalid EVM reorg attach entry")
		}
		if !block.IsOrdered() {
			continue
		}
		if block.GetOrder() == 0 {
			return nil, block, fmt.Errorf("ordered reorg candidate has no predecessor")
		}
		previous := b.bd.GetBlockByOrder(block.GetOrder() - 1)
		if previous == nil || previous.GetState() == nil {
			return nil, block, fmt.Errorf("EVM reorg predecessor is unavailable at order %d", block.GetOrder()-1)
		}
		original, ok := previous.GetState().(*state.BlockState)
		if !ok {
			return nil, block, fmt.Errorf("unsupported EVM reorg anchor state")
		}
		// BlockState contains only value fields. Reorganization must not
		// rewrite the accepted anchor via a mutable native-state pointer.
		frozen := *original
		return &frozen, block, nil
	}
	return nil, nil, nil
}

// Only called before raw candidate storage, tracking, index/default-state
// mutation, notifications or any EVM head change. EVM lock stays held through
// application, so this read cannot race with another recovery/head writer.
func (b *BlockChain) preflightLocalEVMReorg(reader localEVMStateReader, candidate blockdag.IBlock, attach, detach *list.List, before *localReorgBoundary) error {
	if detach.Len() == 0 {
		return nil
	}
	anchor, _, err := b.evmReorgAnchor(attach)
	if err == nil && anchor != nil {
		_, err = reader.EVMHeaderForState(anchor)
	}
	if err == nil {
		return nil
	}
	local := &LocalEVMStateUnavailableError{Candidate: *candidate.GetHash(), Cause: err}
	if anchor != nil {
		local.AnchorNumber, local.AnchorHash, local.AnchorRoot = anchor.GetEVMNumber(), anchor.GetEVMHash(), anchor.GetEVMRoot()
		frozen := *anchor.(*state.BlockState)
		local.anchor = &frozen
	}
	// Persisted order mappings are still exactly their pre-Add values here.
	orders := make(map[uint]uint)
	for entry := detach.Front(); entry != nil; entry = entry.Next() {
		old := entry.Value.(*blockdag.BlockOrderHelp)
		if old.OldOrder != blockdag.MaxBlockOrder {
			orders[old.OldOrder] = old.Block.GetID()
		}
	}
	for entry := attach.Front(); entry != nil; entry = entry.Next() {
		block := entry.Value.(blockdag.IBlock)
		if block.IsOrdered() {
			id, readErr := b.DB().GetBlockIdByOrder(block.GetOrder())
			if readErr != nil {
				local.Fatal, local.Cause = true, fmt.Errorf("cannot prove pre-reorg order mapping: %w", readErr)
				break
			}
			orders[block.GetOrder()] = id
		}
	}
	rollbackErr := b.bd.Rollback()
	if rollbackErr == nil {
		rollbackErr = b.verifyLocalReorgRollback(reader, candidate.GetHash(), before, orders)
	}
	if rollbackErr != nil {
		local.Fatal, local.Cause = true, fmt.Errorf("unproven speculative DAG rollback: %w; original preflight: %v", rollbackErr, err)
	}
	b.holdLocalEVMRecovery(local)
	return local
}

func (b *BlockChain) verifyLocalReorgRollback(reader localEVMStateReader, candidate *ethcommon.Hash, before *localReorgBoundary, orders map[uint]uint) error {
	if before == nil {
		return fmt.Errorf("pre-Add boundary is unavailable")
	}
	after, err := b.captureLocalReorgBoundary(reader, candidate)
	if err != nil {
		return err
	}
	if before.tipHash != after.tipHash || before.tipID != after.tipID || before.order != after.order || before.total != after.total ||
		before.nativeRoot != after.nativeRoot || before.nativeEVM != after.nativeEVM || before.actualEVM != after.actualEVM ||
		before.best != after.best || before.committed != after.committed || !bytes.Equal(before.bestBytes, after.bestBytes) || !bytes.Equal(before.durableBest, after.durableBest) {
		return fmt.Errorf("native/EVM committed boundary changed")
	}
	if b.bd.HasBlock(candidate) || before.candidateStored != after.candidateStored {
		return fmt.Errorf("candidate storage changed across failed preflight")
	}
	for order, expectedID := range orders {
		id, err := b.DB().GetBlockIdByOrder(order)
		if err != nil || id != expectedID {
			return fmt.Errorf("persisted native order %d changed", order)
		}
		block := b.bd.GetBlockByOrder(order)
		if (expectedID == blockdag.MaxId && block != nil) || (expectedID != blockdag.MaxId && (block == nil || block.GetID() != expectedID || block.GetOrder() != order)) {
			return fmt.Errorf("speculative native order %d was not restored", order)
		}
	}
	return nil
}
