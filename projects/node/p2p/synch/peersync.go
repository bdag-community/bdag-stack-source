package synch

import (
	"fmt"
	"math/rand"
	"sync"
	"sync/atomic"
	"time"

	blockdag "github.com/BlockdagNetworkLabs/bdag/blockdag"
	"github.com/BlockdagNetworkLabs/bdag/common/system"
	"github.com/BlockdagNetworkLabs/bdag/consensus/model"
	"github.com/BlockdagNetworkLabs/bdag/core/blockchain"
	"github.com/BlockdagNetworkLabs/bdag/core/event"
	"github.com/BlockdagNetworkLabs/bdag/core/protocol"
	"github.com/BlockdagNetworkLabs/bdag/core/types"
	"github.com/BlockdagNetworkLabs/bdag/p2p/peers"
	pb "github.com/BlockdagNetworkLabs/bdag/p2p/proto/v1"
	"github.com/BlockdagNetworkLabs/bdag/params"
	"github.com/BlockdagNetworkLabs/bdag/services/notifymgr/notify"
	ethcommon "github.com/ethereum/go-ethereum/common"
	"github.com/libp2p/go-libp2p/core/peer"
)

const (
	// stallSampleInterval the interval at which we will check to see if our
	// sync has stalled.
	stallSampleInterval = 30 * time.Second
)

type PeerSync struct {
	sy *Sync

	splock      sync.RWMutex
	syncPeer    *peers.Peer
	lastSync    time.Time
	lastBlockID uint
	// dag sync
	dagSync *blockdag.DAGSync

	started  int32
	shutdown int32
	msgChan  chan any
	wg       sync.WaitGroup
	quit     chan struct{}

	pause          bool
	interrupt      chan struct{}
	processID      uint64
	processLock    sync.Mutex
	processWorkNum atomic.Int32
	processwg      sync.WaitGroup

	inFlightLock sync.Mutex
	inFlight     map[ethcommon.Hash]struct{}

	deferredUpdateLock  sync.Mutex
	deferredUpdatePeers map[peer.ID]struct{}

	evmStateGuardStrikeLock sync.Mutex
	evmStateGuardStrikes    map[evmStateGuardStrikeKey]int

	// relay seams are nil in production and let focused tests exercise the
	// concrete RelayInventory fanout and filtering path without a live host.
	relayInventoryPeers func() []*peers.Peer
	relayInventorySend  func(*peers.Peer, []*pb.InvVect) error
}

func (ps *PeerSync) Start() error {
	// Already started?
	if atomic.AddInt32(&ps.started, 1) != 1 {
		return nil
	}

	log.Info("P2P PeerSync Start")
	ps.dagSync = blockdag.NewDAGSync(ps.sy.p2p.BlockChain().Blockdag())

	ps.wg.Add(1)
	go ps.handler()
	return nil
}

func (ps *PeerSync) Stop() error {
	if atomic.AddInt32(&ps.shutdown, 1) != 1 {
		log.Warn("PeerSync is already in the process of shutting down")
		return nil
	}
	log.Info("P2P PeerSync Stoping...")

	close(ps.quit)
	ps.processwg.Wait()
	ps.wg.Wait()
	log.Info("P2P PeerSync Stoped")
	return nil
}

func (ps *PeerSync) IsRunning() bool {
	if atomic.LoadInt32(&ps.shutdown) != 0 {
		return false
	}
	if atomic.LoadInt32(&ps.started) == 0 {
		return false
	}
	return true
}

