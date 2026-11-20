package bdag

import (
	"bytes"
	"errors"
	"fmt"
	"time"

	bdagconsensus "github.com/BlockdagNetworkLabs/bdag/bevm/bdag/consensus"
	blockdag "github.com/BlockdagNetworkLabs/bdag/blockdag"
	"github.com/BlockdagNetworkLabs/bdag/consensus/model"
	qtypes "github.com/BlockdagNetworkLabs/bdag/core/types"
	"github.com/BlockdagNetworkLabs/bdag/upgrades"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/params"
)

type unsafeEVMRewindError struct {
	reason string
}

func (e *unsafeEVMRewindError) Error() string {
	return "unsafe EVM rewind refused: " + e.reason
}

func newUnsafeEVMRewindError(format string, args ...interface{}) error {
	return &unsafeEVMRewindError{reason: fmt.Sprintf(format, args...)}
}

func isUnsafeEVMRewindError(err error) bool {
	var rewindErr *unsafeEVMRewindError
	return errors.As(err, &rewindErr)
}

type evmRecoveryStep struct {
	order uint64
	state model.BlockState
}

type evmRecoveryFailureMemo struct {
	currentNumber uint64
	currentHash   common.Hash
	targetOrder   uint64
	targetNumber  uint64
	targetHash    common.Hash
	err           error
}

var errEVMStateReconstructionInterrupted = errors.New("EVM state reconstruction interrupted")

type evmLineageLimits struct {
	maxDepth  uint64
	maxBytes  uint64
	interrupt <-chan struct{}
}

func (b *BdagChain) replayGovernanceAdmissibilityAtOrder(order uint64) (GovernanceAdmissibility, error) {
	if b == nil || b.consensus == nil || b.consensus.BlockChain() == nil {
		return GovernanceAdmissibility{}, fmt.Errorf("native blockchain is unavailable for EVM replay at order %d", order)
	}
	nativeBlock := b.consensus.BlockChain().GetBlockByOrder(order)
	if nativeBlock == nil {
		return GovernanceAdmissibility{}, fmt.Errorf("native block is unavailable for EVM replay at order %d", order)
	}
	return ReplayGovernanceAdmissibilityAtLayer(uint64(nativeBlock.GetLayer())), nil
}

const (
	maxMissingEVMRecoveryGap           = 1024
	maxEVMRecoveryAnchorBacktrack      = 262144
	maxNativeEVMRebuildFromGenesis     = maxEVMRecoveryAnchorBacktrack
	maxAutomaticEVMReconstructionDepth = 2048
	maxAutomaticEVMReconstructionBytes = 64 * 1024 * 1024
	maxStoredEVMRecoveryReplay         = maxAutomaticEVMReconstructionDepth - 1
	evmRecoveryTimingLogEvery          = 1000
	evmRecoverySlowStep                = 500 * time.Millisecond
)

type evmNumberBlockFinder interface {
	GetBlockByNumber(number uint64) blockdag.IBlock
}

type lockedEVMNumberBlockFinder interface {
	GetBlockByNumberNoLock(number uint64) blockdag.IBlock
}

func isRecoverableEVMState(state model.BlockState) bool {
	return state != nil &&
		state.IsOrdered() &&
		state.GetOrder() < uint64(blockdag.MaxBlockOrder) &&
		hasRecordedEVMCommitment(state)
}

func isRecoverableEVMStateAtOrder(state model.BlockState, order uint64) bool {
	return state != nil &&
		order < uint64(blockdag.MaxBlockOrder) &&
		hasRecordedEVMCommitment(state)
}

func hasRecordedEVMCommitment(state model.BlockState) bool {
	return state != nil &&
		(state.GetEVMNumber() != 0 ||
			state.GetEVMHash() != (common.Hash{}) ||
			state.GetEVMRoot() != (common.Hash{}))
}

func isRecoverableEVMFallbackState(state model.BlockState) bool {
	return isRecoverableEVMState(state) && !state.GetStatus().KnownInvalid()
}

func isRecoverableEVMFallbackStateAtOrder(state model.BlockState, order uint64) bool {
	return isRecoverableEVMStateAtOrder(state, order) && !state.GetStatus().KnownInvalid()
}

func isRecoverableEVMAnchorCandidate(state model.BlockState, currentNumber uint64) bool {
	return isRecoverableEVMFallbackState(state) && state.GetEVMNumber() <= currentNumber
}

func isRecoverableEVMAnchorCandidateAtOrder(state model.BlockState, order uint64, currentNumber uint64) bool {
	return isRecoverableEVMFallbackStateAtOrder(state, order) && state.GetEVMNumber() <= currentNumber
}

func hasRecordedEVMParentGap(currentNumber uint64, targetNumber uint64, recordedParentExists bool) bool {
	if targetNumber <= currentNumber+1 {
		return false
	}
	return !recordedParentExists
}

func shouldForceSequentialEVMRebuild(currentNumber uint64, currentHash common.Hash, targetNumber uint64, targetHash common.Hash) bool {
	if currentNumber == targetNumber && currentHash == targetHash {
		return false
	}
	return currentNumber >= targetNumber
}

func shouldLogEVMRecoveryTiming(count uint64, elapsed time.Duration) bool {
	return count == 1 || count%evmRecoveryTimingLogEvery == 0 || elapsed >= evmRecoverySlowStep
}

func isNativeEVMRebuildAnchor(state model.BlockState, order uint64, header *types.Header, target model.BlockState) bool {
	if header == nil || target == nil {
		return false
	}
	if !isRecoverableEVMAnchorCandidateAtOrder(state, order, header.Number.Uint64()) {
		return false
	}
	if state.GetEVMNumber() != header.Number.Uint64() || state.GetEVMHash() != header.Hash() {
		return false
	}
	if state.GetEVMNumber() > target.GetEVMNumber() || order > target.GetOrder() {
		return false
	}
	return true
}

func isCurrentEVMGenesis(curBlockHeader *types.Header, genesisHeader *types.Header) bool {
	if curBlockHeader == nil || curBlockHeader.Number == nil || genesisHeader == nil || genesisHeader.Number == nil {
		return false
	}
	return curBlockHeader.Number.Uint64() == 0 &&
		genesisHeader.Number.Uint64() == 0 &&
		curBlockHeader.Hash() == genesisHeader.Hash()
}

func shouldLogRecoveryScanSkip(count uint64) bool {
	return count == 1 || count == 10 || count == 100 || (count > 0 && count%10000 == 0)
}

func isBetterEVMRecoveryAnchor(candidate model.BlockState, candidateOrder uint64, best model.BlockState, bestOrder uint64, currentNumber uint64) bool {
	if candidate == nil {
		return false
	}
	if currentNumber > 0 && candidate.GetEVMNumber() == 0 {
		return false
	}
	if best == nil {
		return true
	}
	if candidate.GetEVMNumber() != best.GetEVMNumber() {
		return candidate.GetEVMNumber() > best.GetEVMNumber()
	}
	return candidateOrder > bestOrder
}

func recoveryFailureMemoMatches(memo *evmRecoveryFailureMemo, curHeader *types.Header, target model.BlockState) bool {
	if memo == nil || curHeader == nil || curHeader.Number == nil || target == nil {
		return false
	}
	return memo.currentNumber == curHeader.Number.Uint64() &&
		memo.currentHash == curHeader.Hash() &&
		memo.targetOrder == target.GetOrder() &&
		memo.targetNumber == target.GetEVMNumber() &&
		memo.targetHash == target.GetEVMHash()
}

func (b *BdagChain) memoizedEVMRecoveryFailure(curHeader *types.Header, target model.BlockState) error {
	if b == nil || !recoveryFailureMemoMatches(b.lastEVMRecoveryFailure, curHeader, target) {
		return nil
	}
	return b.lastEVMRecoveryFailure.err
}

func (b *BdagChain) rememberEVMRecoveryFailure(curHeader *types.Header, target model.BlockState, err error) {
	if b == nil || curHeader == nil || curHeader.Number == nil || target == nil || err == nil {
		return
	}
	b.lastEVMRecoveryFailure = &evmRecoveryFailureMemo{
		currentNumber: curHeader.Number.Uint64(),
		currentHash:   curHeader.Hash(),
		targetOrder:   target.GetOrder(),
		targetNumber:  target.GetEVMNumber(),
		targetHash:    target.GetEVMHash(),
		err:           err,
	}
}

func (b *BdagChain) clearEVMRecoveryFailure() {
	if b != nil {
		b.lastEVMRecoveryFailure = nil
	}
}

func nativeEVMRebuildFromGenesisExceedsLimit(anchorOrder uint64, targetOrder uint64) bool {
	return anchorOrder == 0 && targetOrder > maxNativeEVMRebuildFromGenesis
}

func validateStrictRecoveredEVMState(order uint64, recState model.BlockState, header *types.Header) error {
	if recState == nil {
		return fmt.Errorf("strict EVM recovery mismatch at native order %d: recorded block state is nil", order)
	}
	if header == nil || header.Number == nil {
		return fmt.Errorf("strict EVM recovery mismatch at native order %d: rebuilt EVM header is unavailable", order)
	}
	rebuiltNumber := header.Number.Uint64()
	rebuiltHash := header.Hash()
	if recState.GetEVMNumber() == rebuiltNumber &&
		recState.GetEVMHash() == rebuiltHash &&
		recState.GetEVMRoot() == header.Root {
		return nil
	}
	return fmt.Errorf(
		"strict EVM recovery mismatch at native order %d (state.order=%d): recorded number=%d hash=%s root=%s rebuilt number=%d hash=%s root=%s",
		order,
		recState.GetOrder(),
		recState.GetEVMNumber(),
		recState.GetEVMHash().String(),
		recState.GetEVMRoot().String(),
		rebuiltNumber,
		rebuiltHash.String(),
		header.Root.String(),
	)
}

func validateEVMRewindTargetHeader(state model.BlockState, header *types.Header) error {
	if state == nil {
		return fmt.Errorf("EVM rewind target state is nil")
	}
	if header == nil || header.Number == nil {
		return fmt.Errorf("EVM rewind target header is unavailable: target.evm.number=%d target.evm.hash=%s target.evm.root=%s",
			state.GetEVMNumber(), state.GetEVMHash().String(), state.GetEVMRoot().String())
	}
	if header.Number.Uint64() != state.GetEVMNumber() ||
		header.Hash() != state.GetEVMHash() ||
		header.Root != state.GetEVMRoot() {
		return fmt.Errorf("EVM rewind target header mismatch: header.number=%d header.hash=%s header.root=%s target.evm.number=%d target.evm.hash=%s target.evm.root=%s",
			header.Number.Uint64(), header.Hash().String(), header.Root.String(),
			state.GetEVMNumber(), state.GetEVMHash().String(), state.GetEVMRoot().String())
	}
	return nil
}

func validateEVMRewindTargetState(state model.BlockState, header *types.Header, hasState bool) error {
	if err := validateEVMRewindTargetHeader(state, header); err != nil {
		return err
	}
	if !hasState {
		return fmt.Errorf("EVM rewind target state is unavailable: target.evm.number=%d target.evm.hash=%s target.evm.root=%s",
			state.GetEVMNumber(), state.GetEVMHash().String(), state.GetEVMRoot().String())
	}
	return nil
}

func validateEVMHeadMatchesState(state model.BlockState, header *types.Header) error {
	if state == nil {
		return fmt.Errorf("target block state is nil")
	}
	if header == nil || header.Number == nil {
		return fmt.Errorf("current EVM head is unavailable")
	}
	if header.Number.Uint64() == state.GetEVMNumber() &&
		header.Hash() == state.GetEVMHash() &&
		header.Root == state.GetEVMRoot() {
		return nil
	}
	return fmt.Errorf(
		"current EVM head does not match target state: target.number=%d target.hash=%s target.root=%s cur.number=%d cur.hash=%s cur.root=%s",
		state.GetEVMNumber(), state.GetEVMHash().String(), state.GetEVMRoot().String(),
		header.Number.Uint64(), header.Hash().String(), header.Root.String())
}

