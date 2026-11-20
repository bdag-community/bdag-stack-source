package model

import (
	"github.com/BlockdagNetworkLabs/bdag/core/types"
	"github.com/BlockdagNetworkLabs/bdag/database/common"
	ethcommon "github.com/ethereum/go-ethereum/common"
	ethtypes "github.com/ethereum/go-ethereum/core/types"
)

type DataBase interface {
	Name() string
	Init() error
	Close()
	Rebuild(mgr IndexManager) error
	GetInfo() (*common.DatabaseInfo, error)
	PutInfo(di *common.DatabaseInfo) error
	GetBestChainState() ([]byte, error)
	PutBestChainState(data []byte) error
	GetBlock(hash *ethcommon.Hash) (*types.SerializedBlock, error)
	GetBlockBytes(hash *ethcommon.Hash) ([]byte, error)
	GetHeader(hash *ethcommon.Hash) (*types.BlockHeader, error)
	PutBlock(block *types.SerializedBlock) error
	HasBlock(hash *ethcommon.Hash) bool
	GetDagInfo() ([]byte, error)
	PutDagInfo(data []byte) error
	GetDAGBlock(blockID uint) ([]byte, error)
	PutDAGBlock(blockID uint, data []byte) error
	DeleteDAGBlock(blockID uint) error
	GetDAGBlockIdByHash(bh *ethcommon.Hash) (uint, error)
	PutDAGBlockIdByHash(bh *ethcommon.Hash, id uint) error
	DeleteDAGBlockIdByHash(bh *ethcommon.Hash) error
	PutMainChainBlock(blockID uint) error
	HasMainChainBlock(blockID uint) bool
	DeleteMainChainBlock(blockID uint) error
	PutBlockIdByOrder(order uint, id uint) error
	GetBlockIdByOrder(order uint) (uint, error)
	PutDAGTip(id uint, isMain bool) error
	GetDAGTips() ([]uint, error)
	DeleteDAGTip(id uint) error
	PutDiffAnticone(id uint) error
	GetDiffAnticones() ([]uint, error)
	DeleteDiffAnticone(id uint) error
	Get(key []byte) ([]byte, error)
	Put(key []byte, value []byte) error
	PutTxIdxEntrys(sblock *types.SerializedBlock, block Block) error
	GetTxIdxEntry(id *ethcommon.Hash, verbose bool) (*ethtypes.Transaction, *ethcommon.Hash, error)
	DeleteTxIdxEntrys(block *types.SerializedBlock) error
	IsInvalidTxIdxEmpty() bool
	GetInvalidTxIdxTip() (uint64, *ethcommon.Hash, error)
	PutInvalidTxIdxTip(order uint64, bh *ethcommon.Hash) error
	PutInvalidTxs(sblock *types.SerializedBlock, block Block) error
	DeleteInvalidTxs(sblock *types.SerializedBlock, block Block) error
	CleanInvalidTxIdx() error
	GetAddrIdxTip() (*ethcommon.Hash, uint, error)
	PutAddrIdxTip(bh *ethcommon.Hash, order uint) error
	PutAddrIdx(sblock *types.SerializedBlock, block Block, stxos [][]byte) error
	GetTxForAddress(addr ethcommon.Address, numToSkip, numRequested uint32, reverse bool) ([]*common.RetrievedTx, uint32, error)
	DeleteAddrIdx(sblock *types.SerializedBlock, stxos [][]byte) error
	CleanAddrIdx(finish bool) error
	IsLegacy() bool
	TryUpgrade(di *common.DatabaseInfo, interrupt <-chan struct{}) error
	GetEstimateFee() ([]byte, error)
	PutEstimateFee(data []byte) error
	DeleteEstimateFee() error
	Snapshot() error
	SnapshotInfo() string
	DBEngine() string
	StartTrack(info string) error
	StopTrack() error
}
