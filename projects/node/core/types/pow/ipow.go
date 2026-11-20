package pow

import (
	"encoding/hex"
	"math/big"

	"github.com/BlockdagNetworkLabs/bdag/core/json"
	"github.com/ethereum/go-ethereum/common"
)

// the pow length is 178
const POW_LENGTH = 178

// proof data length is 169
const PROOFDATA_LENGTH = 169

type PowType byte
type PowBytes []byte

const (
	//pow type enum
	BLAKE2BD          PowType = 0
	CUCKAROO          PowType = 1
	CUCKATOO          PowType = 2
	CUCKAROOM         PowType = 3
	X16RV3            PowType = 4
	X8R16             PowType = 5
	BlockdagKECCAK256 PowType = 6
	CRYPTONIGHT       PowType = 7
	BDAGXKECCAKV1     PowType = 8
	BLAKE2S           PowType = 9
	SCRYPT            PowType = 10
)

var PowMapString = map[PowType]any{
	BLAKE2BD:          "blake2bd",
	CUCKAROO:          "cuckaroo",
	CUCKATOO:          "cuckatoo",
	CUCKAROOM:         "cuckaroom",
	X16RV3:            "x16rv3",
	X8R16:             "x8r16",
	BlockdagKECCAK256: "Blockdag_keccak256",
	CRYPTONIGHT:       "cryptonight",
	BDAGXKECCAKV1:     "bdag_xkeccak_v1",
	BLAKE2S:           "blake2s",
	SCRYPT:            "scrypt",
}

func GetPowName(powType PowType) string {
	val, ok := PowMapString[powType]
	if !ok {
		return ""
	}
	return val.(string)
}

type ProofDataType [PROOFDATA_LENGTH]byte

func (this *ProofDataType) String() string {
	return hex.EncodeToString(this[:])
}

func (this *ProofDataType) Bytes() []byte {
	return this[:]
}

type IPow interface {
	// verify result difficulty
	Verify(headerData []byte, blockHash common.Hash, targetDiff uint32) error
	//set header nonce
	SetNonce(nonce uint32)
	//set extranonce1
	SetExtraNonce1(extranonce1 uint32)
	//calc next diff
	GetNextDiffBig(weightedSumDiv *big.Int, oldDiffBig *big.Int, currentPowPercent *big.Int) *big.Int
	GetExtraNonce1() uint32
	GetNonce() uint32
	GetPowType() PowType
	//set pow type
	SetPowType(powType PowType)
	GetProofData() string
	//set proof data
	SetProofData([]byte)
	Bytes() PowBytes
	BlockData() PowBytes
	//if cur_reduce_diff > 0 compare cur_reduce_diff with powLimitBits or minDiff ，the cur_reduce_diff should less than powLimitBits , and should more than min diff
	//if cur_reduce_diff <=0 return powLimit or min diff
	GetSafeDiff(cur_reduce_diff uint64) *big.Int
	// pow percent
	PowPercent() *big.Int
	//pow result
	GetPowResult() json.PowResult
	//SetParams
	SetParams(params *PowConfig)
	//SetHeight
	SetMainHeight(height MainHeight)
	CheckAvailable() bool
	CompareDiff(newtarget *big.Int, target *big.Int) bool
	FindSolver(headerData []byte, blockHash common.Hash, targetDiffBits uint32) bool
}

type Pow struct {
	PowType    PowType       //header pow type 1 bytes
	ExtraNonce1 uint32        //Miner extranonce1 4 bytes
	Nonce      uint32        //header nonce 4 bytes
	ProofData  ProofDataType // 1 edge_bits  168  bytes circle length total 169 bytes
	params     *PowConfig
	mainHeight MainHeight
}

// get pow instance
func GetInstance(powType PowType,extraNonce1 uint32, nonce uint32, proofData []byte) IPow {
	var instance IPow
	switch powType {
	case BLAKE2BD:
		instance = &Blake2bd{}
	case BLAKE2S:
		instance = &Blake2s{}
	case SCRYPT:
		instance = &Scrypt{}
	case BlockdagKECCAK256:
		instance = &BlockdagKeccak256{}
	case CRYPTONIGHT:
		instance = &CryptoNight{}
	case BDAGXKECCAKV1:
		instance = &bdagXKeccakV1{}
	default:
		instance = &Blake2bd{}
	}
	instance.SetPowType(powType)
	instance.SetExtraNonce1(extraNonce1)
	instance.SetNonce(nonce)
	instance.SetProofData(proofData)
	return instance
}

func (this *Pow) SetPowType(powType PowType) {
	this.PowType = powType
}

func (this *Pow) SetParams(params *PowConfig) {
	this.params = GetPowConfig().Set(params)
}

func (this *Pow) SetMainHeight(mainHeight MainHeight) {
	this.mainHeight = mainHeight
}

func (this *Pow) GetPowType() PowType {
	return this.PowType
}

func (this *Pow) GetNonce() uint32 {
	return this.Nonce
}

func (this *Pow) SetNonce(nonce uint32) {
	this.Nonce = nonce
}

func (this *Pow) GetExtraNonce1() uint32 {
	return this.ExtraNonce1
}

func (this *Pow) SetExtraNonce1(extranonce1 uint32) {
	this.ExtraNonce1 = extranonce1
}

func (this *Pow) GetProofData() string {
	return this.ProofData.String()
}

// set proof data except pow type
func (this *Pow) SetProofData(data []byte) {
	l := len(data)
	copy(this.ProofData[0:l], data[:])
}

func (this *Pow) PowPercent() *big.Int {
	targetPercent := big.NewInt(int64(this.params.GetPercentByHeightAndType(this.mainHeight, this.GetPowType())))
	targetPercent.Lsh(targetPercent, 32)
	return targetPercent
}

// check pow is available
func (this *Pow) CheckAvailable() bool {
	return this.params.GetPercentByHeightAndType(this.mainHeight, this.PowType) > 0
}
