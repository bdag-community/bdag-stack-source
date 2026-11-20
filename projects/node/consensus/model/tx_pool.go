package model

import (
	"github.com/BlockdagNetworkLabs/bdag/core/types"
	"github.com/ethereum/go-ethereum/common"
)

type TxPool interface {
	RemoveTransaction(tx *types.Tx, removeRedeemers bool)

	RemoveDoubleSpends(tx *types.Tx)

	RemoveOrphan(tx *types.Tx)

	ProcessOrphans(tx *types.Tx) []*types.TxDesc

	MaybeAcceptTransaction(tx *types.Tx, isNew, rateLimit bool) ([]*common.Hash, error)

	HaveTransaction(hash *common.Hash) bool

	PruneExpiredTx()

	ProcessTransaction(tx *types.Tx, allowOrphan, rateLimit, allowHighFees bool) ([]*types.TxDesc, error)

	GetMainHeight() int64

	AddTransaction(tx *types.Tx, height uint64, fee int64)

	// IsSupportVMTx() bool
}

type FeeEstimator interface {
	RegisterBlock(block *types.SerializedBlock, mainheight uint) error
	Rollback(hash *common.Hash) error
}
