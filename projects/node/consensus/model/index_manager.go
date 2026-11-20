package model

import (
	"github.com/BlockdagNetworkLabs/bdag/core/types"
	"github.com/ethereum/go-ethereum/common"
)

// IndexManager provides a generic interface that the is called when blocks are
// connected and disconnected to and from the tip of the main chain for the
// purpose of supporting optional indexes.
type IndexManager interface {
	// Init is invoked during chain initialize in order to allow the index
	// manager to initialize itself and any indexes it is managing.  The
	// channel parameter specifies a channel the caller can close to signal
	// that the process should be interrupted.  It can be nil if that
	// behavior is not desired.
	Init() error

	// ConnectBlock is invoked when a new block has been connected to the
	// main chain.
	ConnectBlock(sblock *types.SerializedBlock, block Block) error

	// DisconnectBlock is invoked when a block has been disconnected from
	// the main chain.
	DisconnectBlock(sblock *types.SerializedBlock, block Block) error

	UpdateMainTip(bh *common.Hash, order uint64) error

	HasTx(txid *common.Hash) bool
}
