package pow

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math/big"

	"github.com/BlockdagNetworkLabs/bdag/common/hash"
	"github.com/BlockdagNetworkLabs/bdag/core/json"
	ethcommon "github.com/ethereum/go-ethereum/common"
	// "golang.org/x/exp/rand"
)

type Scrypt struct {
	Pow
}

func (this *Scrypt) GetPowResult() json.PowResult {
	return json.PowResult{
		PowName:   PowMapString[this.GetPowType()].(string),
		PowType:   uint8(this.GetPowType()),
		Nonce:     this.GetNonce(),
		ProofData: nil,
	}
}

func (this *Scrypt) Verify(headerData []byte, blockHash ethcommon.Hash, targetDiffBits uint32) error {
	// Rebuild the EXACT 80-byte PoW header the miner hashed:
	// [ version(4) | prev(32) | stateRoot(32) | nBits(4) | nTime(4) | nonce(4) ]

	if len(headerData) < 185 { // The nonce slice below requires bytes [181:185].
		return errors.New("headerData too short")
	}

	fullHeader := make([]byte, 80)

	// version(4) + prev(32) come first in your wire layout
	v := binary.LittleEndian.Uint32(headerData[0:4])
	binary.BigEndian.PutUint32(fullHeader[0:4], v)

	copy(fullHeader[4:36], headerData[4:36]) // prevblock (LE)

	copy(fullHeader[36:68], headerData[100:132]) //powroot (LE)

	t := binary.LittleEndian.Uint32(headerData[136:140])
	binary.BigEndian.PutUint32(fullHeader[68:72], t) // nTime (BE)

	b := binary.LittleEndian.Uint32(headerData[132:136])
	binary.BigEndian.PutUint32(fullHeader[72:76], b) //nbits (BE)

	// Nonce is stored after the 5-byte PoW head (powType + 4 zeros):
	copy(fullHeader[76:80], headerData[181:185])

	target := CompactToBig(targetDiffBits)
	if target.Sign() <= 0 {
		return fmt.Errorf("block target difficulty of %064x is too low", target)
	}
	if target.Cmp(this.params.ScryptPowLimit) > 0 {
		return fmt.Errorf("block target difficulty of %064x is higher than max of %064x",
			target, this.params.ScryptPowLimit)
	}

	h := hash.HashbdagScrypt(fullHeader)
	hashNum := HashToBig(&h)

	if hashNum.Cmp(target) > 0 {
		return fmt.Errorf("block hash of %064x is higher than expected max of %064x",
			hashNum, target)
	}
	return nil
}

func (this *Scrypt) GetNextDiffBig(weightedSumDiv *big.Int, oldDiffBig *big.Int, currentPowPercent *big.Int) *big.Int {
	nextDiffBig := weightedSumDiv.Mul(weightedSumDiv, oldDiffBig)
	defer func() {
		nextDiffBig = nextDiffBig.Rsh(nextDiffBig, 32)

	}()
	targetPercent := this.PowPercent()
	if targetPercent.Cmp(big.NewInt(0)) <= 0 {
		return nextDiffBig
	}
	currentPowPercent.Mul(currentPowPercent, big.NewInt(100))
	nextDiffBig.Mul(nextDiffBig, targetPercent)
	nextDiffBig.Div(nextDiffBig, currentPowPercent)
	return nextDiffBig
}

func (this *Scrypt) GetSafeDiff(cur_reduce_diff uint64) *big.Int {
	limitBits := this.params.ScryptPowLimitBits
	limitBitsBig := CompactToBig(limitBits)
	// fmt.Println("limitBitsBig:",limitBitsBig)
	if cur_reduce_diff <= 0 {
		return limitBitsBig
	}
	newTarget := &big.Int{}
	newTarget = newTarget.SetUint64(cur_reduce_diff)
	// Limit new value to the proof of work limit.
	if newTarget.Cmp(this.params.ScryptPowLimit) > 0 {
		newTarget.Set(this.params.ScryptPowLimit)
	}
	return newTarget
}

// compare the target
// wether target match the target diff
func (this *Scrypt) CompareDiff(newTarget *big.Int, target *big.Int) bool {
	return newTarget.Cmp(target) <= 0
}

// pow proof data
func (this *Scrypt) Bytes() PowBytes {
	r := make(PowBytes, 0)
	//write pow type 1 byte
	r = append(r, []byte{byte(this.PowType)}...)

	n1 := make([]byte, 4)
	binary.LittleEndian.PutUint32(n1, this.ExtraNonce1)
	r = append(r, n1...)
	// write nonce 4 bytes
	n := make([]byte, 4)
	binary.LittleEndian.PutUint32(n, this.Nonce)
	r = append(r, n...)

	//write ProofData 169 bytes
	r = append(r, this.ProofData[:]...)
	return PowBytes(r)
}

// pow proof data
func (this *Scrypt) BlockData() PowBytes {
	l := len(this.Bytes())
	//PROOFDATA_LENGTH
	return PowBytes(this.Bytes()[:l-PROOFDATA_LENGTH])
}

// not support
func (this *Scrypt) FindSolver(headerData []byte, blockHash ethcommon.Hash, targetDiffBits uint32) bool {
	if err := this.Verify(headerData, blockHash, targetDiffBits); err == nil {
		return true
	}
	return false
}