// validateExactEVMRewindPostcondition verifies the observation made after a
// rewind operation. The feasibility check above deliberately permits a live
// head above the requested target so it can be used before SetHead; it must not
// be reused to claim that SetHead landed exactly where requested.
func validateExactEVMRewindPostcondition(
	state model.BlockState,
	header *types.Header,
	getBlock func(common.Hash, uint64) *types.Block,
	hasState func(common.Hash) bool,
) error {
	if state == nil {
		return newUnsafeEVMRewindError("target block state is nil")
	}
	if !hasRecordedEVMCommitment(state) {
		return newUnsafeEVMRewindError("target EVM state is empty: target.order=%d target.evm.number=%d target.evm.hash=%s target.evm.root=%s",
			state.GetOrder(), state.GetEVMNumber(), state.GetEVMHash().String(), state.GetEVMRoot().String())
	}
	if header == nil || header.Number == nil {
		return newUnsafeEVMRewindError("current EVM head is unavailable")
	}
	targetNumber := state.GetEVMNumber()
	targetHash := state.GetEVMHash()
	targetRoot := state.GetEVMRoot()
	if header.Number.Uint64() != targetNumber {
		return newUnsafeEVMRewindError("rewind landed at number=%d, want=%d", header.Number.Uint64(), targetNumber)
	}
	if header.Hash() != targetHash {
		return newUnsafeEVMRewindError("rewind landed at hash=%s, want=%s", header.Hash().String(), targetHash.String())
	}
	if header.Root != targetRoot {
		return newUnsafeEVMRewindError("rewind landed at root=%s, want=%s", header.Root.String(), targetRoot.String())
	}
	if getBlock == nil {
		return newUnsafeEVMRewindError("EVM block accessor is unavailable")
	}
	block := getBlock(targetHash, targetNumber)
	if block == nil {
		return newUnsafeEVMRewindError("rewind target body is unavailable: number=%d hash=%s", targetNumber, targetHash.String())
	}
	if block.NumberU64() != targetNumber || block.Hash() != targetHash {
		return newUnsafeEVMRewindError("rewind target body identity mismatch: number=%d hash=%s", block.NumberU64(), block.Hash().String())
	}
	if hasState == nil || !hasState(targetRoot) {
		return newUnsafeEVMRewindError("rewind target state is unavailable: number=%d hash=%s root=%s", targetNumber, targetHash.String(), targetRoot.String())
	}
	return nil
}

type evmRewindChain interface {
	CurrentBlock() *types.Header
	Genesis() *types.Block
	GetHeader(common.Hash, uint64) *types.Header
	GetBlock(common.Hash, uint64) *types.Block
	GetBlockByNumber(uint64) *types.Block
	HasState(common.Hash) bool
	SetHead(uint64) error
}

// evmRewindChainProvider exposes the existing EVM chain operations used by
// rewind orchestration. Production uses the concrete chain; the bounded test
// seam supplies a real temporary geth chain wrapper without bypassing callers.
var evmRewindChainProvider = func(b *BdagChain) evmRewindChain {
	if b == nil || b.chain == nil || b.chain.Ether() == nil {
		return nil
	}
	return b.chain.Ether().BlockChain()
}

func validateSafeEVMRewindTarget(curHeader *types.Header, target model.BlockState, getHeader func(common.Hash, uint64) *types.Header, hasState func(common.Hash) bool) error {
	if curHeader == nil || curHeader.Number == nil {
		return newUnsafeEVMRewindError("current EVM header is unavailable")
	}
	if target == nil {
		return newUnsafeEVMRewindError("target block state is nil")
	}
	if !hasRecordedEVMCommitment(target) {
		return newUnsafeEVMRewindError("target EVM state is empty: target.order=%d target.evm.number=%d target.evm.hash=%s target.evm.root=%s",
			target.GetOrder(), target.GetEVMNumber(), target.GetEVMHash().String(), target.GetEVMRoot().String())
	}

	currentNumber := curHeader.Number.Uint64()
	targetNumber := target.GetEVMNumber()
	if currentNumber < targetNumber {
		return newUnsafeEVMRewindError("current EVM head %d is below target %d", currentNumber, targetNumber)
	}
	if currentNumber == targetNumber {
		if curHeader.Hash() != target.GetEVMHash() {
			return newUnsafeEVMRewindError("current EVM head hash mismatch at target number=%d currentHash=%s targetHash=%s",
				targetNumber, curHeader.Hash().String(), target.GetEVMHash().String())
		}
		targetRoot := target.GetEVMRoot()
		if curHeader.Root != targetRoot {
			return newUnsafeEVMRewindError("current EVM head root mismatch at target number=%d currentRoot=%s targetRoot=%s",
				targetNumber, curHeader.Root.String(), targetRoot.String())
		}
		if hasState != nil && !hasState(targetRoot) {
			return newUnsafeEVMRewindError("current EVM state is missing number=%d hash=%s root=%s",
				targetNumber, target.GetEVMHash().String(), targetRoot.String())
		}
		return nil
	}
	if targetNumber == 0 {
		return newUnsafeEVMRewindError("current EVM head %d would be rewound to genesis", currentNumber)
	}
	if getHeader == nil || hasState == nil {
		return newUnsafeEVMRewindError("EVM chain accessors are unavailable")
	}

	targetHash := target.GetEVMHash()
	targetHeader := getHeader(targetHash, targetNumber)
	if targetHeader == nil || targetHeader.Number == nil {
		return newUnsafeEVMRewindError("target EVM header is missing number=%d hash=%s", targetNumber, targetHash.String())
	}
	if targetHeader.Number.Uint64() != targetNumber {
		return newUnsafeEVMRewindError("target EVM header number mismatch expected=%d actual=%d hash=%s",
			targetNumber, targetHeader.Number.Uint64(), targetHash.String())
	}
	if targetHeader.Hash() != targetHash {
		return newUnsafeEVMRewindError("target EVM header hash mismatch number=%d expected=%s actual=%s",
			targetNumber, targetHash.String(), targetHeader.Hash().String())
	}
	targetRoot := target.GetEVMRoot()
	if targetHeader.Root != targetRoot {
		return newUnsafeEVMRewindError("target EVM root mismatch number=%d hash=%s expectedRoot=%s actualRoot=%s",
			targetNumber, targetHash.String(), targetRoot.String(), targetHeader.Root.String())
	}
	if !hasState(targetRoot) {
		return newUnsafeEVMRewindError("target EVM state is missing number=%d hash=%s root=%s",
			targetNumber, targetHash.String(), targetRoot.String())
	}
	return nil
}

func validateEVMSetHeadDepth(currentNumber, targetNumber uint64) error {
	if currentNumber <= targetNumber {
		return nil
	}
	depth := currentNumber - targetNumber
	if depth <= params.FullImmutabilityThreshold {
		return nil
	}
	return newUnsafeEVMRewindError("normal EVM SetHead from %d to %d exceeds state search threshold %d; prepare an exact stateful scratch anchor before replay",
		currentNumber, targetNumber, params.FullImmutabilityThreshold)
}

func (b *BdagChain) ReplayNativeEVMBlock(sb *qtypes.SerializedBlock, nativeLayer uint64) (*types.Header, error) {
	if b == nil || b.chain == nil || b.chain.Ether() == nil || b.chain.Ether().BlockChain() == nil {
		return nil, fmt.Errorf("bdag EVM chain is unavailable")
	}
	if sb == nil || sb.Block() == nil {
		return nil, fmt.Errorf("native block body is unavailable")
	}

	eb, err := BuildEVMBlock(sb)
	if err != nil {
		return nil, err
	}
	parent := b.chain.Ether().BlockChain().CurrentBlock()
	if parent == nil {
		return nil, fmt.Errorf("EVM parent header is unavailable")
	}

	block, _, _, err := b.buildBlock(parent, eb.Transactions(), eb.Timestamp().Unix(), sb.Block().Header.CoinbaseAddress, sb.Block().Header.Reward, ReplayGovernanceAdmissibilityAtLayer(nativeLayer))
	if err != nil {
		return nil, err
	}
	inserted, err := b.chain.Ether().BlockChain().InsertChain(types.Blocks{block})
	if err != nil {
		return nil, err
	}
	if inserted != 1 {
		return nil, fmt.Errorf("insert chain")
	}
	return block.Header(), nil
}

// BuildNativeEVMBlockCandidate executes a native block against the current EVM
// head without inserting it. Recovery tooling uses this to prove that an
// external canonical block is reproducible before changing either chain head.
func (b *BdagChain) BuildNativeEVMBlockCandidate(sb *qtypes.SerializedBlock, nativeLayer uint64) (*types.Block, error) {
	if b == nil || b.chain == nil || b.chain.Ether() == nil || b.chain.Ether().BlockChain() == nil {
		return nil, fmt.Errorf("bdag EVM chain is unavailable")
	}
	if sb == nil || sb.Block() == nil {
		return nil, fmt.Errorf("native block body is unavailable")
	}

	eb, err := BuildEVMBlock(sb)
	if err != nil {
		return nil, err
	}
	parent := b.chain.Ether().BlockChain().CurrentBlock()
	if parent == nil {
		return nil, fmt.Errorf("EVM parent header is unavailable")
	}
	block, _, _, err := b.buildBlock(parent, eb.Transactions(), eb.Timestamp().Unix(), sb.Block().Header.CoinbaseAddress, sb.Block().Header.Reward, ReplayGovernanceAdmissibilityAtLayer(nativeLayer))
	if err != nil {
		return nil, err
	}
	return block, nil
}

func validateRecordedEVMImportTarget(block *types.Block, target model.BlockState) error {
	if block == nil || block.Header() == nil || block.Header().Number == nil {
		return fmt.Errorf("recorded native EVM import candidate is unavailable")
	}
	if target == nil || !hasRecordedEVMCommitment(target) || target.GetStatus().KnownInvalid() {
		return fmt.Errorf("recorded native EVM import target is unavailable")
	}
	if block.NumberU64() != target.GetEVMNumber() || block.Hash() != target.GetEVMHash() || block.Root() != target.GetEVMRoot() {
		return fmt.Errorf("recorded native EVM import candidate mismatch: candidate number=%d hash=%s root=%s, recorded number=%d hash=%s root=%s",
			block.NumberU64(), block.Hash().String(), block.Root().String(),
			target.GetEVMNumber(), target.GetEVMHash().String(), target.GetEVMRoot().String())
	}
	return nil
}

// ImportRecordedNativeEVMBlock stages an already reproduced native EVM block
// without changing the canonical indexes, then atomically activates it. The
// immutable native record is checked before any canonical mutation.
func (b *BdagChain) ImportRecordedNativeEVMBlock(block *types.Block, target model.BlockState) (*types.Header, error) {
	if b == nil || b.chain == nil || b.chain.Ether() == nil || b.chain.Ether().BlockChain() == nil {
		return nil, fmt.Errorf("bdag EVM chain is unavailable")
	}
	if err := validateRecordedEVMImportTarget(block, target); err != nil {
		return nil, err
	}
	bc := b.chain.Ether().BlockChain()
	current := bc.CurrentBlock()
	if current == nil || current.Number == nil {
		return nil, fmt.Errorf("EVM head is unavailable before recorded native import")
	}
	anchor := bc.GetBlock(current.Hash(), current.Number.Uint64())
	if anchor == nil {
		return nil, fmt.Errorf("EVM anchor block is unavailable before recorded native import: number=%d hash=%s",
			current.Number.Uint64(), current.Hash().String())
	}
	if block.NumberU64() != anchor.NumberU64()+1 || block.ParentHash() != anchor.Hash() {
		return nil, fmt.Errorf("recorded native EVM import is not contiguous: anchor number=%d hash=%s, block number=%d parent=%s",
			anchor.NumberU64(), anchor.Hash().String(), block.NumberU64(), block.ParentHash().String())
	}
	replayed, err := b.stageExactEVMLineage(anchor, types.Blocks{block})
	if err != nil {
		return nil, fmt.Errorf("stage recorded native EVM block %d hash=%s: %w", block.NumberU64(), block.Hash().String(), err)
	}
	if replayed != 1 {
		return nil, fmt.Errorf("staged recorded native EVM block count mismatch: got %d want 1", replayed)
	}
	return block.Header(), nil
}

// NativeEVMEnvelopeMatches reports whether remote could have been built from
// sb at the current EVM head without considering execution-derived fields. A
// matching envelope with a different block hash indicates a state or rule-set
// mismatch and must not be treated as an ordinary invalid native candidate.
func (b *BdagChain) NativeEVMEnvelopeMatches(sb *qtypes.SerializedBlock, remote *types.Block, nativeLayer uint64) bool {
	if b == nil || b.chain == nil || b.chain.Ether() == nil || b.chain.Ether().BlockChain() == nil ||
		sb == nil || sb.Block() == nil || remote == nil || remote.Header() == nil {
		return false
	}
	parent := b.chain.Ether().BlockChain().CurrentBlock()
	return b.NativeEVMEnvelopeMatchesAtParent(sb, remote, parent, nativeLayer)
}

