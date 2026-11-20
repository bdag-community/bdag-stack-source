package blockchain

import (
	"errors"
	"fmt"
	"os"

	blockdag "github.com/BlockdagNetworkLabs/bdag/blockdag"
	"github.com/BlockdagNetworkLabs/bdag/consensus/model"
	"github.com/BlockdagNetworkLabs/bdag/core/state"
	"github.com/BlockdagNetworkLabs/bdag/core/types"
	ethcommon "github.com/ethereum/go-ethereum/common"
	gethtypes "github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/rlp"
)

type evmStateGuard struct {
	commitmentStartOrder uint64
}

type evmStateCommitmentData struct {
	Domain              []byte
	Version             uint32
	ParentRoot          ethcommon.Hash
	TxRoot              ethcommon.Hash
	Difficulty          uint32
	Timestamp           uint64
	BlockHeight         uint64
	CoinbaseAddress     ethcommon.Address
	Reward              uint64
	PowType             uint8
	Parents             []ethcommon.Hash
	Transactions        []ethcommon.Hash
	MainParentHash      ethcommon.Hash
	MainParentState     ethcommon.Hash
	AnchorEVMNumber     uint64
	AnchorEVMHash       ethcommon.Hash
	AnchorEVMRoot       ethcommon.Hash
	TemplateEVMNumber   uint64
	TemplateEVMHash     ethcommon.Hash
	TemplateEVMRoot     ethcommon.Hash
	TemplateReceiptRoot ethcommon.Hash
}

func newEVMStateGuard(commitmentStartOrder uint64) *evmStateGuard {
	return &evmStateGuard{
		commitmentStartOrder: commitmentStartOrder,
	}
}

func (g *evmStateGuard) enabled() bool {
	return g != nil && g.commitmentStartOrder > 0
}

func (g *evmStateGuard) commitmentActive(order uint64) bool {
	return g.enabled() && order >= g.commitmentStartOrder
}

func (g *evmStateGuard) enforcingCommitment(order uint64) bool {
	return g.commitmentActive(order)
}

func (b *BlockChain) CalculateEVMStateCommitmentRoot(block *types.Block, mainParent blockdag.IBlock, header *gethtypes.Header) (*ethcommon.Hash, error) {
	if mainParent == nil || mainParent.GetState() == nil {
		return nil, fmt.Errorf("EVM state commitment candidate is missing a main parent anchor")
	}
	if header == nil || header.Number == nil {
		return nil, fmt.Errorf("EVM state commitment candidate is missing an EVM header")
	}
	parents := make([]ethcommon.Hash, 0, len(block.Parents))
	for _, parent := range block.Parents {
		parents = append(parents, *parent)
	}
	txs := make([]ethcommon.Hash, 0, len(block.Transactions))
	for _, tx := range block.Transactions {
		txs = append(txs, tx.Hash())
	}
	data := evmStateCommitmentData{
		Domain:              []byte("BDAG:EVMTEMPLATE:v1"),
		Version:             block.Header.Version,
		ParentRoot:          block.Header.ParentRoot,
		TxRoot:              block.Header.TxRoot,
		Difficulty:          block.Header.Difficulty,
		Timestamp:           uint64(block.Header.Timestamp.Unix()),
		BlockHeight:         block.Header.BlockHeight,
		CoinbaseAddress:     block.Header.CoinbaseAddress,
		Reward:              block.Header.Reward,
		PowType:             uint8(block.Header.Pow.GetPowType()),
		Parents:             parents,
		Transactions:        txs,
		MainParentHash:      *mainParent.GetHash(),
		MainParentState:     *mainParent.GetState().Root(),
		AnchorEVMNumber:     mainParent.GetState().GetEVMNumber(),
		AnchorEVMHash:       mainParent.GetState().GetEVMHash(),
		AnchorEVMRoot:       mainParent.GetState().GetEVMRoot(),
		TemplateEVMNumber:   header.Number.Uint64(),
		TemplateEVMHash:     header.Hash(),
		TemplateEVMRoot:     header.Root,
		TemplateReceiptRoot: header.ReceiptHash,
	}
	encoded, err := rlp.EncodeToBytes(data)
	if err != nil {
		return nil, err
	}
	return ComputeKeccak256Hash(encoded), nil
}

func cloneBlockForEVMStateCommitment(block *types.Block) *types.Block {
	blockCopy := *block
	blockCopy.Header.StateRoot = ethcommon.Hash{}
	return &blockCopy
}

