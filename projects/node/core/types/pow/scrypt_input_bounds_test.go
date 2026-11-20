package pow_test

import (
	"encoding/binary"
	"fmt"
	"math/big"
	"testing"

	"github.com/BlockdagNetworkLabs/bdag/common/hash"
	"github.com/BlockdagNetworkLabs/bdag/core/types/pow"
	"github.com/ethereum/go-ethereum/common"
)

func TestScryptInputBounds(t *testing.T) {
	for length := 0; length < 185; length++ {
		panicked := false
		var err error
		func() {
			defer func() {
				if recover() != nil {
					panicked = true
				}
			}()
			err = pow.GetInstance(pow.SCRYPT, 0, 0, nil).Verify(make([]byte, length), common.Hash{}, 0)
		}()
		if panicked || err == nil || err.Error() != "headerData too short" {
			t.Errorf("length=%d must return fixed short-input error without panic (panic=%v)", length, panicked)
		}
	}
}

// Direct calls to the C-backed hash always receive a complete 80-byte buffer.
func TestScryptCompleteInputControls(t *testing.T) {
	limit := new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 256), big.NewInt(1))
	p := pow.GetInstance(pow.SCRYPT, 0, 0, nil)
	p.SetParams(&pow.PowConfig{ScryptPowLimit: limit})
	cases := 0
	for _, nonce := range []uint32{0, 1, 255, 256, 0x01020304, 0x7fffffff, 0x80000000, 0xffffffff} {
		for _, length := range []int{185, 186, 354} {
			input := make([]byte, length)
			for i := range input {
				input[i] = byte(i*37 + 11)
			}
			binary.LittleEndian.PutUint32(input[181:185], nonce)
			full := make([]byte, 80)
			binary.BigEndian.PutUint32(full[0:4], binary.LittleEndian.Uint32(input[0:4]))
			copy(full[4:36], input[4:36])
			copy(full[36:68], input[100:132])
			binary.BigEndian.PutUint32(full[68:72], binary.LittleEndian.Uint32(input[136:140]))
			binary.BigEndian.PutUint32(full[72:76], binary.LittleEndian.Uint32(input[132:136]))
			copy(full[76:80], input[181:185])
			h := hash.HashbdagScrypt(full)
			number := pow.HashToBig(&h)
			for _, bits := range []uint32{0, 0x01800001, 0x01010000, 0x2100ffff, 0x2200ffff} {
				target := pow.CompactToBig(bits)
				want := ""
				switch {
				case target.Sign() <= 0:
					want = fmt.Sprintf("block target difficulty of %064x is too low", target)
				case target.Cmp(limit) > 0:
					want = fmt.Sprintf("block target difficulty of %064x is higher than max of %064x", target, limit)
				case number.Cmp(target) > 0:
					want = fmt.Sprintf("block hash of %064x is higher than expected max of %064x", number, target)
				}
				for _, blockHash := range []common.Hash{{}, common.HexToHash("0x1234")} {
					err := p.Verify(input, blockHash, bits)
					got := ""
					if err != nil {
						got = err.Error()
					}
					if got != want {
						t.Fatalf("complete input result changed nonce=%08x len=%d bits=%08x", nonce, length, bits)
					}
					cases++
				}
			}
		}
	}
	if cases != 240 {
		t.Fatal("complete-input coverage changed")
	}
	t.Logf("240 exact complete-input/difficulty/argument controls; no peers or native storage invoked")
}