// NativeEVMEnvelopeMatchesAtParent reports whether remote carries the native
// block's complete execution-independent envelope at the supplied EVM parent.
// Recovery can use this while collecting a validated import batch without
// advancing the persisted EVM head after every block.
func (b *BdagChain) NativeEVMEnvelopeMatchesAtParent(sb *qtypes.SerializedBlock, remote *types.Block, parent *types.Header, nativeLayer uint64) bool {
	if b == nil || b.chain == nil || sb == nil || sb.Block() == nil ||
		remote == nil || remote.Header() == nil {
		return false
	}
	if parent == nil || parent.Number == nil {
		return false
	}
	native := sb.Block()
	expectedExtra := bdagconsensus.EncodeRewardExtra(native.Header.Reward)
	if upgrades.RewardLayerBindingEnforced(nativeLayer) {
		expectedExtra = bdagconsensus.EncodeRewardExtraWithLayer(native.Header.Reward, nativeLayer)
	}
	expectedTime := ResolveEVMBlockTimestamp(parent.Time, native.Header.Timestamp.Unix(), b.chain.Config().Eth.Genesis.Config.ChainID)
	if remote.NumberU64() != parent.Number.Uint64()+1 ||
		remote.ParentHash() != parent.Hash() ||
		remote.Time() != expectedTime ||
		remote.Coinbase() != native.Header.CoinbaseAddress ||
		!bytes.Equal(remote.Extra(), expectedExtra) {
		return false
	}
	remoteTxs := remote.Transactions()
	if len(remoteTxs) != len(native.Transactions) {
		return false
	}
	for i, tx := range native.Transactions {
		if tx == nil || remoteTxs[i] == nil || tx.Hash() != remoteTxs[i].Hash() {
			return false
		}
	}
	return true
}

func (b *BdagChain) ImportCanonicalEVMBlock(block *types.Block) error {
	if b == nil || b.chain == nil || b.chain.Ether() == nil || b.chain.Ether().BlockChain() == nil {
		return fmt.Errorf("bdag EVM chain is unavailable")
	}
	if block == nil || block.Header() == nil || block.Header().Number == nil {
		return fmt.Errorf("canonical EVM block is unavailable")
	}
	bc := b.chain.Ether().BlockChain()
	parent := bc.CurrentBlock()
	if parent == nil || parent.Number == nil {
		return fmt.Errorf("EVM parent header is unavailable")
	}
	number := block.NumberU64()
	if parent.Number.Uint64()+1 != number {
		return fmt.Errorf("canonical EVM import out of sequence: parent=%d block=%d", parent.Number.Uint64(), number)
	}
	if block.ParentHash() != parent.Hash() {
		return fmt.Errorf("canonical EVM import parent mismatch at %d: got parent=%s want=%s",
			number, block.ParentHash().String(), parent.Hash().String())
	}
	if existing := bc.GetBlock(block.Hash(), number); existing != nil {
		if bc.HasState(existing.Root()) {
			if err := bc.SetHeadToExistingBlock(existing.Hash(), number); err != nil {
				return fmt.Errorf("advance EVM head to existing canonical block %d: %w", number, err)
			}
			return nil
		}
		log.Debug("Re-importing canonical EVM block to rebuild missing state",
			"number", number,
			"hash", block.Hash().String(),
			"root", block.Root().String())
	}
	if existing := bc.GetBlockByNumber(number); existing != nil && existing.Hash() != block.Hash() {
		log.Debug("Replacing divergent canonical EVM block during recovery import",
			"number", number,
			"existing", existing.Hash().String(),
			"imported", block.Hash().String())
	}
	inserted, err := bc.InsertChain(types.Blocks{block})
	if err != nil {
		return err
	}
	if inserted != 1 {
		return fmt.Errorf("canonical EVM import inserted %d blocks, want 1", inserted)
	}
	return nil
}

// ImportCanonicalEVMBlocks validates and imports a contiguous canonical block
// range in one chain operation. Offline local-source recovery uses this to
// avoid restarting header verification and state prefetch for every block.
func (b *BdagChain) ImportCanonicalEVMBlocks(blocks types.Blocks) error {
	if b == nil || b.chain == nil || b.chain.Ether() == nil || b.chain.Ether().BlockChain() == nil {
		return fmt.Errorf("bdag EVM chain is unavailable")
	}
	if len(blocks) == 0 {
		return nil
	}
	bc := b.chain.Ether().BlockChain()
	parent := bc.CurrentBlock()
	if parent == nil || parent.Number == nil {
		return fmt.Errorf("EVM parent header is unavailable")
	}
	previous := parent
	for i, block := range blocks {
		if block == nil || block.Header() == nil || block.Header().Number == nil {
			return fmt.Errorf("canonical EVM batch block %d is unavailable", i)
		}
		if block.NumberU64() != previous.Number.Uint64()+1 || block.ParentHash() != previous.Hash() {
			return fmt.Errorf("canonical EVM batch is not contiguous at index %d: parent=%d %s block=%d %s blockParent=%s",
				i, previous.Number.Uint64(), previous.Hash().String(), block.NumberU64(), block.Hash().String(), block.ParentHash().String())
		}
		previous = block.Header()
	}
	inserted, err := bc.InsertChain(blocks)
	if err != nil {
		if inserted >= len(blocks) {
			return fmt.Errorf("canonical EVM batch failed after index %d: %w", inserted, err)
		}
		return fmt.Errorf("canonical EVM batch failed at index %d block %d: %w", inserted, blocks[inserted].NumberU64(), err)
	}
	if inserted != len(blocks) {
		return fmt.Errorf("canonical EVM batch inserted %d blocks, want %d", inserted, len(blocks))
	}
	return nil
}

func checkEVMStateReconstructionInterrupted(interrupt <-chan struct{}) error {
	select {
	case <-interrupt:
		return errEVMStateReconstructionInterrupted
	default:
		return nil
	}
}

func validateExactEVMLineageTarget(target *types.Block, expectedNumber uint64, expectedHash, expectedRoot common.Hash) error {
	if target == nil || target.Header() == nil || target.Header().Number == nil {
		return fmt.Errorf("EVM state reconstruction target block is unavailable")
	}
	if target.NumberU64() != expectedNumber || target.Hash() != expectedHash || target.Root() != expectedRoot {
		return fmt.Errorf("EVM state reconstruction target mismatch: got number=%d hash=%s root=%s want number=%d hash=%s root=%s",
			target.NumberU64(), target.Hash().String(), target.Root().String(),
			expectedNumber, expectedHash.String(), expectedRoot.String())
	}
	return nil
}

func collectExactEVMLineage(
	target *types.Block,
	expectedNumber uint64,
	expectedHash common.Hash,
	expectedRoot common.Hash,
	getBlock func(common.Hash, uint64) *types.Block,
	isAnchor func(*types.Block) bool,
	limits evmLineageLimits,
) (*types.Block, types.Blocks, error) {
	if err := validateExactEVMLineageTarget(target, expectedNumber, expectedHash, expectedRoot); err != nil {
		return nil, nil, err
	}
	if getBlock == nil || isAnchor == nil {
		return nil, nil, fmt.Errorf("EVM state reconstruction accessors are unavailable")
	}
	if limits.maxDepth == 0 {
		return nil, nil, fmt.Errorf("EVM state reconstruction depth is zero")
	}
	if limits.maxBytes == 0 {
		return nil, nil, fmt.Errorf("EVM state reconstruction byte limit is zero")
	}

	reverse := make(types.Blocks, 0, 16)
	var retainedBytes uint64
	for cursor := target; ; {
		if err := checkEVMStateReconstructionInterrupted(limits.interrupt); err != nil {
			return nil, nil, err
		}
		if isAnchor(cursor) {
			for left, right := 0, len(reverse)-1; left < right; left, right = left+1, right-1 {
				reverse[left], reverse[right] = reverse[right], reverse[left]
			}
			return cursor, reverse, nil
		}
		if uint64(len(reverse)) >= limits.maxDepth {
			return nil, nil, fmt.Errorf("no EVM reconstruction anchor found within %d blocks of target %d", limits.maxDepth, target.NumberU64())
		}
		blockBytes := cursor.Size()
		if blockBytes > limits.maxBytes-retainedBytes {
			return nil, nil, fmt.Errorf("EVM state reconstruction lineage exceeds byte limit %d at block %d hash=%s",
				limits.maxBytes, cursor.NumberU64(), cursor.Hash().String())
		}
		retainedBytes += blockBytes
		reverse = append(reverse, cursor)

		if cursor.NumberU64() == 0 {
			return nil, nil, fmt.Errorf("EVM state reconstruction reached genesis without an acceptable anchor")
		}
		parentNumber := cursor.NumberU64() - 1
		parentHash := cursor.ParentHash()
		parent := getBlock(parentHash, parentNumber)
		if parent == nil || parent.Header() == nil || parent.Header().Number == nil {
			return nil, nil, fmt.Errorf("EVM state reconstruction parent is unavailable at %d hash=%s", parentNumber, parentHash.String())
		}
		if parent.NumberU64() != parentNumber || parent.Hash() != parentHash {
			return nil, nil, fmt.Errorf("EVM state reconstruction parent mismatch at %d: got number=%d hash=%s want hash=%s",
				parentNumber, parent.NumberU64(), parent.Hash().String(), parentHash.String())
		}
		cursor = parent
	}
}

func collectMissingEVMStatePath(
	target *types.Block,
	expectedNumber uint64,
	expectedHash common.Hash,
	expectedRoot common.Hash,
	getBlock func(common.Hash, uint64) *types.Block,
	hasState func(common.Hash) bool,
	limits evmLineageLimits,
) (*types.Block, types.Blocks, error) {
	if hasState == nil {
		return nil, nil, fmt.Errorf("EVM state reconstruction state accessor is unavailable")
	}
	return collectExactEVMLineage(
		target,
		expectedNumber,
		expectedHash,
		expectedRoot,
		getBlock,
		func(block *types.Block) bool { return hasState(block.Root()) },
		limits,
	)
}

func exactEVMLineageIsCanonical(anchor *types.Block, blocks types.Blocks, getCanonicalHash func(uint64) common.Hash) bool {
	if anchor == nil || getCanonicalHash == nil || getCanonicalHash(anchor.NumberU64()) != anchor.Hash() {
		return false
	}
	for _, block := range blocks {
		if block == nil || getCanonicalHash(block.NumberU64()) != block.Hash() {
			return false
		}
	}
	return true
}

func validateBoundedEVMForkActivation(
	oldHead *types.Header,
	target *types.Block,
	getBlock func(common.Hash, uint64) *types.Block,
	limits evmLineageLimits,
) error {
	if oldHead == nil || oldHead.Number == nil || target == nil || target.Header() == nil || target.Header().Number == nil {
		return fmt.Errorf("EVM fork activation endpoints are unavailable")
	}
	if getBlock == nil || limits.maxDepth == 0 || limits.maxBytes == 0 {
		return fmt.Errorf("EVM fork activation bounds are unavailable")
	}
	oldBlock := getBlock(oldHead.Hash(), oldHead.Number.Uint64())
	if oldBlock == nil {
		return fmt.Errorf("current EVM head block is unavailable for bounded fork activation: number=%d hash=%s",
			oldHead.Number.Uint64(), oldHead.Hash().String())
	}
	newBlock := target
	var retainedBlocks, retainedBytes uint64
	account := func(label string, block *types.Block) error {
		if err := checkEVMStateReconstructionInterrupted(limits.interrupt); err != nil {
			return err
		}
		if block == nil || block.Header() == nil || block.Header().Number == nil {
			return fmt.Errorf("%s EVM fork block is unavailable", label)
		}
		if retainedBlocks >= limits.maxDepth {
			return fmt.Errorf("EVM fork activation exceeds block limit %d at %s block %d hash=%s",
				limits.maxDepth, label, block.NumberU64(), block.Hash().String())
		}
		blockBytes := block.Size()
		if blockBytes > limits.maxBytes-retainedBytes {
			return fmt.Errorf("EVM fork activation exceeds byte limit %d at %s block %d hash=%s",
				limits.maxBytes, label, block.NumberU64(), block.Hash().String())
		}
		retainedBlocks++
		retainedBytes += blockBytes
		return nil
	}
	parent := func(label string, block *types.Block) (*types.Block, error) {
		if block.NumberU64() == 0 {
			return nil, fmt.Errorf("EVM fork activation reached divergent genesis on the %s chain", label)
		}
		number := block.NumberU64() - 1
		hash := block.ParentHash()
		parentBlock := getBlock(hash, number)
		if parentBlock == nil || parentBlock.Header() == nil || parentBlock.Header().Number == nil ||
			parentBlock.NumberU64() != number || parentBlock.Hash() != hash {
			return nil, fmt.Errorf("%s EVM fork parent is unavailable at %d hash=%s", label, number, hash.String())
		}
		return parentBlock, nil
	}

	for oldBlock.NumberU64() > newBlock.NumberU64() {
		if err := account("old", oldBlock); err != nil {
			return err
		}
		var err error
		oldBlock, err = parent("old", oldBlock)
		if err != nil {
			return err
		}
	}
	for newBlock.NumberU64() > oldBlock.NumberU64() {
		if err := account("new", newBlock); err != nil {
			return err
		}
		var err error
		newBlock, err = parent("new", newBlock)
		if err != nil {
			return err
		}
	}
	for oldBlock.Hash() != newBlock.Hash() {
		if err := account("old", oldBlock); err != nil {
			return err
		}
		if err := account("new", newBlock); err != nil {
			return err
		}
		var err error
		oldBlock, err = parent("old", oldBlock)
		if err != nil {
			return err
		}
		newBlock, err = parent("new", newBlock)
		if err != nil {
			return err
		}
	}
	return nil
}

