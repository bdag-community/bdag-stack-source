package common

import (
	"github.com/ethereum/go-ethereum/metrics"
)

const (
	// ingressMeterName is the prefix of the per-packet inbound metrics.
	ingressMeterName = "p2p/ingress/"

	// egressMeterName is the prefix of the per-packet outbound metrics.
	egressMeterName = "p2p/egress/"
)

var (
	IngressConnectMeter = metrics.NewRegisteredMeter("p2p/serves/", nil)
	IngressTrafficMeter = metrics.NewRegisteredMeter(ingressMeterName, nil)
	EgressConnectMeter  = metrics.NewRegisteredMeter("p2p/dials/", nil)
	EgressTrafficMeter  = metrics.NewRegisteredMeter(egressMeterName, nil)
	ActivePeerGauge     = metrics.NewRegisteredGauge("p2p/peers/", nil)

	MiningFreshnessConsensusPeersGauge       = metrics.NewRegisteredGauge("p2p/miningFreshness/consensusPeers", nil)
	MiningFreshnessFreshConsensusPeersGauge  = metrics.NewRegisteredGauge("p2p/miningFreshness/freshConsensusPeers", nil)
	MiningFreshnessStaleConsensusPeersGauge  = metrics.NewRegisteredGauge("p2p/miningFreshness/staleConsensusPeers", nil)
	MiningFreshnessBestPeerMainOrderGauge    = metrics.NewRegisteredGauge("p2p/miningFreshness/bestPeerMainOrder", nil)
	MiningFreshnessBestPeerLeadBlocksGauge   = metrics.NewRegisteredGauge("p2p/miningFreshness/bestPeerLeadBlocks", nil)
	MiningFreshnessBestGraphStateAgeMsGauge  = metrics.NewRegisteredGauge("p2p/miningFreshness/bestGraphStateAgeMs", nil)
	MiningFreshnessAvgGraphStateAgeMsGauge   = metrics.NewRegisteredGauge("p2p/miningFreshness/avgGraphStateAgeMs", nil)
	MiningFreshnessMaxGraphStateAgeMsGauge   = metrics.NewRegisteredGauge("p2p/miningFreshness/maxGraphStateAgeMs", nil)
	MiningFreshnessSyncPeerPresentGauge      = metrics.NewRegisteredGauge("p2p/miningFreshness/syncPeerPresent", nil)
	MiningFreshnessSyncPeerFreshGauge        = metrics.NewRegisteredGauge("p2p/miningFreshness/syncPeerFresh", nil)
	MiningFreshnessSyncPeerGraphStateMsGauge = metrics.NewRegisteredGauge("p2p/miningFreshness/syncPeerGraphStateAgeMs", nil)
	MiningFreshnessMiningFreshGauge          = metrics.NewRegisteredGauge("p2p/miningFreshness/miningFresh", nil)

	PeerFreshnessConnectedConsensusGauge = metrics.NewRegisteredGauge("p2p/freshness/connectedConsensus", nil)
	PeerFreshnessStaleConsensusGauge     = metrics.NewRegisteredGauge("p2p/freshness/staleConsensus", nil)
	PeerFreshnessAvgGraphStateAgeMsGauge = metrics.NewRegisteredGauge("p2p/freshness/avgGraphStateAgeMs", nil)
	PeerFreshnessMaxGraphStateAgeMsGauge = metrics.NewRegisteredGauge("p2p/freshness/maxGraphStateAgeMs", nil)

	PeerCompatibilityTotalGauge        = metrics.NewRegisteredGauge("p2p/peerCompatibility/total", nil)
	PeerCompatibilityCompatibleGauge   = metrics.NewRegisteredGauge("p2p/peerCompatibility/compatible", nil)
	PeerCompatibilityUnknownGauge      = metrics.NewRegisteredGauge("p2p/peerCompatibility/unknown", nil)
	PeerCompatibilityStaleGauge        = metrics.NewRegisteredGauge("p2p/peerCompatibility/stale", nil)
	PeerCompatibilityWrongForkGauge    = metrics.NewRegisteredGauge("p2p/peerCompatibility/wrongFork", nil)
	PeerCompatibilityWrongDatasetGauge = metrics.NewRegisteredGauge("p2p/peerCompatibility/wrongDataset", nil)
	PeerCompatibilityUnsafeGauge       = metrics.NewRegisteredGauge("p2p/peerCompatibility/unsafe", nil)

	PeerUsefulnessTotalGauge               = metrics.NewRegisteredGauge("p2p/peerUsefulness/total", nil)
	PeerUsefulnessUsefulForTargetGauge     = metrics.NewRegisteredGauge("p2p/peerUsefulness/usefulForTarget", nil)
	PeerUsefulnessSyncMiningPreferredGauge = metrics.NewRegisteredGauge("p2p/peerUsefulness/syncMiningPreferred", nil)
	PeerUsefulnessSyncMiningUsableGauge    = metrics.NewRegisteredGauge("p2p/peerUsefulness/syncMiningUsable", nil)
	PeerUsefulnessStaleProofUsableGauge    = metrics.NewRegisteredGauge("p2p/peerUsefulness/staleProofUsable", nil)
	PeerUsefulnessDiscoveryRelayOnlyGauge  = metrics.NewRegisteredGauge("p2p/peerUsefulness/discoveryRelayOnly", nil)
	PeerUsefulnessDeprioritizedGauge       = metrics.NewRegisteredGauge("p2p/peerUsefulness/deprioritized", nil)
	PeerUsefulnessAvoidGauge               = metrics.NewRegisteredGauge("p2p/peerUsefulness/avoid", nil)
	PeerUsefulnessUnknownGauge             = metrics.NewRegisteredGauge("p2p/peerUsefulness/unknown", nil)

	PeerReconnectScheduledCounter     = metrics.NewRegisteredCounter("p2p/peerReconnect/scheduled", nil)
	PeerReconnectAttemptsCounter      = metrics.NewRegisteredCounter("p2p/peerReconnect/attempts", nil)
	PeerReconnectSuccessesCounter     = metrics.NewRegisteredCounter("p2p/peerReconnect/successes", nil)
	PeerReconnectFailuresCounter      = metrics.NewRegisteredCounter("p2p/peerReconnect/failures", nil)
	PeerReconnectBackoffSkipsCounter  = metrics.NewRegisteredCounter("p2p/peerReconnect/skips/backoff", nil)
	PeerReconnectInFlightSkipsCounter = metrics.NewRegisteredCounter("p2p/peerReconnect/skips/inFlight", nil)
	PeerReconnectSlotFullSkipsCounter = metrics.NewRegisteredCounter("p2p/peerReconnect/skips/slotFull", nil)
	PeerReconnectInFlightGauge        = metrics.NewRegisteredGauge("p2p/peerReconnect/inFlight", nil)
	PeerReconnectBackoffGauge         = metrics.NewRegisteredGauge("p2p/peerReconnect/backoffPeers", nil)

	PeerDiscoveryAdvertiseAttemptsCounter  = metrics.NewRegisteredCounter("p2p/peerDiscovery/advertise/attempts", nil)
	PeerDiscoveryAdvertiseSuccessesCounter = metrics.NewRegisteredCounter("p2p/peerDiscovery/advertise/successes", nil)
	PeerDiscoveryAdvertiseFailuresCounter  = metrics.NewRegisteredCounter("p2p/peerDiscovery/advertise/failures", nil)
	PeerDiscoveryFindAttemptsCounter       = metrics.NewRegisteredCounter("p2p/peerDiscovery/find/attempts", nil)
	PeerDiscoveryFindFailuresCounter       = metrics.NewRegisteredCounter("p2p/peerDiscovery/find/failures", nil)
	PeerDiscoveryCandidatesCounter         = metrics.NewRegisteredCounter("p2p/peerDiscovery/candidates/seen", nil)
	PeerDiscoveryCandidatesAcceptedCounter = metrics.NewRegisteredCounter("p2p/peerDiscovery/candidates/accepted", nil)
	PeerDiscoveryCandidatesSkippedCounter  = metrics.NewRegisteredCounter("p2p/peerDiscovery/candidates/skipped", nil)
	PeerDiscoveryReconnectScheduledCounter = metrics.NewRegisteredCounter("p2p/peerDiscovery/reconnect/scheduled", nil)
	PeerDiscoveryLiveConsensusGauge        = metrics.NewRegisteredGauge("p2p/peerDiscovery/liveConsensusPeers", nil)
	PeerDiscoveryTargetLivePeersGauge      = metrics.NewRegisteredGauge("p2p/peerDiscovery/targetLivePeers", nil)
	PeerDiscoveryLivePeerStarvedGauge      = metrics.NewRegisteredGauge("p2p/peerDiscovery/livePeerStarved", nil)
	PeerDiscoveryUsefulConsensusGauge      = metrics.NewRegisteredGauge("p2p/peerDiscovery/usefulConsensusPeers", nil)
	PeerDiscoveryTargetUsefulPeersGauge    = metrics.NewRegisteredGauge("p2p/peerDiscovery/targetUsefulPeers", nil)
	PeerDiscoveryUsefulPeerStarvedGauge    = metrics.NewRegisteredGauge("p2p/peerDiscovery/usefulPeerStarved", nil)

	PeerSourceTotalGauge      = metrics.NewRegisteredGauge("p2p/peerSource/total", nil)
	PeerSourceSeedGauge       = metrics.NewRegisteredGauge("p2p/peerSource/seed", nil)
	PeerSourceNonSeedGauge    = metrics.NewRegisteredGauge("p2p/peerSource/nonSeed", nil)
	PeerSourceSeedOnlyGauge   = metrics.NewRegisteredGauge("p2p/peerSource/isolatedToSeeds", nil)
	PeerSourceRecentGauge     = metrics.NewRegisteredGauge("p2p/peerSource/recent", nil)
	PeerSourceDiscoveredGauge = metrics.NewRegisteredGauge("p2p/peerSource/discovered", nil)
	PeerSourcePeerstoreGauge  = metrics.NewRegisteredGauge("p2p/peerSource/peerstore", nil)
	PeerSourceInboundGauge    = metrics.NewRegisteredGauge("p2p/peerSource/inbound", nil)
	PeerSourceUnknownGauge    = metrics.NewRegisteredGauge("p2p/peerSource/unknown", nil)
)
