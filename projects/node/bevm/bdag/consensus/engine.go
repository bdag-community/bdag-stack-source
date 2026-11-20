package consensus

import (
	"sync"

	"github.com/BlockdagNetworkLabs/bdag/log"
	"github.com/ethereum/go-ethereum/consensus"
	"github.com/ethereum/go-ethereum/ethdb"
	"github.com/ethereum/go-ethereum/rpc"
)

type bdagEngine struct {
	log      log.Logger
	database ethdb.Database

	threads int

	lock      sync.Mutex
	closeOnce sync.Once
}

func New(db ethdb.Database) *bdagEngine {

	return &bdagEngine{
		log:      log.New("module", "bdag/consensus"),
		database: db,
		threads:  1,
	}
}

func (me *bdagEngine) Close() error {
	return nil
}

func (me *bdagEngine) Threads() int {
	me.lock.Lock()
	defer me.lock.Unlock()

	return me.threads
}

func (me *bdagEngine) SetThreads(threads int) {
	me.lock.Lock()
	defer me.lock.Unlock()

	// Update the threads and ping any running seal to pull in any changes
	me.threads = threads
}

func (me *bdagEngine) APIs(chain consensus.ChainHeaderReader) []rpc.API {
	return []rpc.API{}
}
