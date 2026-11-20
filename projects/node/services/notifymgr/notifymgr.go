package notifymgr

import (
	"fmt"
	"sync"
	"time"

	"github.com/BlockdagNetworkLabs/bdag/consensus/model"
	"github.com/BlockdagNetworkLabs/bdag/core/blockchain"
	"github.com/BlockdagNetworkLabs/bdag/core/types"
	"github.com/BlockdagNetworkLabs/bdag/log"
	"github.com/BlockdagNetworkLabs/bdag/node/service"
	"github.com/BlockdagNetworkLabs/bdag/p2p"
	"github.com/BlockdagNetworkLabs/bdag/rpc"
	"github.com/BlockdagNetworkLabs/bdag/services/notifymgr/notify"
	"github.com/BlockdagNetworkLabs/bdag/services/zmq"
	"github.com/ethereum/go-ethereum/common"
	ethtypes "github.com/ethereum/go-ethereum/core/types"
	"github.com/libp2p/go-libp2p/core/peer"
)

const NotifyTickerDur = time.Second

const MaxNotifyProcessTimeout = time.Second * 30

const notifyBufferSize = 1024

// NotifyMgr manage message announce & relay & notification between mempool, websocket, gbt long pull
// and rpc server.
type NotifyMgr struct {
	service.Service

	Server    *p2p.Service
	RpcServer *rpc.RpcServer

	ndCh   chan *notify.NotifyData
	nds    []*notify.NotifyData
	wg     sync.WaitGroup
	quit   chan struct{}
	ticker *time.Ticker

	sync.Mutex

	lastProTime time.Time

	// zmq notification
	zmqNotify zmq.IZMQNotification
}

// AnnounceNewTransactions generates and relays inventory vectors and notifies
// both websocket and getblocktemplate long poll clients of the passed
// transactions.  This function should be called whenever new transactions
// are added to the mempool.
func (ntmgr *NotifyMgr) AnnounceNewTransactions(newTxs []*types.TxDesc, filters []peer.ID) {
	if ntmgr.IsShutdown() {
		return
	}
	if len(newTxs) <= 0 {
		return
	}
	for _, tx := range newTxs {
		nd := &notify.NotifyData{Data: tx, Filters: filters}
		select {
		case ntmgr.ndCh <- nd:
		default:
			log.Warn(fmt.Sprintf("notify queue full, dropping tx %s", tx.Tx.Hash().String()))
		}
	}

	ntmgr.Lock()
	ntmgr.Reset()
	ntmgr.Unlock()
}

// RelayInventory relays the passed inventory vector to all connected peers
// that are not already known to have it.
func (ntmgr *NotifyMgr) RelayInventory(block *types.SerializedBlock, flags uint32, source *peer.ID) {
	ntmgr.relayAcceptedBlock(block, flags, source, time.Time{})
}

func (ntmgr *NotifyMgr) relayAcceptedBlock(block *types.SerializedBlock, flags uint32, source *peer.ID, acceptedAt time.Time) {
	if ntmgr.IsShutdown() {
		return
	}
	fs := blockchain.BehaviorFlags(flags)
	if fs.Has(blockchain.BFRPCAdd) || fs.Has(blockchain.BFBroadcast) {
		err := ntmgr.Server.BroadcastBlock(block, source, acceptedAt)
		if err != nil {
			log.Error(err.Error())
		}
	}
	ntmgr.Server.PeerSync().RelayGraphState()
}

func (ntmgr *NotifyMgr) BroadcastMessage(data any) {
	ntmgr.Server.BroadcastMessage(data)
}

func (ntmgr *NotifyMgr) AddRebroadcastInventory(newTxs []*types.TxDesc) {
	if ntmgr.IsShutdown() {
		return
	}
	for _, tx := range newTxs {
		txhash := tx.Tx.Hash()
		ntmgr.Server.Rebroadcast().AddInventory(&txhash, tx)
	}
}

func (ntmgr *NotifyMgr) RelayTransaction(tx *ethtypes.Transaction) error {
	if ntmgr.Server == nil {
		return fmt.Errorf("no p2p service")
	}
	_, err := ntmgr.Server.RelayTransaction(tx)
	return err
}

// Transaction has one confirmation on the main chain. Now we can mark it as no
// longer needing rebroadcasting.
func (ntmgr *NotifyMgr) TransactionConfirmed(tx *ethtypes.Transaction) {
	ntmgr.Server.Rebroadcast().RemoveInventory([]common.Hash{tx.Hash()})
}
func (ntmgr *NotifyMgr) TransactionsConfirmed(txs []*ethtypes.Transaction) {
	hs := []common.Hash{}
	for _, tx := range txs {
		hs = append(hs, tx.Hash())
	}
	go ntmgr.Server.Rebroadcast().RemoveInventory(hs)
}

