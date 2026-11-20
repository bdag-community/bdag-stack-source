package node

import (
	"github.com/BlockdagNetworkLabs/bdag/config"
	"github.com/BlockdagNetworkLabs/bdag/consensus/model"
	"github.com/BlockdagNetworkLabs/bdag/node/service"
)

// BlockdagLight implements the Blockdag light node service.
type BlockdagLight struct {
	service.Service
	// database
	db     model.DataBase
	config *config.Config
}

func newBlockdagLight(n *Node) (*BlockdagLight, error) {
	light := BlockdagLight{
		config: n.Config,
		db:     n.DB,
	}
	return &light, nil
}
