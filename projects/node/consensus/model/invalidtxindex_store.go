package model

import (
	"github.com/BlockdagNetworkLabs/bdag/core/types"
	"github.com/ethereum/go-ethereum/common"
	ethtypes "github.com/ethereum/go-ethereum/core/types"
)

type InvalidTxIndexStore interface {
	Store
	Stage(stagingArea *StagingArea, bid uint64, block *types.SerializedBlock)
	StageTip(stagingArea *StagingArea, bhash *common.Hash, order uint64)
	IsStaged(stagingArea *StagingArea) bool
	Get(stagingArea *StagingArea, txid *common.Hash) (*ethtypes.Transaction, error)
	GetIdByHash(stagingArea *StagingArea, h *common.Hash) (*common.Hash, error)
	Delete(stagingArea *StagingArea, bid uint64, block *types.SerializedBlock)
	Tip(stagingArea *StagingArea) (uint64, *common.Hash, error)
	IsEmpty() bool
	Clean() error
}