func (b *BlockChain) calculateAnchoredEVMStateCommitment(block *types.Block, mainParent blockdag.IBlock, layer uint64, restoreState model.BlockState) (root *ethcommon.Hash, err error) {
	if mainParent == nil || mainParent.GetState() == nil {
		return nil, fmt.Errorf("EVM state commitment candidate is missing a main parent anchor")
	}
	if restoreState != nil {
		defer func() {
			if _, restoreErr := b.bdagChain.PrepareEnvironment(restoreState); restoreErr != nil && err == nil {
				err = restoreErr
			}
		}()
	}

	if _, err := b.bdagChain.PrepareEnvironment(mainParent.GetState()); err != nil {
		return nil, err
	}
	blockCopy := cloneBlockForEVMStateCommitment(block)
	blockNode := NewBlockNode(types.NewBlock(blockCopy))
	if err := b.bdagCheckConnectBlock(blockNode, layer); err != nil {
		return nil, err
	}
	evmBlock := blockNode.GetbdagBlock()
	if evmBlock == nil || evmBlock.EvmBlock == nil {
		return nil, fmt.Errorf("EVM template dry-run did not produce a candidate EVM block")
	}
	return b.CalculateEVMStateCommitmentRoot(blockCopy, mainParent, evmBlock.EvmBlock.Header())
}

func (b *BlockChain) PrepareEVMStateCommitmentForTemplate(block *types.Block, height uint64) (bool, *ethcommon.Hash, error) {
	if !b.evmStateGuard.enabled() {
		return false, nil, nil
	}

	blockCopy := cloneBlockForEVMStateCommitment(block)
	sblock := types.NewBlock(blockCopy)

	b.ChainLock()
	defer b.ChainUnlock()

	flags := BFNoPoWCheck
	if err := b.checkBlockSanity(sblock, b.timeSource, flags, b.params); err != nil {
		return false, nil, err
	}

	newNode := NewBlockNode(sblock)
	virBlock := b.bd.CreateVirtualBlock(newNode)
	if virBlock == nil {
		return false, nil, ruleError(ErrPrevBlockNotBest, "tipsNode")
	}
	if uint64(virBlock.GetHeight()) != height {
		return false, nil, ruleError(ErrPrevBlockNotBest, "tipsNode height")
	}
	mainParent := b.bd.GetBlockById(virBlock.GetMainParent())
	if mainParent == nil {
		return false, nil, ruleError(ErrPrevBlockNotBest, "main parent")
	}
	virBlock.SetOrder(mainParent.GetOrder() + 1)
	virBlock.GetState().(*state.BlockState).SetDefault(mainParent.GetState().(*state.BlockState))
	if err := b.checkBlockContext(sblock, mainParent, flags); err != nil {
		return false, nil, err
	}

	order := uint64(virBlock.GetOrder())
	if !b.evmStateGuard.commitmentActive(order) {
		return false, nil, nil
	}

	b.evmChainLock.Lock()
	defer b.evmChainLock.Unlock()
	root, err := b.calculateAnchoredEVMStateCommitment(block, mainParent, uint64(virBlock.GetLayer()), mainParent.GetState())
	if err != nil {
		return false, nil, err
	}
	return true, root, nil
}

func (b *BlockChain) checkEVMStateGuardCandidate(ib blockdag.IBlock, blockNode *BlockNode, restoreState model.BlockState) error {
	if !b.evmStateGuard.commitmentActive(uint64(ib.GetOrder())) {
		return nil
	}
	dumpProbeBlockBytes(blockNode)
	mainParent := b.bd.GetBlock(blockNode.GetMainParent())
	if mainParent == nil {
		return b.handleEVMStateGuardError(ib, fmt.Errorf("EVM state commitment main parent is unknown: %s", blockNode.GetMainParent().String()))
	}
	expectedRoot, err := b.calculateAnchoredEVMStateCommitment(blockNode.GetBody().Block(), mainParent, uint64(ib.GetLayer()), restoreState)
	if err != nil {
		return b.handleEVMStateGuardError(ib, err)
	}
	if blockNode.GetBody().Block().Header.StateRoot != *expectedRoot {
		if os.Getenv("BDAG_PROBE_COMMIT") != "" {
			_ = ProbeEVMStateCommitment(b, blockNode.GetBody())
		}
		return b.handleEVMStateGuardError(ib, fmt.Errorf("EVM state commitment mismatch: got %s want %s", blockNode.GetBody().Block().Header.StateRoot, expectedRoot))
	}
	log.Debug("EVM state commitment accepted candidate", "block", ib.GetHash().String(), "order", ib.GetOrder(), "anchor", mainParent.GetHash().String(), "anchor.evm.number", mainParent.GetState().GetEVMNumber(), "anchor.evm.hash", mainParent.GetState().GetEVMHash().String())
	return nil
}

