package blockchain

import (
	"errors"
	"math/big"
	"strings"
	"testing"
	"time"

	corestate "github.com/BlockdagNetworkLabs/bdag/core/state"
	coretypes "github.com/BlockdagNetworkLabs/bdag/core/types"
	"github.com/BlockdagNetworkLabs/bdag/core/types/pow"
	ethcommon "github.com/ethereum/go-ethereum/common"
	gethtypes "github.com/ethereum/go-ethereum/core/types"
)

func TestEVMNativeRebuildStateCommitmentValidation(t *testing.T) {
	parentHash := ethcommon.HexToHash("0x" + strings.Repeat("11", ethcommon.HashLength))
	evmRoot := ethcommon.HexToHash("0x" + strings.Repeat("22", ethcommon.HashLength))
	native := &coretypes.Block{
		Header: coretypes.BlockHeader{
			Version:         1,
			ParentRoot:      parentHash,
			TxRoot:          ethcommon.HexToHash("0x" + strings.Repeat("33", ethcommon.HashLength)),
			Difficulty:      1,
			Timestamp:       time.Unix(100, 0),
			BlockHeight:     10,
			CoinbaseAddress: ethcommon.HexToAddress("0x1111111111111111111111111111111111111111"),
			Reward:          50,
			Pow:             pow.GetInstance(pow.SCRYPT, 0, 0, nil),
		},
		Parents: []*ethcommon.Hash{&parentHash},
	}
	mainParent := newCommitmentTestBlock(
		parentHash,
		9,
		ethcommon.HexToHash("0x"+strings.Repeat("44", ethcommon.HashLength)),
		ethcommon.HexToHash("0x"+strings.Repeat("55", ethcommon.HashLength)),
	)
	header := &gethtypes.Header{
		Number:      big.NewInt(10),
		Root:        evmRoot,
		ReceiptHash: ethcommon.HexToHash("0x" + strings.Repeat("66", ethcommon.HashLength)),
	}
	bc := &BlockChain{}

	expected, err := bc.CalculateEVMStateCommitmentRoot(native, mainParent, header)
	if err != nil {
		t.Fatalf("calculate EVM state commitment: %v", err)
	}
	native.Header.StateRoot = *expected
	if err := bc.validateEVMNativeRebuildStateCommitment(coretypes.NewBlock(native), mainParent, header); err != nil {
		t.Fatalf("validate matching state commitment: %v", err)
	}

	native.Header.StateRoot = ethcommon.HexToHash("0x" + strings.Repeat("77", ethcommon.HashLength))
	err = bc.validateEVMNativeRebuildStateCommitment(coretypes.NewBlock(native), mainParent, header)
	if !errors.Is(err, errEVMNativeRebuildStateCommitment) {
		t.Fatalf("mismatch did not preserve commitment validation classification: %v", err)
	}
	if !strings.Contains(err.Error(), "native state root") {
		t.Fatalf("mismatch did not identify the immutable native state root: %v", err)
	}
}

func TestRequiresEVMNativeStateCommitment(t *testing.T) {
	bc := &BlockChain{evmStateGuard: newEVMStateGuard(100)}
	if bc.requiresEVMNativeStateCommitment(99) {
		t.Fatal("native catch-up required a commitment before the activation boundary")
	}
	if !bc.requiresEVMNativeStateCommitment(100) {
		t.Fatal("native catch-up did not require a commitment at the activation boundary")
	}
	if (&BlockChain{}).requiresEVMNativeStateCommitment(100) {
		t.Fatal("native catch-up required a commitment without an enabled guard")
	}
}

