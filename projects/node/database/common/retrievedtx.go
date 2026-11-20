package common

import (
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/common"
)

type RetrievedTx struct {
	Bytes   []byte
	BlkHash *common.Hash // Only set when transaction is in a block.
	Tx      *types.Transaction
}
