package p2p

import (
	"math/rand"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/BlockdagNetworkLabs/bdag/core/types"
	"github.com/BlockdagNetworkLabs/bdag/p2p/peers"
	pb "github.com/BlockdagNetworkLabs/bdag/p2p/proto/v1"
	"github.com/BlockdagNetworkLabs/bdag/p2p/synch"
	"github.com/BlockdagNetworkLabs/bdag/params"
	"github.com/BlockdagNetworkLabs/bdag/services/notifymgr/notify"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/txpool"
	"github.com/ethereum/go-ethereum/core/txpool/legacypool"
)

const (
	MaxRegainMempoolSize      = 1000
	defaultPendingInvTTL      = 10 * time.Minute
	defaultMaxPendingInvItems = 10000
)

type broadcastInventoryAdd relayMsg

type broadcastInventoryDel []common.Hash

type relayMsg struct {
	hash *common.Hash
	data any
}

type pendingInv struct {
	data  any
	added time.Time
}

type mempoolRegainSnapshot struct {
	isCurrent bool
	peer      *peers.Peer
	count     int
}

type Rebroadcast struct {
	started  int32
	shutdown int32

	wg   sync.WaitGroup
	quit chan struct{}

	modifyRebroadcastInv chan any

	s *Service

	regainMP atomic.Bool

	// sendMempool is injectable for deterministic transition tests. Production
	// instances install the network-backed sender in NewRebroadcast.
	sendMempool     func(*peers.Peer, uint64) error
	snapshotMempool func() mempoolRegainSnapshot

	pendingInvTTL  time.Duration
	maxPendingInvs int
}

func (r *Rebroadcast) Start() {
	// Already started?
	if atomic.AddInt32(&r.started, 1) != 1 {
		return
	}

	log.Info("Starting Rebroadcast")

	r.wg.Add(2)
	go r.handler()
	go r.mempoolHandler()
}

func (r *Rebroadcast) Stop() error {
	// Make sure this only happens once.
	if atomic.AddInt32(&r.shutdown, 1) != 1 {
		log.Info("Rebroadcast is already in the process of shutting down")
		return nil
	}

	log.Info("Rebroadcast shutting down")

	close(r.quit)

	r.wg.Wait()
	return nil

}

func (r *Rebroadcast) mempoolHandler() {
	timer := time.NewTicker(params.ActiveNetParams.TargetTimePerBlock)
out:
	for {
		select {
		case <-timer.C:
			r.onRegainMempool()
		case <-r.quit:
			break out
		}
	}
	timer.Stop()
	r.wg.Done()
}

func (r *Rebroadcast) handler() {
	timer := time.NewTimer(params.ActiveNetParams.TargetTimePerBlock)
	pendingInvs := make(map[common.Hash]pendingInv)

out:
	for {
		select {
		case riv := <-r.modifyRebroadcastInv:
			switch msg := riv.(type) {
			case broadcastInventoryAdd:
				pendingInvs[*msg.hash] = pendingInv{data: msg.data, added: time.Now()}
			case broadcastInventoryDel:
				hs := []common.Hash(msg)
				for _, h := range hs {
					delete(pendingInvs, h)
				}
			}

		case <-timer.C:
			now := time.Now()
			// Evict old entries based on TTL.
			for h, inv := range pendingInvs {
				if now.Sub(inv.added) > r.pendingInvTTL {
					delete(pendingInvs, h)
				}
			}
			// Enforce maximum size by removing oldest entries.
			if len(pendingInvs) > r.maxPendingInvs {
				type kv struct {
					h common.Hash
					t time.Time
				}
				items := make([]kv, 0, len(pendingInvs))
				for h, inv := range pendingInvs {
					items = append(items, kv{h: h, t: inv.added})
				}
				sort.Slice(items, func(i, j int) bool {
					return items[i].t.Before(items[j].t)
				})
				over := len(pendingInvs) - r.maxPendingInvs
				for i := 0; i < over; i++ {
					delete(pendingInvs, items[i].h)
				}
			}
			isCurrent := r.s.PeerSync().IsCurrent()
			if !isCurrent {
				timer.Reset(params.ActiveNetParams.TargetTimePerBlock)
				break
			}
			startTime := time.Now()
			nds := collectRebroadcastNotifyData(
				pendingInvs,
				func(h common.Hash) bool { return r.s.TxMemPool().Has(h) },
				func() bool { return time.Since(startTime) > time.Second },
			)
			if len(nds) > 0 {
				r.s.RelayInventory(nds)
				r.s.sy.Peers().UpdateBroadcasts()
			}
			timer.Reset(rebroadcastInterval(len(pendingInvs)))
		case <-r.quit:
			break out
		}
	}
	timer.Stop()

cleanup:
	for {
		select {
		case <-r.modifyRebroadcastInv:
		default:
			break cleanup
		}
	}
	r.wg.Done()
}

