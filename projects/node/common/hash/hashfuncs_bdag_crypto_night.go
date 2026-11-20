package hash

import (
	"github.com/BlockdagNetworkLabs/crypto/cryptonight"
	"github.com/ethereum/go-ethereum/common"
)

// CryptoNight calculates hash(b) and returns the resulting bytes as a Hash.
func HashCryptoNight(b []byte) common.Hash {
	h := cryptonight.Sum(b, 2)
	hashR := [32]byte{}
	copy(hashR[:32], h[:32])
	return common.Hash(hashR)
}
