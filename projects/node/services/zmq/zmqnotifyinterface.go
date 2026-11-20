package zmq

import (
	_ "log"

	"github.com/BlockdagNetworkLabs/bdag/config"
	"github.com/BlockdagNetworkLabs/bdag/core/types"
)

// The interface for ZeroMQ notificaion
type IZMQNotification interface {
	// init
	Init(cfg *config.Config)

	// is enable
	IsEnable() bool

	// block accepted
	BlockAccepted(block *types.SerializedBlock)

	// block connected
	BlockConnected(block *types.SerializedBlock)

	// block connected
	BlockDisconnected(block *types.SerializedBlock)

	// Shutdown
	Shutdown()
}

// New ZMQ Notification
func NewZMQNotification(cfg *config.Config) IZMQNotification {
	zmq := &ZMQNotification{}
	zmq.Init(cfg)
	return zmq
}
