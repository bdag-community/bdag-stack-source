package bdag

import (
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
)

type Block struct {
	Id       *common.Hash
	Txs      []*types.Transaction
	Time     time.Time
	EvmBlock *types.Block
}

func (b *Block) ID() *common.Hash {
	return b.Id
}

func (b *Block) Timestamp() time.Time {
	return b.Time
}

func (b *Block) Transactions() []*types.Transaction {
	return b.Txs
}

func (b *Block) Clean() {
	b.Id = nil
	b.Txs = nil
	b.EvmBlock = nil
}
