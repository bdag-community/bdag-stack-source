package types

// import (
// 	"bytes"
// 	"math/big"
// 	"testing"
// 	"time"

// 	"github.com/BlockdagNetworkLabs/bdag/core/json"
// 	"github.com/BlockdagNetworkLabs/bdag/core/types/pow"
// 	"github.com/ethereum/go-ethereum/common"
// )

// // dummyPow is a minimal implementation of pow.IPow used only for tests.
// type dummyPow struct{}

// func (d *dummyPow) Verify([]byte, common.Hash, uint32) error             { return nil }
// func (d *dummyPow) SetNonce(uint64)                                      {}
// func (d *dummyPow) GetNextDiffBig(*big.Int, *big.Int, *big.Int) *big.Int { return big.NewInt(0) }
// func (d *dummyPow) GetNonce() uint64                                     { return 0 }
// func (d *dummyPow) GetPowType() pow.PowType                              { return pow.BLAKE2BD }
// func (d *dummyPow) SetPowType(pow.PowType)                               {}
// func (d *dummyPow) GetProofData() string                                 { return "" }
// func (d *dummyPow) SetProofData([]byte)                                  {}
// func (d *dummyPow) Bytes() pow.PowBytes                                  { return make(pow.PowBytes, pow.POW_LENGTH) }
// func (d *dummyPow) BlockData() pow.PowBytes {
// 	return make(pow.PowBytes, pow.POW_LENGTH-pow.PROOFDATA_LENGTH)
// }
// func (d *dummyPow) GetSafeDiff(uint64) *big.Int                 { return big.NewInt(0) }
// func (d *dummyPow) PowPercent() *big.Int                        { return big.NewInt(0) }
// func (d *dummyPow) GetPowResult() json.PowResult                { return json.PowResult{} }
// func (d *dummyPow) SetParams(*pow.PowConfig)                    {}
// func (d *dummyPow) SetMainHeight(pow.MainHeight)                {}
// func (d *dummyPow) CheckAvailable() bool                        { return true }
// func (d *dummyPow) CompareDiff(*big.Int, *big.Int) bool         { return true }
// func (d *dummyPow) FindSolver([]byte, common.Hash, uint32) bool { return true }

// // TestBlockHeaderSerializeDeserialize ensures that headers round-trip
// // through RLP serialization without loss when no upgrade bits are set.
// func TestBlockHeaderSerializeDeserialize(t *testing.T) {
// 	h := &BlockHeader{
// 		Version:         0x20000000,
// 		ParentRoot:      common.HexToHash("0x01"),
// 		TxRoot:          common.HexToHash("0x02"),
// 		StateRoot:       common.HexToHash("0x03"),
// 		Difficulty:      1,
// 		Timestamp:       time.Unix(1, 0),
// 		BlockHeight:     1,
// 		CoinbaseAddress: common.HexToAddress("0x0"),
// 		Reward:          100,
// 		Pow:             &dummyPow{},
// 	}

// 	var buf bytes.Buffer
// 	if err := h.Serialize(&buf); err != nil {
// 		t.Fatalf("serialize: %v", err)
// 	}

// 	var decoded BlockHeader
// 	if err := decoded.Deserialize(bytes.NewReader(buf.Bytes())); err != nil {
// 		t.Fatalf("deserialize: %v", err)
// 	}

// 	if decoded.Version != h.Version || decoded.ParentRoot != h.ParentRoot ||
// 		decoded.TxRoot != h.TxRoot || decoded.StateRoot != h.StateRoot ||
// 		decoded.Difficulty != h.Difficulty || decoded.BlockHeight != h.BlockHeight ||
// 		decoded.CoinbaseAddress != h.CoinbaseAddress || decoded.Reward != h.Reward ||
// 		!decoded.Timestamp.Equal(h.Timestamp) {
// 		t.Fatalf("headers differ after round-trip: %#v vs %#v", decoded, h)
// 	}

// 	// The dummy pow used in this test is replaced with a real implementation
// 	// during deserialization, so a direct BlockData byte-for-byte comparison is
// 	// not meaningful here. The field-by-field checks above ensure encoding and
// 	// decoding remain stable.
// }