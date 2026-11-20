package types_test

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"math"
	"math/big"
	"testing"
	"time"

	serialization "github.com/BlockdagNetworkLabs/bdag/core/serialization"
	native "github.com/BlockdagNetworkLabs/bdag/core/types"
	"github.com/BlockdagNetworkLabs/bdag/core/types/pow"
	"github.com/ethereum/go-ethereum/common"
	eth "github.com/ethereum/go-ethereum/core/types"
)

func decodeHeaderFixture() native.BlockHeader {
	return native.BlockHeader{Version: 1, Timestamp: time.Unix(1758551400, 0),
		Pow: pow.GetInstance(pow.SCRYPT, 0x01020304, 0x80000000, nil)}
}

func decodeCountFixture(t *testing.T, parents, transactions uint64) []byte {
	t.Helper()
	var b bytes.Buffer
	h := decodeHeaderFixture()
	if err := h.Serialize(&b); err != nil {
		t.Fatal(err)
	}
	if err := serialization.WriteVarInt(&b, 0, parents); err != nil {
		t.Fatal(err)
	}
	if parents <= native.MaxParentsPerBlock {
		for i := uint64(0); i < parents; i++ {
			if err := serialization.WriteElements(&b, &common.Hash{}); err != nil {
				t.Fatal(err)
			}
		}
		if err := serialization.WriteVarInt(&b, 0, transactions); err != nil {
			t.Fatal(err)
		}
	}
	return b.Bytes()
}

func TestNativeBlockDecodeCountBound(t *testing.T) {
	// Fail on a bounded sentinel BEFORE any huge count reaches an old allocator.
	for _, count := range []uint64{native.MaxTxPerBlock + 1, math.MaxUint64} {
		var block native.Block
		err := block.Decode(bytes.NewReader(decodeCountFixture(t, 0, count)), 0)
		want := fmt.Sprintf("Block.Decode: too many transactions to fit into a block [count %d, max %d]", count, native.MaxTxPerBlock)
		if err == nil || err.Error() != want || block.Transactions != nil {
			t.Fatalf("count=%d not refused before transaction allocation (capacity=%d)", count, cap(block.Transactions))
		}
	}
}

func TestNativeBlockDecodeBoundaryTruncation(t *testing.T) {
	for _, count := range []uint64{1, native.MaxTxPerBlock} {
		var block native.Block
		err := block.Decode(bytes.NewReader(decodeCountFixture(t, 0, count)), 0)
		if !errors.Is(err, io.EOF) || len(block.Transactions) != 0 || cap(block.Transactions) != int(count) {
			t.Fatalf("declared allowed count=%d with no RLP payload changed behavior", count)
		}
	}
	var block native.Block
	if err := block.Decode(bytes.NewReader(decodeCountFixture(t, native.MaxParentsPerBlock+1, 0)), 0); err == nil || block.Parents != nil || block.Transactions != nil {
		t.Fatal("existing parent preallocation refusal changed")
	}
	full := decodeCountFixture(t, 0, 0)
	for length := 0; length < len(full); length++ {
		var partial native.Block
		if err := partial.Decode(bytes.NewReader(full[:length]), 0); err == nil || partial.Transactions != nil {
			t.Fatalf("truncated header/count input length=%d accepted or allocated transactions", length)
		}
	}
}

// These transactions are well-formed RLP fixtures, NOT signed/chain-valid inputs.
func TestNativeBlockDecodeCodecRoundTrip(t *testing.T) {
	for _, count := range []int{0, 1, 2, 16} {
		block := native.Block{Header: decodeHeaderFixture(), Parents: []*common.Hash{{1}, {2}}}
		for i := 0; i < count; i++ {
			block.Transactions = append(block.Transactions, eth.NewTransaction(uint64(i), common.Address{3},
				big.NewInt(1), 21000, big.NewInt(1), []byte{byte(i)}))
		}
		var encoded bytes.Buffer
		if err := block.Serialize(&encoded); err != nil {
			t.Fatal(err)
		}
		wire := append(append([]byte(nil), encoded.Bytes()...), 0xab)
		r := bytes.NewReader(wire)
		var decoded native.Block
		if err := decoded.Decode(r, 0); err != nil || r.Len() != 1 || len(decoded.Transactions) != count ||
			decoded.Header.BlockHash() != block.Header.BlockHash() || len(decoded.Parents) != 2 {
			t.Fatalf("bounded complete codec fixture count=%d changed", count)
		}
		var rebuilt bytes.Buffer
		if err := decoded.Serialize(&rebuilt); err != nil || !bytes.Equal(encoded.Bytes(), rebuilt.Bytes()) {
			t.Fatal("complete block byte identity changed")
		}
		for i, tx := range block.Transactions {
			if tx.Hash() != decoded.Transactions[i].Hash() {
				t.Fatal("RLP transaction identity changed")
			}
		}
	}
}