func (ps *PeerSync) handler() {
	stallTicker := time.NewTicker(stallSampleInterval)
	defer stallTicker.Stop()

out:
	for {
		select {
		case m := <-ps.msgChan:
			switch msg := m.(type) {
			case pauseMsg:
				ps.pause = !ps.pause
				msg.unpause <- ps.pause

			case *ConnectedMsg:
				ps.processConnected(msg)

			case *DisconnectedMsg:
				ps.processDisconnected(msg)
			case *GetBlocksMsg:
				ps.processGetBlocks(msg.pe, msg.blocks)
			case *GetBlockDatasMsg:
				ps.processGetBlockDatas(msg.pe, msg.blocks)
			case *GetDatasMsg:
				_ = ps.OnGetData(msg.pe, msg.data.Invs)
			// case *OnFilterAddMsg:
			// 	ps.OnFilterAdd(msg.pe, msg.data)
			// case *OnFilterClearMsg:
			// 	ps.OnFilterClear(msg.pe, msg.data)
			// case *OnFilterLoadMsg:
			// 	ps.OnFilterLoad(msg.pe, msg.data)
			case *OnMsgMemPool:
				if err := ps.OnMemPool(msg.pe, msg.data); err != nil {
					log.Error(fmt.Sprintf("Failed to process mempool message from peer %s: %v", msg.pe.GetID().String(), err))
				}

			case *UpdateGraphStateMsg:
				err := ps.processUpdateGraphState(msg.pe)
				if err != nil {
					log.Trace(err.Error())
				}
			case *syncDAGBlocksMsg:
				ps.processSyncDAGBlocks(msg.pe)
			case *PeerUpdateMsg:
				ps.OnPeerUpdate(msg.pe)

			case *SyncQNRMsg:
				err := ps.processQNR(msg)
				if err != nil {
					log.Warn(err.Error())
				}
			default:
				log.Warn(fmt.Sprintf("Invalid message type in task "+
					"handler: %T", msg))
			}

		case <-stallTicker.C:
			ps.handleStallSample()

		case <-ps.quit:
			break out
		}
	}

	// Drain any wait channels before going away so there is nothing left
	// waiting on this goroutine.
cleanup:
	for {
		select {
		case <-ps.msgChan:
		default:
			break cleanup
		}
	}

	ps.wg.Done()
	log.Trace("Peer Sync handler done")
}

func (ps *PeerSync) handleStallSample() {
	if atomic.LoadInt32(&ps.shutdown) != 0 {
		return
	}
	lbid := ps.Chain().Blockdag().GetLastBlockID()
	if ps.lastBlockID <= 0 {
		ps.lastBlockID = lbid
		return
	}
	if ps.lastBlockID != lbid {
		// The node made progress since the previous sample. Update the cached
		// value so future checks can detect when the block ID stops advancing
		// and trigger a resync automatically instead of leaving the sync loop
		// idle.
		ps.lastBlockID = lbid
		return
	}
	ps.TryAgainUpdateSyncPeer(true)
}

func (ps *PeerSync) Pause() bool {
	c := make(chan bool)
	ps.msgChan <- pauseMsg{c}
	return <-c
}

func (ps *PeerSync) SyncPeer() *peers.Peer {
	ps.splock.RLock()
	defer ps.splock.RUnlock()

	return ps.syncPeer
}

func (ps *PeerSync) SetSyncPeer(pe *peers.Peer) {
	ps.splock.Lock()
	defer ps.splock.Unlock()

	ps.syncPeer = pe
}

func (ps *PeerSync) OnPeerConnected(pe *peers.Peer) {
	if !ps.IsRunning() {
		return
	}
	ti := pe.Timestamp()
	if !ti.IsZero() {
		// Add the remote peer time as a sample for creating an offset against
		// the local clock to keep the network time in sync.
		ps.sy.p2p.TimeSource().AddTimeSample(pe.GetID().String(), ti)
	}

	ps.updateSyncPeer(false)
}

func (ps *PeerSync) OnPeerDisconnected(pe *peers.Peer) {
	if ps.HasSyncPeer() {
		if ps.isSyncPeer(pe) {
			if ps.deferSyncPeerUpdateAfterDisconnect(pe.GetID()) {
				log.Debug("[DEBUG-syncdag] deferring sync peer update after disconnect",
					"peer", pe.GetID(),
					"processID", ps.processID)
			}
		}
	}
}

func (ps *PeerSync) isSyncPeerID(pid peer.ID) bool {
	ps.splock.RLock()
	defer ps.splock.RUnlock()

	return ps.syncPeer != nil && ps.syncPeer.GetID() == pid
}

func (ps *PeerSync) isSyncPeer(pe *peers.Peer) bool {
	ps.splock.RLock()
	defer ps.splock.RUnlock()

	if ps.syncPeer == nil || pe == nil {
		return false
	}
	if pe == ps.syncPeer || pe.GetID() == ps.syncPeer.GetID() {
		return true
	}
	return false
}

func (ps *PeerSync) PeerUpdate(pe *peers.Peer, immediately bool) {
	// Ignore if we are shutting down.
	if atomic.LoadInt32(&ps.shutdown) != 0 {
		return
	}

	if immediately {
		ps.OnPeerUpdate(pe)
		return
	}

	pe.RunRate(PeerUpdate, UpdateGraphStateTime, func() {
		ps.OnPeerUpdate(pe)
	})

}

func (ps *PeerSync) OnPeerUpdate(pe *peers.Peer) {
	log.Trace(fmt.Sprintf("OnPeerUpdate peer=%v", pe.GetID()))
	ps.updateSyncPeer(false)
}

