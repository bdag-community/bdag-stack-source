package synch

import (
	"fmt"
	"sync"
	"time"

	"github.com/BlockdagNetworkLabs/bdag/common/roughtime"
	"github.com/BlockdagNetworkLabs/bdag/consensus/model"
	"github.com/BlockdagNetworkLabs/bdag/p2p/common"
	"github.com/BlockdagNetworkLabs/bdag/p2p/peers"
	"github.com/BlockdagNetworkLabs/bdag/p2p/runutil"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
)

const ReconnectionTime = time.Second * 30

type peerCompatibilityMetricCounts struct {
	Total        int
	Compatible   int
	Unknown      int
	Stale        int
	WrongFork    int
	WrongDataset int
	Unsafe       int
}

type peerUsefulnessMetricCounts struct {
	Total               int
	UsefulForTarget     int
	SyncMiningPreferred int
	SyncMiningUsable    int
	StaleProofUsable    int
	DiscoveryRelayOnly  int
	Deprioritized       int
	Avoid               int
	Unknown             int
}

func (s *Sync) maintainPeerStatuses() {
	ctx := s.p2p.Context()
	runutil.RunEvery(ctx, s.PeerInterval, func() {
		if s.goodbyes != nil {
			s.goodbyes.sweepRecent(roughtime.Now())
		}
		sem := make(chan struct{}, 100) // limit to 100 concurrent goroutines
		var wg sync.WaitGroup

		// Handle connected peers
		for _, pid := range s.Peers().Connected() {
			pe := s.peers.Get(pid)
			if pe == nil {
				continue
			}

			sem <- struct{}{}
			wg.Add(1)

			go func(id peer.ID, pe *peers.Peer) {
				defer func() {
					<-sem
					wg.Done()
				}()

				select {
				case <-ctx.Done():
					return
				default:
				}

				if s.p2p.Host().Network().Connectedness(id) != network.Connected && !s.IsPeerAtLimit() {
					s.p2p.ConnectToPeer(pe.QAddress().String(), false)
					return
				}

				if !pe.CanConnectWithNetwork() {
					if err := s.sendGoodByeAndDisconnect(common.ErrBadPeer, pe); err != nil {
						log.Debug(fmt.Sprintf("Error when disconnecting with bad peer: %v", err))
					}
					return
				}

				if !pe.IsConsensus() {
					return
				}

				if roughtime.Now().After(pe.ChainStateLastUpdated().Add(s.PeerInterval)) {
					if err := s.reValidatePeer(pe); err != nil {
						log.Debug(fmt.Sprintf("Failed to revalidate peer (%v), peer:%s", err, id))
					}
				}
				s.peerSync.UpdatePeerForkObservation(pe)

				if pe.QNR() == nil && time.Since(pe.ConnectionTime()) > ReconnectionTime && s.p2p.Node() != nil {
					s.peerSync.SyncQNR(pe, s.p2p.Node().String())
				}
			}(pid, pe)
		}

		// Handle disconnected peers
		for _, pid := range s.Peers().Disconnected() {
			pe := s.peers.Get(pid)
			if pe == nil {
				continue
			}

			if s.p2p.Host().Network().Connectedness(pe.GetID()) == network.Connected {
				if !s.expireUnvalidatedConnections(pe, time.Now()) {
					continue
				}
			}

			node := pe.Node()
			if node == nil || time.Since(pe.ConnectionTime()) < ReconnectionTime || pe.IsBad() {
				continue
			}
			s.LookupNode(nil, node)
			s.peers.Remove(pid) // important: free peer memory
		}

		wg.Wait()
		publishPeerFreshnessMetrics(s.peerSync.MiningPeerFreshnessSnapshot(roughtime.Now()))
		publishPeerCompatibilityMetrics(s)
		publishPeerUsefulnessMetrics(s)
	})
}

