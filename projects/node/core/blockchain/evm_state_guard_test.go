package blockchain

import (
	"errors"
	"io"
	"math/big"
	"strings"
	"testing"
	"time"

	bdagevm "github.com/BlockdagNetworkLabs/bdag/bevm/bdag"
	blockdag "github.com/BlockdagNetworkLabs/bdag/blockdag"
	"github.com/BlockdagNetworkLabs/bdag/config"
	"github.com/BlockdagNetworkLabs/bdag/consensus/model"
	"github.com/BlockdagNetworkLabs/bdag/core/shutdown"
	"github.com/BlockdagNetworkLabs/bdag/core/state"
	coretypes "github.com/BlockdagNetworkLabs/bdag/core/types"
	"github.com/BlockdagNetworkLabs/bdag/core/types/pow"
	"github.com/BlockdagNetworkLabs/bdag/database/chaindb"
	ethcommon "github.com/ethereum/go-ethereum/common"
	gethtypes "github.com/ethereum/go-ethereum/core/types"
)

func TestEVMStateGuardActivationFromNetworkParams(t *testing.T) {
	disabled := newEVMStateGuard(0)
	if disabled.enabled() {
		t.Fatal("zero commitment start order should disable the guard")
	}
	if disabled.commitmentActive(0) || disabled.enforcingCommitment(100) {
		t.Fatal("disabled guard should not activate or enforce commitments")
	}

	guard := newEVMStateGuard(100)
	if !guard.enabled() {
		t.Fatal("non-zero commitment start order should enable the guard")
	}
	if guard.commitmentActive(99) || guard.enforcingCommitment(99) {
		t.Fatal("commitment guard activated before the configured start order")
	}
	if !guard.commitmentActive(100) || !guard.enforcingCommitment(100) {
		t.Fatal("commitment guard did not enforce at the configured start order")
	}
}

func TestEVMStateCommitmentIgnoresMutableSolvedHeaderFields(t *testing.T) {
	parentHash := ethcommon.HexToHash("0x" + strings.Repeat("88", ethcommon.HashLength))
	txRoot := ethcommon.HexToHash("0x" + strings.Repeat("99", ethcommon.HashLength))
	evmRoot := ethcommon.HexToHash("0x" + strings.Repeat("aa", ethcommon.HashLength))
	receipts := ethcommon.HexToHash("0x" + strings.Repeat("bb", ethcommon.HashLength))
	block := &coretypes.Block{
		Header: coretypes.BlockHeader{
			Version:         1,
			ParentRoot:      parentHash,
			TxRoot:          txRoot,
			Difficulty:      1,
			Timestamp:       time.Unix(100, 0),
			BlockHeight:     10,
			CoinbaseAddress: ethcommon.HexToAddress("0x1111111111111111111111111111111111111111"),
			Reward:          50,
			Pow:             pow.GetInstance(pow.SCRYPT, 0, 0, nil),
		},
		Parents: []*ethcommon.Hash{&parentHash},
	}
	anchor := newCommitmentTestBlock(parentHash, 9, evmRoot, ethcommon.HexToHash("0x"+strings.Repeat("77", ethcommon.HashLength)))
	header := &gethtypes.Header{
		Number:      big.NewInt(10),
		Root:        evmRoot,
		ReceiptHash: receipts,
	}

	bc := &BlockChain{}
	before, err := bc.CalculateEVMStateCommitmentRoot(block, anchor, header)
	if err != nil {
		t.Fatalf("CalculateEVMStateCommitmentRoot unexpected error: %v", err)
	}

	solved := *block
	solved.Header.StateRoot = ethcommon.HexToHash("0x" + strings.Repeat("cc", ethcommon.HashLength))
	solved.Header.PowRoot = ethcommon.HexToHash("0x" + strings.Repeat("dd", ethcommon.HashLength))
	solved.Header.Pow = pow.GetInstance(pow.SCRYPT, 123, 456, []byte{1, 2, 3})
	after, err := bc.CalculateEVMStateCommitmentRoot(&solved, anchor, header)
	if err != nil {
		t.Fatalf("CalculateEVMStateCommitmentRoot after solve unexpected error: %v", err)
	}
	if *before != *after {
		t.Fatalf("commitment changed after mutable solved header fields changed: before %s after %s", before, after)
	}

	anchor.state.SetOrder(20)
	afterAnchorOrderDrift, err := bc.CalculateEVMStateCommitmentRoot(&solved, anchor, header)
	if err != nil {
		t.Fatalf("CalculateEVMStateCommitmentRoot after anchor order drift unexpected error: %v", err)
	}
	if *before != *afterAnchorOrderDrift {
		t.Fatalf("commitment changed after anchor order drift: before %s after %s", before, afterAnchorOrderDrift)
	}

	changedHeader := *header
	changedHeader.Root = ethcommon.HexToHash("0x" + strings.Repeat("ee", ethcommon.HashLength))
	changed, err := bc.CalculateEVMStateCommitmentRoot(block, anchor, &changedHeader)
	if err != nil {
		t.Fatalf("CalculateEVMStateCommitmentRoot changed EVM root unexpected error: %v", err)
	}
	if *before == *changed {
		t.Fatal("commitment did not change when the EVM state root changed")
	}

	changedAnchor := newCommitmentTestBlock(parentHash, 9, ethcommon.HexToHash("0x"+strings.Repeat("ab", ethcommon.HashLength)), *anchor.state.Root())
	changedAnchorCommitment, err := bc.CalculateEVMStateCommitmentRoot(block, changedAnchor, header)
	if err != nil {
		t.Fatalf("CalculateEVMStateCommitmentRoot changed anchor unexpected error: %v", err)
	}
	if *before == *changedAnchorCommitment {
		t.Fatal("commitment did not change when the parent EVM anchor changed")
	}
}