func (ps *PeerSync) HasSyncPeer() bool {
	return ps.SyncPeer() != nil
}

func (ps *PeerSync) Chain() *blockchain.BlockChain {
	return ps.sy.p2p.BlockChain()
}

// beginSync performs the "should a sync start, and if so mark it as started"
// check-then-act sequence under processLock, then releases it before
// returning. Returns ok=false if no sync should start right now (already
// syncing, paused, interrupted, or already caught up with every candidate
// peer).
//
// Once this returns ok=true, ps.SetSyncPeer has already been called, so
// HasSyncPeer() now reports true via its own independent lock (splock) --
// that alone is what prevents a second concurrent sync from starting for
// the rest of the operation. processLock does not need to stay held beyond
// this decision; see startSync's doc comment for why it used to be (and no
// longer is).
func (ps *PeerSync) beginSync() (bestPeer *peers.Peer, best *blockchain.BestState, gs *blockdag.GraphState, ok bool) {
	ps.processLock.Lock()
	defer ps.processLock.Unlock()

	if ps.IsInterrupt() {
		return nil, nil, nil, false
	}
	// Return now if we're already syncing.
	if ps.HasSyncPeer() {
		return nil, nil, nil, false
	}
	if ps.pause {
		log.Debug("P2P is pause.")
		return nil, nil, nil, false
	}
	best = ps.Chain().BestSnapshot()
	bestPeer = ps.getBestPeer()
	if bestPeer == nil {
		return nil, nil, nil, false
	}

	ps.processID++
	if ps.IsInterrupt() {
		return nil, nil, nil, false
	}
	ps.processwg.Add(1)
	gs = bestPeer.GraphState()
	protocolVersion := uint32(0)
	services := uint64(0)
	if cs := bestPeer.ChainState(); cs != nil {
		protocolVersion = cs.ProtocolVersion
		services = cs.Services
	}
	log.Info("Syncing graph state", "cur", best.GraphState.String(), "target", gs.String(), "peer", bestPeer.GetID().String(),
		"protocol", protocolVersion, "services", protocol.ServiceFlag(services).String(), "processID", ps.processID)
	// When the current height is less than a known checkpoint we
	// can use block headers to learn about which blocks comprise
	// the chain up to the checkpoint and perform less validation
	// for them.  This is possible since each header contains the
	// hash of the previous header and a merkle root.  Therefore if
	// we validate all of the received headers link together
	// properly and the checkpoint hashes match, we can be sure the
	// hashes for the blocks in between are accurate.  Further, once
	// the full blocks are downloaded, the merkle root is computed
	// and compared against the value in the header which proves the
	// full block hasn't been tampered with.
	//
	// Once we have passed the final checkpoint, or checkpoints are
	// disabled, use standard inv messages learn about the blocks
	// and fully validate them.  Finally, regression test mode does
	// not support the headers-first approach so do normal block
	// downloads when in regression test mode.
	ps.SetSyncPeer(bestPeer)
	ps.dagSync.SetGraphState(gs)
	return bestPeer, best, gs, true
}