func validateCanonicalEVMLineageWindow(
	target *types.Block,
	getBlock func(common.Hash, uint64) *types.Block,
	getCanonicalHash func(uint64) common.Hash,
	limits evmLineageLimits,
) error {
	if target == nil || target.Header() == nil || target.Header().Number == nil || getBlock == nil || getCanonicalHash == nil {
		return fmt.Errorf("canonical EVM lineage validation accessors are unavailable")
	}
	if limits.maxDepth == 0 || limits.maxBytes == 0 {
		return fmt.Errorf("canonical EVM lineage validation bounds are unavailable")
	}
	var retainedBytes uint64
	for cursor, depth := target, uint64(0); ; depth++ {
		if err := checkEVMStateReconstructionInterrupted(limits.interrupt); err != nil {
			return err
		}
		if getCanonicalHash(cursor.NumberU64()) != cursor.Hash() {
			return fmt.Errorf("canonical EVM index mismatch at block %d: indexed=%s lineage=%s",
				cursor.NumberU64(), getCanonicalHash(cursor.NumberU64()).String(), cursor.Hash().String())
		}
		blockBytes := cursor.Size()
		if blockBytes > limits.maxBytes-retainedBytes {
			return fmt.Errorf("canonical EVM lineage validation exceeds byte limit %d at block %d hash=%s",
				limits.maxBytes, cursor.NumberU64(), cursor.Hash().String())
		}
		retainedBytes += blockBytes
		if cursor.NumberU64() == 0 || depth+1 == limits.maxDepth {
			return nil
		}
		number := cursor.NumberU64() - 1
		hash := cursor.ParentHash()
		parent := getBlock(hash, number)
		if parent == nil || parent.Header() == nil || parent.Header().Number == nil ||
			parent.NumberU64() != number || parent.Hash() != hash {
			return fmt.Errorf("canonical EVM lineage parent is unavailable at %d hash=%s", number, hash.String())
		}
		cursor = parent
	}
}

func validateBoundedCanonicalTailDeletion(
	start uint64,
	getCanonicalHash func(uint64) common.Hash,
	maxEntries uint64,
	interrupt <-chan struct{},
) error {
	if getCanonicalHash == nil || maxEntries == 0 {
		return fmt.Errorf("canonical EVM tail deletion bounds are unavailable")
	}
	for entries := uint64(0); ; entries++ {
		if err := checkEVMStateReconstructionInterrupted(interrupt); err != nil {
			return err
		}
		if entries > ^uint64(0)-start {
			return fmt.Errorf("canonical EVM tail deletion range overflows after block %d", start)
		}
		number := start + entries
		if getCanonicalHash(number) == (common.Hash{}) {
			return nil
		}
		if entries >= maxEntries {
			return fmt.Errorf("canonical EVM tail deletion exceeds entry limit %d from block %d", maxEntries, start)
		}
	}
}

func (b *BdagChain) activateExistingEVMForkBounded(target *types.Block) error {
	bc := b.chain.Ether().BlockChain()
	before := bc.CurrentBlock()
	if err := validateBoundedEVMForkActivation(
		before,
		target,
		bc.GetBlock,
		evmLineageLimits{
			maxDepth:  maxAutomaticEVMReconstructionDepth,
			maxBytes:  maxAutomaticEVMReconstructionBytes,
			interrupt: b.recoveryInterrupt(),
		},
	); err != nil {
		return err
	}
	if target.ParentHash() != before.Hash() {
		if err := validateBoundedCanonicalTailDeletion(
			target.NumberU64(),
			bc.GetCanonicalHash,
			maxAutomaticEVMReconstructionDepth,
			b.recoveryInterrupt(),
		); err != nil {
			return err
		}
	}
	if err := checkEVMStateReconstructionInterrupted(b.recoveryInterrupt()); err != nil {
		return err
	}
	afterValidation := bc.CurrentBlock()
	if before == nil || afterValidation == nil || before.Number == nil || afterValidation.Number == nil ||
		before.Number.Uint64() != afterValidation.Number.Uint64() || before.Hash() != afterValidation.Hash() {
		return fmt.Errorf("EVM head changed during bounded fork activation preflight")
	}
	_, err := bc.SetCanonicalHeadAtomic(
		target,
		maxAutomaticEVMReconstructionDepth,
		maxAutomaticEVMReconstructionBytes,
	)
	return err
}

func (b *BdagChain) validateRecentCanonicalEVMLineage(target *types.Block) error {
	bc := b.chain.Ether().BlockChain()
	return validateCanonicalEVMLineageWindow(
		target,
		bc.GetBlock,
		bc.GetCanonicalHash,
		evmLineageLimits{
			maxDepth:  maxAutomaticEVMReconstructionDepth,
			maxBytes:  maxAutomaticEVMReconstructionBytes,
			interrupt: b.recoveryInterrupt(),
		},
	)
}

func stageAndActivateExactEVMLineage(
	anchor *types.Block,
	blocks types.Blocks,
	hasState func(common.Hash) bool,
	importWithoutHead func(*types.Block) error,
	commitRoot func(common.Hash) error,
	headUnchanged func() bool,
	activate func(*types.Block) error,
	interrupt <-chan struct{},
) (uint64, error) {
	if len(blocks) == 0 {
		return 0, nil
	}
	if anchor == nil || anchor.Header() == nil || anchor.Header().Number == nil ||
		hasState == nil || importWithoutHead == nil || commitRoot == nil || headUnchanged == nil || activate == nil {
		return 0, fmt.Errorf("EVM state reconstruction staging accessors are unavailable")
	}
	if !hasState(anchor.Root()) {
		return 0, fmt.Errorf("EVM state reconstruction anchor is not stateful: number=%d hash=%s root=%s",
			anchor.NumberU64(), anchor.Hash().String(), anchor.Root().String())
	}

	previous := anchor
	for i, block := range blocks {
		if block == nil || block.Header() == nil || block.Header().Number == nil {
			return 0, fmt.Errorf("EVM state reconstruction block %d is unavailable", i)
		}
		if block.NumberU64() != previous.NumberU64()+1 || block.ParentHash() != previous.Hash() {
			return 0, fmt.Errorf("EVM state reconstruction lineage is not contiguous at index %d: parent=%d %s block=%d %s blockParent=%s",
				i, previous.NumberU64(), previous.Hash().String(), block.NumberU64(), block.Hash().String(), block.ParentHash().String())
		}
		previous = block
	}
	if !headUnchanged() {
		return 0, fmt.Errorf("EVM head changed before state reconstruction staging")
	}

	for i, block := range blocks {
		if err := checkEVMStateReconstructionInterrupted(interrupt); err != nil {
			return 0, err
		}
		if !headUnchanged() {
			return 0, fmt.Errorf("EVM head changed during state reconstruction before block %d", block.NumberU64())
		}
		if !hasState(block.Root()) {
			if err := importWithoutHead(block); err != nil {
				return 0, fmt.Errorf("stage EVM reconstruction block index=%d number=%d hash=%s: %w",
					i, block.NumberU64(), block.Hash().String(), err)
			}
		}
		if !hasState(block.Root()) {
			return 0, fmt.Errorf("staged EVM reconstruction block is not stateful: number=%d hash=%s root=%s",
				block.NumberU64(), block.Hash().String(), block.Root().String())
		}
		if !headUnchanged() {
			return 0, fmt.Errorf("non-head EVM state reconstruction import changed the canonical head at block %d hash=%s",
				block.NumberU64(), block.Hash().String())
		}
	}

	target := blocks[len(blocks)-1]
	if err := commitRoot(target.Root()); err != nil {
		return 0, fmt.Errorf("commit staged EVM reconstruction root %s at block %d: %w", target.Root().String(), target.NumberU64(), err)
	}
	if err := checkEVMStateReconstructionInterrupted(interrupt); err != nil {
		return 0, err
	}
	if !headUnchanged() {
		return 0, fmt.Errorf("EVM head changed before staged state reconstruction activation")
	}
	if err := activate(target); err != nil {
		return 0, fmt.Errorf("activate staged EVM reconstruction target %d hash=%s: %w", target.NumberU64(), target.Hash().String(), err)
	}
	return uint64(len(blocks)), nil
}

func (b *BdagChain) recoveryInterrupt() <-chan struct{} {
	if b == nil || b.consensus == nil {
		return nil
	}
	return b.consensus.Interrupt()
}

func findRecordedEVMReplayTargetState(number, maxOrder uint64, getState func(uint64) model.BlockState) model.BlockState {
	if getState == nil {
		return nil
	}
	var target model.BlockState
	for low, high := uint64(0), maxOrder; low <= high; {
		order := low + (high-low)/2
		state := getState(order)
		if state == nil {
			return nil
		}
		recordedNumber := state.GetEVMNumber()
		if recordedNumber < number {
			low = order + 1
			continue
		}
		if recordedNumber == number {
			target = state
		}
		if order == 0 {
			break
		}
		high = order - 1
	}
	return target
}

func (b *BdagChain) recordedEVMReplayTarget(number uint64, supplied model.BlockState) (model.BlockState, error) {
	target := supplied
	if target == nil {
		if b == nil || b.consensus == nil || b.consensus.BlockChain() == nil {
			return nil, fmt.Errorf("recorded EVM replay target %d is unavailable", number)
		}
		nativeChain := b.consensus.BlockChain()
		tip := nativeChain.GetMainChainTip()
		if tip == nil || tip.GetState() == nil || tip.GetState().GetEVMNumber() < number {
			return nil, fmt.Errorf("native chain cannot resolve recorded EVM replay target %d", number)
		}
		target = findRecordedEVMReplayTargetState(number, uint64(tip.GetOrder()), func(order uint64) model.BlockState {
			block := nativeChain.GetBlockByOrder(order)
			if block == nil {
				return nil
			}
			return block.GetState()
		})
	}
	if target == nil || !hasRecordedEVMCommitment(target) || target.GetStatus().KnownInvalid() {
		return nil, fmt.Errorf("recorded EVM replay target %d is not recoverable", number)
	}
	if target.GetEVMNumber() != number {
		return nil, fmt.Errorf("recorded EVM replay target number mismatch: got %d want %d", target.GetEVMNumber(), number)
	}
	return target, nil
}

// ImportStoredCanonicalEVMRange restores a bounded gap from the exact parent
// lineage of the next recorded native EVM commitment.
func (b *BdagChain) ImportStoredCanonicalEVMRange(first, last uint64) (uint64, error) {
	return b.importStoredCanonicalEVMRange(first, last, nil)
}

// ImportStoredCanonicalEVMRangeForState restores a gap from the exact immutable
// EVM commitment recorded by the native state currently being replayed. Callers
// which already hold that state must use this form instead of resolving another
// native order with the same EVM number.
func (b *BdagChain) ImportStoredCanonicalEVMRangeForState(first, last uint64, target model.BlockState) (uint64, error) {
	if target == nil {
		return 0, fmt.Errorf("recorded EVM replay target state is unavailable")
	}
	return b.importStoredCanonicalEVMRange(first, last, target)
}

