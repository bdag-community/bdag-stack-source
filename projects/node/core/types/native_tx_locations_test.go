package types_test

import (
	"bytes"
	"math"
	"math/big"
	"testing"

	native "github.com/BlockdagNetworkLabs/bdag/core/types"
	"github.com/ethereum/go-ethereum/common"
	eth "github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/rlp"
	"github.com/holiman/uint256"
)

// Unsigned codec fixtures, not valid chain transactions, blobs or signatures.
func txLocationTransactions() []*eth.Transaction {
	to := common.Address{3}
	return []*eth.Transaction{
		eth.NewTransaction(0, to, big.NewInt(1), 21000, big.NewInt(1), []byte{1}),
		eth.NewTx(&eth.AccessListTx{ChainID: big.NewInt(1043), Nonce: 1, GasPrice: big.NewInt(1),
			Gas: 21000, To: &to, Value: big.NewInt(1), Data: []byte{2}, AccessList: eth.AccessList{{Address: to, StorageKeys: []common.Hash{{4}}}}}),
		eth.NewTx(&eth.DynamicFeeTx{ChainID: big.NewInt(1043), Nonce: 2, GasTipCap: big.NewInt(1),
			GasFeeCap: big.NewInt(2), Gas: 21000, To: &to, Value: big.NewInt(1), Data: []byte{3}}),
		eth.NewTx(&eth.BlobTx{ChainID: uint256.NewInt(1043), Nonce: 3, GasTipCap: uint256.NewInt(1),
			GasFeeCap: uint256.NewInt(2), Gas: 21000, To: to, Value: uint256.NewInt(1), Data: []byte{4},
			BlobFeeCap: uint256.NewInt(1), V: uint256.NewInt(0), R: uint256.NewInt(0), S: uint256.NewInt(0)}),
	}
}

func txLocationBlock(t *testing.T, transactions []*eth.Transaction) (native.Block, []byte) {
	t.Helper()
	block := native.Block{Header: decodeHeaderFixture(), Transactions: transactions}
	var encoded bytes.Buffer
	if err := block.Serialize(&encoded); err != nil {
		t.Fatal(err)
	}
	return block, encoded.Bytes()
}

func TestNativeTxLocCompleteRecord(t *testing.T) {
	all := txLocationTransactions()
	cases := [][]*eth.Transaction{nil, {all[0]}, {all[1]}, {all[2]}, {all[3]}, {all[0], all[1]}, all}
	for _, transactions := range cases {
		original, wire := txLocationBlock(t, transactions)
		r := bytes.NewBuffer(append([]byte(nil), wire...))
		var decoded native.Block
		locations, err := decoded.DeserializeTxLoc(r)
		if err != nil || r.Len() != 0 || len(locations) != len(transactions) || len(decoded.Transactions) != len(transactions) ||
			decoded.Header.BlockHash() != original.Header.BlockHash() {
			t.Fatalf("complete record count=%d not decoded and consumed exactly", len(transactions))
		}
		start := native.BaseBlockHeaderPayload + 2 // zero parents and short transaction count.
		for i, location := range locations {
			if location.TxStart != start || location.TxLen <= 0 || location.TxStart+location.TxLen > len(wire) {
				t.Fatal("transaction locations must be positive, contiguous and inside the record")
			}
			var tx eth.Transaction
			if err := tx.DecodeRLP(rlp.NewStream(bytes.NewReader(wire[location.TxStart:location.TxStart+location.TxLen]), 0)); err != nil ||
				tx.Hash() != transactions[i].Hash() || decoded.Transactions[i].Hash() != transactions[i].Hash() {
				t.Fatal("location slice or decoded transaction identity differs")
			}
			start += location.TxLen
		}
		if start != len(wire) {
			t.Fatal("transaction locations do not cover the complete payload")
		}
		wrapped := native.NewBlock(&original)
		wrappedLocations, err := wrapped.TxLoc()
		if err != nil || len(wrappedLocations) != len(locations) {
			t.Fatal("public TxLoc adapter failed")
		}
		for i := range locations {
			if locations[i] != wrappedLocations[i] {
				t.Fatal("public adapter location differs")
			}
		}
	}
	t.Log("all four implemented vendored types0..3 and mixed RLP record locations checked; no blob sidecar/chain-validity claim")
}

func TestNativeTxLocMalformedInput(t *testing.T) {
	prefix := decodeCountFixture(t, 0, 1)
	for _, payload := range [][]byte{nil, {0x80}, {0xc0}, {0xb8}, {0xb8, 0xff}, {0x82, 0x04, 0x01}} {
		var block native.Block
		locations, err := block.DeserializeTxLoc(bytes.NewBuffer(append(append([]byte(nil), prefix...), payload...)))
		if err == nil || locations != nil || len(block.Transactions) != 0 {
			t.Fatal("malformed/truncated RLP must surface an error without an invented transaction/location")
		}
	}
	_, valid := txLocationBlock(t, txLocationTransactions()[:2])
	for length := 0; length < len(valid); length++ {
		var block native.Block
		if _, err := block.DeserializeTxLoc(bytes.NewBuffer(valid[:length])); err == nil {
			t.Fatalf("truncated record length=%d accepted", length)
		}
	}
}

func TestNativeTxLocReaderComposition(t *testing.T) {
	_, wire := txLocationBlock(t, txLocationTransactions())
	r := bytes.NewBuffer(append(append([]byte(nil), wire...), 0xab))
	var block native.Block
	locations, err := block.DeserializeTxLoc(r)
	if err != nil || len(locations) != 4 || r.Len() != 1 {
		t.Fatal("record decoder consumed too much or too little")
	}
	if suffix, err := r.ReadByte(); err != nil || suffix != 0xab {
		t.Fatal("composed reader suffix changed")
	}
}

func TestNativeTxLocExistingCountLimits(t *testing.T) {
	for _, count := range []uint64{native.MaxTxPerBlock + 1, math.MaxUint64} {
		var block native.Block
		locations, err := block.DeserializeTxLoc(bytes.NewBuffer(decodeCountFixture(t, 0, count)))
		if err == nil || locations != nil || block.Transactions != nil {
			t.Fatal("existing transaction preallocation bound changed")
		}
	}
	var block native.Block
	if _, err := block.DeserializeTxLoc(bytes.NewBuffer(decodeCountFixture(t, native.MaxParentsPerBlock+1, 0))); err == nil || block.Parents != nil {
		t.Fatal("existing parent bound changed")
	}
}
