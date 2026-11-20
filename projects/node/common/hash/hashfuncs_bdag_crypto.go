package hash

import (
	BlockdagSha3 "github.com/BlockdagNetworkLabs/crypto/sha3"
	"github.com/ethereum/go-ethereum/common"
)

// bdag X Keccak V1 calculates hash(b) and returns the resulting bytes as a Hash.
// 2 round of NewLegacyKeccak512
// 1 round of NewBlockdagKeccak256
func HashbdagXKeccakV1(input []byte) common.Hash {
	// input length 117 bytes
	h1 := BlockdagSha3.NewLegacyKeccak512()
	// first round
	h1.Write(input)
	// result length 64 bytes
	var round1 [64]byte
	input = h1.Sum(round1[:0])
	// second round
	h2 := BlockdagSha3.NewLegacyKeccak512()
	h2.Write(input)
	// result length 64 bytes
	var round2 [64]byte
	input = h2.Sum(round2[:0])
	// Attention
	// hash result first byte  ^1
	// specialized processing
	// Prevent compatibility of existing ASIC
	input[0] ^= 1
	// third round
	h3 := BlockdagSha3.NewBlockdagKeccak256()
	// result length 32 bytes
	h3.Write(input)
	var result common.Hash
	h3.Sum(result[:0])
	return result
}