func TestEVMStateGuardRewindRejectsEmptyAnchorAsRecoveryError(t *testing.T) {
	bc := &BlockChain{}
	anchor := newEmptyEVMAnchor(13351891)
	ib := newCommitmentTestBlock(
		ethcommon.HexToHash("0x01"),
		12970383,
		ethcommon.HexToHash("0x02"),
		ethcommon.HexToHash("0x03"),
	)
	guardErr := ruleError(ErrEVMStateGuard, "EVM state commitment mismatch")

	err := bc.rewindEVMStateGuardRejection(anchor, ib, guardErr)
	if !isEVMStateGuardRuleError(err) {
		t.Fatalf("empty anchor error lost EVM state guard classification: %v", err)
	}
	if !isEVMStateGuardRecoveryError(err) {
		t.Fatalf("empty anchor error was not classified as guard recovery: %v", err)
	}
	if !strings.Contains(err.Error(), "EVM state guard recovery failed") {
		t.Fatalf("empty anchor error did not explain guard recovery failure: %v", err)
	}
	if !strings.Contains(err.Error(), "empty EVM rewind anchor") {
		t.Fatalf("empty anchor error did not include target anchor diagnosis: %v", err)
	}
	var recoveryErr evmStateGuardRecoveryError
	if !errors.As(err, &recoveryErr) {
		t.Fatalf("empty anchor error did not wrap recovery details: %T %v", err, err)
	}
	if recoveryErr.recoveryErr == nil {
		t.Fatal("empty anchor recovery details did not preserve the recovery cause")
	}
}

func TestEVMStateGuardRewindCallerPreservesTypedFailure(t *testing.T) {
	bc := &BlockChain{bdagChain: &bdagevm.BdagChain{}}
	anchor := state.NewBlockState(1)
	anchor.SetOrder(1)
	anchor.SetEVM(&gethtypes.Header{
		Number: big.NewInt(7),
		Root:   ethcommon.HexToHash("0x07"),
	})
	ib := newCommitmentTestBlock(
		ethcommon.HexToHash("0x11"),
		8,
		ethcommon.HexToHash("0x08"),
		ethcommon.HexToHash("0x09"),
	)
	guardErr := ruleError(ErrEVMStateGuard, "EVM state commitment mismatch")

	err := bc.rewindEVMStateGuardRejection(anchor, ib, guardErr)
	if !isEVMStateGuardRuleError(err) || !isEVMStateGuardRecoveryError(err) {
		t.Fatalf("rewind caller error lost guard classification: %v", err)
	}
	var recoveryErr evmStateGuardRecoveryError
	if !errors.As(err, &recoveryErr) || recoveryErr.guardErr == nil || recoveryErr.guardErr.Error() != guardErr.Error() {
		t.Fatalf("rewind caller error lost original guard cause: %v", err)
	}
	if !strings.Contains(err.Error(), "EVM chain is unavailable") {
		t.Fatalf("rewind caller error lost typed unavailable rewind cause: %v", err)
	}
}

func TestEVMStateGuardReorgStartRewindClassifiesEmptyAnchorWhenEnforcing(t *testing.T) {
	bc := &BlockChain{evmStateGuard: newEVMStateGuard(1)}
	anchor := newEmptyEVMAnchor(13351891)
	ib := newCommitmentTestBlock(
		ethcommon.HexToHash("0x04"),
		12970383,
		ethcommon.HexToHash("0x05"),
		ethcommon.HexToHash("0x06"),
	)

	err := bc.rewindEVMStateGuardReorgStart(anchor, ib)
	if !isEVMStateGuardRuleError(err) {
		t.Fatalf("enforcing reorg rewind error lost EVM state guard classification: %v", err)
	}
	if !isEVMStateGuardRecoveryError(err) {
		t.Fatalf("enforcing reorg rewind error was not classified as guard recovery: %v", err)
	}
	if !strings.Contains(err.Error(), "empty EVM rewind anchor") {
		t.Fatalf("enforcing reorg rewind error did not include target anchor diagnosis: %v", err)
	}
}