func TestSnapshotEVMNativeRecordedStateCopiesAllValidationFields(t *testing.T) {
	recordedHeader := &gethtypes.Header{
		ParentHash: ethcommon.HexToHash("0x" + strings.Repeat("11", ethcommon.HashLength)),
		Number:     big.NewInt(101),
		Root:       ethcommon.HexToHash("0x" + strings.Repeat("22", ethcommon.HashLength)),
		Extra:      []byte("recorded lineage"),
	}
	mutatedHeader := &gethtypes.Header{
		ParentHash: ethcommon.HexToHash("0x" + strings.Repeat("33", ethcommon.HashLength)),
		Number:     big.NewInt(202),
		Root:       ethcommon.HexToHash("0x" + strings.Repeat("44", ethcommon.HashLength)),
		Extra:      []byte("mutated lineage"),
	}
	state := &corestate.BlockState{}
	state.SetEVM(recordedHeader)
	state.Invalid()

	recorded, err := snapshotEVMNativeRecordedState(state)
	if err != nil {
		t.Fatalf("snapshot recorded state: %v", err)
	}
	state.SetEVM(mutatedHeader)
	state.Valid()

	if recorded.number != recordedHeader.Number.Uint64() ||
		recorded.hash != recordedHeader.Hash() ||
		recorded.root != recordedHeader.Root ||
		!recorded.status.KnownInvalid() {
		t.Fatalf("recorded snapshot changed with mutable state: status=%d number=%d hash=%s root=%s",
			recorded.status, recorded.number, recorded.hash, recorded.root)
	}
}

func TestRestoreEVMNativeRecordedStateRestoresCompleteState(t *testing.T) {
	recordedHeader := &gethtypes.Header{
		ParentHash: ethcommon.HexToHash("0x" + strings.Repeat("11", ethcommon.HashLength)),
		Number:     big.NewInt(101),
		Root:       ethcommon.HexToHash("0x" + strings.Repeat("22", ethcommon.HashLength)),
		Extra:      []byte("recorded lineage"),
	}
	state := corestate.NewBlockState(77)
	state.SetOrder(88)
	state.SetWeight(99)
	state.SetEVM(recordedHeader)
	state.Valid()
	recordedRoot := ethcommon.HexToHash("0x" + strings.Repeat("33", ethcommon.HashLength))
	state.SetRoot(&recordedRoot)

	recorded, err := snapshotEVMNativeRecordedState(state)
	if err != nil {
		t.Fatalf("snapshot recorded state: %v", err)
	}
	mutatedHeader := gethtypes.CopyHeader(recordedHeader)
	mutatedHeader.Number = big.NewInt(202)
	mutatedHeader.Root = ethcommon.HexToHash("0x" + strings.Repeat("44", ethcommon.HashLength))
	state.SetOrder(303)
	state.SetWeight(404)
	state.SetEVM(mutatedHeader)
	state.Invalid()
	mutatedRoot := ethcommon.HexToHash("0x" + strings.Repeat("55", ethcommon.HashLength))
	state.SetRoot(&mutatedRoot)

	if err := restoreEVMNativeRecordedState(state, recorded); err != nil {
		t.Fatalf("restore recorded state: %v", err)
	}
	if state.GetID() != 77 || state.GetOrder() != 88 || state.GetWeight() != 99 ||
		state.GetStatus().KnownInvalid() || state.GetEVMNumber() != 101 ||
		state.GetEVMHash() != recordedHeader.Hash() || state.GetEVMRoot() != recordedHeader.Root ||
		state.Root() == nil || *state.Root() != recordedRoot {
		t.Fatalf("recorded state was not fully restored: id=%d order=%d weight=%d status=%d number=%d hash=%s evmRoot=%s root=%v",
			state.GetID(), state.GetOrder(), state.GetWeight(), state.GetStatus(), state.GetEVMNumber(),
			state.GetEVMHash(), state.GetEVMRoot(), state.Root())
	}
}

func TestValidateUpdatedEVMNativeRecordedStateRejectsNonEVMMutation(t *testing.T) {
	header := &gethtypes.Header{
		ParentHash: ethcommon.HexToHash("0x" + strings.Repeat("11", ethcommon.HashLength)),
		Number:     big.NewInt(101),
		Root:       ethcommon.HexToHash("0x" + strings.Repeat("22", ethcommon.HashLength)),
	}
	state := corestate.NewBlockState(77)
	state.SetOrder(88)
	state.SetWeight(99)
	state.SetEVM(header)
	state.Valid()
	recorded, err := snapshotEVMNativeRecordedState(state)
	if err != nil {
		t.Fatalf("snapshot recorded state: %v", err)
	}
	if err := validateUpdatedEVMNativeRecordedState(88, state, recorded, header, true); err != nil {
		t.Fatalf("unchanged authoritative state rejected: %v", err)
	}

	state.SetWeight(100)
	if err := validateUpdatedEVMNativeRecordedState(88, state, recorded, header, true); err == nil ||
		!strings.Contains(err.Error(), "complete native state changed") {
		t.Fatalf("non-EVM native mutation was not rejected: %v", err)
	}
	if err := validateUpdatedEVMNativeRecordedState(88, state, recorded, header, false); err != nil {
		t.Fatalf("manual forensic validation rejected a non-EVM recorded-state difference: %v", err)
	}
}

