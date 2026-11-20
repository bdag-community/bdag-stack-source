package peers

import (
	"errors"
	"sort"
	"sync"
	"time"

	"github.com/BlockdagNetworkLabs/bdag/consensus/model"
	"github.com/BlockdagNetworkLabs/bdag/p2p/common"
	"github.com/BlockdagNetworkLabs/bdag/p2p/qnr"
	ethcommon "github.com/ethereum/go-ethereum/common"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	ma "github.com/multiformats/go-multiaddr"
)

var (
	// ErrPeerUnknown is returned when there is an attempt to obtain data from a peer that is not known.
	ErrPeerUnknown = errors.New("peer unknown")
)

// Status is the structure holding the peer status information.
type Status struct {
	lock  sync.RWMutex
	peers map[peer.ID]*Peer

	p2p P2PRPC
}

type MiningRelayPeerScore struct {
	HasGraphState bool
	Fresh         bool
	LeadTooHigh   bool
	GraphStateAge time.Duration
	MainOrder     uint64
	LeadBlocks    int64
	ID            peer.ID
}

type MiningRelayPeerOrderingResult struct {
	FreshFanout int
	StaleFanout int
	Scores      map[peer.ID]MiningRelayPeerScore
}

func (p *Status) Remove(pid peer.ID) {
	p.lock.Lock()
	defer p.lock.Unlock()
	delete(p.peers, pid)
}

// Bad returns the peers that are bad.
func (p *Status) Bad() []peer.ID {
	p.lock.RLock()
	defer p.lock.RUnlock()

	peers := make([]peer.ID, 0, len(p.peers))
	for pid, status := range p.peers {
		if status.IsBad() {
			peers = append(peers, pid)
		}
	}
	return peers
}

// Connecting returns the peers that are connecting.
func (p *Status) Connecting() []peer.ID {
	p.lock.RLock()
	defer p.lock.RUnlock()
	peers := make([]peer.ID, 0, len(p.peers))
	for pid, status := range p.peers {
		if status.ConnectionState().IsConnecting() {
			peers = append(peers, pid)
		}
	}
	return peers
}

// Connected returns the peers that are connected.
func (p *Status) Connected() []peer.ID {
	p.lock.RLock()
	defer p.lock.RUnlock()
	peers := make([]peer.ID, 0, len(p.peers))
	for pid, status := range p.peers {
		if status.ConnectionState().IsConnected() {
			peers = append(peers, pid)
		}
	}
	return peers
}

func (p *Status) ConnectedPeers() []*Peer {
	p.lock.RLock()
	defer p.lock.RUnlock()
	peers := make([]*Peer, 0, len(p.peers))
	for _, status := range p.peers {
		if status.ConnectionState().IsConnected() {
			peers = append(peers, status)
		}
	}
	return peers
}

func (p *Status) CanSyncPeers() []*Peer {
	p.lock.RLock()
	defer p.lock.RUnlock()
	peers := make([]*Peer, 0, len(p.peers))
	for _, pe := range p.peers {
		if !p.IsActive(pe) ||
			!pe.IsConsensus() {
			continue
		}
		peers = append(peers, pe)
	}
	return peers
}

func (p *Status) AllPeers() []*Peer {
	p.lock.RLock()
	defer p.lock.RUnlock()
	peers := make([]*Peer, 0, len(p.peers))
	for _, status := range p.peers {
		peers = append(peers, status)
	}
	return peers
}

// Active returns the peers that are connecting or connected.
func (p *Status) Active() []peer.ID {
	p.lock.RLock()
	defer p.lock.RUnlock()
	peers := make([]peer.ID, 0, len(p.peers))
	for pid, pe := range p.peers {
		if p.IsActive(pe) {
			peers = append(peers, pid)
		}
	}
	return peers
}

// ActiveIDSet returns the currently active peer IDs as a lookup set.
func (p *Status) ActiveIDSet() map[peer.ID]struct{} {
	p.lock.RLock()
	defer p.lock.RUnlock()

	peers := make(map[peer.ID]struct{}, len(p.peers))
	for pid, pe := range p.peers {
		if p.IsActive(pe) {
			peers[pid] = struct{}{}
		}
	}
	return peers
}