// startSync will choose the best peer among the available candidate peers to
// download/sync the blockchain from.  When syncing is already running, it
// simply returns.  It also examines the candidates for any which are no longer
// candidates and removes them as needed.
//
// The initial decision (should a sync start, and marking it as started) runs
// under processLock via beginSync; the sync operation itself -- which can run
// for a very long time on a large backlog -- does not hold processLock. Once
// beginSync has called SetSyncPeer, syncPeer/splock alone is what prevents a
// second concurrent sync from starting, so there is nothing left for
// processLock to protect here. It used to be held for this whole function
// (a single top-level defer), which meant every other peer's periodic
// sync-check (updateSyncPeer -> startSync) blocked for the full duration of
// an active sync rather than just the brief decision -- confirmed via a live
// mutex profile attributing tens of seconds of average wait per contention
// here, the largest single contributor in that profile.
func (ps *PeerSync) startSync() {
	bestPeer, best, gs, ok := ps.beginSync()
	if !ok {
		log.Trace("You're already up to date, no synchronization is required.")
		return
	}

cleanup:
	for {
		select {
		case <-ps.interrupt:
		default:
			break cleanup
		}
	}
	ps.interrupt = make(chan struct{})
	startTime := time.Now()
	ps.lastSync = startTime
	longSyncMod := false

	if gs.GetTotal() >= best.GraphState.GetTotal()+MaxBlockLocatorsPerMsg {
		longSyncMod = true
		ps.sy.p2p.Consensus().Events().Send(event.New(event.DownloaderStart))
	}
	refresh := true
	add := 0
	noProgressOrphanPasses := 0
	for {
		if ps.IsInterrupt() {
			break
		}
		ret := ps.IntellectSyncBlocks(refresh, bestPeer)
		if ret == nil ||
			ret.act.IsNothing() {
			break
		} else if ret.act.IsContinue() {
			refresh = ret.orphan
			if ret.add <= 0 {
				if ret.orphan && noProgressOrphanPasses == 0 {
					noProgressOrphanPasses++
					log.Debug("Refreshing orphan sync after no-progress duplicate-orphan pass", "processID", ps.processID)
					continue
				}
				if ret.orphan {
					log.Warn("Rotating sync peer after no-progress orphan reconciliation", "processID", ps.processID)
					go ps.TryAgainUpdateSyncPeer(true)
				}
				break
			}
			noProgressOrphanPasses = 0
			add += ret.add
			ps.logSyncETA(best.GraphState, gs, add, time.Since(startTime), bestPeer)
			if !ps.checkContinueSync() {
				break
			}
		} else if ret.act.IsTryAgain() {
			go ps.TryAgainUpdateSyncPeer(false)
			break
		}
	}
	//
	log.Info("The sync of graph state has ended", "spend", time.Since(startTime).Truncate(time.Second).String(), "processID", ps.processID)
	if add > 0 {
		if longSyncMod && !ps.IsInterrupt() {
			if ps.IsCurrent() {
				log.Info("You're up to date now.")
				ps.Chain().RebuildUpgradesFromHeadersIfNeeded("peer-sync")
			}
		}
	}
	if longSyncMod {
		ps.sy.p2p.Consensus().Events().Send(event.New(event.DownloaderEnd))
	}
	ps.SetSyncPeer(nil)
	ps.processwg.Done()
}

func (ps *PeerSync) logSyncETA(cur *blockdag.GraphState, target *blockdag.GraphState, processed int, elapsed time.Duration, peer *peers.Peer) {
	remaining, eta := estimateSyncETA(cur, target, processed, elapsed)
	if peer == nil || remaining <= 0 || eta <= 0 {
		return
	}
	log.Info("Syncing graph state ETA",
		"cur", cur.String(),
		"target", target.String(),
		"peer", peer.GetID().String(),
		"remainingMainOrder", remaining,
		"processed", processed,
		"elapsed", elapsed.Truncate(time.Second).String(),
		"eta", eta.Truncate(time.Second).String(),
		"processID", ps.processID,
	)
}

func estimateSyncETA(cur *blockdag.GraphState, target *blockdag.GraphState, processed int, elapsed time.Duration) (int64, time.Duration) {
	if cur == nil || target == nil || processed <= 0 || elapsed <= 0 {
		return 0, 0
	}
	remaining := int64(target.GetMainOrder()) - int64(cur.GetMainOrder())
	if remaining <= 0 {
		return remaining, 0
	}
	rate := float64(processed) / elapsed.Seconds()
	if rate <= 0 {
		return remaining, 0
	}
	eta := time.Duration((float64(remaining) / rate) * float64(time.Second))
	return remaining, eta
}

// getBestPeer
func (ps *PeerSync) getBestPeer() *peers.Peer {
	bestPeer, _ := ps.bestSyncPeerSnapshot()
	return bestPeer
}

func (ps *PeerSync) bestSyncPeerSnapshot() (*peers.Peer, *blockdag.GraphState) {
	best := ps.Chain().BestSnapshot()
	return selectBestSyncPeerWithLatency(ps.sy.peers.CanSyncPeers(), best.GraphState, ps.lowestLatencyPeer)
}

func selectBestSyncPeer(syncPeers []*peers.Peer, bestGraphState *blockdag.GraphState) (*peers.Peer, *blockdag.GraphState) {
	return selectBestSyncPeerWithLatency(syncPeers, bestGraphState, nil)
}

