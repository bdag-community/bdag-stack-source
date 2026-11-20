package model

import (
	"github.com/BlockdagNetworkLabs/bdag/consensus/model"
	"github.com/ethereum/go-ethereum/common"
)

// GHOSTDAGDataStore represents a store of BlockGHOSTDAGData
type GHOSTDAGDataStore interface {
	model.Store
	Stage(stagingArea *model.StagingArea, blockHash *common.Hash, blockGHOSTDAGData *BlockGHOSTDAGData, isTrustedData bool)
	IsStaged(stagingArea *model.StagingArea) bool
	Get(dbContext DBReader, stagingArea *model.StagingArea, blockHash *common.Hash, isTrustedData bool) (*BlockGHOSTDAGData, error)
	UnstageAll(stagingArea *model.StagingArea)
}