func (b *BlockChain) checkEVMStateGuardCandidateWithBuiltBlock(ib blockdag.IBlock, blockNode *BlockNode) error {
	if !b.evmStateGuard.commitmentActive(uint64(ib.GetOrder())) {
		return nil
	}
	if blockNode == nil || blockNode.GetbdagBlock() == nil || blockNode.GetbdagBlock().EvmBlock == nil {
		return b.checkEVMStateGuardCandidate(ib, blockNode, b.orderedParentState(ib))
	}
	dumpProbeBlockBytes(blockNode)
	mainParent := b.bd.GetBlock(blockNode.GetMainParent())
	if mainParent == nil {
		return b.handleEVMStateGuardError(ib, fmt.Errorf("EVM state commitment main parent is unknown: %s", blockNode.GetMainParent().String()))
	}
	blockCopy := cloneBlockForEVMStateCommitment(blockNode.GetBody().Block())
	expectedRoot, err := b.CalculateEVMStateCommitmentRoot(blockCopy, mainParent, blockNode.GetbdagBlock().EvmBlock.Header())
	if err != nil {
		return b.handleEVMStateGuardError(ib, err)
	}
	if blockNode.GetBody().Block().Header.StateRoot != *expectedRoot {
		if os.Getenv("BDAG_PROBE_COMMIT") != "" {
			_ = ProbeEVMStateCommitment(b, blockNode.GetBody())
		}
		return b.handleEVMStateGuardError(ib, fmt.Errorf("EVM state commitment mismatch: got %s want %s", blockNode.GetBody().Block().Header.StateRoot, expectedRoot))
	}
	log.Debug("EVM state commitment accepted built candidate", "block", ib.GetHash().String(), "order", ib.GetOrder(), "anchor", mainParent.GetHash().String(), "anchor.evm.number", mainParent.GetState().GetEVMNumber(), "anchor.evm.hash", mainParent.GetState().GetEVMHash().String())
	return nil
}

func (b *BlockChain) handleEVMStateGuardError(ib blockdag.IBlock, err error) error {
	if !b.evmStateGuard.commitmentActive(uint64(ib.GetOrder())) {
		return nil
	}
	desc := fmt.Sprintf("EVM state guard rejected block %s at order %d: %v", ib.GetHash().String(), ib.GetOrder(), err)
	return ruleErrorWithCause(ErrEVMStateGuard, desc, err)
}

func isEVMStateGuardRuleError(err error) bool {
	return isRuleErrorCode(err, ErrEVMStateGuard)
}

func (b *BlockChain) rewindEVMStateGuardRejection(anchor model.BlockState, ib blockdag.IBlock, err error) error {
	if !isEVMStateGuardRuleError(err) {
		return err
	}
	blockHash, order := evmStateGuardBlockID(ib)
	if anchor == nil {
		log.Error("EVM state guard rejection has no EVM rewind anchor", "block", blockHash, "order", order, "err", err)
		return b.evmStateGuardRecoveryError(ib, err, fmt.Errorf("EVM rewind target state is nil"))
	}
	if anchorErr := validateEVMStateGuardRewindAnchor(anchor); anchorErr != nil {
		log.Error("EVM state guard rejection has invalid EVM rewind anchor",
			"block", blockHash,
			"order", order,
			"targetState.order", anchor.GetOrder(),
			"targetEVMNumber", anchor.GetEVMNumber(),
			"targetEVMHash", anchor.GetEVMHash().String(),
			"targetEVMRoot", anchor.GetEVMRoot().String(),
			"err", anchorErr)
		return b.evmStateGuardRecoveryError(ib, err, anchorErr)
	}
	if rewindErr := b.bdagChain.RewindTo(anchor); rewindErr != nil {
		log.Error("EVM state guard failed to rewind rejected batch", "block", blockHash, "order", order, "targetEVMNumber", anchor.GetEVMNumber(), "targetEVMHash", anchor.GetEVMHash().String(), "err", rewindErr)
		return b.evmStateGuardRecoveryError(ib, err, rewindErr)
	}
	log.Warn("EVM state guard rewound rejected batch", "block", blockHash, "order", order, "targetEVMNumber", anchor.GetEVMNumber(), "targetEVMHash", anchor.GetEVMHash().String())
	return err
}

func (b *BlockChain) rejectEVMStateGuardCandidate(anchor model.BlockState, ib blockdag.IBlock, err error) error {
	if isEVMStateGuardRuleError(err) && ib != nil && b.bd != nil {
		b.bd.InvalidBlock(ib)
	}
	return b.rewindEVMStateGuardRejection(anchor, ib, err)
}