func (b *BdagChain) importStoredCanonicalEVMRange(first, last uint64, suppliedTarget model.BlockState) (uint64, error) {
	if b == nil || b.chain == nil || b.chain.Ether() == nil || b.chain.Ether().BlockChain() == nil {
		return 0, fmt.Errorf("bdag EVM chain is unavailable")
	}
	if last < first {
		return 0, nil
	}
	if first == 0 {
		return 0, fmt.Errorf("stored EVM replay cannot start before genesis")
	}
	if last-first >= maxStoredEVMRecoveryReplay {
		return 0, fmt.Errorf("stored EVM replay range %d..%d exceeds automatic limit %d", first, last, maxStoredEVMRecoveryReplay)
	}
	if last == ^uint64(0) {
		return 0, fmt.Errorf("stored EVM replay target number overflows after %d", last)
	}

	bc := b.chain.Ether().BlockChain()
	cur := bc.CurrentBlock()
	if cur == nil || cur.Number == nil {
		return 0, fmt.Errorf("EVM head is unavailable before stored replay")
	}
	if cur.Number.Uint64()+1 != first {
		return 0, fmt.Errorf("stored EVM replay is not contiguous with head: head=%d first=%d", cur.Number.Uint64(), first)
	}

	targetNumber := last + 1
	recordedTarget, err := b.recordedEVMReplayTarget(targetNumber, suppliedTarget)
	if err != nil {
		return 0, err
	}
	target := bc.GetBlock(recordedTarget.GetEVMHash(), targetNumber)
	if target == nil {
		return 0, fmt.Errorf("exact recorded EVM replay target block is unavailable: number=%d hash=%s",
			targetNumber, recordedTarget.GetEVMHash().String())
	}
	anchor, lineage, err := collectExactEVMLineage(
		target,
		targetNumber,
		recordedTarget.GetEVMHash(),
		recordedTarget.GetEVMRoot(),
		bc.GetBlock,
		func(block *types.Block) bool {
			return block.NumberU64() == cur.Number.Uint64() && block.Hash() == cur.Hash()
		},
		evmLineageLimits{
			maxDepth:  maxAutomaticEVMReconstructionDepth,
			maxBytes:  maxAutomaticEVMReconstructionBytes,
			interrupt: b.recoveryInterrupt(),
		},
	)
	if err != nil {
		return 0, fmt.Errorf("collect exact recorded EVM replay lineage: %w", err)
	}
	expectedLineage := targetNumber - cur.Number.Uint64()
	if uint64(len(lineage)) != expectedLineage || len(lineage) < 2 {
		return 0, fmt.Errorf("exact recorded EVM replay lineage length mismatch: got %d want %d", len(lineage), expectedLineage)
	}
	gap := lineage[:len(lineage)-1]
	if gap[0].NumberU64() != first || gap[len(gap)-1].NumberU64() != last || target.ParentHash() != gap[len(gap)-1].Hash() {
		return 0, fmt.Errorf("exact recorded EVM replay lineage does not cover requested gap %d..%d", first, last)
	}
	replayed, err := b.stageExactEVMLineage(anchor, gap)
	if err != nil {
		return 0, fmt.Errorf("stage exact recorded EVM replay gap %d..%d: %w", first, last, err)
	}
	return replayed, nil
}

func (b *BdagChain) stageExactEVMLineage(anchor *types.Block, blocks types.Blocks) (uint64, error) {
	bc := b.chain.Ether().BlockChain()
	original := bc.CurrentBlock()
	if original == nil || original.Number == nil {
		return 0, fmt.Errorf("current EVM head is unavailable before state reconstruction")
	}
	originalNumber := original.Number.Uint64()
	originalHash := original.Hash()
	headUnchanged := func() bool {
		head := bc.CurrentBlock()
		return head != nil && head.Number != nil && head.Number.Uint64() == originalNumber && head.Hash() == originalHash
	}
	activate := func(target *types.Block) error {
		if exactEVMLineageIsCanonical(anchor, blocks, bc.GetCanonicalHash) {
			return bc.SetHeadToExistingBlock(target.Hash(), target.NumberU64())
		}
		log.Warn("Activating exact recorded EVM fork after staged state reconstruction",
			"from.number", originalNumber,
			"from.hash", originalHash.String(),
			"target.number", target.NumberU64(),
			"target.hash", target.Hash().String())
		return b.activateExistingEVMForkBounded(target)
	}
	replayed, err := stageAndActivateExactEVMLineage(
		anchor,
		blocks,
		bc.HasState,
		func(block *types.Block) error {
			_, err := bc.InsertBlockWithoutSetHead(block, false)
			return err
		},
		b.CommitEVMStateRoot,
		headUnchanged,
		activate,
		b.recoveryInterrupt(),
	)
	if err != nil {
		return replayed, err
	}
	return replayed, nil
}

func (b *BdagChain) rebuildMissingEVMTargetState(target *types.Block, state model.BlockState) error {
	bc := b.chain.Ether().BlockChain()
	if state == nil {
		return fmt.Errorf("recorded EVM state reconstruction target is unavailable")
	}
	anchor, blocks, err := collectMissingEVMStatePath(
		target,
		state.GetEVMNumber(),
		state.GetEVMHash(),
		state.GetEVMRoot(),
		bc.GetBlock,
		bc.HasState,
		evmLineageLimits{
			maxDepth:  maxAutomaticEVMReconstructionDepth,
			maxBytes:  maxAutomaticEVMReconstructionBytes,
			interrupt: b.recoveryInterrupt(),
		},
	)
	if err != nil {
		return err
	}
	if len(blocks) == 0 {
		return nil
	}

	log.Warn("Reconstructing missing EVM recovery target state from exact stored lineage",
		"target.number", target.NumberU64(),
		"target.hash", target.Hash().String(),
		"target.root", target.Root().String(),
		"anchor.number", anchor.NumberU64(),
		"anchor.hash", anchor.Hash().String(),
		"replayBlocks", len(blocks))

	replayed, err := b.stageExactEVMLineage(anchor, blocks)
	if err != nil {
		return fmt.Errorf("stage exact EVM lineage to target %d hash=%s: %w", target.NumberU64(), target.Hash().String(), err)
	}
	after := bc.CurrentBlock()
	if after == nil || after.Number == nil || after.Number.Uint64() != target.NumberU64() || after.Hash() != target.Hash() || after.Root != target.Root() || !bc.HasState(after.Root) {
		return fmt.Errorf("EVM state reconstruction did not land on stateful target %d hash=%s root=%s", target.NumberU64(), target.Hash().String(), target.Root().String())
	}
	log.Info("Reconstructed and committed missing EVM recovery target state",
		"target.number", target.NumberU64(),
		"target.hash", target.Hash().String(),
		"target.root", target.Root().String(),
		"anchor.number", anchor.NumberU64(),
		"replayed", replayed)
	return nil
}

func (b *BdagChain) GetEVMHeader(hash common.Hash, number uint64) *types.Header {
	if b == nil || b.chain == nil || b.chain.Ether() == nil || b.chain.Ether().BlockChain() == nil {
		return nil
	}
	return b.chain.Ether().BlockChain().GetHeader(hash, number)
}

func (b *BdagChain) nativeEVMRebuildAnchor(curBlockHeader *types.Header, target model.BlockState) (model.BlockState, uint64, bool) {
	if curBlockHeader == nil || target == nil || b == nil || b.consensus == nil || b.consensus.BlockChain() == nil {
		return nil, 0, false
	}

	if finder, ok := b.consensus.BlockChain().(lockedEVMNumberBlockFinder); ok {
		if ib := finder.GetBlockByNumberNoLock(curBlockHeader.Number.Uint64()); ib != nil {
			if state := ib.GetState(); isNativeEVMRebuildAnchor(state, uint64(ib.GetOrder()), curBlockHeader, target) {
				return state, uint64(ib.GetOrder()), true
			}
		}
	}

	genesis := b.chain.Ether().BlockChain().Genesis()
	if genesis == nil {
		return nil, 0, false
	}
	genesisState := b.consensus.BlockChain().GetBlockState(0)
	if isCurrentEVMGenesis(curBlockHeader, genesis.Header()) && isNativeEVMRebuildAnchor(genesisState, 0, genesis.Header(), target) {
		return genesisState, 0, true
	}
	return nil, 0, false
}

func (b *BdagChain) rebuildEVMFromNativeChain(anchorState model.BlockState, anchorOrder uint64, target model.BlockState, getError func(string) error) (*types.Header, error) {
	if anchorState == nil || target == nil {
		return nil, getError("native EVM rebuild anchor is unavailable")
	}
	if anchorOrder > target.GetOrder() {
		return nil, getError(fmt.Sprintf("native EVM rebuild anchor order %d is ahead of target order %d", anchorOrder, target.GetOrder()))
	}
	if nativeEVMRebuildFromGenesisExceedsLimit(anchorOrder, target.GetOrder()) {
		return nil, getError(fmt.Sprintf("native EVM rebuild from genesis to order %d exceeds safe replay limit %d; restore a canonical snapshot or provide a closer clean EVM state", target.GetOrder(), maxNativeEVMRebuildFromGenesis))
	}

	if err := b.RewindTo(anchorState); err != nil {
		return nil, err
	}

	bc := b.chain.Ether().BlockChain()
	startOrder := anchorOrder + 1
	targetOrder := target.GetOrder()
	if startOrder > targetOrder {
		cur := bc.CurrentBlock()
		if err := validateExactEVMRewindPostcondition(target, cur, bc.GetBlock, bc.HasState); err == nil {
			return cur, nil
		}
		return nil, getError("native EVM rebuild target is not reachable from anchor")
	}

	log.Warn(
		"Local EVM data is not contiguous with native chain; rebuilding EVM state from native chain data",
		"from.order", startOrder,
		"to.order", targetOrder,
		"anchor.evmNumber", anchorState.GetEVMNumber(),
		"target.evmNumber", target.GetEVMNumber(),
	)

	var rebuilt uint64
	rebuildStart := time.Now()
	for order := startOrder; order <= targetOrder; order++ {
		stepStart := time.Now()
		select {
		case <-b.consensus.Interrupt():
			return nil, getError("native EVM rebuild interrupted")
		default:
		}

		stateStart := time.Now()
		recState := b.consensus.BlockChain().GetBlockState(order)
		stateLookupElapsed := time.Since(stateStart)
		if recState == nil {
			return nil, getError(fmt.Sprintf("missing native block state at order %d", order))
		}
		if !isRecoverableEVMStateAtOrder(recState, order) {
			log.Warn("Skipping unrecoverable native block state during EVM rebuild", "requested.order", order, "state.order", recState.GetOrder(), "evm.number", recState.GetEVMNumber(), "evm.hash", recState.GetEVMHash().String())
			continue
		}
		if recState.GetStatus().KnownInvalid() {
			continue
		}

		cur := bc.CurrentBlock()
		if cur.Number.Uint64() == recState.GetEVMNumber() && cur.Hash() == recState.GetEVMHash() {
			if err := validateExactEVMRewindPostcondition(recState, cur, bc.GetBlock, bc.HasState); err == nil {
				continue
			}
		}
		if recState.GetEVMNumber() > cur.Number.Uint64()+1 {
			if _, err := b.importStoredCanonicalEVMRange(cur.Number.Uint64()+1, recState.GetEVMNumber()-1, recState); err != nil {
				return nil, getError(fmt.Sprintf("fill stored EVM gap before native order %d: %v", order, err))
			}
			cur = bc.CurrentBlock()
		}
		if cur.Number.Uint64()+1 != recState.GetEVMNumber() {
			return nil, getError(fmt.Sprintf("native EVM rebuild is not sequential at order %d: head=%d recorded=%d", order, cur.Number.Uint64(), recState.GetEVMNumber()))
		}

		nativeBlockStart := time.Now()
		sb, err := b.consensus.BlockChain().BlockByOrder(order)
		nativeBlockElapsed := time.Since(nativeBlockStart)
		if err != nil {
			return nil, getError(err.Error())
		}
		buildStart := time.Now()
		eb, err := BuildEVMBlock(sb)
		if err != nil {
			return nil, getError(err.Error())
		}

		admissibility, err := b.replayGovernanceAdmissibilityAtOrder(order)
		if err != nil {
			return nil, getError(err.Error())
		}
		block, _, _, err := b.buildBlock(cur, eb.Transactions(), eb.Timestamp().Unix(), sb.Block().Header.CoinbaseAddress, sb.Block().Header.Reward, admissibility)
		if err != nil {
			return nil, getError(err.Error())
		}
		if err := validateStrictRecoveredEVMState(order, recState, block.Header()); err != nil {
			return nil, getError(err.Error())
		}
		buildElapsed := time.Since(buildStart)

		insertStart := time.Now()
		st, err := bc.InsertChain(types.Blocks{block})
		insertElapsed := time.Since(insertStart)
		if err != nil {
			return nil, err
		}
		if st != 1 {
			return nil, getError("insert chain")
		}

		rebuilt++
		stepElapsed := time.Since(stepStart)
		if shouldLogEVMRecoveryTiming(rebuilt, stepElapsed) || order == targetOrder {
			head := bc.CurrentBlock()
			log.Info("[DEBUG-evm-rewind] native rebuild step",
				"rebuilt", rebuilt,
				"order", order,
				"target.order", targetOrder,
				"evm.number", head.Number.Uint64(),
				"stateLookup", stateLookupElapsed,
				"nativeBlockLookup", nativeBlockElapsed,
				"build", buildElapsed,
				"insert", insertElapsed,
				"step", stepElapsed,
				"total", time.Since(rebuildStart),
			)
		}
		if rebuilt == 1 || rebuilt%10000 == 0 || order == targetOrder {
			head := bc.CurrentBlock()
			log.Info(
				"Native EVM rebuild progress",
				"rebuilt", rebuilt,
				"order", order,
				"target.order", targetOrder,
				"evm.number", head.Number.Uint64(),
				"evm.hash", head.Hash().String(),
			)
		}
	}

	cur := bc.CurrentBlock()
	if err := validateExactEVMRewindPostcondition(target, cur, bc.GetBlock, bc.HasState); err == nil {
		return cur, nil
	}
	return nil, getError("prepare environment")
}