func selectBestSyncPeerWithLatency(syncPeers []*peers.Peer, bestGraphState *blockdag.GraphState, latencySelector func([]*peers.Peer) (*peers.Peer, time.Duration)) (*peers.Peer, *blockdag.GraphState) {
	if bestGraphState == nil {
		return nil, nil
	}

	var bestPeer *peers.Peer
	var bestPeerGraphState *blockdag.GraphState
	equalPeers := make([]*peers.Peer, 0, len(syncPeers))
	for _, sp := range syncPeers {
		// Remove sync candidate peers that are no longer candidates due
		// to passing their latest known block.  NOTE: The < is
		// intentional as opposed to <=.  While techcnically the peer
		// doesn't have a later block when it's equal, it will likely
		// have one soon so it is a reasonable choice.  It also allows
		// the case where both are at 0 such as during regression test.
		gs := sp.GraphState()
		if gs == nil {
			continue
		}
		if observation := sp.ForkObservation(); observation != nil && observation.DivergentMainChain() {
			log.Warn("Skipping divergent sync peer candidate",
				"peer", sp.GetID().String(),
				"status", observation.Status,
				"reason", observation.Reason,
				"mainTip", observation.MainTip.String(),
				"syncPoint", observation.SyncPoint.String())
			continue
		}
		if !gs.IsExcellent(bestGraphState) {
			continue
		}
		// the best sync candidate is the most updated peer
		if bestPeer == nil {
			bestPeer = sp
			bestPeerGraphState = gs
			equalPeers = append(equalPeers[:0], sp)
			continue
		}
		if gs.IsExcellent(bestPeerGraphState) {
			bestPeer = sp
			bestPeerGraphState = gs
			equalPeers = append(equalPeers[:0], sp)
		} else if gs.IsEqual(bestPeerGraphState) {
			equalPeers = append(equalPeers, sp)
		}
	}
	if len(equalPeers) <= 0 {
		return nil, nil
	}

	if len(equalPeers) == 1 {
		return equalPeers[0], bestPeerGraphState
	}
	if latencySelector != nil {
		lowLatencyPeer, latency := latencySelector(equalPeers)
		if lowLatencyPeer != nil {
			log.Debug("Selected lowest-latency sync peer", "peer", lowLatencyPeer.GetID().String(), "latency", latency.String(), "candidates", len(equalPeers))
			return lowLatencyPeer, bestPeerGraphState
		}
	}

	index := int(rand.Int63n(int64(len(equalPeers))))
	if index >= len(equalPeers) {
		index = 0
	}
	return equalPeers[index], bestPeerGraphState
}

func (ps *PeerSync) lowestLatencyPeer(candidatePeers []*peers.Peer) (*peers.Peer, time.Duration) {
	if ps == nil || ps.sy == nil || ps.sy.p2p == nil || ps.sy.p2p.Host() == nil {
		return nil, 0
	}
	var selected *peers.Peer
	var selectedLatency time.Duration
	for _, pe := range candidatePeers {
		if pe == nil {
			continue
		}
		latency := ps.sy.p2p.Host().Peerstore().LatencyEWMA(pe.GetID())
		if latency <= 0 {
			continue
		}
		if selected == nil || latency < selectedLatency {
			selected = pe
			selectedLatency = latency
		}
	}
	return selected, selectedLatency
}

// IsCurrent returns true if we believe we are synced with our peers, false if we
// still have blocks to check
func (ps *PeerSync) IsCurrent() bool {
	if !ps.Chain().IsCurrent() {
		return false
	}

	return ps.IsCompleteForSyncPeer()
}

func (ps *PeerSync) IsNearlySynced() bool {
	if !ps.Chain().IsCurrent() {
		return false
	}
	syncPeers := ps.sy.peers.CanSyncPeers()
	if len(syncPeers) <= 0 {
		return true
	}
	best := ps.Chain().BestSnapshot()
	nearlySyncedOrder := best.GraphState.GetMainOrder() + blockdag.StableConfirmations
	for _, sp := range syncPeers {
		gs := sp.GraphState()
		if gs == nil {
			continue
		}
		if nearlySyncedOrder >= gs.GetMainOrder() {
			continue
		}
		return false
	}
	return true
}

func (ps *PeerSync) MiningPeerFreshnessSnapshot(now time.Time) model.P2PMiningFreshnessSnapshot {
	if now.IsZero() {
		now = time.Now()
	}
	freshnessMaxAge := params.ActiveNetParams.TargetTimePerBlock * 3
	leadTolerance := uint64(blockdag.StableConfirmations)
	var localMainOrder uint64
	if best := ps.Chain().BestSnapshot(); best != nil && best.GraphState != nil {
		localMainOrder = uint64(best.GraphState.GetMainOrder())
	}
	var syncPeerID peer.ID
	if sp := ps.SyncPeer(); sp != nil {
		syncPeerID = sp.GetID()
	}
	return ps.sy.peers.FreshnessSnapshot(now, freshnessMaxAge, leadTolerance, localMainOrder, syncPeerID)
}

