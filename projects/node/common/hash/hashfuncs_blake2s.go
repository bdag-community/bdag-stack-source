package hash

import (
	blake2s "github.com/BlockdagNetworkLabs/bdag/blake2sext"
	"github.com/ethereum/go-ethereum/common"
)

// bdag blake2s calculates hash(b) and returns the resulting bytes as a Hash.Add commentMore actions
func HashbdagBlake2s(input []byte) common.Hash {

	hash, err := blake2s.New256(nil)
	if err != nil {
		return common.Hash{}
	}

	if _, err := hash.Write(input); err != nil {
		return common.Hash{}
	}

	// Create the output
	var result common.Hash
	hash.Sum(result[:0])
	return result
}