func (b *BdagChain) prepareEnvironment(state model.BlockState) (*types.Header, error) {
	if state == nil {
		return nil, fmt.Errorf("bdag chain env error: target block state is nil")
	}
	if b == nil {
		return nil, fmt.Errorf("bdag chain env error: EVM chain is unavailable")
	}
	if err := checkEVMStateReconstructionInterrupted(b.recoveryInterrupt()); err != nil {
		return nil, err
	}
	bc := evmRewindChainProvider(b)
	if bc == nil {
		return nil, fmt.Errorf("bdag chain env error: EVM chain is unavailable")
	}
	curBlockHeader := bc.CurrentBlock()
	if curBlockHeader == nil || curBlockHeader.Number == nil {
		return nil, fmt.Errorf("bdag chain env error: current EVM head is unavailable")
	}
	var unsafeRewindErr error
	if curBlockHeader.Number.Uint64() > state.GetEVMNumber() {
		err := b.RewindTo(state)
		if err != nil {
			// RewindTo may have changed the underlying head before detecting an
			// unsafe postcondition. Never carry the pre-rewind snapshot into the
			// bounded recovery scan or report success from stale evidence.
			freshHead, rereadErr := rereadEVMHeadAfterRewindFailure(err, bc.CurrentBlock)
			if rereadErr != nil {
				return nil, rereadErr
			}
			curBlockHeader = freshHead
			if !isUnsafeEVMRewindError(err) {
				return nil, err
			}
			unsafeRewindErr = err
			log.Warn("Direct EVM rewind target is unavailable; scanning for a stateful recovery anchor",
				"err", err,
				"target.evmNumber", state.GetEVMNumber(),
				"target.evmHash", state.GetEVMHash())
		} else {
			curBlockHeader = bc.CurrentBlock()
			if curBlockHeader == nil || curBlockHeader.Number == nil {
				return nil, fmt.Errorf("bdag chain env error: current EVM head is unavailable after rewind")
			}
		}
	}
	if err := validateExactEVMRewindPostcondition(state, curBlockHeader, bc.GetBlock, bc.HasState); err == nil {
		b.clearEVMRecoveryFailure()
		return curBlockHeader, nil
	}
	getError := func(msg string) error {
		return fmt.Errorf("bdag chain env error:targetEVM.number=%d, targetEVM.hash=%s, targetState.order=%d, cur.number=%d, cur.hash=%s, %s", state.GetEVMNumber(), state.GetEVMHash().String(), state.GetOrder(), curBlockHeader.Number, curBlockHeader.Hash().String(), msg)
	}
	if state.GetOrder() <= 0 {
		if unsafeRewindErr != nil {
			return nil, fmt.Errorf("%w: %s", unsafeRewindErr, getError("reach genesis"))
		}
		return nil, getError("reach genesis")
	}
	if !isRecoverableEVMState(state) {
		return nil, getError("target BDAG block state is unordered")
	}
	currentEVMNumber := curBlockHeader.Number.Uint64()
	log.Info("Start to find cur block state", "state.order", state.GetOrder(), "evm.Number", state.GetEVMNumber(), "cur.number", curBlockHeader.Number.Uint64())
	if err := b.memoizedEVMRecoveryFailure(curBlockHeader, state); err != nil {
		log.Warn("Skipping repeated BDAG EVM recovery anchor scan after prior miss",
			"current.evmNumber", currentEVMNumber,
			"target.evmNumber", state.GetEVMNumber(),
			"target.order", state.GetOrder(),
			"err", err)
		return nil, err
	}
	scanStart := time.Now()
	if state.GetEVMNumber() > currentEVMNumber && state.GetEVMNumber()-currentEVMNumber > maxEVMRecoveryAnchorBacktrack {
		if nativeAnchorState, nativeAnchorOrder, ok := b.nativeEVMRebuildAnchor(curBlockHeader, state); ok {
			log.Warn(
				"Local EVM head is too far behind native chain for anchor scan; rebuilding from native chain data",
				"current.evmNumber", currentEVMNumber,
				"target.evmNumber", state.GetEVMNumber(),
				"anchor.order", nativeAnchorOrder,
				"anchor.evmNumber", nativeAnchorState.GetEVMNumber(),
			)
			return b.rebuildEVMFromNativeChain(nativeAnchorState, nativeAnchorOrder, state, getError)
		}
	}
	var (
		curBlockState             model.BlockState
		fallbackState             model.BlockState
		recoveryOrder             uint64
		fallbackOrder             uint64
		skippedStatelessAnchors   uint64
		firstStatelessAnchorOrder uint64
		lastStatelessAnchorOrder  uint64
		lastStatelessAnchorErr    error
	)
	list := []evmRecoveryStep{{order: state.GetOrder(), state: state}}
	missingGap := 0
	var scanned, missingStates, unrecoverableStates uint64
	for order := state.GetOrder(); order > 0; order-- {
		if err := checkEVMStateReconstructionInterrupted(b.recoveryInterrupt()); err != nil {
			return nil, err
		}
		requestedOrder := order - 1
		scanned++
		prevState := b.consensus.BlockChain().GetBlockState(requestedOrder)
		if prevState == nil {
			missingStates++
			missingGap++
			log.Warn("Missing BDAG block state during recovery", "state.order", requestedOrder, "gap", missingGap)
			if missingGap >= maxMissingEVMRecoveryGap {
				log.Warn("Aborting BDAG state scan due to consecutive gaps", "missingGap", missingGap, "lastChecked.order", requestedOrder)
				break
			}
			continue
		}
		if !isRecoverableEVMStateAtOrder(prevState, requestedOrder) {
			unrecoverableStates++
			log.Warn("Skipping unrecoverable BDAG block state during EVM recovery scan", "requested.order", requestedOrder, "state.order", prevState.GetOrder(), "evm.number", prevState.GetEVMNumber(), "evm.hash", prevState.GetEVMHash().String())
			continue
		}
		if prevState.GetOrder() != requestedOrder {
			log.Warn("Recovering BDAG state with mismatched embedded order", "requested.order", requestedOrder, "state.order", prevState.GetOrder(), "evm.number", prevState.GetEVMNumber(), "evm.hash", prevState.GetEVMHash().String())
		}
		missingGap = 0
		list = append(list, evmRecoveryStep{order: requestedOrder, state: prevState})
		if isRecoverableEVMAnchorCandidateAtOrder(prevState, requestedOrder, currentEVMNumber) {
			if err := validateSafeEVMRewindTarget(curBlockHeader, prevState, bc.GetHeader, bc.HasState); err == nil {
				if isBetterEVMRecoveryAnchor(prevState, requestedOrder, fallbackState, fallbackOrder, currentEVMNumber) {
					fallbackState = prevState
					fallbackOrder = requestedOrder
				}
			} else if fallbackState == nil {
				skippedStatelessAnchors++
				if firstStatelessAnchorOrder == 0 {
					firstStatelessAnchorOrder = requestedOrder
				}
				lastStatelessAnchorOrder = requestedOrder
				lastStatelessAnchorErr = err
				if shouldLogRecoveryScanSkip(skippedStatelessAnchors) {
					log.Warn("Skipping BDAG state as recovery anchor because local EVM target is not stateful",
						"requested.order", requestedOrder,
						"state.order", prevState.GetOrder(),
						"evm.number", prevState.GetEVMNumber(),
						"evm.hash", prevState.GetEVMHash().String(),
						"skipped", skippedStatelessAnchors,
						"err", err)
				}
			}
		}
		if prevState.GetEVMNumber() == curBlockHeader.Number.Uint64() &&
			prevState.GetEVMHash() == curBlockHeader.Hash() &&
			validateExactEVMRewindPostcondition(prevState, curBlockHeader, bc.GetBlock, bc.HasState) == nil {
			curBlockState = prevState
			recoveryOrder = requestedOrder
			break
		}
		if prevState.GetEVMNumber() < curBlockHeader.Number.Uint64() {
			if fallbackState != nil {
				break
			}
			if currentEVMNumber-prevState.GetEVMNumber() >= maxEVMRecoveryAnchorBacktrack {
				log.Warn("Aborting BDAG state scan because no local EVM recovery anchor was found within backtrack limit", "current.evmNumber", currentEVMNumber, "last.evmNumber", prevState.GetEVMNumber(), "limit", maxEVMRecoveryAnchorBacktrack)
				break
			}
		}
	}
	log.Info("[DEBUG-evm-rewind] anchor scan complete",
		"target.order", state.GetOrder(),
		"target.evmNumber", state.GetEVMNumber(),
		"current.evmNumber", currentEVMNumber,
		"scanned", scanned,
		"recoverySteps", len(list),
		"missingStates", missingStates,
		"unrecoverableStates", unrecoverableStates,
		"foundExact", curBlockState != nil,
		"hasFallback", fallbackState != nil,
		"skippedStatelessAnchors", skippedStatelessAnchors,
		"elapsed", time.Since(scanStart),
	)
	if skippedStatelessAnchors > 0 {
		log.Warn("BDAG EVM recovery scan skipped stateless local anchors",
			"skipped", skippedStatelessAnchors,
			"first.order", firstStatelessAnchorOrder,
			"last.order", lastStatelessAnchorOrder,
			"last.err", lastStatelessAnchorErr)
	}
	if curBlockState == nil {
		if fallbackState == nil {
			if nativeAnchorState, nativeAnchorOrder, ok := b.nativeEVMRebuildAnchor(curBlockHeader, state); ok {
				return b.rebuildEVMFromNativeChain(nativeAnchorState, nativeAnchorOrder, state, getError)
			}
			err := getError(fmt.Sprintf("can't find a recent stateful local EVM recovery anchor; refusing destructive genesis fallback (backtrackLimit=%d skippedStatelessAnchors=%d)",
				maxEVMRecoveryAnchorBacktrack, skippedStatelessAnchors))
			b.rememberEVMRecoveryFailure(curBlockHeader, state, err)
			return nil, err
		}
		log.Warn("Falling back to closest stateful local EVM recovery anchor",
			"fallback.order", fallbackState.GetOrder(),
			"fallback.evmNumber", fallbackState.GetEVMNumber(),
			"fallback.evmHash", fallbackState.GetEVMHash())
		curBlockState = fallbackState
		recoveryOrder = fallbackOrder
	}
	if err := checkEVMStateReconstructionInterrupted(b.recoveryInterrupt()); err != nil {
		return nil, err
	}
	if curBlockHeader.Number.Uint64() > curBlockState.GetEVMNumber() ||
		curBlockHeader.Hash() != curBlockState.GetEVMHash() {
		if err := b.RewindTo(curBlockState); err != nil {
			return nil, err
		}
		curBlockHeader = b.chain.Ether().BlockChain().CurrentBlock()
	}
	log.Info("Find cur block state", "state.order", curBlockState.GetOrder(), "evm.Number", curBlockState.GetEVMNumber())
	filtered := make([]evmRecoveryStep, 0, len(list))
	for _, step := range list {
		if isRecoverableEVMStateAtOrder(step.state, step.order) && step.order >= recoveryOrder {
			filtered = append(filtered, step)
		}
	}
	list = filtered
	restoreStart := time.Now()
	var restored uint64
	for i := len(list) - 1; i >= 0; i-- {
		// This legacy restore path is also used by startup recovery. Honour
		// shutdown between blocks so Stop can join the worker before DB close.
		if err := checkEVMStateReconstructionInterrupted(b.recoveryInterrupt()); err != nil {
			return nil, err
		}
		stepStart := time.Now()
		step := list[i]
		recState := step.state
		if !isRecoverableEVMStateAtOrder(recState, step.order) {
			log.Warn("Skipping unrecoverable BDAG block state during EVM recovery", "requested.order", step.order, "state.order", recState.GetOrder(), "evm.number", recState.GetEVMNumber(), "evm.hash", recState.GetEVMHash().String())
			continue
		}
		if recState.GetStatus().KnownInvalid() {
			continue
		}
		targetNumber := recState.GetEVMNumber()
		cur := bc.CurrentBlock()
		if cur.Number.Uint64() == targetNumber &&
			recState.GetEVMHash() == cur.Hash() &&
			validateExactEVMRewindPostcondition(recState, cur, bc.GetBlock, bc.HasState) == nil {
			continue
		}
		forceSequentialRebuild := shouldForceSequentialEVMRebuild(
			cur.Number.Uint64(),
			cur.Hash(),
			targetNumber,
			recState.GetEVMHash(),
		)
		if forceSequentialRebuild {
			log.Warn(
				"Recorded EVM state is not ahead of current recovery head; rebuilding sequentially",
				"requested.order", step.order,
				"state.order", recState.GetOrder(),
				"current.evmNumber", cur.Number.Uint64(),
				"recorded.evmNumber", targetNumber,
				"recorded.evmHash", recState.GetEVMHash().String(),
			)
		}
		if !forceSequentialRebuild && targetNumber > cur.Number.Uint64()+1 {
			if _, err := b.importStoredCanonicalEVMRange(cur.Number.Uint64()+1, targetNumber-1, recState); err != nil {
				return nil, getError(fmt.Sprintf("fill stored EVM gap before native order %d: %v", step.order, err))
			}
			cur = bc.CurrentBlock()
		}
		log.Debug("Try to restore block state for EVM", "evm.hash", recState.GetEVMHash().String(), "evm.number", recState.GetEVMNumber(), "requested.order", step.order, "state.order", recState.GetOrder())
		storedBlockLookupStart := time.Now()
		block := b.chain.Ether().BlockChain().GetBlock(recState.GetEVMHash(), recState.GetEVMNumber())
		storedBlockLookupElapsed := time.Since(storedBlockLookupStart)
		if forceSequentialRebuild {
			block = nil
		}
		var nativeBlockElapsed, buildElapsed time.Duration
		rebuiltBlock := false
		if block != nil && !forceSequentialRebuild && targetNumber > 0 && cur.Number.Uint64()+1 != targetNumber {
			parentBlock := bc.GetBlockByNumber(targetNumber - 1)
			if hasRecordedEVMParentGap(cur.Number.Uint64(), targetNumber, parentBlock != nil) {
				log.Warn(
					"Recorded EVM block has a parent gap; rebuilding sequentially from current head",
					"requested.order", step.order,
					"state.order", recState.GetOrder(),
					"current.evmNumber", cur.Number.Uint64(),
					"recorded.evmNumber", targetNumber,
					"recorded.evmHash", recState.GetEVMHash().String(),
				)
				block = nil
			}
		}
		if block == nil {
			rebuiltBlock = true
			log.Info("Try to rebuild evm block", "requested.order", step.order, "state.order", recState.GetOrder())
			nativeBlockStart := time.Now()
			sb, err := b.consensus.BlockChain().BlockByOrder(step.order)
			nativeBlockElapsed = time.Since(nativeBlockStart)
			if err != nil {
				return nil, getError(err.Error())
			}
			// dtxs := list[i].GetDuplicateTxs()
			// if len(dtxs) > 0 {
			// 	for _, index := range dtxs {
			// 		sb.Transactions()[index].IsDuplicate = true
			// 	}
			// }

			buildStart := time.Now()
			eb, err := BuildEVMBlock(sb)
			if err != nil {
				return nil, getError(err.Error())
			}
			if len(eb.Transactions()) <= 0 {
				log.Warn("Rebuilt EVM block has no transactions; consensus block was empty", "requested.order", step.order, "state.order", recState.GetOrder(), "evm.number", recState.GetEVMNumber(), "evm.hash", recState.GetEVMHash().String(), "dag.txCount", len(sb.Block().Transactions))
			}
			parent := cur
			if !forceSequentialRebuild && targetNumber > 0 && parent.Number.Uint64()+1 != targetNumber {
				parentBlock := bc.GetBlockByNumber(targetNumber - 1)
				if parentBlock == nil {
					log.Warn(
						"Recorded EVM state has a parent gap; rebuilding sequentially from current head",
						"requested.order", step.order,
						"state.order", recState.GetOrder(),
						"current.evmNumber", parent.Number.Uint64(),
						"recorded.evmNumber", targetNumber,
						"recorded.evmHash", recState.GetEVMHash().String(),
					)
				} else {
					parent = parentBlock.Header()
				}
			}
			// Get BlueScore for the re-built block
			// blueScore := b.getBlueScore(sb.Hash())

			// Recovery rebuild of an already-validated historical block: the
			// blocklist was enforced (if applicable) at original connect time,
			// and enforcement never alters the tx set, so re-enforcing here is
			// unnecessary and must not be able to fail the rebuild.
			admissibility, admissibilityErr := b.replayGovernanceAdmissibilityAtOrder(step.order)
			if admissibilityErr != nil {
				return nil, getError(admissibilityErr.Error())
			}
			block, _, _, err = b.buildBlock(parent, eb.Transactions(), eb.Timestamp().Unix(), sb.Block().Header.CoinbaseAddress, sb.Block().Header.Reward, admissibility)
			if err != nil {
				return nil, getError(err.Error())
			}
			buildElapsed = time.Since(buildStart)
		}
		if err := validateStrictRecoveredEVMState(step.order, recState, block.Header()); err != nil {
			return nil, getError(err.Error())
		}
		insertStart := time.Now()
		st, err := b.chain.Ether().BlockChain().InsertChain(types.Blocks{block})
		insertElapsed := time.Since(insertStart)
		if err != nil {
			return nil, err
		}
		if st != 1 {
			return nil, getError("insert chain")
		}
		restored++
		stepElapsed := time.Since(stepStart)
		if shouldLogEVMRecoveryTiming(restored, stepElapsed) || i == 0 {
			head := b.chain.Ether().BlockChain().CurrentBlock()
			log.Info("[DEBUG-evm-rewind] recovery restore step",
				"restored", restored,
				"requested.order", step.order,
				"state.order", recState.GetOrder(),
				"target.evmNumber", targetNumber,
				"head.evmNumber", head.Number.Uint64(),
				"rebuiltBlock", rebuiltBlock,
				"forceSequential", forceSequentialRebuild,
				"storedBlockLookup", storedBlockLookupElapsed,
				"nativeBlockLookup", nativeBlockElapsed,
				"build", buildElapsed,
				"insert", insertElapsed,
				"step", stepElapsed,
				"total", time.Since(restoreStart),
			)
		}

	}
	cur := b.chain.Ether().BlockChain().CurrentBlock()
	if err := validateExactEVMRewindPostcondition(state, cur, bc.GetBlock, bc.HasState); err == nil {
		b.clearEVMRecoveryFailure()
		return cur, nil
	}
	return nil, getError("prepare environment")
}

