package pow_test

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"reflect"
	"testing"
	"time"

	serialization "github.com/BlockdagNetworkLabs/bdag/core/serialization"
	native "github.com/BlockdagNetworkLabs/bdag/core/types"
	"github.com/BlockdagNetworkLabs/bdag/core/types/pow"
	"github.com/BlockdagNetworkLabs/bdag/params"
	"github.com/ethereum/go-ethereum/common"
)

func compatibilityHeader(p pow.IPow) native.BlockHeader {
	return native.BlockHeader{Version: 0x01020304, ParentRoot: common.HexToHash("0x1234"),
		TxRoot: common.HexToHash("0x5678"), StateRoot: common.HexToHash("0x9abc"),
		PowRoot: common.HexToHash("0xdef0"), Difficulty: 0x1f00ffff,
		Timestamp: time.Unix(1758551400, 0), BlockHeight: 0x1122334455667788,
		CoinbaseAddress: common.HexToAddress("0x123456"), Reward: 0x8877665544332211, Pow: p}
}

// This is characterization, not approval of legacy layouts or factory fallback.
func TestNativePowWireMatrix(t *testing.T) {
	values := []uint32{0, 1, 255, 256, 0x01020304, 0x7fffffff, 0x80000000, 0xffffffff}
	implementations := []string{"*pow.Blake2bd", "*pow.Blake2bd", "*pow.Blake2bd",
		"*pow.Blake2bd", "*pow.Blake2bd", "*pow.Blake2bd", "*pow.BlockdagKeccak256",
		"*pow.CryptoNight", "*pow.bdagXKeccakV1", "*pow.Blake2s", "*pow.Scrypt"}
	cases, preserved := 0, 0
	for id := pow.PowType(0); id <= pow.SCRYPT; id++ {
		for _, extra := range values {
			for _, nonce := range values {
				for pattern := 0; pattern < 2; pattern++ {
					proof := make([]byte, pow.PROOFDATA_LENGTH)
					if pattern != 0 {
						for i := range proof {
							proof[i] = byte(i*37 + 11)
						}
					}
					p := pow.GetInstance(id, extra, nonce, proof)
					if got := reflect.TypeOf(p).String(); got != implementations[int(id)] {
						t.Fatalf("id=%d factory=%s", id, got)
					}
					wire := []byte{byte(id)}
					word := make([]byte, 4)
					binary.LittleEndian.PutUint32(word, nonce)
					wantExtra, wantNonce := nonce, uint32(0)
					trailing := 0
					switch id {
					case pow.SCRYPT:
						x := make([]byte, 4)
						binary.LittleEndian.PutUint32(x, extra)
						wire = append(wire, x...)
						wire = append(wire, word...)
						wantExtra, wantNonce = extra, nonce
					case pow.BLAKE2S:
						wire = append(wire, make([]byte, 5)...)
						wire = append(wire, word...)
						wire = append(wire, 0, 0, 0, 0, 34, 17)
						wantExtra, wantNonce, trailing = 0, nonce<<8, 7
					default:
						wire = append(wire, word...)
						wire = append(wire, 0, 0, 0, 0)
					}
					wire = append(wire, proof...)
					if !bytes.Equal(p.Bytes(), wire) {
						t.Fatalf("id=%d encoder layout changed", id)
					}
					r := bytes.NewReader(wire)
					var decoded pow.IPow
					if err := serialization.ReadElements(r, &decoded); err != nil {
						t.Fatal(err)
					}
					if decoded.GetPowType() != id || decoded.GetExtraNonce1() != wantExtra ||
						decoded.GetNonce() != wantNonce || r.Len() != trailing ||
						decoded.GetProofData() != fmt.Sprintf("%x", wire[9:pow.POW_LENGTH]) {
						t.Fatalf("id=%d extra=%08x nonce=%08x decoded fields/consumption differ", id, extra, nonce)
					}
					h := compatibilityHeader(p)
					var stable, mining bytes.Buffer
					if err := h.Serialize(&stable); err != nil {
						t.Fatal(err)
					}
					if err := h.SerializeForMiningSubmit(&mining); err != nil {
						t.Fatal(err)
					}
					if !bytes.Equal(stable.Bytes(), mining.Bytes()) || stable.Len() != 176+len(wire) ||
						!bytes.Equal(stable.Bytes()[176:], wire) || len(h.BlockData()) != 185 {
						t.Fatal("header layout or stable/mining format changed")
					}
					var rebuilt native.BlockHeader
					rh := bytes.NewReader(stable.Bytes())
					if err := rebuilt.Deserialize(rh); err != nil {
						t.Fatal(err)
					}
					var rebuiltMining native.BlockHeader
					if err := rebuiltMining.DeserializeForMiningSubmit(bytes.NewReader(mining.Bytes())); err != nil {
						t.Fatal(err)
					}
					if rh.Len() != trailing || rebuilt.Pow.GetNonce() != wantNonce ||
						rebuiltMining.Pow.GetNonce() != wantNonce || rebuilt.BlockHash() != rebuiltMining.BlockHash() {
						t.Fatal("header decoder or stable/mining identity differs")
					}
					hWithoutPow, rebuiltWithoutPow := h, rebuilt
					hWithoutPow.Pow, rebuiltWithoutPow.Pow = nil, nil
					if !reflect.DeepEqual(hWithoutPow, rebuiltWithoutPow) {
						t.Fatal("non-PoW header fields changed")
					}
					wantHashPreserved := id == pow.SCRYPT || nonce == 0
					if (h.BlockHash() == rebuilt.BlockHash()) != wantHashPreserved {
						t.Fatalf("id=%d nonce=%08x hash identity differs from characterized layout", id, nonce)
					}
					if id == pow.SCRYPT {
						if !bytes.Equal(p.Bytes(), decoded.Bytes()) || p.GetProofData() != decoded.GetProofData() ||
							!bytes.Equal(h.BlockData(), rebuilt.BlockData()) || stable.Len() != h.SerializeSize() {
							t.Fatal("SCRYPT round-trip invariant failed")
						}
						preserved++
					}
					cases++
				}
			}
		}
	}
	if cases != 1408 || preserved != 128 {
		t.Fatal("matrix coverage changed")
	}
	t.Logf("characterized %d combinations, %d active SCRYPT exact round trips; legacy defects not waived", cases, preserved)
}