func TestEVMStateGuardReorgStartRewindLeavesEmptyAnchorUnclassifiedBeforeEnforcement(t *testing.T) {
	bc := &BlockChain{evmStateGuard: newEVMStateGuard(0)}
	anchor := newEmptyEVMAnchor(13351891)
	ib := newCommitmentTestBlock(
		ethcommon.HexToHash("0x07"),
		12970383,
		ethcommon.HexToHash("0x08"),
		ethcommon.HexToHash("0x09"),
	)

	err := bc.rewindEVMStateGuardReorgStart(anchor, ib)
	if err == nil {
		t.Fatal("expected empty reorg rewind anchor to be rejected")
	}
	if isEVMStateGuardRuleError(err) {
		t.Fatalf("non-enforcing reorg rewind error should not be classified as EVM state guard: %v", err)
	}
	if !strings.Contains(err.Error(), "empty EVM rewind anchor") {
		t.Fatalf("non-enforcing reorg rewind error did not include target anchor diagnosis: %v", err)
	}
}

func TestEVMStateGuardAbortRollsBackNativeDAGCandidate(t *testing.T) {
	db, err := chaindb.New(&config.Config{DataDir: ""})
	if err != nil {
		t.Fatalf("create chain db: %v", err)
	}
	defer db.Close()

	bd := blockdag.New(blockdag.PHANTOM, -1, db, nil)
	genesisData := rollbackTestBlockData{
		hash:      ethcommon.HexToHash("0x01"),
		timestamp: 1,
	}
	_, _, genesis, _ := bd.AddBlock(genesisData)
	if genesis == nil {
		t.Fatal("failed to add genesis block")
	}
	if err := bd.Commit(); err != nil {
		t.Fatalf("commit genesis: %v", err)
	}

	childHash := ethcommon.HexToHash("0x02")
	childData := rollbackTestBlockData{
		hash:       childHash,
		parents:    []*ethcommon.Hash{genesis.GetHash()},
		mainParent: genesis.GetHash(),
		timestamp:  2,
	}
	_, _, child, _ := bd.AddBlock(childData)
	if child == nil {
		t.Fatal("failed to add child block")
	}
	if got := bd.GetMainChainTip().GetHash(); got.Cmp(childHash) != 0 {
		t.Fatalf("test setup expected child tip %s, got %s", childHash, got)
	}

	guardErr := ruleError(ErrEVMStateGuard, "EVM state commitment mismatch")
	bc := &BlockChain{bd: bd}
	err = bc.abortEVMStateGuardNativeCandidate(child, guardErr)
	if !isEVMStateGuardRuleError(err) {
		t.Fatalf("rollback lost EVM guard classification: %T %v", err, err)
	}
	if bd.HasBlock(&childHash) {
		t.Fatal("EVM guard rollback left rejected child in the native DAG")
	}
	if got := bd.GetMainChainTip().GetHash(); got.Cmp(*genesis.GetHash()) != 0 {
		t.Fatalf("EVM guard rollback did not restore parent tip: got %s want %s", got, genesis.GetHash())
	}
}

type commitmentTestBlock struct {
	hash  ethcommon.Hash
	state *state.BlockState
}

type rollbackTestBlockData struct {
	hash       ethcommon.Hash
	parents    []*ethcommon.Hash
	mainParent *ethcommon.Hash
	timestamp  int64
}

func (b rollbackTestBlockData) GetHash() *ethcommon.Hash {
	return &b.hash
}

func (b rollbackTestBlockData) GetParents() []*ethcommon.Hash {
	return b.parents
}

func (b rollbackTestBlockData) GetMainParent() *ethcommon.Hash {
	if b.mainParent != nil {
		return b.mainParent
	}
	return &ethcommon.Hash{}
}

func (b rollbackTestBlockData) GetTimestamp() int64 {
	return b.timestamp
}

func (b rollbackTestBlockData) GetPriority() int {
	return blockdag.MaxPriority
}

func newEmptyEVMAnchor(order uint64) *state.BlockState {
	st := state.NewBlockState(1)
	st.SetOrder(order)
	return st
}

func newCommitmentTestBlock(hash ethcommon.Hash, evmNumber uint64, evmRoot ethcommon.Hash, nativeRoot ethcommon.Hash) *commitmentTestBlock {
	st := state.NewBlockState(1)
	st.SetOrder(1)
	st.SetRoot(&nativeRoot)
	st.SetEVM(&gethtypes.Header{
		Number: big.NewInt(int64(evmNumber)),
		Root:   evmRoot,
	})
	return &commitmentTestBlock{hash: hash, state: st}
}

