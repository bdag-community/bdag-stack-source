package p2p

import (
	"sort"
	"sync"
	"time"

	"github.com/BlockdagNetworkLabs/bdag/p2p/peers"
	"github.com/libp2p/go-libp2p/core/peer"
)

const (
	blockRelayScoreMaxPeers      = 512
	blockRelayRecentSuccessAge   = 2 * time.Minute
	blockRelayRecentFailureAge   = 30 * time.Second
	blockRelayLatencyPenaltyStep = 100 * time.Millisecond
)

type blockRelayScoreTracker struct {
	mu    sync.Mutex
	peers map[peer.ID]blockRelayPeerHistory
}

type blockRelayPeerHistory struct {
	successes           uint64
	failures            uint64
	consecutiveFailures uint64
	ewmaLatency         time.Duration
	lastSuccess         time.Time
	lastFailure         time.Time
	lastSeen            time.Time
}

type blockRelayPeerScoreSnapshot struct {
	quality     int64
	ewmaLatency time.Duration
}

func newBlockRelayScoreTracker() *blockRelayScoreTracker {
	return &blockRelayScoreTracker{peers: make(map[peer.ID]blockRelayPeerHistory)}
}

func (t *blockRelayScoreTracker) recordSuccess(id peer.ID, latency time.Duration, now time.Time) {
	t.record(id, latency, true, now)
}

func (t *blockRelayScoreTracker) recordFailure(id peer.ID, latency time.Duration, now time.Time) {
	t.record(id, latency, false, now)
}

func (t *blockRelayScoreTracker) record(id peer.ID, latency time.Duration, success bool, now time.Time) {
	if t == nil || id == "" {
		return
	}
	if now.IsZero() {
		now = time.Now()
	}
	if latency < 0 {
		latency = 0
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.peers == nil {
		t.peers = make(map[peer.ID]blockRelayPeerHistory)
	}
	history := t.peers[id]
	if success {
		history.successes++
		history.consecutiveFailures = 0
		history.lastSuccess = now
	} else {
		history.failures++
		history.consecutiveFailures++
		history.lastFailure = now
	}
	if latency > 0 {
		if history.ewmaLatency == 0 {
			history.ewmaLatency = latency
		} else {
			history.ewmaLatency = (history.ewmaLatency*7 + latency) / 8
		}
	}
	history.lastSeen = now
	t.peers[id] = history
	t.pruneLocked(now)
}

func (t *blockRelayScoreTracker) score(id peer.ID, now time.Time) blockRelayPeerScoreSnapshot {
	if t == nil || id == "" {
		return blockRelayPeerScoreSnapshot{}
	}
	if now.IsZero() {
		now = time.Now()
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	history := t.peers[id]
	return relayScoreFromHistory(history, now)
}

func (t *blockRelayScoreTracker) sortBroadcastPeers(candidates []*peers.Peer, freshness map[peer.ID]peers.MiningRelayPeerScore, now time.Time) {
	if t == nil || len(candidates) < 2 {
		return
	}
	if now.IsZero() {
		now = time.Now()
	}
	t.mu.Lock()
	snapshots := make(map[peer.ID]blockRelayPeerScoreSnapshot, len(candidates))
	for _, candidate := range candidates {
		if candidate == nil {
			continue
		}
		id := candidate.GetID()
		snapshots[id] = relayScoreFromHistory(t.peers[id], now)
	}
	t.mu.Unlock()

	sort.SliceStable(candidates, func(i, j int) bool {
		left := candidates[i].GetID()
		right := candidates[j].GetID()
		leftTier := miningRelayFreshnessTier(freshness[left])
		rightTier := miningRelayFreshnessTier(freshness[right])
		if leftTier != rightTier {
			return leftTier < rightTier
		}
		leftScore := snapshots[left]
		rightScore := snapshots[right]
		if leftScore.quality != rightScore.quality {
			return leftScore.quality > rightScore.quality
		}
		if leftScore.ewmaLatency > 0 && rightScore.ewmaLatency > 0 && leftScore.ewmaLatency != rightScore.ewmaLatency {
			return leftScore.ewmaLatency < rightScore.ewmaLatency
		}
		return false
	})
}

func relayScoreFromHistory(history blockRelayPeerHistory, now time.Time) blockRelayPeerScoreSnapshot {
	score := int64(history.successes)*2 - int64(history.failures)*4 - int64(history.consecutiveFailures)*8
	if !history.lastSuccess.IsZero() && now.Sub(history.lastSuccess) <= blockRelayRecentSuccessAge {
		score += 10
	}
	if !history.lastFailure.IsZero() && now.Sub(history.lastFailure) <= blockRelayRecentFailureAge {
		score -= 20
	}
	if history.ewmaLatency > 0 {
		score -= int64(history.ewmaLatency / blockRelayLatencyPenaltyStep)
	}
	return blockRelayPeerScoreSnapshot{quality: score, ewmaLatency: history.ewmaLatency}
}

func miningRelayFreshnessTier(score peers.MiningRelayPeerScore) int {
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

func (t *blockRelayScoreTracker) pruneLocked(now time.Time) {
	if len(t.peers) <= blockRelayScoreMaxPeers {
		return
	}
	type candidate struct {
		id       peer.ID
		lastSeen time.Time
	}
	items := make([]candidate, 0, len(t.peers))
	for id, history := range t.peers {
		items = append(items, candidate{id: id, lastSeen: history.lastSeen})
	}
	sort.Slice(items, func(i, j int) bool {
		return items[i].lastSeen.Before(items[j].lastSeen)
	})
	for len(t.peers) > blockRelayScoreMaxPeers && len(items) > 0 {
		delete(t.peers, items[0].id)
		items = items[1:]
	}
}
