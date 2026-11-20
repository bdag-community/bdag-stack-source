package miner

import (
	"encoding/hex"
	"encoding/json"
	"math/big"
	"reflect"
	"testing"
	"time"

	"github.com/BlockdagNetworkLabs/bdag/services/mining"
	ethcommon "github.com/ethereum/go-ethereum/common"
	ethtypes "github.com/ethereum/go-ethereum/core/types"
)

func TestGBTTransactionFeeGoldenAlignment(t *testing.T) {
	recipient := ethcommon.HexToAddress("0x0000000000000000000000000000000000000001")
	txs := []*ethtypes.Transaction{
		ethtypes.NewTx(&ethtypes.LegacyTx{Nonce: 0, GasPrice: big.NewInt(3), Gas: 21000, To: &recipient, Value: big.NewInt(0)}),
		ethtypes.NewTx(&ethtypes.LegacyTx{Nonce: 1, GasPrice: big.NewInt(5), Gas: 23000, To: &recipient, Value: big.NewInt(0)}),
		ethtypes.NewTx(&ethtypes.LegacyTx{Nonce: 2, GasPrice: big.NewInt(0), Gas: 21000, To: &recipient, Value: big.NewInt(0)}),
	}
	cases := []struct {
		name  string
		order []int
		fees  []int64
	}{
		{"empty", nil, nil},
		{"one", []int{0}, []int64{63000}},
		{"two", []int{0, 1}, []int64{63000, 115000}},
		{"zero_fee", []int{2}, []int64{0}},
		{"reordered", []int{1, 2, 0}, []int64{115000, 0, 63000}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := newRemoteGBTResultTestMiner()
			prepareFeeTestMiner(m)
			m.template.Fees = make([]int64, len(tc.order)+1)
			for i, index := range tc.order {
				m.template.Block.Transactions = append(m.template.Block.Transactions, txs[index])
				m.template.Fees[i+1] = tc.fees[i]
				m.template.BlockFees += tc.fees[i]
			}
			m.template.BlockFees -= 99
			m.template.Fees[0] = -m.template.BlockFees
			beforeFees := append([]int64(nil), m.template.Fees...)
			beforeHeader := m.template.Block.Header
			result, err := (&GBTWorker{miner: m}).getResult(nil)
			if err != nil {
				t.Fatal(err)
			}
			raw, err := json.Marshal(result)
			if err != nil {
				t.Fatal(err)
			}
			t.Logf("GBT_GOLDEN_JSON %s %s", tc.name, raw)
			var wire struct{ Transactions []struct{ Fee int64 } }
			if err := json.Unmarshal(raw, &wire); err != nil {
				t.Fatal(err)
			}
			if len(result.Transactions) != len(tc.order) || len(wire.Transactions) != len(tc.order) || result.BlockFees != m.template.BlockFees {
				t.Fatal("count or aggregate changed")
			}
			for i, index := range tc.order {
				want := tc.fees[i]
				if result.Transactions[i].Fee != want || wire.Transactions[i].Fee != want {
					t.Errorf("transaction%d API/wire fee=%d/%d want%d", i, result.Transactions[i].Fee, wire.Transactions[i].Fee, want)
				}
				encoded, err := txs[index].MarshalBinary()
				if err != nil {
					t.Fatal(err)
				}
				tx := result.Transactions[i]
				if tx.Hash != txs[index].Hash().String() || tx.Data != hex.EncodeToString(encoded) || tx.Weight != 2000000 || len(tx.Depends) != 0 {
					t.Fatal("transaction identity, RLP, weight or dependencies changed")
				}
			}
			if !reflect.DeepEqual(beforeFees, m.template.Fees) || !reflect.DeepEqual(beforeHeader, m.template.Block.Header) {
				t.Fatal("template mutated")
			}
			// The sentinel is not a transaction fee; varying it must not alter
			// any emitted field, including aggregate, reward and authorization.
			m.template.Fees[0] = 123456
			second, err := (&GBTWorker{miner: m}).getResult(nil)
			if err != nil {
				t.Fatal(err)
			}
			secondRaw, err := json.Marshal(second)
			if err != nil {
				t.Fatal(err)
			}
			if string(secondRaw) != string(raw) {
				t.Error("aggregate sentinel leaked into template response")
			}
		})
	}
}

func TestGBTTransactionFeeMalformedLayout(t *testing.T) {
	recipient := ethcommon.HexToAddress("0x0000000000000000000000000000000000000001")
	tx := ethtypes.NewTx(&ethtypes.LegacyTx{GasPrice: big.NewInt(3), Gas: 21000, To: &recipient, Value: big.NewInt(0)})
	for _, tc := range []struct {
		name string
		fees []int64
	}{
		{"nil", nil}, {"empty", []int64{}}, {"missing_transaction", []int64{-63000}}, {"extra", []int64{-63000, 63000, 1}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := newRemoteGBTResultTestMiner()
			prepareFeeTestMiner(m)
			m.template.Block.Transactions = ethtypes.Transactions{tx}
			m.template.Fees = tc.fees
			result, err := (&GBTWorker{miner: m}).getResult(nil)
			if err == nil || result != nil {
				t.Fatal("malformed layout must fail without partial response")
			}
			m.template.Fees = []int64{-63000, 63000}
			result, err = (&GBTWorker{miner: m}).getResult(nil)
			if err != nil || result.Transactions[0].Fee != 63000 {
				t.Fatal("valid next template did not recover")
			}
		})
	}
}

func TestGBTEmptyTransactionFeeCompatibility(t *testing.T) {
	for _, fees := range [][]int64{nil, {}, {0}, {99, 100}} {
		m := newRemoteGBTResultTestMiner()
		prepareFeeTestMiner(m)
		m.template.Fees = fees
		result, err := (&GBTWorker{miner: m}).getResult(nil)
		if err != nil || len(result.Transactions) != 0 || result.BlockFees != 99 || result.Reward != 10 {
			t.Fatal("empty transaction compatibility changed")
		}
	}
}

func prepareFeeTestMiner(m *Miner) {
	m.policy = &mining.Policy{PeerID: "test-peer"}
	m.timeSource = fixedMedianTimeSource{now: time.Unix(1_770_000_010, 0)}
	m.minTimestamp = time.Unix(1_770_000_000, 0)
	m.lastTemplate = time.Unix(1_770_000_001, 0)
}
