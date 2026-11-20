package pow_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	native "github.com/BlockdagNetworkLabs/bdag/core/types"
	"github.com/BlockdagNetworkLabs/bdag/core/types/pow"
	"github.com/BlockdagNetworkLabs/bdag/params"
	"github.com/ethereum/go-ethereum/common"
	eth "github.com/ethereum/go-ethereum/core/types"
	"io"
	"math/big"
	"reflect"
	"testing"
	"time"
)

type issue624Reader struct{ io.Reader }
type issue624Row struct {
	ID                         byte
	Name                       string
	Factory                    string
	Cases                      int
	DecodeAccepted             int
	DecodeRejected             int
	CompleteRoundTrips         int
	HashChanged                int
	FieldsChanged              int
	ProofChanged               int
	TailChanged                int
	TransactionOrParentChanged int
	FirstNonRoundTrip          map[string]any
}

func TestIssue624CompleteBodyCodecMatrix(t *testing.T) {
	values := []uint32{0, 1, 255, 256, 0x01020304, 0x7fffffff, 0x80000000, 0xffffffff}
	address := common.HexToAddress("0x1234")
	txs := eth.Transactions{}
	for i := 0; i < 2; i++ {
		txs = append(txs, eth.NewTx(&eth.LegacyTx{Nonce: uint64(i), GasPrice: big.NewInt(1), Gas: 21000, To: &address, Value: big.NewInt(0), Data: []byte{0xa0, byte(i)}}))
	}
	parent := common.HexToHash("0xabcd")
	total := 0
	for id := pow.PowType(0); id <= pow.SCRYPT; id++ {
		row := issue624Row{ID: byte(id), Name: pow.GetPowName(id), Factory: reflect.TypeOf(pow.GetInstance(id, 0, 0, nil)).String()}
		for _, extra := range values {
			for _, nonce := range values {
				for pattern := 0; pattern < 2; pattern++ {
					proof := make([]byte, pow.PROOFDATA_LENGTH)
					if pattern != 0 {
						for i := range proof {
							proof[i] = byte(i*37 + 11)
						}
					}
					for count := 0; count <= 2; count++ {
						for _, generic := range []bool{false, true} {
							p := pow.GetInstance(id, extra, nonce, proof)
							h := native.BlockHeader{Version: 0x01020304, ParentRoot: common.HexToHash("0x1234"), TxRoot: common.HexToHash("0x5678"), StateRoot: common.HexToHash("0x9abc"), PowRoot: common.HexToHash("0xdef0"), Difficulty: 0x1f00ffff, Timestamp: time.Unix(1758551400, 0), BlockHeight: 0x1122334455667788, CoinbaseAddress: address, Reward: 0x8877665544332211, Pow: p}
							original := native.Block{Header: h, Parents: []*common.Hash{&parent}, Transactions: txs[:count]}
							var wire bytes.Buffer
							if err := original.Serialize(&wire); err != nil {
								t.Fatal(err)
							}
							suffix := []byte{0xab, 0xcd, 0xef, 0xfe}
							var reader io.Reader = bytes.NewReader(append(append([]byte(nil), wire.Bytes()...), suffix...))
							if generic {
								reader = issue624Reader{reader}
							}
							var decoded native.Block
							err := decoded.Deserialize(reader)
							row.Cases++
							total++
							if err != nil {
								row.DecodeRejected++
								if row.FirstNonRoundTrip == nil {
									row.FirstNonRoundTrip = map[string]any{"extra": extra, "nonce": nonce, "pattern": pattern, "transactions": count, "generic_reader": generic, "decode_error_type": fmt.Sprintf("%T", err)}
								}
								continue
							}
							row.DecodeAccepted++
							hashEqual := decoded.BlockHash() == original.BlockHash()
							fieldsEqual := decoded.Header.Pow.GetPowType() == id && decoded.Header.Pow.GetExtraNonce1() == extra && decoded.Header.Pow.GetNonce() == nonce
							proofEqual := decoded.Header.Pow.GetProofData() == p.GetProofData()
							tail, e := io.ReadAll(reader)
							tailEqual := e == nil && bytes.Equal(tail, suffix)
							recordsEqual := len(decoded.Parents) == 1 && *decoded.Parents[0] == parent && len(decoded.Transactions) == count
							if recordsEqual {
								for i, tx := range decoded.Transactions {
									if tx.Hash() != txs[i].Hash() {
										recordsEqual = false
									}
								}
							}
							var rebuilt bytes.Buffer
							encodeErr := decoded.Serialize(&rebuilt)
							exact := hashEqual && fieldsEqual && proofEqual && tailEqual && recordsEqual && encodeErr == nil && bytes.Equal(rebuilt.Bytes(), wire.Bytes())
							if !hashEqual {
								row.HashChanged++
							}
							if !fieldsEqual {
								row.FieldsChanged++
							}
							if !proofEqual {
								row.ProofChanged++
							}
							if !tailEqual {
								row.TailChanged++
							}
							if !recordsEqual {
								row.TransactionOrParentChanged++
							}
							if exact {
								row.CompleteRoundTrips++
							} else if row.FirstNonRoundTrip == nil {
								row.FirstNonRoundTrip = map[string]any{"extra": extra, "nonce": nonce, "pattern": pattern, "transactions": count, "generic_reader": generic, "hash_equal": hashEqual, "fields_equal": fieldsEqual, "proof_equal": proofEqual, "tail_equal": tailEqual, "records_equal": recordsEqual, "wire_bytes": wire.Len()}
							}
						}
					}
				}
			}
		}
		if row.Cases != 768 {
			t.Fatal("incomplete bounded matrix")
		}
		if id == pow.SCRYPT && row.CompleteRoundTrips != row.Cases {
			t.Fatalf("active SCRYPT complete body invariant failed: %#v", row)
		}
		if id == pow.BDAGXKECCAKV1 && row.HashChanged == 0 {
			t.Fatal("mixnet codec risk was not reproduced")
		}
		data, err := json.Marshal(row)
		if err != nil {
			t.Fatal(err)
		}
		t.Log("ISSUE624_BODY_ROW " + string(data))
	}
	if total != 8448 {
		t.Fatal("matrix count differs")
	}
}

func TestIssue624DeclaredModeEligibility(t *testing.T) {
	modes := []struct {
		name   string
		p      *params.Params
		active pow.PowType
	}{
		{"mainnet", &params.MainNetParams, pow.SCRYPT},
		{"mainnet-isolated", &params.MainNetIsolatedParams, pow.SCRYPT},
		{"testnet", &params.TestNetParams, pow.SCRYPT},
		{"privnet", &params.PrivNetParams, pow.SCRYPT},
		{"devnet", &params.DevNetParams, pow.SCRYPT},
		{"mixnet", &params.MixNetParams, pow.BDAGXKECCAKV1}}
	for _, mode := range modes {
		for _, height := range []pow.MainHeight{0, 1, 49, 50, 51, 99, 100, 101, 0xffffffff} {
			for id := pow.PowType(0); id <= pow.SCRYPT; id++ {
				p := pow.GetInstance(id, 0, 1, nil)
				p.SetParams(mode.p.PowConfig)
				p.SetMainHeight(height)
				want := id == mode.active
				if p.CheckAvailable() != want {
					t.Fatalf("mode=%s height=%d id=%d eligibility changed", mode.name, height, id)
				}
			}
		}
		t.Logf("ISSUE624_MODE name=%s allowed=%s checked=99 source-config-only=true", mode.name, pow.GetPowName(mode.active))
	}
}
