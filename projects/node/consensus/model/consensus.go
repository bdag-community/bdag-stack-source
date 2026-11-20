package model

import (
	"github.com/BlockdagNetworkLabs/bdag/config"
	"github.com/BlockdagNetworkLabs/bdag/core/event"
	"github.com/BlockdagNetworkLabs/bdag/engine/txscript"
	"github.com/BlockdagNetworkLabs/bdag/params"
	"github.com/ethereum/go-ethereum/common"
)

// Consensus maintains the current core state of the node
type Consensus interface {
	Init() error
	GenesisHash() *common.Hash
	Config() *config.Config
	DatabaseContext() DataBase
	BlockChain() BlockChain
	IndexManager() IndexManager
	Events() *event.Feed
	MedianTimeSource() MedianTimeSource
	SigCache() *txscript.SigCache
	Interrupt() <-chan struct{}
	Params() *params.Params
	Rebuild() error
	Shutdown()
}