func TestEVMNativeRecordedLineageRejectsCompetingLocalFork(t *testing.T) {
	recordedHeader := &gethtypes.Header{
		ParentHash: ethcommon.HexToHash("0x" + strings.Repeat("55", ethcommon.HashLength)),
		Number:     big.NewInt(303),
		Root:       ethcommon.HexToHash("0x" + strings.Repeat("66", ethcommon.HashLength)),
		Extra:      []byte("recorded fork"),
	}
	competingHeader := gethtypes.CopyHeader(recordedHeader)
	competingHeader.ParentHash = ethcommon.HexToHash("0x" + strings.Repeat("77", ethcommon.HashLength))
	competingHeader.Extra = []byte("wrong local fork")

	state := &corestate.BlockState{}
	state.SetEVM(recordedHeader)
	recorded, err := snapshotEVMNativeRecordedState(state)
	if err != nil {
		t.Fatalf("snapshot recorded state: %v", err)
	}

	// Simulate updateBlockState replacing the live object with the locally rebuilt fork.
	state.SetEVM(competingHeader)
	if state.GetEVMHash() != competingHeader.Hash() {
		t.Fatal("test setup did not move the live state to the competing fork")
	}
	if err := validateEVMNativeRecordedLineage(404, recorded, recordedHeader); err != nil {
		t.Fatalf("matching recorded lineage rejected: %v", err)
	}
	err = validateEVMNativeRecordedLineage(404, recorded, competingHeader)
	if !errors.Is(err, errEVMNativeRebuildRecordedLineage) {
		t.Fatalf("competing local fork did not preserve recorded-lineage classification: %v", err)
	}
	if !strings.Contains(err.Error(), "wrong local fork") && !strings.Contains(err.Error(), competingHeader.Hash().String()) {
		t.Fatalf("competing local fork error did not identify the rebuilt header: %v", err)
	}
}

func TestEVMNativeRebuildModeBlocksUncommittedAutomaticHealing(t *testing.T) {
	for _, opts := range []EVMNativeRebuildOptions{
		{RequireCurrentAnchor: true},
		{SkipIfEVMBusy: true},
		{RequireCurrentAnchor: true, SkipIfEVMBusy: true},
	} {
		err := validateEVMNativeRebuildMode(opts)
		if !errors.Is(err, errEVMNativeRebuildForensicOnly) {
			t.Fatalf("uncommitted automatic mode was not rejected for %+v: %v", opts, err)
		}
	}
	if err := validateEVMNativeRebuildMode(EVMNativeRebuildOptions{}); err != nil {
		t.Fatalf("explicit manual forensic mode rejected: %v", err)
	}
	if err := validateEVMNativeRebuildMode(EVMNativeRebuildOptions{
		RequireCurrentAnchor:   true,
		RequireStateCommitment: true,
		SkipIfEVMBusy:          true,
	}); err != nil {
		t.Fatalf("authoritative automatic mode rejected: %v", err)
	}
}

func TestStoredEVMReplayGap(t *testing.T) {
	first, last, ok, err := storedEVMReplayGap(100, 103)
	if err != nil {
		t.Fatalf("gap rejected: %v", err)
	}
	if !ok || first != 101 || last != 102 {
		t.Fatalf("unexpected gap: first=%d last=%d ok=%t", first, last, ok)
	}

	if _, _, ok, err := storedEVMReplayGap(100, 101); err != nil || ok {
		t.Fatalf("sequential replay reported a gap: ok=%t err=%v", ok, err)
	}
	if _, _, _, err := storedEVMReplayGap(100, 100); err == nil {
		t.Fatal("non-advancing recorded EVM number was accepted")
	}
}