// IsCompleteForSyncPeer returns true if we believe we are synced with our peers, false if we
// still have blocks to check
func (ps *PeerSync) IsCompleteForSyncPeer() bool {
	// if blockChain thinks we are current and we have no syncPeer it
	// is probably right.
	sp := ps.SyncPeer()
	if sp == nil {
		return true
	}

	// No matter what chain thinks, if we are below the block we are syncing
	// to we are not current.
	gs := sp.GraphState()
	if gs == nil {
		return true
	}
	// if the sync peer is not the best peer, return false
	if gs.IsExcellent(ps.Chain().BestSnapshot().GraphState) {
		log.Trace("comparing the current best vs sync last",
			"current.best", ps.Chain().BestSnapshot().GraphState.String(), "sync.last", gs.String())
		return false
	}

	return true
}

// IntellectSyncBlocks is the main function to sync the blockchain
func (ps *PeerSync) IntellectSyncBlocks(refresh bool, pe *peers.Peer) *ProcessResult {
	if pe == nil {
		log.Trace(fmt.Sprintf("IntellectSyncBlocks has not sync peer, return directly"), "processID", ps.processID)
		return nil
	}
	if ps.pause {
		log.Debug("P2P is pause.", "processID", ps.processID)
		return nil
	}
	if ps.Chain().GetOrphansTotal() >= blockchain.MaxOrphanBlocks || refresh {
		err := ps.Chain().RefreshOrphans()
		if err != nil {
			log.Trace(fmt.Sprintf("IntellectSyncBlocks failed to refresh orphans, err=%v", err.Error()), "processID", ps.processID)
		}
	}
	if ps.IsInterrupt() {
		return nil
	}
	allOrphan := ps.Chain().CheckRecentOrphansParents()
	if ps.IsInterrupt() {
		return nil
	}
	if len(allOrphan) > 0 {
		log.Trace(fmt.Sprintf("IntellectSyncBlocks do ps.GetBlock, peer=%v,allOrphan=%v ", pe.GetID(), allOrphan), "processID", ps.processID)
		if len(allOrphan) == 1 {
			return ps.processGetBlockDatas(pe, allOrphan)
		}
		return ps.processGetBlocks(pe, allOrphan)
	} else {
		log.Trace(fmt.Sprintf("IntellectSyncBlocks do ps.syncDAGBlocks, peer=%v ", pe.GetID()), "processID", ps.processID)
		return ps.processSyncDAGBlocks(pe)
	}
}

func (ps *PeerSync) updateSyncPeer(force bool) {
	if !ps.IsRunning() {
		return
	}
	if force && ps.HasSyncPeer() {
		ps.requestSyncPeerInterrupt()
	}
	if ps.processWorkNum.Load() >= 2 {
		return
	}
	ps.processWorkNum.Add(1)
	log.Debug("Updating sync peer", "force", force)
	if force && ps.processWorkNum.Load() >= 2 {
		ps.requestSyncPeerInterrupt()
	}
	ps.startSync()
	ps.processWorkNum.Add(-1)
}

func (ps *PeerSync) requestSyncPeerInterrupt() {
	interrupt := ps.interrupt
	go func() {
		select {
		case interrupt <- struct{}{}:
		case <-time.After(time.Second):
		}
	}()
}

func (ps *PeerSync) deferSyncPeerUpdateAfterDisconnect(pid peer.ID) bool {
	ps.deferredUpdateLock.Lock()
	if ps.deferredUpdatePeers == nil {
		ps.deferredUpdatePeers = make(map[peer.ID]struct{})
	}
	if _, ok := ps.deferredUpdatePeers[pid]; ok {
		ps.deferredUpdateLock.Unlock()
		return false
	}
	ps.deferredUpdatePeers[pid] = struct{}{}
	ps.deferredUpdateLock.Unlock()

	go ps.updateSyncPeerAfterDisconnect(pid)
	return true
}

func (ps *PeerSync) clearDeferredSyncPeerUpdate(pid peer.ID) {
	ps.deferredUpdateLock.Lock()
	defer ps.deferredUpdateLock.Unlock()

	delete(ps.deferredUpdatePeers, pid)
}