func collectRebroadcastNotifyData(
	pendingInvs map[common.Hash]pendingInv,
	hasTx func(common.Hash) bool,
	timeBudgetExceeded func() bool,
) []*notify.NotifyData {
	nds := make([]*notify.NotifyData, 0, len(pendingInvs))
	for h, inv := range pendingInvs {
		if timeBudgetExceeded() {
			break
		}
		data := inv.data
		if _, ok := data.(*types.TxDesc); !ok {
			continue
		}

		// Check if the transaction still exists in the pool (pending or queued).
		if !hasTx(h) {
			delete(pendingInvs, h)
			continue
		}
		nds = append(nds, &notify.NotifyData{Data: data})
	}
	return nds
}

func rebroadcastInterval(pendingInvCount int) time.Duration {
	rt := int64(pendingInvCount/50) * int64(params.ActiveNetParams.TargetTimePerBlock)
	if rt < int64(params.ActiveNetParams.TargetTimePerBlock) {
		rt = int64(params.ActiveNetParams.TargetTimePerBlock)
	}
	return time.Duration(rt)
}

func (r *Rebroadcast) AddInventory(h *common.Hash, data any) {
	// Ignore if shutting down.
	if atomic.LoadInt32(&r.shutdown) != 0 {
		return
	}

	r.modifyRebroadcastInv <- broadcastInventoryAdd{hash: h, data: data}
}

func (r *Rebroadcast) RemoveInventory(hs []common.Hash) {
	// Ignore if shutting down.
	if atomic.LoadInt32(&r.shutdown) != 0 {
		return
	}
	if len(hs) <= 0 {
		return
	}
	r.modifyRebroadcastInv <- broadcastInventoryDel(hs)
}

func (r *Rebroadcast) RegainMempool() {
	r.regainMP.Store(true)
}

func (r *Rebroadcast) onRegainMempool() {
	if !r.regainMP.Swap(false) {
		return
	}
	rearm := func() { r.regainMP.Store(true) }
	snapshot := r.snapshotMempool
	if snapshot == nil {
		snapshot = func() mempoolRegainSnapshot {
			if !r.s.PeerSync().IsCurrent() {
				return mempoolRegainSnapshot{}
			}
			canPeers := []*peers.Peer{}
			for _, pe := range r.s.Peers().CanSyncPeers() {
				if time.Since(pe.GetMempoolReqTime()) > params.ActiveNetParams.TargetTimePerBlock {
					canPeers = append(canPeers, pe)
				}
			}
			if len(canPeers) == 0 {
				return mempoolRegainSnapshot{isCurrent: true}
			}
			pending, queued := r.s.TxMemPool().Stats()
			return mempoolRegainSnapshot{isCurrent: true, peer: canPeers[rand.Intn(len(canPeers))], count: pending + queued}
		}
	}
	snap := snapshot()
	if !snap.isCurrent {
		rearm()
		return
	}
	if snap.peer == nil {
		rearm()
		return
	}
	shouldSend, _ := regainMempoolDecision(true, true, snap.count, 1)
	if !shouldSend {
		// An oversize snapshot is a real drain transition, not a completed
		// regain. Keep the claim dirty so a later timer tick can observe the
		// smaller pool even when no new notification is emitted.
		if snap.count > MaxRegainMempoolSize {
			rearm()
		}
		return
	}
	send := r.sendMempool
	if send == nil {
		send = func(peer *peers.Peer, count uint64) error {
			_, err := r.s.sy.Send(peer, synch.RPCMemPool, &pb.MemPoolRequest{TxsNum: count})
			return err
		}
	}
	go func() {
		if err := send(snap.peer, uint64(snap.count)); err != nil {
			rearm()
		}
	}()
}

func (r *Rebroadcast) hasPendingRegainMempool() bool { return r.regainMP.Load() }

func (r *Rebroadcast) consumeRegainMempool() { r.regainMP.Store(false) }

func regainMempoolDecision(pendingSignal bool, isCurrent bool, mempoolTxCount int, relayPeerCount int) (bool, bool) {
	if !pendingSignal || !isCurrent || relayPeerCount <= 0 {
		return false, false
	}
	if mempoolTxCount > MaxRegainMempoolSize {
		return false, true
	}
	return true, true
}

func NewRebroadcast(s *Service) *Rebroadcast {
	r := Rebroadcast{
		s:                    s,
		quit:                 make(chan struct{}),
		modifyRebroadcastInv: make(chan any),
		pendingInvTTL:        defaultPendingInvTTL,
		maxPendingInvs:       defaultMaxPendingInvItems,
	}
	r.sendMempool = func(peer *peers.Peer, count uint64) error {
		_, err := s.sy.Send(peer, synch.RPCMemPool, &pb.MemPoolRequest{TxsNum: count})
		return err
	}
	r.RegainMempool()

	return &r
}

func existsInPending(pool *legacypool.LegacyPool, hash common.Hash) bool {
	pending := pool.Pending(txpool.PendingFilter{})
	for _, txs := range pending {
		for _, tx := range txs {
			if tx.Tx.Hash() == hash {
				return true
			}
		}
	}
	return false
}
