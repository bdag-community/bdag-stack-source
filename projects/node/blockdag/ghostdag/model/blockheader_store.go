package model

import (
	"github.com/BlockdagNetworkLabs/bdag/consensus/model"
	"github.com/ethereum/go-ethereum/common"
)

// BlockHeaderStore represents a store of block headers
type BlockHeaderStore interface {
	model.Store
	BlockHeader(dbContext DBReader, stagingArea *model.StagingArea, blockHash *common.Hash) (BlockHeader, error)
	HasBlockHeader(dbContext DBReader, stagingArea *model.StagingArea, blockHash *common.Hash) (bool, error)
	BlockHeaders(dbContext DBReader, stagingArea *model.StagingArea, blockHashes []*common.Hash) ([]BlockHeader, error)
	Delete(stagingArea *model.StagingArea, blockHash *common.Hash)
	Count(stagingArea *model.StagingArea) uint64
}