type evmStateGuardRecoveryError struct {
	guardErr    error
	recoveryErr error
}

func (e evmStateGuardRecoveryError) Error() string {
	if e.recoveryErr == nil {
		return "EVM state guard recovery failed"
	}
	if e.guardErr == nil {
		return fmt.Sprintf("EVM state guard recovery failed: %v", e.recoveryErr)
	}
	return fmt.Sprintf("EVM state guard recovery failed: %v; original guard error: %v", e.recoveryErr, e.guardErr)
}

func (e evmStateGuardRecoveryError) Unwrap() error {
	return e.recoveryErr
}

func (b *BlockChain) evmStateGuardRecoveryError(ib blockdag.IBlock, guardErr error, recoveryErr error) error {
	desc := fmt.Sprintf("EVM state guard recovery failed: %v", recoveryErr)
	if ib != nil {
		blockHash, order := evmStateGuardBlockID(ib)
		desc = fmt.Sprintf("EVM state guard recovery failed for block %s at order %d: %v", blockHash, order, recoveryErr)
	}
	return ruleErrorWithCause(ErrEVMStateGuard, desc, evmStateGuardRecoveryError{
		guardErr:    guardErr,
		recoveryErr: recoveryErr,
	})
}

func (b *BlockChain) rewindEVMStateGuardReorgStart(anchor model.BlockState, ib blockdag.IBlock) error {
	if anchorErr := validateEVMStateGuardRewindAnchor(anchor); anchorErr != nil {
		if b.enforcingEVMStateGuard(ib) {
			return b.evmStateGuardRecoveryError(ib, nil, anchorErr)
		}
		return evmStateGuardRecoveryError{recoveryErr: anchorErr}
	}
	if rewindErr := b.bdagChain.RewindTo(anchor); rewindErr != nil {
		if b.enforcingEVMStateGuard(ib) {
			return b.evmStateGuardRecoveryError(ib, nil, rewindErr)
		}
		return evmStateGuardRecoveryError{recoveryErr: rewindErr}
	}
	return nil
}

func (b *BlockChain) enforcingEVMStateGuard(ib blockdag.IBlock) bool {
	return b != nil && b.evmStateGuard != nil && ib != nil && b.evmStateGuard.enforcingCommitment(uint64(ib.GetOrder()))
}

func evmStateGuardBlockID(ib blockdag.IBlock) (string, uint) {
	if ib == nil {
		return "<nil>", 0
	}
	hash := ib.GetHash()
	if hash == nil {
		return "<nil>", ib.GetOrder()
	}
	return hash.String(), ib.GetOrder()
}

func validateEVMStateGuardRewindAnchor(anchor model.BlockState) error {
	if anchor == nil {
		return fmt.Errorf("EVM rewind target state is nil")
	}
	if anchor.GetEVMHash() == (ethcommon.Hash{}) && anchor.GetEVMRoot() == (ethcommon.Hash{}) {
		return fmt.Errorf("empty EVM rewind anchor: targetState.order=%d target.evm.number=%d target.evm.hash=%s target.evm.root=%s",
			anchor.GetOrder(),
			anchor.GetEVMNumber(),
			anchor.GetEVMHash().String(),
			anchor.GetEVMRoot().String())
	}
	return nil
}

func isEVMStateGuardRecoveryError(err error) bool {
	if err == nil {
		return false
	}
	var recoveryErr evmStateGuardRecoveryError
	return errors.As(err, &recoveryErr)
}

func (b *BlockChain) firstOrderedParentState(blocks []blockdag.IBlock) model.BlockState {
	for _, ib := range blocks {
		if state := b.orderedParentState(ib); state != nil {
			return state
		}
	}
	return nil
}

func (b *BlockChain) orderedParentState(ib blockdag.IBlock) model.BlockState {
	if ib == nil || !ib.IsOrdered() || ib.GetOrder() == 0 {
		return nil
	}
	prev := b.bd.GetBlockByOrder(ib.GetOrder() - 1)
	if prev == nil {
		return nil
	}
	return prev.GetState()
}

func isRuleErrorCode(err error, code ErrorCode) bool {
	if err == nil {
		return false
	}
	var ruleErr RuleError
	if errors.As(err, &ruleErr) && ruleErr.ErrorCode == code {
		return true
	}
	var ruleErrPtr *RuleError
	return errors.As(err, &ruleErrPtr) && ruleErrPtr != nil && ruleErrPtr.ErrorCode == code
}