// An initial handshake remains PeerDisconnected until chain-state validation
// completes. Give each transport the existing stream-open + response budget,
// not just one maintenance tick. This grants no consensus/active-peer status.
func (s *Sync) expireUnvalidatedConnections(pe *peers.Peer, now time.Time) bool {
	// Recheck the snapshot under the handshake lock when available. Never wait
	// on a stalled handshake here: even a held lock must not prevent expiry.
	if pe.HSlock.TryLock() {
		defer pe.HSlock.Unlock()
	}
	if pe.IsConnected() {
		return false
	}
	for _, conn := range s.p2p.Host().Network().ConnsToPeer(pe.GetID()) {
		if !pe.IsBad() && now.Sub(conn.Stat().Opened) < ReqTimeout+RespTimeout {
			continue
		}
		// Close only the expired connection. A newer connection must not renew
		// an old one's deadline, nor be killed because the old one expired.
		if err := conn.Close(); err != nil {
			log.Debug(err.Error())
		}
	}
	return s.p2p.Host().Network().Connectedness(pe.GetID()) != network.Connected
}

func publishPeerFreshnessMetrics(snapshot model.P2PMiningFreshnessSnapshot) {
	common.MiningFreshnessConsensusPeersGauge.Update(int64(snapshot.ConsensusPeerCount))
	common.MiningFreshnessFreshConsensusPeersGauge.Update(int64(snapshot.FreshConsensusPeerCount))
	common.MiningFreshnessStaleConsensusPeersGauge.Update(int64(snapshot.StaleConsensusPeerCount))
	common.MiningFreshnessBestPeerMainOrderGauge.Update(int64(snapshot.BestPeerMainOrder))
	common.MiningFreshnessBestPeerLeadBlocksGauge.Update(snapshot.BestPeerLeadBlocks)
	common.MiningFreshnessBestGraphStateAgeMsGauge.Update(snapshot.BestPeerGraphStateAge.Milliseconds())
	common.MiningFreshnessAvgGraphStateAgeMsGauge.Update(snapshot.AvgGraphStateAge.Milliseconds())
	common.MiningFreshnessMaxGraphStateAgeMsGauge.Update(snapshot.MaxGraphStateAge.Milliseconds())
	common.MiningFreshnessSyncPeerPresentGauge.Update(boolGauge(snapshot.SyncPeerPresent))
	common.MiningFreshnessSyncPeerFreshGauge.Update(boolGauge(snapshot.SyncPeerFresh))
	common.MiningFreshnessSyncPeerGraphStateMsGauge.Update(snapshot.SyncPeerGraphStateAge.Milliseconds())
	common.MiningFreshnessMiningFreshGauge.Update(boolGauge(snapshot.MiningFresh))

	common.PeerFreshnessConnectedConsensusGauge.Update(int64(snapshot.ConsensusPeerCount))
	common.PeerFreshnessStaleConsensusGauge.Update(int64(snapshot.StaleConsensusPeerCount))
	common.PeerFreshnessAvgGraphStateAgeMsGauge.Update(snapshot.AvgGraphStateAge.Milliseconds())
	common.PeerFreshnessMaxGraphStateAgeMsGauge.Update(snapshot.MaxGraphStateAge.Milliseconds())
}

func boolGauge(v bool) int64 {
	if v {
		return 1
	}
	return 0
}

func publishPeerCompatibilityMetrics(s *Sync) {
	if s == nil || s.Peers() == nil {
		return
	}
	counts := peerCompatibilityMetricCountsFromSnapshots(s.Peers().StatsSnapshots())
	common.PeerCompatibilityTotalGauge.Update(int64(counts.Total))
	common.PeerCompatibilityCompatibleGauge.Update(int64(counts.Compatible))
	common.PeerCompatibilityUnknownGauge.Update(int64(counts.Unknown))
	common.PeerCompatibilityStaleGauge.Update(int64(counts.Stale))
	common.PeerCompatibilityWrongForkGauge.Update(int64(counts.WrongFork))
	common.PeerCompatibilityWrongDatasetGauge.Update(int64(counts.WrongDataset))
	common.PeerCompatibilityUnsafeGauge.Update(int64(counts.Unsafe))
}