func (ntmgr *NotifyMgr) Start() error {
	if err := ntmgr.Service.Start(); err != nil {
		return err
	}
	//
	log.Info("Start NotifyMgr...")

	ntmgr.wg.Add(1)
	go ntmgr.handler()

	return nil
}

func (ntmgr *NotifyMgr) Stop() error {
	log.Info("try stop NotifyMgr")
	if err := ntmgr.Service.Stop(); err != nil {
		return err
	}
	log.Info("Stop NotifyMgr...")

	close(ntmgr.quit)
	ntmgr.wg.Wait()

	if ntmgr.ticker != nil {
		ntmgr.ticker.Stop()
		ntmgr.ticker = nil
	}

	ntmgr.zmqNotify.Shutdown()
	return nil
}

func (ntmgr *NotifyMgr) handler() {
out:
	for {
		select {
		case <-ntmgr.ticker.C:
			ntmgr.handleStallSample()

		case <-ntmgr.quit:
			break out
		}
	}

	ntmgr.wg.Done()
	log.Trace("NotifyMgr handler done")
}

func (ntmgr *NotifyMgr) handleStallSample() {
	if ntmgr.IsShutdown() {
		return
	}

	nds := make([]*notify.NotifyData, 0, len(ntmgr.ndCh))
	for {
		select {
		case nd := <-ntmgr.ndCh:
			nds = append(nds, nd)
		default:
			goto process
		}
	}

process:
	if len(nds) == 0 {
		return
	}

	txds := []*types.TxDesc{}
	for _, nd := range nds {
		txd, ok := nd.Data.(*types.TxDesc)
		if ok {
			log.Trace(fmt.Sprintf("Announce new transaction :hash=%s height=%d add=%s", txd.Tx.Hash().String(), txd.Height, txd.Added.String()))

			txds = append(txds, txd)
		}
	}
	ntmgr.Server.RelayInventory(nds)

	if len(txds) > 0 {
		if ntmgr.RpcServer != nil && ntmgr.RpcServer.IsStarted() {
			ntmgr.RpcServer.NotifyNewTransactions(txds)
		}
	}

	ntmgr.Lock()
	ntmgr.lastProTime = time.Now()
	ntmgr.Unlock()
}

func (ntmgr *NotifyMgr) IsTimeout() bool {
	return time.Since(ntmgr.lastProTime) >= MaxNotifyProcessTimeout
}

func (ntmgr *NotifyMgr) Reset() {
	if !ntmgr.IsTimeout() {
		if ntmgr.ticker == nil {
			return
		}
		ntmgr.ticker.Reset(NotifyTickerDur)
	}
}

func (ntmgr *NotifyMgr) handleNotifyMsg(notification *blockchain.Notification) {
	//this handler is getting called from the maybeAcceptBlock (sendNotification)
	if ntmgr.IsShutdown() {
		return
	}
	switch notification.Type {
	case blockchain.BlockAccepted:
		band, ok := notification.Data.(*blockchain.BlockAcceptedNotifyData)
		if !ok {
			log.Warn("Chain accepted notification is not BlockAcceptedNotifyData.")
			break
		}
		block := band.Block
		ntmgr.zmqNotify.BlockAccepted(block)
		// Don't relay if we are not current. Other peers that are current
		// should already know about it
		if !ntmgr.Server.IsCurrent() {
			log.Trace("we are not current")
			return
		}
		log.Trace("we are current, can do relay")
		ntmgr.relayAcceptedBlock(block, uint32(band.Flags), band.Source, band.AcceptedAt)

	// A block has been connected to the main block chain.
	case blockchain.BlockConnected:
		blockSlice, ok := notification.Data.([]any)
		if !ok {
			log.Warn("Chain connected notification is not a block slice.")
			break
		}
		if len(blockSlice) != 3 {
			log.Warn("Chain connected notification is wrong size slice.")
			break
		}
		block := blockSlice[0].(*types.SerializedBlock)
		ntmgr.zmqNotify.BlockConnected(block)

	// A block has been disconnected from the main block chain.
	case blockchain.BlockDisconnected:
		log.Trace("Chain disconnected notification.")
		blockSlice, ok := notification.Data.([]any)
		if !ok {
			log.Warn("Chain disconnected notification is not a block slice.")
			break
		}
		ntmgr.zmqNotify.BlockDisconnected(blockSlice[0].(*types.SerializedBlock))
	}
}

func New(p2pServer *p2p.Service, consensus model.Consensus) *NotifyMgr {
	ntmgr := &NotifyMgr{
		quit:        make(chan struct{}),
		ticker:      time.NewTicker(time.Second),
		ndCh:        make(chan *notify.NotifyData, notifyBufferSize),
		Server:      p2pServer,
		lastProTime: time.Now(),
		zmqNotify:   zmq.NewZMQNotification(consensus.Config()),
	}
	consensus.BlockChain().(*blockchain.BlockChain).Subscribe(ntmgr.handleNotifyMsg)
	return ntmgr
}
