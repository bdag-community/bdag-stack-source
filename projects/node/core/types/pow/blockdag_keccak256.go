package pow

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math/big"

	"github.com/BlockdagNetworkLabs/bdag/common/hash"
	"github.com/BlockdagNetworkLabs/bdag/core/json"
	"github.com/ethereum/go-ethereum/common"
	ethcommon "github.com/ethereum/go-ethereum/common"
)

type BlockdagKeccak256 struct {
	Pow
}

func (this *BlockdagKeccak256) GetPowResult() json.PowResult {
	return json.PowResult{
		PowName:   PowMapString[this.GetPowType()].(string),
		PowType:   uint8(this.GetPowType()),
		Nonce:     this.GetNonce(),
		ProofData: nil,
	}
}

func (this *BlockdagKeccak256) Verify(headerData []byte, blockHash ethcommon.Hash, targetDiffBits uint32) error {
	target := CompactToBig(targetDiffBits)
	if target.Sign() <= 0 {
		str := fmt.Sprintf("block target difficulty of %064x is too "+
			"low", target)
		return errors.New(str)
	}
	//The target difficulty must be less than the maximum allowed.
	if target.Cmp(this.params.BlockdagKeccak256PowLimit) > 0 {
		str := fmt.Sprintf("block target difficulty of %064x is "+
			"higher than max of %064x", target, this.params.BlockdagKeccak256PowLimit)
		return errors.New(str)
	}
	h := hash.HashBlockdagKeccak256(headerData)
	hashNum := HashToBig(&h)
	if hashNum.Cmp(target) > 0 {
		str := fmt.Sprintf("block hash of %064x is higher than"+
			" expected max of %064x", hashNum, target)
		return errors.New(str)
	}
	return nil
}

func (this *BlockdagKeccak256) GetNextDiffBig(weightedSumDiv *big.Int, oldDiffBig *big.Int, currentPowPercent *big.Int) *big.Int {
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

func (this *BlockdagKeccak256) GetSafeDiff(cur_reduce_diff uint64) *big.Int {
	limitBits := this.params.BlockdagKeccak256PowLimitBits
	limitBitsBig := CompactToBig(limitBits)
	if cur_reduce_diff <= 0 {
		return limitBitsBig
	}
	newTarget := &big.Int{}
	newTarget = newTarget.SetUint64(cur_reduce_diff)
	// Limit new value to the proof of work limit.
	if newTarget.Cmp(this.params.BlockdagKeccak256PowLimit) > 0 {
		newTarget.Set(this.params.BlockdagKeccak256PowLimit)
	}
	return newTarget
}

// compare the target
// wether target match the target diff
func (this *BlockdagKeccak256) CompareDiff(newTarget *big.Int, target *big.Int) bool {
	return newTarget.Cmp(target) <= 0
}

// pow proof data
func (this *BlockdagKeccak256) Bytes() PowBytes {
	r := make(PowBytes, 0)
	// write pow type 1 byte
	r = append(r, []byte{byte(this.PowType)}...)
	// write nonce 8 bytes
	n := make([]byte, 8)
	binary.LittleEndian.PutUint32(n, this.Nonce)
	r = append(r, n...)
	//write ProofData 169 bytes
	r = append(r, this.ProofData[:]...)
	return PowBytes(r)
}

// pow proof data
func (this *BlockdagKeccak256) BlockData() PowBytes {
	l := len(this.Bytes())
	return PowBytes(this.Bytes()[:l-PROOFDATA_LENGTH])
}

// not support
func (this *BlockdagKeccak256) FindSolver(headerData []byte, blockHash common.Hash, targetDiffBits uint32) bool {
	if err := this.Verify(headerData, blockHash, targetDiffBits); err == nil {
		return true
	}
	return false
}
