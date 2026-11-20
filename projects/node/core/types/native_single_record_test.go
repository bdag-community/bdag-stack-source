package types_test

import (
	"bytes"
	native "github.com/BlockdagNetworkLabs/bdag/core/types"
	"github.com/BlockdagNetworkLabs/bdag/core/types/pow"
	"github.com/ethereum/go-ethereum/common"
	eth "github.com/ethereum/go-ethereum/core/types"
	"math/big"
	"testing"
	"time"
)

func singleRecordFixture(t *testing.T, count int) (native.Block, []byte) {
	t.Helper()
	address := common.Address{3}
	parent := common.Hash{4}
	block := native.Block{Header: native.BlockHeader{Timestamp: time.Unix(1, 0), Pow: pow.GetInstance(pow.SCRYPT, 0xffffffff, 0x80000000, nil)}, Parents: []*common.Hash{&parent}}
	for i := 0; i < count; i++ {
		block.Transactions = append(block.Transactions, eth.NewTx(&eth.LegacyTx{Nonce: uint64(i), GasPrice: big.NewInt(1), Gas: 21000, To: &address, Value: big.NewInt(0), Data: []byte{byte(i)}}))
	}
	var wire bytes.Buffer
	if err := block.Serialize(&wire); err != nil {
		t.Fatal(err)
	}
	return block, wire.Bytes()
}
func TestNativeByteRecordRejectsSuffix(t *testing.T) {
	for _, count := range []int{0, 1, 2} {
		original, wire := singleRecordFixture(t, count)
		_, second := singleRecordFixture(t, 1)
		suffixes := [][]byte{{0}, {0xab, 0xcd, 0xef}, second}
		for _, suffix := range suffixes {
			input := append(append([]byte(nil), wire...), suffix...)
			before := append([]byte(nil), input...)
			if block, err := native.NewBlockFromBytes(input); err == nil || block != nil {
				t.Fatalf("single byte record accepted suffix length=%d tx=%d", len(suffix), count)
			}
			if !bytes.Equal(input, before) {
				t.Fatal("refusal modified caller input")
			}
			reader := bytes.NewReader(input)
			block, err := native.NewBlockFromReader(reader)
			if err != nil || *block.Hash() != original.BlockHash() || reader.Len() != len(suffix) {
				t.Fatal("streaming reader composition changed")
			}
		}
	}
}
func TestNativeByteRecordExactRoundTrip(t *testing.T) {
	for _, count := range []int{0, 1, 2} {
		original, wire := singleRecordFixture(t, count)
		block, err := native.NewBlockFromBytes(wire)
		if err != nil || block == nil || *block.Hash() != original.BlockHash() || len(block.Transactions()) != count {
			t.Fatal("complete byte record identity changed")
		}
		raw, err := block.Bytes()
		if err != nil || !bytes.Equal(raw, wire) {
			t.Fatal("exact serialized record changed")
		}
		for size := 0; size < len(wire); size++ {
			if block, err := native.NewBlockFromBytes(wire[:size]); err == nil || block != nil {
				t.Fatalf("truncated record accepted length=%d tx=%d", size, count)
			}
		}
	}
}