// Disconnecting returns the peers that are disconnecting.
func (p *Status) Disconnecting() []peer.ID {
	p.lock.RLock()
	defer p.lock.RUnlock()
	peers := make([]peer.ID, 0, len(p.peers))
	for pid, status := range p.peers {
		if status.ConnectionState().IsDisconnecting() {
			peers = append(peers, pid)
		}
	}
	return peers
}

// Disconnected returns the peers that are disconnected.
func (p *Status) Disconnected() []peer.ID {
	p.lock.RLock()
	defer p.lock.RUnlock()
	peers := make([]peer.ID, 0, len(p.peers))
	for pid, status := range p.peers {
		if status.ConnectionState().IsDisconnected() {
			peers = append(peers, pid)
		}
	}
	return peers
}

// Inactive returns the peers that are disconnecting or disconnected.
func (p *Status) Inactive() []peer.ID {
	p.lock.RLock()
	defer p.lock.RUnlock()
	peers := make([]peer.ID, 0, len(p.peers))
	for pid, pe := range p.peers {
		if !p.IsActive(pe) {
			peers = append(peers, pid)
		}
	}
	return peers
}

// All returns all the peers regardless of state.
func (p *Status) All() []peer.ID {
	p.lock.RLock()
	defer p.lock.RUnlock()
	pids := make([]peer.ID, 0, len(p.peers))
	for pid := range p.peers {
		pids = append(pids, pid)
	}
	return pids
}

func (p *Status) DirInbound() []peer.ID {
	p.lock.RLock()
	defer p.lock.RUnlock()
	peers := make([]peer.ID, 0, len(p.peers))
	for pid, pe := range p.peers {
		if p.IsActive(pe) && pe.Direction() == network.DirInbound {
			peers = append(peers, pid)
		}
	}
	return peers
}

// fetch is a helper function that fetches a peer, possibly creating it.
func (p *Status) Get(pid peer.ID) *Peer {
	p.lock.RLock()
	defer p.lock.RUnlock()

	pe, ok := p.peers[pid]
	if !ok {
		return nil
	}
	return pe
}

// fetch is a helper function that fetches a peer, possibly creating it.
func (p *Status) Fetch(pid peer.ID) *Peer {
	p.lock.Lock()
	defer p.lock.Unlock()

	if _, ok := p.peers[pid]; !ok {
		var genHash *ethcommon.Hash
		if p.p2p != nil {
			genHash = p.p2p.GetGenesisHash()
		}
		p.peers[pid] = NewPeer(pid, genHash)
	}
	return p.peers[pid]
}

// Add adds a peer.
// If a peer already exists with this ID its address and direction are updated with the supplied data.
func (p *Status) Add(record *qnr.Record, pid peer.ID, address ma.Multiaddr, direction network.Direction) {
	p.lock.Lock()
	defer p.lock.Unlock()
	if pe, ok := p.peers[pid]; ok {
		// Peer already exists, just update its address info.
		pe.UpdateAddrDir(record, address, direction)
		return
	}
	pe := NewPeer(pid, p.p2p.GetGenesisHash())
	pe.UpdateAddrDir(record, address, direction)

	p.peers[pid] = pe
}

// IncrementBadResponses increments the number of bad responses we have received from the given remote peer.
func (p *Status) IncrementBadResponses(pid peer.ID, err *common.Error) {
	pe := p.Get(pid)
	if pe == nil {
		return
	}
	pe.IncrementBadResponses(err)
}

// SubscribedToSubnet retrieves the peers subscribed to the given
// committee subnet.
func (p *Status) SubscribedToSubnet(index uint64) []peer.ID {
	p.lock.RLock()
	defer p.lock.RUnlock()

	peers := make([]peer.ID, 0, len(p.peers))
	for pid, status := range p.peers {
		// look at active peers
		if p.IsActive(status) && status.metaData != nil && status.metaData.Subnets != nil {
			indices := retrieveIndicesFromBitfield(status.metaData.Subnets)
			for _, idx := range indices {
				if idx == index {
					peers = append(peers, pid)
					break
				}
			}
		}
	}
	return peers
}