func (b *commitmentTestBlock) GetID() uint                  { return 1 }
func (b *commitmentTestBlock) GetHash() *ethcommon.Hash     { return &b.hash }
func (b *commitmentTestBlock) AddParent(blockdag.IBlock)    {}
func (b *commitmentTestBlock) GetParents() *blockdag.IdSet  { return nil }
func (b *commitmentTestBlock) HasParents() bool             { return false }
func (b *commitmentTestBlock) AddChild(blockdag.IBlock)     {}
func (b *commitmentTestBlock) GetChildren() *blockdag.IdSet { return nil }
func (b *commitmentTestBlock) HasChildren() bool            { return false }
func (b *commitmentTestBlock) RemoveChild(uint)             {}
func (b *commitmentTestBlock) GetMainParent() uint          { return 0 }
func (b *commitmentTestBlock) GetLayer() uint               { return 1 }
func (b *commitmentTestBlock) GetHeight() uint              { return 1 }
func (b *commitmentTestBlock) Encode(io.Writer) error       { return nil }
func (b *commitmentTestBlock) Decode(io.Reader) error       { return nil }
func (b *commitmentTestBlock) GetData() blockdag.IBlockData { return nil }
func (b *commitmentTestBlock) SetData(blockdag.IBlockData)  {}
func (b *commitmentTestBlock) IsLoaded() bool               { return true }
func (b *commitmentTestBlock) AttachParent(blockdag.IBlock) {}
func (b *commitmentTestBlock) DetachParent(blockdag.IBlock) {}
func (b *commitmentTestBlock) AttachChild(blockdag.IBlock)  {}
func (b *commitmentTestBlock) DetachChild(blockdag.IBlock)  {}
func (b *commitmentTestBlock) GetState() model.BlockState   { return b.state }
func (b *commitmentTestBlock) Bytes() []byte                { return nil }
func (b *commitmentTestBlock) SetOrder(uint)                {}
func (b *commitmentTestBlock) GetOrder() uint               { return 1 }
func (b *commitmentTestBlock) IsOrdered() bool              { return true }

// A local missing recovery anchor is not proof of a bad peer block, even
// before commitment enforcement. It must reach the existing fail-closed path.
func TestEVMReorgRecoveryBeforeEnforcementRollsBackAndStops(t *testing.T) {
	db, err := chaindb.New(&config.Config{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	bd := blockdag.New(blockdag.PHANTOM, -1, db, nil)
	_, _, genesis, _ := bd.AddBlock(rollbackTestBlockData{hash: ethcommon.HexToHash("0x11"), timestamp: 1})
	if genesis == nil {
		t.Fatal("missing fixture genesis")
	}
	if err := bd.Commit(); err != nil {
		t.Fatal(err)
	}
	childHash := ethcommon.HexToHash("0x12")
	_, _, child, _ := bd.AddBlock(rollbackTestBlockData{hash: childHash, parents: []*ethcommon.Hash{genesis.GetHash()}, mainParent: genesis.GetHash(), timestamp: 2})
	if child == nil {
		t.Fatal("missing fixture candidate")
	}
	if err := db.StartBlockApplyTrack(childHash.String(), genesis.GetHash().String(), shutdown.DatasetState{}); err != nil {
		t.Fatal(err)
	}
	bc := &BlockChain{bd: bd, evmStateGuard: newEVMStateGuard(0)}
	cause := bc.rewindEVMStateGuardReorgStart(newEmptyEVMAnchor(1), child)
	if !isEVMStateGuardRecoveryError(cause) {
		t.Fatalf("local rewind error will escape recovery handling: %v", cause)
	}
	if isEVMStateGuardRuleError(cause) {
		t.Fatal("local state failure classified as peer rule violation")
	}
	cause = bc.abortEVMStateGuardNativeCandidate(child, cause)
	if bd.HasBlock(&childHash) || !upgradeRebuildBlocksMatch(bd.GetMainChainTip(), genesis) {
		t.Fatal("local recovery failure did not restore native candidate")
	}
	if !isEVMStateGuardRecoveryError(cause) {
		t.Fatal("rollback lost the local recovery failure")
	}
	fatal := bc.failLocalEVMApply(childHash, cause)
	if _, pending, err := db.PendingBlockApplyRecovery(); err != nil || !pending {
		t.Fatalf("recovery marker lost: pending=%v err=%v", pending, err)
	}
	if isEVMStateGuardRuleError(fatal) {
		t.Fatal("fatal local failure accuses peer")
	}
	if _, _, err := bc.ProcessBlock(nil, 0, nil); err == nil {
		t.Fatal("block intake continued after local recovery failure")
	}
}