func TestNativePowDecoderBounds(t *testing.T) {
	valid := pow.GetInstance(pow.SCRYPT, 0xffffffff, 0x80000000, nil).Bytes()
	for length := 0; length < pow.POW_LENGTH; length++ {
		var p pow.IPow
		if serialization.ReadElements(bytes.NewReader(valid[:length]), &p) == nil {
			t.Fatalf("accepted truncated payload length=%d", length)
		}
	}
	for _, id := range []byte{11, 127, 255} {
		wire := append([]byte(nil), valid...)
		wire[0] = id
		var p pow.IPow
		if serialization.ReadElements(bytes.NewReader(wire), &p) == nil {
			t.Fatalf("accepted unknown id=%d", id)
		}
	}
	var p pow.IPow
	r := bytes.NewReader(append(append([]byte(nil), valid...), 0xab))
	if err := serialization.ReadElements(r, &p); err != nil || r.Len() != 1 {
		t.Fatal("reader composition boundary changed")
	}
}

func TestNativePowModeDeclarations(t *testing.T) {
	modes := []struct {
		name   string
		params *params.Params
		active pow.PowType
	}{
		{"mainnet", &params.MainNetParams, pow.SCRYPT},
		{"mainnet-isolated", &params.MainNetIsolatedParams, pow.SCRYPT},
		{"testnet", &params.TestNetParams, pow.SCRYPT},
		{"privnet", &params.PrivNetParams, pow.SCRYPT},
		{"devnet", &params.DevNetParams, pow.SCRYPT},
		{"mixnet", &params.MixNetParams, pow.BDAGXKECCAKV1},
	}
	for _, mode := range modes {
		t.Run(mode.name, func(t *testing.T) {
			if err := mode.params.PowConfig.Check(); err != nil {
				t.Fatal(err)
			}
			for _, height := range []pow.MainHeight{0, 1, 49, 50, 51, 99, 100, 101, 0xffffffff} {
				for id := pow.PowType(0); id <= pow.SCRYPT; id++ {
					want := pow.PercentValue(0)
					if id == mode.active {
						want = 100
					}
					if mode.params.PowConfig.GetPercentByHeightAndType(height, id) != want {
						t.Fatalf("mode=%s height=%d id=%d eligibility declaration changed", mode.name, height, id)
					}
				}
			}
			h := mode.params.GenesisBlock.Block().Header
			if h.Pow.GetPowType() != pow.SCRYPT {
				t.Fatal("genesis PoW declaration changed")
			}
			var wire bytes.Buffer
			if err := h.Serialize(&wire); err != nil {
				t.Fatal(err)
			}
			var decoded native.BlockHeader
			r := bytes.NewReader(wire.Bytes())
			if err := decoded.Deserialize(r); err != nil || r.Len() != 0 || decoded.BlockHash() != h.BlockHash() {
				t.Fatal("genesis codec/hash round trip failed")
			}
			t.Logf("source mode=%s active=%s genesis=%s; not a live-network observation", mode.name,
				pow.GetPowName(mode.active), pow.GetPowName(h.Pow.GetPowType()))
		})
	}
}