func (p *Status) StatsSnapshots() []*StatsSnap {
	p.lock.RLock()
	defer p.lock.RUnlock()

	pes := make([]*StatsSnap, 0, len(p.peers))
	for _, pe := range p.peers {
		ss, err := pe.StatsSnapshot()
		if err != nil {
			continue
		}
		pes = append(pes, ss)
	}
	return pes
}

func (p *Status) FreshnessSnapshot(now time.Time, maxAge time.Duration, leadToleranceBlocks uint64, localMainOrder uint64, syncPeerID peer.ID) model.P2PMiningFreshnessSnapshot {
	p.lock.RLock()
	defer p.lock.RUnlock()

	if now.IsZero() {
		now = time.Now()
	}
	snapshot := model.P2PMiningFreshnessSnapshot{
		FreshnessMaxAge:         maxAge,
		PeerLeadToleranceBlocks: leadToleranceBlocks,
		SyncPeerPresent:         syncPeerID != "",
		MiningFreshReasonCode:   model.P2PMiningFreshNoConsensusPeers,
	}
	var totalAge time.Duration
	for _, pe := range p.peers {
		pe.lock.RLock()
		connected := pe.peerState.IsConnected()
		hasGraphState := !pe.graphStateTime.IsZero() && pe.chainState != nil && pe.chainState.GraphState != nil
		isSyncPeer := syncPeerID != "" && pe.pid == syncPeerID
		consensus := pe.isConsensus()
		var age time.Duration
		var mainOrder uint64
		if hasGraphState {
			age = now.Sub(pe.graphStateTime)
			mainOrder = uint64(pe.graphState().GetMainOrder())
		}
		pe.lock.RUnlock()
		if age < 0 {
			age = 0
		}

		// Observe the sync peer even when it is not (yet) classified as
		// consensus. Otherwise SyncPeerFresh stays at its zero value (false)
		// and mining freshness spuriously reports sync_peer_stale while the
		// rest of the consensus set is tip-fresh.
		if isSyncPeer && connected && hasGraphState {
			snapshot.SyncPeerObserved = true
			snapshot.SyncPeerGraphStateAge = age
			snapshot.SyncPeerFresh = maxAge <= 0 || age <= maxAge
		}

		if !connected || !consensus || !hasGraphState {
			continue
		}
		snapshot.ConsensusPeerCount++
		totalAge += age
		if maxAge <= 0 || age <= maxAge {
			snapshot.FreshConsensusPeerCount++
		} else {
			snapshot.StaleConsensusPeerCount++
		}
		if age > snapshot.MaxGraphStateAge {
			snapshot.MaxGraphStateAge = age
		}
		if snapshot.BestPeerMainOrder == 0 || mainOrder > snapshot.BestPeerMainOrder ||
			(mainOrder == snapshot.BestPeerMainOrder && (snapshot.BestPeerGraphStateAge == 0 || age < snapshot.BestPeerGraphStateAge)) {
			snapshot.BestPeerMainOrder = mainOrder
			snapshot.BestPeerGraphStateAge = age
			// Peer lead is how far the best peer is ahead of us. Local being
			// ahead of lagging/forked peers is not catch-up distance and must
			// not surface as a multi-million negative "lead".
			lead := int64(mainOrder) - int64(localMainOrder)
			if lead < 0 {
				lead = 0
			}
			snapshot.BestPeerLeadBlocks = lead
		}
	}
	if snapshot.ConsensusPeerCount > 0 {
		snapshot.AvgGraphStateAge = totalAge / time.Duration(snapshot.ConsensusPeerCount)
	}
	snapshot.SetMiningFreshnessVerdict()
	return snapshot
}

