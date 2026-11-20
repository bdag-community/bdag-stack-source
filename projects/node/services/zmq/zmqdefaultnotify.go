//go:build !zmq
// +build !zmq

package zmq

import (
	"github.com/BlockdagNetworkLabs/bdag/config"
	"github.com/BlockdagNetworkLabs/bdag/core/types"
)

// This ZeroMQ notification is default for Blockdag
// If you want to enable ZMQ for Blockdag, you must use 'zmq' tags when go building
type ZMQNotification struct {
}

// Initialization notification
func (zn *ZMQNotification) Init(cfg *config.Config) {
	log.Info("ZMQ:Not Supported")
}

func (zn *ZMQNotification) IsEnable() bool {
	return false
}

// block accepted
func (zn *ZMQNotification) BlockAccepted(block *types.SerializedBlock) {

}

// block connected
func (zn *ZMQNotification) BlockConnected(block *types.SerializedBlock) {

}

// block connected
func (zn *ZMQNotification) BlockDisconnected(block *types.SerializedBlock) {

}

// Shutdown
func (zn *ZMQNotification) Shutdown() {

}
