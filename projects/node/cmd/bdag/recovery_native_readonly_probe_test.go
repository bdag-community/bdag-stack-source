package main

import (
	"bytes"
	"math/big"
	"strings"
	"testing"

	dag "github.com/BlockdagNetworkLabs/bdag/blockdag"
	"github.com/BlockdagNetworkLabs/bdag/core/blockchain"
	"github.com/BlockdagNetworkLabs/bdag/core/dbnamespace"
	"github.com/BlockdagNetworkLabs/bdag/core/serialization"
	"github.com/BlockdagNetworkLabs/bdag/core/state"
	bdagrawdb "github.com/BlockdagNetworkLabs/bdag/database/rawdb"
	"github.com/ethereum/go-ethereum/common"
	ethtypes "github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/ethdb"
)

func TestInspectNativeReadonlyHeadReportsDurableMainTip(t *testing.T) {
	db := bdagrawdb.NewMemoryDatabase()
	nativeHash := common.HexToHash("0x1111")
	nativeRoot := common.HexToHash("0x2222")
	evmRoot := common.HexToHash("0x3333")
	evmHash := writeNativeProbeBlock(t, db, nativeProbeBlock{
		id:        42,
		hash:      nativeHash,
		order:     100,
		height:    90,
		layer:     95,
		stateRoot: nativeRoot,
		evmNumber: 77,
		evmRoot:   evmRoot,
	})
	writeNativeProbeBestState(t, db, nativeHash, 43)
	if err := bdagrawdb.WriteDAGTips(db, []uint64{42}); err != nil {
		t.Fatalf("write DAG tips: %v", err)
	}
	bdagrawdb.WriteBlockID(db, &nativeHash, 42)
	if err := bdagrawdb.WriteBlockOrderSnapshot(db, 100, 42); err != nil {
		t.Fatalf("write order snapshot: %v", err)
	}

	result, err := inspectNativeReadonlyHead(db, "memory-BdagChain", "")
	if err != nil {
		t.Fatalf("inspectNativeReadonlyHead returned error: %v", err)
	}
	if result.MainTipOrder != 100 {
		t.Fatalf("main tip order = %d, want 100", result.MainTipOrder)
	}
	if !equalHex(result.MainTipHash, nativeHash.String()) {
		t.Fatalf("main tip hash = %s, want %s", result.MainTipHash, nativeHash.String())
	}
	if !equalHex(result.MainTipStateRoot, nativeRoot.String()) {
		t.Fatalf("main tip state root = %s, want %s", result.MainTipStateRoot, nativeRoot.String())
	}
	if result.MainTipEVMNumber != 77 {
		t.Fatalf("main tip EVM number = %d, want 77", result.MainTipEVMNumber)
	}
	if !equalHex(result.MainTipEVMHash, evmHash.String()) {
		t.Fatalf("main tip EVM hash = %s, want %s", result.MainTipEVMHash, evmHash.String())
	}
	if !equalHex(result.MainTipEVMRoot, evmRoot.String()) {
		t.Fatalf("main tip EVM root = %s, want %s", result.MainTipEVMRoot, evmRoot.String())
	}
	if !result.BestStateMatchesMainTip {
		t.Fatal("best-chain state did not match main tip")
	}
	if !result.OrderIndexMatchesMainTip {
		t.Fatal("order index did not match main tip")
	}
	reasons := validateNativeReadonlyProbe(result, nativeReadonlyProbeOptions{
		ExpectOrder:        100,
		ExpectOrderSet:     true,
		ExpectHash:         nativeHash.String(),
		ExpectStateRoot:    nativeRoot.String(),
		ExpectEVMNumber:    77,
		ExpectEVMNumberSet: true,
		ExpectEVMHash:      evmHash.String(),
		ExpectEVMRoot:      evmRoot.String(),
	})
	if len(reasons) != 0 {
		t.Fatalf("validation reasons = %v, want none", reasons)
	}
}

func TestValidateNativeReadonlyProbeFailsBrokenOrderIndex(t *testing.T) {
	db := bdagrawdb.NewMemoryDatabase()
	nativeHash := common.HexToHash("0x4444")
	writeNativeProbeBlock(t, db, nativeProbeBlock{
		id:        7,
		hash:      nativeHash,
		order:     12,
		height:    11,
		layer:     12,
		stateRoot: common.HexToHash("0x5555"),
		evmNumber: 9,
		evmRoot:   common.HexToHash("0x6666"),
	})
	writeNativeProbeBestState(t, db, nativeHash, 8)
	if err := bdagrawdb.WriteDAGTips(db, []uint64{7}); err != nil {
		t.Fatalf("write DAG tips: %v", err)
	}
	bdagrawdb.WriteBlockID(db, &nativeHash, 7)
	if err := bdagrawdb.WriteBlockOrderSnapshot(db, 12, 99); err != nil {
		t.Fatalf("write order snapshot: %v", err)
	}

	result, err := inspectNativeReadonlyHead(db, "memory-BdagChain", "")
	if err != nil {
		t.Fatalf("inspectNativeReadonlyHead returned error: %v", err)
	}
	if result.OrderIndexMatchesMainTip {
		t.Fatal("broken order index unexpectedly matched main tip")
	}
	reasons := strings.Join(validateNativeReadonlyProbe(result, nativeReadonlyProbeOptions{}), "; ")
	if !strings.Contains(reasons, "order index") {
		t.Fatalf("validation reasons %q missing order index failure", reasons)
	}
}