func (ps *PeerSync) updateSyncPeerAfterDisconnect(pid peer.ID) {
	defer ps.clearDeferredSyncPeerUpdate(pid)

	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	timeout := time.NewTimer(ReqTimeout + RespTimeout)
	defer timeout.Stop()

	for {
		if !ps.IsRunning() {
			return
		}
		if !ps.isSyncPeerID(pid) {
			log.Debug("[DEBUG-syncdag] running deferred sync peer update",
				"peer", pid,
				"reason", "sync peer cleared")
			ps.updateSyncPeer(false)
			return
		}

		select {
		case <-ticker.C:
		case <-timeout.C:
			log.Debug("[DEBUG-syncdag] forcing deferred sync peer update",
				"peer", pid,
				"reason", "sync peer still active after disconnect grace")
			ps.updateSyncPeer(true)
			return
		case <-ps.quit:
			return
		}
	}
}

func (ps *PeerSync) TryAgainUpdateSyncPeer(immediately bool) {
	if !immediately {
		<-time.After(DefaultRateTaskTime)
	}
	ps.updateSyncPeer(true)
}

func (ps *PeerSync) checkContinueSync() bool {
	if !ps.IsRunning() {
		return false
	}
	log.Debug("Continue sync peer")
	sp := ps.SyncPeer()
	if sp != nil {
		spgs := sp.GraphState()
		if !sp.IsConnected() || spgs == nil {
			go ps.TryAgainUpdateSyncPeer(true)
			return false
		}
		bestPeer := ps.getBestPeer()
		if bestPeer == nil {
			go ps.TryAgainUpdateSyncPeer(true)
			return false
		}
		if bestPeer != sp {
			log.Info("Refreshing sync peer", "current", sp.GetID().String(),
				"best", bestPeer.GetID().String(), "processID", ps.processID)
			go ps.TryAgainUpdateSyncPeer(true)
			return false
		}
	}
	return true
}

func (ps *PeerSync) RelayInventory(nds []*notify.NotifyData) {
	var peerList []*peers.Peer
	if ps.relayInventoryPeers != nil {
		peerList = ps.relayInventoryPeers()
	} else {
		peerList = ps.sy.Peers().CanSyncPeers()
	}
	for _, pe := range peerList {
		invs, running := relayInventoryForPeer(pe, nds, ps.IsRunning)
		if !running {
			return
		}
		if len(invs) == 0 {
			continue
		}

		var err error
		if ps.relayInventorySend != nil {
			err = ps.relayInventorySend(pe, invs)
		} else {
			err = ps.sy.tryToSendInventoryRequest(pe, invs)
		}
		if err != nil {
			log.Error(fmt.Sprintf("Failed to send inventory request to peer %s: %v", pe.GetID().String(), err))
		}
	}
}

func relayInventoryForPeer(pe *peers.Peer, nds []*notify.NotifyData, isRunning func() bool) ([]*pb.InvVect, bool) {
	invs := make([]*pb.InvVect, 0, len(nds))
	for _, nd := range nds {
		if !isRunning() {
			return nil, false
		}
		if nd.IsFilter(pe.GetID()) {
			continue
		}

		switch value := nd.Data.(type) {
		case *types.TxDesc:
			hash := value.Tx.Hash()
			hashString := hash.String()
			if pe.HasBroadcast(hashString) {
				continue
			}
			// Don't relay the transaction to the peer when it has
			// transaction relaying disabled.
			if pe.DisableRelayTx() {
				continue
			}
			feeFilter := pe.FeeFilter()
			if feeFilter > 0 && value.FeePerKB < feeFilter {
				continue
			}
			// Don't relay the transaction if there is a bloom
			// filter loaded and the transaction doesn't match it.
			// filter := pe.Filter()
			// if filter.IsLoaded() {
			// if !filter.MatchTxAndUpdate(value.Tx) {
			// continue
			// }
			// }
			invs = append(invs, NewInvVect(InvTypeTx, &hash))
			log.Trace("Relay inventory tx", "hash", hashString, "peer", pe.GetID())
			pe.Broadcast(hashString, value)

		case types.BlockHeader:
			blockHash := value.BlockHash()
			invs = append(invs, NewInvVect(InvTypeBlock, &blockHash))
			log.Trace("Relay inventory block", "hash", blockHash, "peer", pe.GetID())
		}
	}
	return invs, true
}
func (ps *PeerSync) RelayGraphState() {
	for _, pe := range ps.sy.Peers().CanSyncPeers() {
		ps.UpdateGraphState(pe)
	}
}

