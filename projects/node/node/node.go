package node

import (
	"os"
	"path/filepath"
	"sync"

	"github.com/BlockdagNetworkLabs/bdag/common/roughtime"
	"github.com/BlockdagNetworkLabs/bdag/common/system"
	"github.com/BlockdagNetworkLabs/bdag/config"
	"github.com/BlockdagNetworkLabs/bdag/consensus"
	"github.com/BlockdagNetworkLabs/bdag/consensus/model"
	"github.com/BlockdagNetworkLabs/bdag/core/event"
	"github.com/BlockdagNetworkLabs/bdag/database/chaindb"
	"github.com/BlockdagNetworkLabs/bdag/node/service"
	"github.com/BlockdagNetworkLabs/bdag/params"
	"github.com/BlockdagNetworkLabs/bdag/services/health"
	"github.com/gofrs/flock"
)

// Node works as a server container for all service can be registered.
// such as p2p, rpc, ws etc.
type Node struct {
	service.Service
	lock sync.RWMutex

	startupTime int64

	// config
	Config *config.Config
	Params *params.Params

	// database layer
	DB model.DataBase

	dirLock *flock.Flock // prevents concurrent use of instance directory

	interrupt <-chan struct{}

	consensus model.Consensus

	health *health.Server
}

func NewNode(cfg *config.Config, chainParams *params.Params, interrupt <-chan struct{}) (*Node, error) {
	n := Node{
		Config:    cfg,
		Params:    chainParams,
		interrupt: interrupt,
	}
	n.InitServices()

	// Acquire the instance directory lock.
	if err := n.openDataDir(); err != nil {
		return nil, err
	}
	return &n, nil
}

func (n *Node) Stop() error {
	n.lock.Lock()
	defer n.lock.Unlock()

	log.Info("Stopping Server")
	if err := n.Service.Stop(); err != nil {
		return err
	}
	if n.health != nil {
		n.health.SetReady(false)
	}
	n.DB.Close()
	// Release instance directory lock.
	n.closeDataDir()
	return nil
}

func (n *Node) Start() error {
	n.lock.Lock()
	defer n.lock.Unlock()

	log.Info("Starting Node")
	// Already started?
	if err := n.Service.Start(); err != nil {
		return err
	}

	if n.health != nil {
		n.health.SetReady(true)
	}

	// Finished node start
	// Server startup time. Used for the uptime command for uptime calculation.
	n.startupTime = roughtime.Now().Unix()
	n.consensus.Events().Send(event.New(event.Initialized))
	return nil
}

func (n *Node) RegisterService() error {
	chainDB, err := chaindb.Open(n.Config, n.interrupt)
	if err != nil {
		return err
	}
	n.DB = chainDB
	if chainDB == nil {
		return ErrNodeNoDB
	}
	n.consensus = consensus.New(n.Config, n.DB, n.interrupt, system.ShutdownRequestChannel)

	if n.Config.LightNode {
		return n.registerBlockdagLight()
	}
	return n.registerBlockdagFull()
}

// register services as Blockdag Full node
func (n *Node) registerBlockdagFull() error {
	fullNode, err := newBlockdagFullNode(n)
	if err != nil {
		return err
	}
	n.Services().RegisterService(fullNode)
	return nil
}

// register services as the Blockdag Light node
func (n *Node) registerBlockdagLight() error {
	lightNode, err := newBlockdagLight(n)
	if err != nil {
		return err
	}
	n.Services().RegisterService(lightNode)
	return nil
}

// return Blockdag full
func (n *Node) GetBlockdagFull() *BlockdagFull {
	var qm *BlockdagFull
	if err := n.Services().FetchService(&qm); err != nil {
		log.Error(err.Error())
		return nil
	}
	return qm
}

func (n *Node) openDataDir() error {
	if n.Config.DataDir == "" {
		return nil // ephemeral
	}

	instdir := n.Config.DataDir
	if err := os.MkdirAll(instdir, 0700); err != nil {
		return err
	}
	// Lock the instance directory to prevent concurrent use by another instance as well as
	// accidental use of the instance directory as a database.
	n.dirLock = flock.New(filepath.Join(instdir, "LOCK"))

	if locked, err := n.dirLock.TryLock(); err != nil {
		return err
	} else if !locked {
		return ErrDatadirUsed
	}
	return nil
}

func (n *Node) closeDataDir() {
	// Release instance directory lock.
	if n.dirLock != nil && n.dirLock.Locked() {
		n.dirLock.Unlock()
		n.dirLock = nil
	}
}