func TestValidateNativeReadonlyProbeAllowsForeignMarkerButBlocksApplicableOrMalformed(t *testing.T) {
	base := nativeReadonlyProbeResult{BestStateMatchesMainTip: true, OrderIndexMatchesMainTip: true}
	foreign := base
	foreign.ShutdownMarkerForeign = true
	foreign.ShutdownMarkerClass = "foreign_marker"
	if reasons := validateNativeReadonlyProbe(&foreign, nativeReadonlyProbeOptions{RequireCleanShutdown: true}); len(reasons) != 0 {
		t.Fatalf("foreign marker blocked dataset: %v", reasons)
	}
	applicable := base
	applicable.ShutdownMarkerPending = true
	applicable.ShutdownMarkerClass = "applicable_interrupted_operation"
	if reasons := validateNativeReadonlyProbe(&applicable, nativeReadonlyProbeOptions{RequireCleanShutdown: true}); len(reasons) == 0 {
		t.Fatal("applicable marker did not block validation")
	}
	malformed := base
	malformed.ShutdownMarkerPending = true
	malformed.ShutdownMarkerMalformed = true
	malformed.ShutdownMarkerClass = "malformed_marker"
	if reasons := validateNativeReadonlyProbe(&malformed, nativeReadonlyProbeOptions{RequireCleanShutdown: true}); len(reasons) == 0 || !strings.Contains(reasons[0], "malformed marker") {
		t.Fatalf("malformed marker reasons = %v", reasons)
	}
}

type nativeProbeBlock struct {
	id        uint64
	hash      common.Hash
	order     uint64
	height    uint64
	layer     uint64
	stateRoot common.Hash
	evmNumber uint64
	evmRoot   common.Hash
}

func writeNativeProbeBlock(t *testing.T, db ethdb.Database, block nativeProbeBlock) common.Hash {
	t.Helper()
	evmHeader := &ethtypes.Header{
		Number:     new(big.Int).SetUint64(block.evmNumber),
		Root:       block.evmRoot,
		Difficulty: big.NewInt(1),
		GasLimit:   1,
		Time:       block.evmNumber + 1,
	}
	blockState := state.NewBlockState(block.id)
	blockState.SetOrder(block.order)
	blockState.SetRoot(&block.stateRoot)
	blockState.SetEVM(evmHeader)
	stateBytes, err := blockState.Bytes()
	if err != nil {
		t.Fatalf("encode block state: %v", err)
	}

	var buf bytes.Buffer
	writeNativeProbeElements(t, &buf, uint32(block.id), &block.hash, uint32(0), uint32(0), uint32(dag.MaxId), uint32(block.layer), uint32(block.height), uint64(len(stateBytes)))
	if _, err := buf.Write(stateBytes); err != nil {
		t.Fatalf("write state bytes: %v", err)
	}
	writeNativeProbeElements(t, &buf, uint32(0), uint32(0), uint32(0))
	if err := bdagrawdb.WriteDAGBlockRaw(db, uint(block.id), buf.Bytes()); err != nil {
		t.Fatalf("write DAG block: %v", err)
	}
	return evmHeader.Hash()
}

func writeNativeProbeBestState(t *testing.T, db ethdb.Database, tipHash common.Hash, total uint64) {
	t.Helper()
	workSum := []byte{1}
	serialized := make([]byte, common.HashLength+8+8+common.HashLength+4+len(workSum))
	copy(serialized[:common.HashLength], tipHash[:])
	offset := common.HashLength
	dbnamespace.ByteOrder.PutUint64(serialized[offset:], total)
	offset += 8
	dbnamespace.ByteOrder.PutUint64(serialized[offset:], 0)
	offset += 8
	offset += common.HashLength
	dbnamespace.ByteOrder.PutUint32(serialized[offset:], uint32(len(workSum)))
	offset += 4
	copy(serialized[offset:], workSum)
	if _, err := blockchain.DeserializeBestChainState(serialized); err != nil {
		t.Fatalf("invalid test best-chain state: %v", err)
	}
	if err := bdagrawdb.WriteBestChainState(db, serialized); err != nil {
		t.Fatalf("write best-chain state: %v", err)
	}
}

func writeNativeProbeElements(t *testing.T, buf *bytes.Buffer, elements ...any) {
	t.Helper()
	if err := serialization.WriteElements(buf, elements...); err != nil {
		t.Fatalf("write native probe elements: %v", err)
	}
}