// EnforceNodeBloomFlag disconnects the peer if the server is not configured to
// allow bloom filters.  Additionally, if the peer has negotiated to a protocol
// version  that is high enough to observe the bloom filter service support bit,
// it will be banned since it is intentionally violating the protocol.
// func (ps *PeerSync) EnforceNodeBloomFlag(sp *peers.Peer) bool {
// 	services := sp.Services()
// 	if services&protocol.Bloom != protocol.Bloom {
// 		// Disconnect the peer regardless of protocol version or banning
// 		// state.
// 		log.Debug(fmt.Sprintf("%s sent a filterclear request with no "+
// 			"filter loaded -- disconnecting", sp.GetID().String()))
// 		ps.immediatelyDisconnected(sp)
// 		return false
// 	}

// 	return true
// }

// OnFilterAdd is invoked when a peer receives a filteradd Blockdag
// message and is used by remote peers to add data to an already loaded bloom
// filter.  The peer will be disconnected if a filter is not loaded when this
// message is received or the server is not configured to allow bloom filters.
// func (ps *PeerSync) OnFilterAdd(sp *peers.Peer, msg *types.MsgFilterAdd) {
// 	// Disconnect and/or ban depending on the node bloom services flag and
// 	// negotiated protocol version.
// 	if !ps.EnforceNodeBloomFlag(sp) {
// 		return
// 	}
// 	// filter := sp.Filter()
// 	// if !filter.IsLoaded() {
// 	// log.Debug(fmt.Sprintf("%s sent a filterclear request with no "+
// 	// 	"filter loaded -- disconnecting", sp.GetID().String()))
// 	// ps.immediatelyDisconnected(sp)
// 	// return
// 	// }

// 	// filter.Add(msg.Data)
// }

// OnFilterClear is invoked when a peer receives a filterclear Blockdag
// message and is used by remote peers to clear an already loaded bloom filter.
// The peer will be disconnected if a filter is not loaded when this message is
// received  or the server is not configured to allow bloom filters.
// func (ps *PeerSync) OnFilterClear(sp *peers.Peer, msg *types.MsgFilterClear) {
// 	// Disconnect and/or ban depending on the node bloom services flag and
// 	// negotiated protocol version.
// 	if !ps.EnforceNodeBloomFlag(sp) {
// 		return
// 	}
// 	filter := sp.Filter()

// 	if !filter.IsLoaded() {
// 		log.Debug(fmt.Sprintf("%s sent a filterclear request with no "+
// 			"filter loaded -- disconnecting", sp.GetID().String()))
// 		ps.immediatelyDisconnected(sp)
// 		return
// 	}

// 	filter.Unload()
// }

// OnFilterLoad is invoked when a peer receives a filterload Blockdag
// message and it used to load a bloom filter that should be used for
// delivering merkle blocks and associated transactions that match the filter.
// The peer will be disconnected if the server is not configured to allow bloom
// filters.
// func (ps *PeerSync) OnFilterLoad(sp *peers.Peer, msg *types.MsgFilterLoad) {
// 	// Disconnect and/or ban depending on the node bloom services flag and
// 	// negotiated protocol version.
// 	if !ps.EnforceNodeBloomFlag(sp) {
// 		return
// 	}
// 	filter := sp.Filter()
// 	sp.DisableRelayTx()

// 	filter.Reload(msg)
// }

func (ps *PeerSync) IsInterrupt() bool {
	if system.InterruptRequested(ps.interrupt) ||
		system.InterruptRequested(ps.quit) {
		return true
	}
	return false
}

func NewPeerSync(sy *Sync) *PeerSync {
	peerSync := &PeerSync{
		sy:                   sy,
		msgChan:              make(chan any),
		quit:                 make(chan struct{}),
		pause:                false,
		interrupt:            make(chan struct{}),
		inFlight:             make(map[ethcommon.Hash]struct{}),
		deferredUpdatePeers:  make(map[peer.ID]struct{}),
		evmStateGuardStrikes: make(map[evmStateGuardStrikeKey]int),
	}

	return peerSync
}

func (ps *PeerSync) reserveSyncBlock(hash ethcommon.Hash) bool {
	ps.inFlightLock.Lock()
	defer ps.inFlightLock.Unlock()
	if ps.inFlight == nil {
		ps.inFlight = make(map[ethcommon.Hash]struct{})
	}
	if _, ok := ps.inFlight[hash]; ok {
		return false
	}
	ps.inFlight[hash] = struct{}{}
	return true
}

func (ps *PeerSync) releaseSyncBlocks(hashes []ethcommon.Hash) {
	if len(hashes) == 0 {
		return
	}
	ps.inFlightLock.Lock()
	defer ps.inFlightLock.Unlock()
	for _, hash := range hashes {
		delete(ps.inFlight, hash)
	}
}
