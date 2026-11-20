package model

import (
	"github.com/BlockdagNetworkLabs/bdag/rpc/api"
	"github.com/ethereum/go-ethereum/common"
)

type BdagChain interface {
	RegisterAPIs(apis []api.API)
	GetBlockIDByTxHash(txhash *common.Hash) uint64
}
