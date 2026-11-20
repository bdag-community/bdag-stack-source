package main

import (
	"bytes"
	"fmt"
	"math/big"
	"testing"
	"time"

	native "github.com/BlockdagNetworkLabs/bdag/core/types"
	"github.com/BlockdagNetworkLabs/bdag/core/types/pow"
	"github.com/BlockdagNetworkLabs/bdag/core/dbnamespace"
	"github.com/ethereum/go-ethereum/common"
	eth "github.com/ethereum/go-ethereum/core/types"
)

func ibdBoundsFixture(t *testing.T, count int) (native.Block, []byte, []byte) {
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
	frame := make([]byte, 4+wire.Len())
	dbnamespace.ByteOrder.PutUint32(frame[:4], uint32(wire.Len()))
	copy(frame[4:], wire.Bytes())
	return block, wire.Bytes(), frame
}

func TestIBDRecordBoundsRejectsTruncation(t *testing.T) {
	_, _, frame := ibdBoundsFixture(t, 1)
	for size := 0; size < len(frame); size++ {
		t.Run(fmt.Sprintf("prefix_%d", size), func(t *testing.T) {
			input := append([]byte(nil), frame[:size]...)
			before := append([]byte(nil), input...)
			defer func() {
				if v := recover(); v != nil {
					t.Errorf("malformed frame panicked: %v", v)
				}
			}()
			var block IBDBlock
			if err := block.Decode(input); err == nil {
				t.Error("accepted truncated frame")
			}
			if !bytes.Equal(input, before) {
				t.Error("modified caller input")
			}
		})
	}
}

func TestIBDRecordBoundsRejectsOverflow(t *testing.T) {
	for _, length := range []uint32{1, 0xfffffffc, 0xfffffffd, 0xfffffffe, 0xffffffff} {
		t.Run(fmt.Sprintf("length_%d", length), func(t *testing.T) {
			input := make([]byte, 4)
			dbnamespace.ByteOrder.PutUint32(input, length)
			defer func() {
				if v := recover(); v != nil {
					t.Errorf("declared length panicked: %v", v)
				}
			}()
			var block IBDBlock
			if err := block.Decode(input); err == nil {
				t.Error("accepted missing body")
			}
		})
	}
}

func TestIBDRecordBoundsHonorsSliceLength(t *testing.T) {
	_, _, frame := ibdBoundsFixture(t, 1)
	input := frame[:4]
	var block IBDBlock
	if err := block.Decode(input); err == nil {
		t.Fatal("consumed body beyond supplied slice length")
	}
}

func TestIBDRecordBoundsValidConcatenation(t *testing.T) {
	for _, count := range []int{0, 1, 2} {
		t.Run(fmt.Sprintf("transactions_%d", count), func(t *testing.T) {
			original, wire, frame := ibdBoundsFixture(t, count)
			_, _, next := ibdBoundsFixture(t, 1)
			input := append(append([]byte(nil), frame...), next...)
			before := append([]byte(nil), input...)
			var block IBDBlock
			if err := block.Decode(input); err != nil {
				t.Fatal(err)
			}
			if int(block.length) != len(wire) || block.blk == nil || *block.blk.Hash() != original.BlockHash() {
				t.Fatal("valid record identity changed")
			}
			raw, err := block.blk.Bytes()
			if err != nil || !bytes.Equal(raw, wire) || !bytes.Equal(input[4+int(block.length):], next) {
				t.Fatal("valid bytes or concatenated frame changed")
			}
			if !bytes.Equal(input, before) {
				t.Fatal("modified caller input")
			}
		})
	}
}
