package model

import (
	"github.com/BlockdagNetworkLabs/bdag/consensus/model"
	"github.com/ethereum/go-ethereum/common"
)

// DAGTopologyManager exposes methods for querying relationships
// between blocks in the DAG
type DAGTopologyManager interface {
	Parents(stagingArea *model.StagingArea, blockHash *common.Hash) ([]*common.Hash, error)
	Children(stagingArea *model.StagingArea, blockHash *common.Hash) ([]*common.Hash, error)
	IsParentOf(stagingArea *model.StagingArea, blockHashA *common.Hash, blockHashB *common.Hash) (bool, error)
	IsChildOf(stagingArea *model.StagingArea, blockHashA *common.Hash, blockHashB *common.Hash) (bool, error)
	IsAncestorOf(stagingArea *model.StagingArea, blockHashA *common.Hash, blockHashB *common.Hash) (bool, error)
	IsAncestorOfAny(stagingArea *model.StagingArea, blockHash *common.Hash, potentialDescendants []*common.Hash) (bool, error)
	IsAnyAncestorOf(stagingArea *model.StagingArea, potentialAncestors []*common.Hash, blockHash *common.Hash) (bool, error)
	IsInSelectedParentChainOf(stagingArea *model.StagingArea, blockHashA *common.Hash, blockHashB *common.Hash) (bool, error)
	ChildInSelectedParentChainOf(stagingArea *model.StagingArea, lowHash, highHash *common.Hash) (*common.Hash, error)

	SetParents(stagingArea *model.StagingArea, blockHash *common.Hash, parentHashes []*common.Hash) error
}
