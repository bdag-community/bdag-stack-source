package model

import (
	"strings"
	"time"
)

const (
	P2PMiningFreshOK                     = "ok"
	P2PMiningFreshNoConsensusPeers       = "no_consensus_peers"
	P2PMiningFreshNotEnoughPeers         = "not_enough_consensus_peers"
	P2PMiningFreshNotEnoughFreshPeers    = "not_enough_fresh_consensus_peers"
	P2PMiningFreshAllConsensusPeersStale = "all_consensus_peers_stale"
	P2PMiningFreshPeerLeadTooHigh        = "peer_lead_exceeds_tolerance"
	P2PMiningFreshSyncPeerStale          = "sync_peer_stale"
	P2PMiningFreshMinConsensusPeers      = 2
)

type P2PMiningFreshnessSnapshot struct {
	FreshnessMaxAge         time.Duration
	PeerLeadToleranceBlocks uint64
	ConsensusPeerCount      int
	FreshConsensusPeerCount int
	StaleConsensusPeerCount int
	BestPeerMainOrder       uint64
	BestPeerLeadBlocks      int64
	BestPeerGraphStateAge   time.Duration
	AvgGraphStateAge        time.Duration
	MaxGraphStateAge        time.Duration
	SyncPeerPresent         bool
	SyncPeerObserved        bool
	SyncPeerFresh           bool
	SyncPeerGraphStateAge   time.Duration
	MiningFresh             bool
	MiningFreshReasonCode   string
}

func (s P2PMiningFreshnessSnapshot) Observed() bool {
	return strings.TrimSpace(s.MiningFreshReasonCode) != ""
}

func (s P2PMiningFreshnessSnapshot) AllowsMining(chainCurrent bool) bool {
	return chainCurrent &&
		s.Observed() &&
		s.MiningFresh &&
		strings.TrimSpace(s.MiningFreshReasonCode) == P2PMiningFreshOK
}

func (s *P2PMiningFreshnessSnapshot) SetMiningFreshnessVerdict() {
	switch {
	case s.ConsensusPeerCount == 0:
		s.MiningFresh = false
		s.MiningFreshReasonCode = P2PMiningFreshNoConsensusPeers
	case s.ConsensusPeerCount < P2PMiningFreshMinConsensusPeers:
		s.MiningFresh = false
		s.MiningFreshReasonCode = P2PMiningFreshNotEnoughPeers
	case s.FreshConsensusPeerCount == 0:
		s.MiningFresh = false
		s.MiningFreshReasonCode = P2PMiningFreshAllConsensusPeersStale
	case s.FreshConsensusPeerCount < P2PMiningFreshMinConsensusPeers:
		s.MiningFresh = false
		s.MiningFreshReasonCode = P2PMiningFreshNotEnoughFreshPeers
	case s.PeerLeadToleranceBlocks > 0 && s.BestPeerLeadBlocks > int64(s.PeerLeadToleranceBlocks):
		s.MiningFresh = false
		s.MiningFreshReasonCode = P2PMiningFreshPeerLeadTooHigh
	// Only fail on sync-peer staleness when that peer was actually observed in
	// this snapshot. SyncPeerPresent alone is not enough: the sync peer may be
	// briefly non-consensus (e.g. Services not yet set) and must not poison an
	// otherwise tip-fresh consensus set with a default SyncPeerFresh=false.
	case s.SyncPeerPresent && s.SyncPeerObserved && !s.SyncPeerFresh:
		s.MiningFresh = false
		s.MiningFreshReasonCode = P2PMiningFreshSyncPeerStale
	default:
		s.MiningFresh = true
		s.MiningFreshReasonCode = P2PMiningFreshOK
	}
}

type P2PService interface {
	IsCurrent() bool
	IsNearlySynced() bool
	MiningPeerFreshnessSnapshot(now time.Time) P2PMiningFreshnessSnapshot
}
