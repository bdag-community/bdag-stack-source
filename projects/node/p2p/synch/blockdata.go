package synch

import (
	"github.com/BlockdagNetworkLabs/bdag/core/types"
	"github.com/ethereum/go-ethereum/common"
)

type BlockData struct {
	Hash  *common.Hash
	Block *types.SerializedBlock
}
