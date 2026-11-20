package main

import (
	"math/big"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/core/types"
)

func TestInspectEVMReadonlyHeadUsesPersistedHeadBlock(t *testing.T) {
	db := rawdb.NewMemoryDatabase()
	root := common.HexToHash("0x1234")
	header := &types.Header{
		Number:     big.NewInt(7),
		Root:       root,
		Difficulty: big.NewInt(1),
		GasLimit:   1,
		Time:       1,
	}
	hash := header.Hash()
	rawdb.WriteHeader(db, header)
	rawdb.WriteCanonicalHash(db, hash, 7)
	rawdb.WriteHeadHeaderHash(db, hash)
	rawdb.WriteHeadBlockHash(db, hash)
	rawdb.WriteLegacyTrieNode(db, root, []byte{0x1})

	result, err := inspectEVMReadonlyHead(db, "memory-chaindata", evmReadonlyProbeOptions{})
	if err != nil {
		t.Fatalf("inspectEVMReadonlyHead returned error: %v", err)
	}
	if result.Number != 7 {
		t.Fatalf("number = %d, want 7", result.Number)
	}
	if result.TargetSource != "head_block" {
		t.Fatalf("target source = %q, want head_block", result.TargetSource)
	}
	if !equalHex(result.HeaderHash, hash.String()) {
		t.Fatalf("header hash = %s, want %s", result.HeaderHash, hash.String())
	}
	if !equalHex(result.HeaderRoot, root.String()) {
		t.Fatalf("header root = %s, want %s", result.HeaderRoot, root.String())
	}
	if !result.HasState {
		t.Fatal("expected state trie root to be present")
	}
	if !result.HeadPointersAgree || !result.FreezerCompatible {
		t.Fatalf("EVM head consistency = pointers:%t freezer:%t", result.HeadPointersAgree, result.FreezerCompatible)
	}
	reasons := validateEVMReadonlyProbe(result, evmReadonlyProbeOptions{
		ExpectNumber:    7,
		ExpectNumberSet: true,
		ExpectHash:      hash.String(),
		ExpectRoot:      root.String(),
		RequireState:    true,
	})
	if len(reasons) != 0 {
		t.Fatalf("validation reasons = %v, want none", reasons)
	}
}

func TestValidateEVMReadonlyProbeFailsMissingStateAndMismatch(t *testing.T) {
	result := &evmReadonlyProbeResult{
		Number:            9,
		HeaderHash:        "0xabc",
		HeaderRoot:        "0xdef",
		HasState:          false,
		TransactionCount:  2,
		ReceiptCount:      1,
		ReceiptsComplete:  false,
		HeadPointersAgree: false,
		FreezerCompatible: false,
	}
	reasons := validateEVMReadonlyProbe(result, evmReadonlyProbeOptions{
		ExpectNumber:    10,
		ExpectNumberSet: true,
		ExpectHash:      "0x123",
		ExpectRoot:      "0x456",
		RequireState:    true,
		RequireReceipts: true,
	})
	got := strings.Join(reasons, "; ")
	for _, want := range []string{
		"state trie root",
		"EVM number 9 differs from expected 10",
		"EVM hash",
		"EVM root",
		"receipt count 1 differs from transaction count 2",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("validation reasons %q missing %q", got, want)
		}
	}
}

func TestInspectEVMReadonlyHeadReportsLatestStatefulInScanBackWindow(t *testing.T) {
	db := rawdb.NewMemoryDatabase()
	statefulRoot := common.HexToHash("0xaaaa")
	volatileRoot := common.HexToHash("0xbbbb")
	stateful := &types.Header{
		Number:     big.NewInt(7),
		Root:       statefulRoot,
		Difficulty: big.NewInt(1),
		GasLimit:   1,
		Time:       1,
	}
	volatile := &types.Header{
		Number:     big.NewInt(9),
		ParentHash: stateful.Hash(),
		Root:       volatileRoot,
		Difficulty: big.NewInt(1),
		GasLimit:   1,
		Time:       2,
	}
	rawdb.WriteHeader(db, stateful)
	rawdb.WriteCanonicalHash(db, stateful.Hash(), 7)
	rawdb.WriteLegacyTrieNode(db, statefulRoot, []byte{0x1})
	rawdb.WriteHeader(db, volatile)
	rawdb.WriteCanonicalHash(db, volatile.Hash(), 9)
	rawdb.WriteHeadHeaderHash(db, volatile.Hash())
	rawdb.WriteHeadBlockHash(db, volatile.Hash())

	result, err := inspectEVMReadonlyHead(db, "memory-chaindata", evmReadonlyProbeOptions{ScanBack: 3})
	if err != nil {
		t.Fatalf("inspectEVMReadonlyHead returned error: %v", err)
	}
	if result.HasState {
		t.Fatal("volatile head unexpectedly had state")
	}
	if result.LatestStateful == nil {
		t.Fatal("latest stateful result missing")
	}
	if result.LatestStateful.Number != 7 {
		t.Fatalf("latest stateful number = %d, want 7", result.LatestStateful.Number)
	}
	if result.LatestStateful.Distance != 2 {
		t.Fatalf("latest stateful distance = %d, want 2", result.LatestStateful.Distance)
	}
	if !equalHex(result.LatestStateful.Root, statefulRoot.String()) {
		t.Fatalf("latest stateful root = %s, want %s", result.LatestStateful.Root, statefulRoot.String())
	}
	if !equalHex(result.LatestStateful.TargetRoot, volatileRoot.String()) {
		t.Fatalf("latest stateful target root = %s, want %s", result.LatestStateful.TargetRoot, volatileRoot.String())
	}
}