func rereadEVMHeadAfterRewindFailure(rewindErr error, readHead func() *types.Header) (*types.Header, error) {
	freshHead := readHead()
	if freshHead == nil || freshHead.Number == nil {
		return nil, fmt.Errorf("bdag chain env error: current EVM head is unavailable after rewind failure: %w", rewindErr)
	}
	return freshHead, nil
}

func (b *BdagChain) PrepareEnvironment(state model.BlockState) (*types.Header, error) {
	return b.prepareEnvironment(state)
}

func (b *BdagChain) EVMHeaderForState(state model.BlockState) (*types.Header, error) {
	targetHeader, err := b.EVMRawHeaderForState(state)
	if err != nil {
		return nil, err
	}
	if err := validateEVMRewindTargetState(state, targetHeader, targetHeader != nil && b.chain.Ether().BlockChain().HasState(targetHeader.Root)); err != nil {
		return nil, err
	}
	return targetHeader, nil
}

func (b *BdagChain) EVMRawHeaderForState(state model.BlockState) (*types.Header, error) {
	if state == nil {
		return nil, fmt.Errorf("target EVM state is nil")
	}
	bc := b.chain.Ether().BlockChain()
	var targetHeader *types.Header
	if state.GetEVMNumber() == 0 {
		if genesis := bc.Genesis(); genesis != nil {
			targetHeader = genesis.Header()
		}
	} else {
		targetHeader = bc.GetHeader(state.GetEVMHash(), state.GetEVMNumber())
	}
	if targetHeader == nil {
		return nil, fmt.Errorf("EVM header unavailable at number %d hash=%s", state.GetEVMNumber(), state.GetEVMHash().String())
	}
	return targetHeader, nil
}

func (b *BdagChain) RewindToEVMNumber(number uint64) error {
	if b == nil || b.chain == nil || b.chain.Ether() == nil || b.chain.Ether().BlockChain() == nil {
		return fmt.Errorf("bdag EVM chain is unavailable")
	}
	bc := b.chain.Ether().BlockChain()
	cur := bc.CurrentBlock()
	if cur == nil || cur.Number == nil {
		return fmt.Errorf("EVM head is unavailable")
	}
	if cur.Number.Uint64() < number {
		return fmt.Errorf("EVM head %d is behind rewind target %d", cur.Number.Uint64(), number)
	}
	if cur.Number.Uint64() == number {
		hasStateStart := time.Now()
		hasState := bc.HasState(cur.Root)
		hasStateElapsed := time.Since(hasStateStart)
		if !hasState {
			log.Warn("[DEBUG-evm-rewind] same-height rewind target state unavailable", "number", number, "hash", cur.Hash().String(), "root", cur.Root.String(), "hasState", hasStateElapsed)
			return fmt.Errorf("EVM state unavailable at number %d hash=%s root=%s", number, cur.Hash().String(), cur.Root.String())
		}
		return nil
	}

	start := time.Now()
	targetLookupStart := time.Now()
	var targetHeader *types.Header
	if number == 0 {
		if genesis := bc.Genesis(); genesis != nil {
			targetHeader = genesis.Header()
		}
	} else {
		targetHeader = bc.GetHeaderByNumber(number)
	}
	targetLookupElapsed := time.Since(targetLookupStart)
	if targetHeader == nil || targetHeader.Number == nil {
		return fmt.Errorf("EVM header unavailable at number %d", number)
	}
	hasStateStart := time.Now()
	targetHasState := bc.HasState(targetHeader.Root)
	hasStateElapsed := time.Since(hasStateStart)
	if !targetHasState {
		log.Warn("[DEBUG-evm-rewind] rewind-by-number target state unavailable", "cur.number", cur.Number.Uint64(), "target.number", number, "target.hash", targetHeader.Hash().String(), "target.root", targetHeader.Root.String(), "targetLookup", targetLookupElapsed, "hasState", hasStateElapsed, "elapsed", time.Since(start))
		return fmt.Errorf("EVM state unavailable at number %d hash=%s root=%s", number, targetHeader.Hash().String(), targetHeader.Root.String())
	}
	if err := validateEVMSetHeadDepth(cur.Number.Uint64(), number); err != nil {
		log.Warn("[DEBUG-evm-rewind] refusing deep rewind-by-number before SetHead", "cur.number", cur.Number.Uint64(), "target.number", number, "target.hash", targetHeader.Hash().String(), "targetLookup", targetLookupElapsed, "hasState", hasStateElapsed, "elapsed", time.Since(start), "err", err)
		return err
	}
	log.Info("Rewinding EVM chain by number",
		"cur.number", cur.Number.Uint64(),
		"cur.hash", cur.Hash().String(),
		"target.number", number,
		"target.hash", targetHeader.Hash().String(),
		"target.root", targetHeader.Root.String())
	setHeadStart := time.Now()
	if err := bc.SetHead(number); err != nil {
		log.Warn("[DEBUG-evm-rewind] rewind-by-number SetHead failed", "cur.number", cur.Number.Uint64(), "target.number", number, "target.hash", targetHeader.Hash().String(), "targetLookup", targetLookupElapsed, "hasState", hasStateElapsed, "setHead", time.Since(setHeadStart), "elapsed", time.Since(start), "err", err)
		return err
	}
	setHeadElapsed := time.Since(setHeadStart)
	after := bc.CurrentBlock()
	if after == nil || after.Number == nil {
		return fmt.Errorf("EVM head is unavailable after rewind to %d", number)
	}
	if after.Number.Uint64() != number || after.Hash() != targetHeader.Hash() {
		return fmt.Errorf("EVM rewind by number did not land correctly: got number=%d hash=%s want number=%d hash=%s",
			after.Number.Uint64(), after.Hash().String(), number, targetHeader.Hash().String())
	}
	log.Info("[DEBUG-evm-rewind] rewind-by-number complete", "from.number", cur.Number.Uint64(), "target.number", number, "target.hash", targetHeader.Hash().String(), "targetLookup", targetLookupElapsed, "hasState", hasStateElapsed, "setHead", setHeadElapsed, "elapsed", time.Since(start))
	return nil
}

