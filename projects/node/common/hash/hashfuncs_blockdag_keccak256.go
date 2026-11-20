package hash

import (
	BlockdagSha3 "github.com/BlockdagNetworkLabs/crypto/sha3"
	"github.com/ethereum/go-ethereum/common"
)

// Blockdag Keccak256 calculates hash(b) and returns the resulting bytes as a Hash.
func HashBlockdagKeccak256(b []byte) common.Hash {
	h := BlockdagSha3.NewBlockdagKeccak256()
	h.Write(b)
	r := h.Sum(nil)
	hashR := [32]byte{}
	copy(hashR[:32], r[:32])
	return common.Hash(hashR)
}