func publishPeerUsefulnessMetrics(s *Sync) {
	if s == nil || s.Peers() == nil {
		return
	}
	counts := peerUsefulnessMetricCountsFromSnapshots(s.Peers().StatsSnapshots())
	common.PeerUsefulnessTotalGauge.Update(int64(counts.Total))
	common.PeerUsefulnessUsefulForTargetGauge.Update(int64(counts.UsefulForTarget))
	common.PeerUsefulnessSyncMiningPreferredGauge.Update(int64(counts.SyncMiningPreferred))
	common.PeerUsefulnessSyncMiningUsableGauge.Update(int64(counts.SyncMiningUsable))
	common.PeerUsefulnessStaleProofUsableGauge.Update(int64(counts.StaleProofUsable))
	common.PeerUsefulnessDiscoveryRelayOnlyGauge.Update(int64(counts.DiscoveryRelayOnly))
	common.PeerUsefulnessDeprioritizedGauge.Update(int64(counts.Deprioritized))
	common.PeerUsefulnessAvoidGauge.Update(int64(counts.Avoid))
	common.PeerUsefulnessUnknownGauge.Update(int64(counts.Unknown))
}

func peerCompatibilityMetricCountsFromSnapshots(snapshots []*peers.StatsSnap) peerCompatibilityMetricCounts {
	var counts peerCompatibilityMetricCounts
	for _, snapshot := range snapshots {
		if !peerCompatibilityMetricPeerIncluded(snapshot) {
			continue
		}
		counts.Total++
		switch peers.AssessPeerCompatibility(snapshot.Fork, time.Time{}).Compatibility {
		case "compatible":
			counts.Compatible++
		case "stale":
			counts.Stale++
		case "wrong-fork":
			counts.WrongFork++
		case "wrong-dataset":
			counts.WrongDataset++
		case "unsafe":
			counts.Unsafe++
		default:
			counts.Unknown++
		}
	}
	return counts
}

func peerUsefulnessMetricCountsFromSnapshots(snapshots []*peers.StatsSnap) peerUsefulnessMetricCounts {
	return peerUsefulnessMetricCountsFromSnapshotsAt(snapshots, roughtime.Now())
}

func peerUsefulnessMetricCountsFromSnapshotsAt(snapshots []*peers.StatsSnap, now time.Time) peerUsefulnessMetricCounts {
	var counts peerUsefulnessMetricCounts
	for _, snapshot := range snapshots {
		if !peerCompatibilityMetricPeerIncluded(snapshot) {
			continue
		}
		counts.Total++
		assessment := peers.AssessPeerCompatibility(snapshot.Fork, now)
		state := assessment.Usefulness
		if assessment.UsefulForTarget {
			counts.UsefulForTarget++
		}
		if state == "sync-mining-usable" && !assessment.ProofFresh {
			counts.StaleProofUsable++
		}
		switch state {
		case "sync-mining-preferred":
			counts.SyncMiningPreferred++
		case "sync-mining-usable":
			counts.SyncMiningUsable++
		case "discovery-relay-only":
			counts.DiscoveryRelayOnly++
		case "deprioritized":
			counts.Deprioritized++
		case "avoid":
			counts.Avoid++
		default:
			counts.Unknown++
		}
	}
	return counts
}

func peerCompatibilityMetricPeerIncluded(snapshot *peers.StatsSnap) bool {
	return snapshot != nil && snapshot.State && snapshot.IsTheSameNetwork() && peers.HasConsensusService(snapshot.Services)
}

func (s *Sync) reValidatePeer(pe *peers.Peer) error {
	if _, err := s.Send(pe, RPCChainState, s.getChainState()); err != nil {
		return err
	}
	if !pe.IsConsensus() {
		return nil
	}
	// Do not return an error for ping requests.
	metadataSeq := s.p2p.MetadataSeq()
	if _, err := s.Send(pe, RPCPingTopic, &metadataSeq); err != nil {
		log.Debug(fmt.Sprintf("Could not ping peer:%v", err))
	}
	return nil
}
