package consensus

import (
	"sync"

	"github.com/BlockdagNetworkLabs/bdag/config"
	"github.com/BlockdagNetworkLabs/bdag/consensus/model"
	"github.com/BlockdagNetworkLabs/bdag/core/blockchain"
	"github.com/BlockdagNetworkLabs/bdag/core/event"
	"github.com/BlockdagNetworkLabs/bdag/engine/txscript"
	"github.com/BlockdagNetworkLabs/bdag/log"
	"github.com/BlockdagNetworkLabs/bdag/params"
	"github.com/BlockdagNetworkLabs/bdag/services/index"
	"github.com/ethereum/go-ethereum/common"
)

type consensus struct {
	lock                   sync.Mutex
	databaseContext        model.DataBase
	cfg                    *config.Config
	interrupt              <-chan struct{}
	shutdownRequestChannel chan struct{}
	// signature cache
	sigCache *txscript.SigCache
	// event system
	events event.Feed
	// clock time service
	mediantimeSource model.MedianTimeSource

	blockchain   model.BlockChain
	indexManager model.IndexManager
}

// Init initializes consensus
func (s *consensus) Init() error {
	s.lock.Lock()
	defer s.lock.Unlock()

	if onEnd := log.LogAndMeasureExecutionTime(log.Root(), "consensus.Init"); onEnd != nil {
		defer onEnd()
	}

	s.indexManager = index.NewManager(index.ToConfig(s.cfg), s)

	// Create a new block chain instance with the appropriate configuration.
	blockchain, err := blockchain.New(s)
	if err != nil {
		return err
	}
	s.blockchain = blockchain
	//
	s.subscribe()
	return blockchain.Init()
}

func (s *consensus) DatabaseContext() model.DataBase {
	return s.databaseContext
}

func (s *consensus) Config() *config.Config {
	return s.cfg
}

func (s *consensus) BlockChain() model.BlockChain {
	return s.blockchain
}

func (s *consensus) IndexManager() model.IndexManager {
	return s.indexManager
}

func (s *consensus) Events() *event.Feed {
	return &s.events
}

func (s *consensus) MedianTimeSource() model.MedianTimeSource {
	return s.mediantimeSource
}

func (s *consensus) SigCache() *txscript.SigCache {
	return s.sigCache
}

func (s *consensus) Interrupt() <-chan struct{} {
	return s.interrupt
}

func (s *consensus) Shutdown() {
	s.shutdownRequestChannel <- struct{}{}
}

func (s *consensus) GenesisHash() *common.Hash {
	return params.ActiveNetParams.Params.GenesisHash
}

func (s *consensus) Params() *params.Params {
	return params.ActiveNetParams.Params
}

func (s *consensus) subscribe() {
	ch := make(chan *event.Event)
	sub := s.events.Subscribe(ch)
	go func() {
		defer sub.Unsubscribe()
		for {
			select {
			case ev := <-ch:
				if ev.Data != nil {
				}
				if ev.Ack != nil {
					ev.Ack <- struct{}{}
				}
			case <-s.interrupt:
				log.Info("Close consensus Event Subscribe")
				return
			}
		}
	}()
}

func (s *consensus) Rebuild() error {
	err := s.databaseContext.Rebuild(s.indexManager)
	if err != nil {
		return err
	}
	return s.blockchain.Rebuild()
}

func New(cfg *config.Config, databaseContext model.DataBase, interrupt <-chan struct{}, shutdownRequestChannel chan struct{}) *consensus {
	return &consensus{
		cfg:                    cfg,
		databaseContext:        databaseContext,
		mediantimeSource:       blockchain.NewMedianTime(),
		interrupt:              interrupt,
		sigCache:               txscript.NewSigCache(cfg.SigCacheMaxSize),
		shutdownRequestChannel: shutdownRequestChannel,
	}
}

func NewPure(cfg *config.Config, databaseContext model.DataBase) *consensus {
	return New(cfg, databaseContext, make(chan struct{}), make(chan struct{}))
}