func (b *BdagChain) SetHeadToExistingEVMState(state model.BlockState) error {
	if state == nil {
		return newUnsafeEVMRewindError("target block state is nil")
	}
	if b == nil || b.chain == nil || b.chain.Ether() == nil || b.chain.Ether().BlockChain() == nil {
		return fmt.Errorf("bdag EVM chain is unavailable")
	}
	if state.GetEVMNumber() == 0 {
		return newUnsafeEVMRewindError("refusing to set EVM import anchor to genesis")
	}
	bc := b.chain.Ether().BlockChain()
	cur := bc.CurrentBlock()
	if cur == nil || cur.Number == nil {
		return newUnsafeEVMRewindError("current EVM header is unavailable")
	}

	targetNumber := state.GetEVMNumber()
	targetHash := state.GetEVMHash()
	targetHeader := bc.GetHeader(targetHash, targetNumber)
	if err := validateEVMRewindTargetHeader(state, targetHeader); err != nil {
		log.Warn("Refusing to set EVM head to invalid recovery anchor",
			"cur.number", cur.Number.Uint64(),
			"cur.hash", cur.Hash().String(),
			"target.evmNumber", targetNumber,
			"target.evmHash", targetHash.String(),
			"target.evmRoot", state.GetEVMRoot().String(),
			"targetHeaderFound", targetHeader != nil,
			"targetHasState", false,
			"err", err)
		return err
	}
	targetBlock := bc.GetBlock(targetHash, targetNumber)
	if targetBlock == nil {
		return newUnsafeEVMRewindError("target EVM block is missing number=%d hash=%s", targetNumber, targetHash.String())
	}
	targetHasState := bc.HasState(targetHeader.Root)
	if !targetHasState {
		if err := b.rebuildMissingEVMTargetState(targetBlock, state); err != nil {
			log.Warn("Unable to reconstruct missing EVM recovery anchor state",
				"cur.number", cur.Number.Uint64(),
				"cur.hash", cur.Hash().String(),
				"target.evmNumber", targetNumber,
				"target.evmHash", targetHash.String(),
				"target.evmRoot", state.GetEVMRoot().String(),
				"err", err)
			return fmt.Errorf("reconstruct missing EVM recovery anchor state: %w", err)
		}
		targetHasState = bc.HasState(targetHeader.Root)
	}
	if err := validateEVMRewindTargetState(state, targetHeader, targetHasState); err != nil {
		return err
	}

	start := time.Now()
	after := bc.CurrentBlock()
	if after == nil || after.Number == nil || after.Number.Uint64() != targetNumber || after.Hash() != targetHash {
		var activateErr error
		if bc.GetCanonicalHash(targetNumber) == targetHash {
			activateErr = bc.SetHeadToExistingBlock(targetHash, targetNumber)
		} else {
			log.Warn("Canonicalizing existing recorded EVM recovery fork",
				"cur.number", cur.Number.Uint64(),
				"cur.hash", cur.Hash().String(),
				"target.evmNumber", targetNumber,
				"target.evmHash", targetHash.String(),
				"target.evmRoot", state.GetEVMRoot().String())
			activateErr = b.activateExistingEVMForkBounded(targetBlock)
		}
		if activateErr != nil {
			log.Warn("Set existing EVM recovery anchor head failed",
				"cur.number", cur.Number.Uint64(),
				"cur.hash", cur.Hash().String(),
				"target.evmNumber", targetNumber,
				"target.evmHash", targetHash.String(),
				"target.evmRoot", state.GetEVMRoot().String(),
				"elapsed", time.Since(start),
				"err", activateErr)
			return activateErr
		}
		after = bc.CurrentBlock()
	}
	if err := validateEVMRewindTargetState(state, after, after != nil && bc.HasState(after.Root)); err != nil {
		return fmt.Errorf("set existing EVM recovery anchor head failed validation: %w", err)
	}
	if got := bc.GetCanonicalHash(targetNumber); got != targetHash {
		return fmt.Errorf("set existing EVM recovery anchor left a noncanonical target index: number=%d got=%s want=%s",
			targetNumber, got.String(), targetHash.String())
	}
	if err := b.validateRecentCanonicalEVMLineage(targetBlock); err != nil {
		return fmt.Errorf("set existing EVM recovery anchor left inconsistent canonical lineage: %w", err)
	}
	b.clearEVMRecoveryFailure()
	log.Info("Set EVM head to existing recovery anchor without rewind",
		"from.number", cur.Number.Uint64(),
		"from.hash", cur.Hash().String(),
		"target.evmNumber", targetNumber,
		"target.evmHash", targetHash.String(),
		"target.evmRoot", state.GetEVMRoot().String(),
		"elapsed", time.Since(start))
	return nil
}

func (b *BdagChain) CommitEVMHeadState() error {
	if b == nil || b.chain == nil || b.chain.Ether() == nil || b.chain.Ether().BlockChain() == nil {
		return fmt.Errorf("bdag EVM chain is unavailable")
	}
	bc := b.chain.Ether().BlockChain()
	head := bc.CurrentBlock()
	if head == nil {
		return nil
	}
	return b.CommitEVMStateRoot(head.Root)
}

func (b *BdagChain) CommitEVMStateRoot(root common.Hash) error {
	if b == nil || b.chain == nil || b.chain.Ether() == nil || b.chain.Ether().BlockChain() == nil {
		return fmt.Errorf("bdag EVM chain is unavailable")
	}
	// PathDB checkpoints journal the live diff hierarchy instead of flattening
	// it. HashDB keeps its existing full commit behavior.
	if err := b.chain.Ether().BlockChain().TrieDB().Checkpoint(root, true); err != nil {
		return err
	}
	db := b.chain.Ether().ChainDb()
	syncer, ok := db.(interface{ SyncKeyValue() error })
	if !ok {
		return fmt.Errorf("EVM key-value database does not support explicit recovery sync")
	}
	if err := syncer.SyncKeyValue(); err != nil {
		return fmt.Errorf("sync EVM recovery checkpoint: %w", err)
	}
	return nil
}

// GetCanonicalEVMHeader returns the locally persisted canonical EVM header at
// number without changing either chain head.
func (b *BdagChain) GetCanonicalEVMHeader(number uint64) *types.Header {
	if b == nil || b.chain == nil || b.chain.Ether() == nil || b.chain.Ether().BlockChain() == nil {
		return nil
	}
	return b.chain.Ether().BlockChain().GetHeaderByNumber(number)
}

func (b *BdagChain) RewindTo(state model.BlockState) error {
	if b == nil {
		return newUnsafeEVMRewindError("EVM chain is unavailable")
	}
	bc := evmRewindChainProvider(b)
	if bc == nil {
		return newUnsafeEVMRewindError("EVM chain is unavailable")
	}
	return rewindEVMToChain(state, bc)
}

func rewindEVMToChain(state model.BlockState, bc evmRewindChain) error {
	if state == nil {
		return newUnsafeEVMRewindError("target block state is nil")
	}
	curBlockHeader := bc.CurrentBlock()
	if curBlockHeader == nil || curBlockHeader.Number == nil {
		return newUnsafeEVMRewindError("current EVM header is unavailable")
	}
	if curBlockHeader.Number.Uint64() < state.GetEVMNumber() {
		return newUnsafeEVMRewindError("current EVM head %d is below rewind target %d", curBlockHeader.Number.Uint64(), state.GetEVMNumber())
	}
	if curBlockHeader.Number.Uint64() == state.GetEVMNumber() {
		if err := validateExactEVMRewindPostcondition(state, curBlockHeader, bc.GetBlock, bc.HasState); err != nil {
			log.Error("Refusing unsafe EVM rewind",
				"err", err,
				"cur.number", curBlockHeader.Number.Uint64(),
				"cur.hash", curBlockHeader.Hash().String(),
				"target.evm.root", state.GetEVMRoot(),
				"target.evm.number", state.GetEVMNumber(),
				"target.evm.hash", state.GetEVMHash())
			return err
		}
		return nil
	}
	start := time.Now()
	log.Info("Try to rewind", "cur.number", curBlockHeader.Number.Uint64(), "cur.hash", curBlockHeader.Hash().String(), "target.evm.root", state.GetEVMRoot(), "target.evm.number", state.GetEVMNumber(), "target.evm.hash", state.GetEVMHash())
	targetLookupStart := time.Now()
	var targetHeader *types.Header
	if state.GetEVMNumber() == 0 {
		if genesis := bc.Genesis(); genesis != nil {
			targetHeader = genesis.Header()
		}
	} else {
		targetHeader = bc.GetHeader(state.GetEVMHash(), state.GetEVMNumber())
	}
	targetLookupElapsed := time.Since(targetLookupStart)
	hasStateStart := time.Now()
	targetHasState := targetHeader != nil && bc.HasState(targetHeader.Root)
	hasStateElapsed := time.Since(hasStateStart)
	if err := validateSafeEVMRewindTarget(curBlockHeader, state, bc.GetHeader, bc.HasState); err != nil {
		log.Warn("[DEBUG-evm-rewind] rewind target validation failed", "cur.number", curBlockHeader.Number.Uint64(), "target.evmNumber", state.GetEVMNumber(), "target.evmHash", state.GetEVMHash(), "targetHeaderFound", targetHeader != nil, "targetHasState", targetHasState, "targetLookup", targetLookupElapsed, "hasState", hasStateElapsed, "elapsed", time.Since(start), "err", err)
		return err
	}
	if err := validateEVMSetHeadDepth(curBlockHeader.Number.Uint64(), state.GetEVMNumber()); err != nil {
		log.Warn("[DEBUG-evm-rewind] refusing deep rewind before SetHead", "cur.number", curBlockHeader.Number.Uint64(), "target.evmNumber", state.GetEVMNumber(), "target.evmHash", state.GetEVMHash(), "targetHeaderFound", targetHeader != nil, "targetHasState", targetHasState, "targetLookup", targetLookupElapsed, "hasState", hasStateElapsed, "elapsed", time.Since(start), "err", err)
		return err
	}
	setHeadStart := time.Now()
	if err := bc.SetHead(state.GetEVMNumber()); err != nil {
		log.Warn("[DEBUG-evm-rewind] rewind SetHead failed", "cur.number", curBlockHeader.Number.Uint64(), "target.evmNumber", state.GetEVMNumber(), "target.evmHash", state.GetEVMHash(), "targetLookup", targetLookupElapsed, "hasState", hasStateElapsed, "setHead", time.Since(setHeadStart), "elapsed", time.Since(start), "err", err)
		return err
	}
	setHeadElapsed := time.Since(setHeadStart)
	cur := bc.CurrentBlock()
	if err := validateExactEVMRewindPostcondition(state, cur, bc.GetBlock, bc.HasState); err != nil {
		return fmt.Errorf("Rewind fail: %w", err)
	}
	log.Info("Rewound", "cur.number", cur.Number.Uint64(), "cur.hash", cur.Hash().String(), "target.evm.root", state.GetEVMRoot(), "target.evm.number", state.GetEVMNumber(), "target.evm.hash", state.GetEVMHash())
	log.Info("[DEBUG-evm-rewind] rewind complete", "from.number", curBlockHeader.Number.Uint64(), "target.evmNumber", state.GetEVMNumber(), "target.evmHash", state.GetEVMHash(), "targetLookup", targetLookupElapsed, "hasState", hasStateElapsed, "setHead", setHeadElapsed, "elapsed", time.Since(start))
	return nil
}