func SortMiningRelayPeers(candidates []*Peer, now time.Time, maxAge time.Duration, leadToleranceBlocks uint64, localMainOrder uint64) MiningRelayPeerOrderingResult {
	if now.IsZero() {
		now = time.Now()
	}
	result := MiningRelayPeerOrderingResult{
		Scores: make(map[peer.ID]MiningRelayPeerScore, len(candidates)),
	}
	type relayCandidate struct {
		peer  *Peer
		score MiningRelayPeerScore
		index int
	}
	ordered := make([]relayCandidate, 0, len(candidates))
	for i, candidate := range candidates {
		score := candidate.miningRelayPeerScore(now, maxAge, leadToleranceBlocks, localMainOrder)
		result.Scores[score.ID] = score
		if score.Fresh && !score.LeadTooHigh {
			result.FreshFanout++
		} else {
			result.StaleFanout++
		}
		ordered = append(ordered, relayCandidate{peer: candidate, score: score, index: i})
	}
	sort.SliceStable(ordered, func(i, j int) bool {
		left := ordered[i].score
		right := ordered[j].score
		leftBucket := miningRelayBucket(left)
		rightBucket := miningRelayBucket(right)
		if leftBucket != rightBucket {
			return leftBucket < rightBucket
		}
		if left.MainOrder != right.MainOrder {
			return left.MainOrder > right.MainOrder
		}
		if left.GraphStateAge != right.GraphStateAge {
			return left.GraphStateAge < right.GraphStateAge
		}
		leftID := left.ID.String()
		rightID := right.ID.String()
		if leftID != rightID {
			return leftID < rightID
		}
		return ordered[i].index < ordered[j].index
	})
	for i := range ordered {
		candidates[i] = ordered[i].peer
	}
	return result
}

func miningRelayBucket(score MiningRelayPeerScore) int {
	switch {
	case score.Fresh && !score.LeadTooHigh:
		return 0
	case score.HasGraphState && score.Fresh:
		return 1
	case score.HasGraphState:
		return 2
	default:
		return 3
	}
}

func (p *Peer) miningRelayPeerScore(now time.Time, maxAge time.Duration, leadToleranceBlocks uint64, localMainOrder uint64) MiningRelayPeerScore {
	p.lock.RLock()
	defer p.lock.RUnlock()

	score := MiningRelayPeerScore{ID: p.pid}
	if !p.peerState.IsConnected() || !p.isConsensus() || p.graphStateTime.IsZero() || p.chainState == nil || p.chainState.GraphState == nil {
		return score
	}
	age := now.Sub(p.graphStateTime)
	if age < 0 {
		age = 0
	}
	mainOrder := uint64(p.chainState.GraphState.MainOrder)
	leadBlocks := int64(mainOrder) - int64(localMainOrder)
	score.HasGraphState = true
	score.GraphStateAge = age
	score.MainOrder = mainOrder
	score.LeadBlocks = leadBlocks
	score.Fresh = maxAge <= 0 || age <= maxAge
	score.LeadTooHigh = leadToleranceBlocks > 0 && leadBlocks > int64(leadToleranceBlocks)
	return score
}

func (p *Status) ForPeers(state PeerConnectionState, closure func(pe *Peer)) {
	p.lock.RLock()
	defer p.lock.RUnlock()

	for _, pe := range p.peers {
		if pe.ConnectionState() != state {
			continue
		}
		closure(pe)
	}
}

func (p *Status) UpdateBroadcasts() {
	p.lock.RLock()
	defer p.lock.RUnlock()

	for _, pe := range p.peers {
		pe.UpdateBroadcast()
	}
}

func (p *Status) CanConnect(pid peer.ID) bool {
	cs := p.p2p.Host().Network().Connectedness(pid)
	return cs == network.CanConnect || cs == network.Connected
}

func (p *Status) IsActive(pe *Peer) bool {
	return pe.IsActive() && p.CanConnect(pe.GetID())
}

func (p *Status) IsActiveID(pid peer.ID) bool {
	pe := p.Get(pid)
	if pe == nil {
		return false
	}
	return p.IsActive(pe)
}

func (p *Status) GetByAddress(address ma.Multiaddr) *Peer {
	p.lock.RLock()
	defer p.lock.RUnlock()
	if address == nil {
		return nil
	}
	for _, pe := range p.peers {
		addr := pe.Address()
		if addr == nil {
			continue
		}
		if addr.Equal(address) {
			return pe
		}
	}
	return nil
}

// NewStatus creates a new status entity.
func NewStatus(p2p P2PRPC) *Status {
	return &Status{
		p2p:   p2p,
		peers: make(map[peer.ID]*Peer),
	}
}
