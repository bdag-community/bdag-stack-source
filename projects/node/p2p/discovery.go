package p2p

import (
	"context"
	"crypto/ecdsa"
	"fmt"
	"net"
	"time"

	"github.com/BlockdagNetworkLabs/bdag/core/json"
	"github.com/BlockdagNetworkLabs/bdag/p2p/common"
	p2ppeers "github.com/BlockdagNetworkLabs/bdag/p2p/peers"
	"github.com/BlockdagNetworkLabs/bdag/p2p/qnode"
	"github.com/BlockdagNetworkLabs/bdag/p2p/qnr"
	"github.com/BlockdagNetworkLabs/bdag/params"
	"github.com/libp2p/go-libp2p"
	dht "github.com/libp2p/go-libp2p-kad-dht"
	"github.com/libp2p/go-libp2p-kad-dht/amino"
	libp2pdiscovery "github.com/libp2p/go-libp2p/core/discovery"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/routing"
	routingdiscovery "github.com/libp2p/go-libp2p/p2p/discovery/routing"
)

const (
	discoveryAdvertiseTTL             = 30 * time.Minute
	discoveryAdvertiseMinDelay        = 5 * time.Minute
	discoveryAdvertiseTimeout         = 20 * time.Second
	discoveryLowPeerInterval          = 30 * time.Second
	discoveryFindInterval             = 2 * time.Minute
	discoveryFindTimeout              = 20 * time.Second
	discoveryFindLimit                = 32
	defaultDiscoveryTargetLivePeers   = 8
	defaultDiscoveryTargetUsefulPeers = 3
)

type discoveryPeerTargetStatus struct {
	LiveConsensusPeers   int
	TargetLivePeers      int
	LivePeerStarved      bool
	UsefulConsensusPeers int
	TargetUsefulPeers    int
	UsefulPeerStarved    bool
}

type discoveryPeerEvidenceStatus struct {
	SourceTotalPeers     int
	SeedPeers            int
	RecentPeers          int
	DiscoveredPeers      int
	PeerstorePeers       int
	InboundPeers         int
	UnknownSourcePeers   int
	CompatibilityTotal   int
	CompatiblePeers      int
	UnknownCompatibility int
	StalePeers           int
	WrongForkPeers       int
	WrongDatasetPeers    int
	UnsafePeers          int
	UsefulnessTotal      int
	UsefulForTargetPeers int
	PreferredPeers       int
	UsablePeers          int
	NonFreshUsablePeers  int
	RelayOnlyPeers       int
	DeprioritizedPeers   int
	AvoidPeers           int
	UnknownUsefulness    int
	PeerEvidence         []*json.PeerDiscoveryPeerEvidence
}

func (status discoveryPeerTargetStatus) Starved() bool {
	return status.LivePeerStarved || status.UsefulPeerStarved
}

func (s *Service) createLocalNode(
	privKey *ecdsa.PrivateKey,
	ipAddr net.IP,
	tcpPort int,
) (*qnode.LocalNode, error) {
	db, err := qnode.OpenDB("")
	if err != nil {
		return nil, fmt.Errorf("could not open node's peer database:%w", err)
	}
	localNode := qnode.NewLocalNode(db, privKey)
	ipEntry := qnr.IP(ipAddr)
	tcpEntry := qnr.TCP(tcpPort)
	localNode.Set(ipEntry)
	localNode.Set(tcpEntry)
	localNode.SetFallbackIP(ipAddr)

	return localNode, nil
}

// filterPeer validates each node that we retrieve from our dht. We
// try to ascertain that the peer can be a valid protocol peer.
// Validity Conditions:
// 1) The local node is still actively looking for peers to
//    connect to.
// 2) Peer has a valid IP and TCP port set in their qnr.
// 3) Peer hasn't been marked as 'bad'
// 4) Peer is not currently active or connected.

func (s *Service) filterPeer(node *qnode.Node) bool {
	if s.staticPeersOnly() {
		return false
	}
	// ignore nodes with no ip address stored.
	if node.IP() == nil {
		return false
	}
	// do not dial nodes with their tcp ports not set
	if err := node.Record().Load(qnr.WithEntry("tcp", new(qnr.TCP))); err != nil {
		if !qnr.IsNotFound(err) {
			log.Debug(fmt.Sprintf("%s Could not retrieve tcp port", err.Error()))
		}
		return false
	}
	peerData, multiAddr, err := convertToAddrInfo(node)
	if err != nil {
		log.Debug(fmt.Sprintf("%s Could not convert to peer data", err.Error()))
		return false
	}
	nodeQNR := node.Record()
	pe := s.Peers().Fetch(peerData.ID)
	pe.SetQNR(nodeQNR)

	if pe.IsActive() {
		return false
	}
	if s.host.Network().Connectedness(peerData.ID) == network.Connected {
		return false
	}
	// Add peer to peer handler.
	s.Peers().Add(nodeQNR, peerData.ID, multiAddr, network.DirUnknown)
	s.recordPeerSource(peerData.ID, peerSourceDiscovered, "accepted DHT/QNR discovery candidate")
	return true
}

// This checks our set max peers in our config, and
// determines whether our currently connected and
// active peers are above our set max peer limit.
func (s *Service) isPeerAtLimit() bool {
	return s.sy.IsPeerAtLimit()
}

func (s *Service) isInboundPeerAtLimit() bool {
	return s.sy.IsInboundPeerAtLimit()
}

func (s *Service) startKademliaDHT() error {
	if s.staticPeersOnly() {
		return nil
	}
	if s.kademliaDHT == nil {
		return fmt.Errorf("no kademlia dht")
	}
	if err := s.kademliaDHT.Bootstrap(s.Context()); err != nil {
		return err
	}
	if s.routingDv == nil {
		s.routingDv = routingdiscovery.NewRoutingDiscovery(s.kademliaDHT)
	}
	return nil
}

// dhtOptions preserves private and isolated networks' existing routing behavior.
// Public networks use the same per-bucket and table limits as libp2p's WAN DHT.
func dhtOptions(h host.Host, networkName string) []dht.Option {
	options := []dht.Option{dht.V1ProtocolOverride(ProtocolDHT()), dht.Mode(dht.ModeServer)}
	if networkName == "mainnet" || networkName == "testnet" {
		options = append(options, dht.RoutingTablePeerDiversityFilter(
			dht.NewRTPeerDiversityFilter(h, amino.DefaultMaxPeersPerIPGroupPerCpl, amino.DefaultMaxPeersPerIPGroup)))
	}
	return options
}

func (s *Service) KademliaDHTOption() libp2p.Option {
	newDHT := func(h host.Host) (routing.PeerRouting, error) {
		var err error
		s.kademliaDHT, err = dht.New(h, dhtOptions(h, params.ActiveNetParams.Name)...)
		return s.kademliaDHT, err
	}
	return libp2p.Routing(newDHT)
}

// listenForNewNodes advertises this node and periodically asks the DHT for
// additional peers. Candidates still pass the normal network handshake and
// synchronization validation before they can become useful peers.
func (s *Service) listenForNewNodes() {
	if s == nil || s.staticPeersOnly() || s.routingDv == nil {
		return
	}
	go s.discoveryLoop()
}

func (s *Service) RefreshQNR() {
	if s == nil || s.staticPeersOnly() {
		return
	}
	s.findDiscoveryPeers()
}

func (s *Service) discoveryLoop() {
	if s == nil || s.Context() == nil {
		return
	}
	nextAdvertise := time.Now()
	findTicker := time.NewTicker(s.discoveryFindInterval())
	defer findTicker.Stop()

	for {
		if !time.Now().Before(nextAdvertise) {
			nextAdvertise = time.Now().Add(s.advertiseDiscovery(s.Context()))
		}
		select {
		case <-s.Context().Done():
			return
		case <-findTicker.C:
			s.findDiscoveryPeers()
			resetDiscoveryTicker(findTicker, s.discoveryFindInterval())
		}
	}
}

func (s *Service) advertiseDiscovery(ctx context.Context) time.Duration {
	if s == nil || s.routingDv == nil {
		return discoveryAdvertiseMinDelay
	}
	base := context.Background()
	if ctx != nil {
		base = ctx
	}
	advertiseCtx, cancel := context.WithTimeout(base, discoveryAdvertiseTimeout)
	defer cancel()

	common.PeerDiscoveryAdvertiseAttemptsCounter.Inc(1)
	ttl, err := s.routingDv.Advertise(advertiseCtx, s.discoveryNamespace(), libp2pdiscovery.TTL(discoveryAdvertiseTTL))
	if err != nil {
		common.PeerDiscoveryAdvertiseFailuresCounter.Inc(1)
		log.Debug(fmt.Sprintf("Could not advertise DHT discovery namespace %s: %v", s.discoveryNamespace(), err))
		return discoveryAdvertiseMinDelay
	}
	common.PeerDiscoveryAdvertiseSuccessesCounter.Inc(1)
	if ttl < discoveryAdvertiseMinDelay {
		return discoveryAdvertiseMinDelay
	}
	return ttl / 2
}

func (s *Service) findDiscoveryPeers() {
	if s == nil || s.staticPeersOnly() || s.routingDv == nil || s.host == nil || s.sy == nil || s.Peers() == nil {
		return
	}
	if s.isPeerAtLimit() {
		return
	}

	base := context.Background()
	if s.Context() != nil {
		base = s.Context()
	}
	ctx, cancel := context.WithTimeout(base, discoveryFindTimeout)
	defer cancel()

	common.PeerDiscoveryFindAttemptsCounter.Inc(1)
	peerCh, err := s.routingDv.FindPeers(ctx, s.discoveryNamespace(), libp2pdiscovery.Limit(discoveryFindLimit))
	if err != nil {
		common.PeerDiscoveryFindFailuresCounter.Inc(1)
		log.Debug(fmt.Sprintf("Could not find DHT discovery peers for namespace %s: %v", s.discoveryNamespace(), err))
		return
	}

	self := s.host.ID()
	for info := range peerCh {
		common.PeerDiscoveryCandidatesCounter.Inc(1)
		if s.isPeerAtLimit() {
			return
		}
		connected := s.host.Network().Connectedness(info.ID) == network.Connected
		candidate, ok, reason := discoveryPeerDialCandidate(info, self, connected, s.Peers().Get(info.ID), false)
		if !ok {
			common.PeerDiscoveryCandidatesSkippedCounter.Inc(1)
			if reason != "" {
				log.Trace(fmt.Sprintf("Skipping DHT discovery peer %s: %s", info.ID.String(), reason))
			}
			continue
		}
		common.PeerDiscoveryCandidatesAcceptedCounter.Inc(1)
		s.recordPeerSource(candidate.ID, peerSourceDiscovered, "found through DHT routing discovery")
		if s.schedulePeerReconnect(candidate, func(ctx context.Context, info peer.AddrInfo) error {
			err := s.connectWithPeerContext(ctx, info, false, false)
			if err != nil {
				log.Trace(fmt.Sprintf("Could not connect with discovered peer %s: %v", info.String(), err))
			}
			return err
		}) {
			common.PeerDiscoveryReconnectScheduledCounter.Inc(1)
		}
	}
}

func (s *Service) discoveryNamespace() string {
	networkName := "unknown"
	if s != nil && s.cfg != nil && s.cfg.Params != nil && s.cfg.Params.Name != "" {
		networkName = s.cfg.Params.Name
	}
	return fmt.Sprintf("%s/discovery/%s", ProtocolDHT(), networkName)
}

func discoveryPeerDialCandidate(info peer.AddrInfo, self peer.ID, alreadyConnected bool, known *p2ppeers.Peer, allowPrivate bool) (peer.AddrInfo, bool, string) {
	if info.ID == "" {
		return peer.AddrInfo{}, false, "missing peer ID"
	}
	if self != "" && info.ID == self {
		return peer.AddrInfo{}, false, "self peer"
	}
	if alreadyConnected {
		return peer.AddrInfo{}, false, "already connected"
	}
	if known != nil {
		if known.IsActive() {
			return peer.AddrInfo{}, false, "known active peer"
		}
		if known.IsBad() {
			return peer.AddrInfo{}, false, "known bad peer"
		}
		if !known.CanConnectWithNetwork() {
			return peer.AddrInfo{}, false, "wrong network"
		}
	}
	filtered, ok := sanitizeAutomaticPeerAddrInfo(info, allowPrivate)
	if !ok {
		return peer.AddrInfo{}, false, "no public dialable addresses"
	}
	return filtered, true, ""
}

func (s *Service) discoveryFindInterval() time.Duration {
	if s == nil || s.sy == nil || s.Peers() == nil {
		return discoveryFindInterval
	}
	status := s.discoveryPeerTargetStatusAt(
		s.Peers().StatsSnapshots(),
		s.discoveryTargetLivePeers(),
		s.discoveryTargetUsefulPeers(),
		time.Now(),
	)
	s.publishDiscoveryTargetMetrics(status)
	return discoveryFindIntervalForTargetStatus(status)
}

func discoveryFindIntervalForTargetStatus(status discoveryPeerTargetStatus) time.Duration {
	if status.Starved() {
		return discoveryLowPeerInterval
	}
	return discoveryFindInterval
}

func (s *Service) discoveryTargetLivePeers() int {
	if s != nil && s.cfg != nil && s.cfg.DiscoveryTargetLivePeers > 0 {
		return s.cfg.DiscoveryTargetLivePeers
	}
	return defaultDiscoveryTargetLivePeers
}

func (s *Service) discoveryTargetUsefulPeers() int {
	if s != nil && s.cfg != nil && s.cfg.DiscoveryTargetUsefulPeers > 0 {
		return s.cfg.DiscoveryTargetUsefulPeers
	}
	return defaultDiscoveryTargetUsefulPeers
}

func (s *Service) discoveryPeerTargetStatusAt(snapshots []*p2ppeers.StatsSnap, liveTarget, usefulTarget int, now time.Time) discoveryPeerTargetStatus {
	status := discoveryPeerTargetStatus{
		TargetLivePeers:   liveTarget,
		TargetUsefulPeers: usefulTarget,
	}
	for _, snapshot := range snapshots {
		if !discoveryPeerSnapshotIncluded(snapshot) {
			continue
		}
		status.LiveConsensusPeers++
		if p2ppeers.AssessPeerCompatibility(snapshot.Fork, now).UsefulForTarget {
			status.UsefulConsensusPeers++
		}
	}
	status.LivePeerStarved = liveTarget > 0 && status.LiveConsensusPeers < liveTarget
	status.UsefulPeerStarved = usefulTarget > 0 && status.UsefulConsensusPeers < usefulTarget
	return status
}

func (s *Service) peerDiscoveryStatusResult() *json.PeerDiscoveryStatusResult {
	staticOnly := s != nil && s.cfg != nil && s.cfg.StaticPeersOnly
	enabled := s != nil && s.cfg != nil && !staticOnly && s.routingDv != nil
	namespace := ""
	if s != nil {
		namespace = s.discoveryNamespace()
	}
	var snapshots []*p2ppeers.StatsSnap
	if s != nil && s.sy != nil && s.Peers() != nil {
		snapshots = s.Peers().StatsSnapshots()
	}
	now := time.Now()
	status := s.discoveryPeerTargetStatusAt(snapshots, s.discoveryTargetLivePeers(), s.discoveryTargetUsefulPeers(), now)
	evidence := s.discoveryPeerEvidenceStatusAt(snapshots, now)
	findInterval := time.Duration(0)
	if enabled {
		findInterval = discoveryFindIntervalForTargetStatus(status)
	}
	return discoveryPeerStatusResult(status, evidence, enabled, staticOnly, namespace, findInterval)
}

func discoveryPeerStatusResult(status discoveryPeerTargetStatus, evidence discoveryPeerEvidenceStatus, enabled, staticOnly bool, namespace string, findInterval time.Duration) *json.PeerDiscoveryStatusResult {
	return &json.PeerDiscoveryStatusResult{
		DiscoveryEnabled:       enabled,
		StaticPeersOnly:        staticOnly,
		Namespace:              namespace,
		LiveConsensusPeers:     status.LiveConsensusPeers,
		TargetLivePeers:        status.TargetLivePeers,
		LivePeerStarved:        status.LivePeerStarved,
		UsefulConsensusPeers:   status.UsefulConsensusPeers,
		TargetUsefulPeers:      status.TargetUsefulPeers,
		UsefulPeerStarved:      status.UsefulPeerStarved,
		PeerStarved:            status.Starved(),
		FindIntervalSeconds:    int64(findInterval / time.Second),
		PeerStarvedReason:      discoveryPeerStarvedReason(status),
		CompatibilityRule:      "fresh-compatible-peers",
		CompatibilityRuleNotes: "useful peers must be live consensus peers with fresh local fork and dataset compatibility evidence; unknown or untimestamped peers do not satisfy the useful-peer target",
		SourceTotalPeers:       evidence.SourceTotalPeers,
		SeedPeers:              evidence.SeedPeers,
		NonSeedPeers:           discoveryNonSeedPeerCount(evidence),
		IsolatedToSeeds:        discoveryIsolatedToSeeds(evidence),
		RecentPeers:            evidence.RecentPeers,
		DiscoveredPeers:        evidence.DiscoveredPeers,
		PeerstorePeers:         evidence.PeerstorePeers,
		InboundPeers:           evidence.InboundPeers,
		UnknownSourcePeers:     evidence.UnknownSourcePeers,
		CompatibilityTotal:     evidence.CompatibilityTotal,
		CompatiblePeers:        evidence.CompatiblePeers,
		UnknownCompatibility:   evidence.UnknownCompatibility,
		StalePeers:             evidence.StalePeers,
		WrongForkPeers:         evidence.WrongForkPeers,
		WrongDatasetPeers:      evidence.WrongDatasetPeers,
		UnsafePeers:            evidence.UnsafePeers,
		UsefulnessTotal:        evidence.UsefulnessTotal,
		UsefulForTargetPeers:   evidence.UsefulForTargetPeers,
		PreferredPeers:         evidence.PreferredPeers,
		UsablePeers:            evidence.UsablePeers,
		NonFreshUsablePeers:    evidence.NonFreshUsablePeers,
		RelayOnlyPeers:         evidence.RelayOnlyPeers,
		DeprioritizedPeers:     evidence.DeprioritizedPeers,
		AvoidPeers:             evidence.AvoidPeers,
		UnknownUsefulness:      evidence.UnknownUsefulness,
		PeerEvidence:           evidence.PeerEvidence,
	}
}

func (s *Service) discoveryPeerEvidenceStatusAt(snapshots []*p2ppeers.StatsSnap, now time.Time) discoveryPeerEvidenceStatus {
	var evidence discoveryPeerEvidenceStatus
	sourceCounts := s.peerSourceMetricCountsFromSnapshots(snapshots)
	evidence.SourceTotalPeers = sourceCounts.Total
	evidence.SeedPeers = sourceCounts.Seed
	evidence.RecentPeers = sourceCounts.Recent
	evidence.DiscoveredPeers = sourceCounts.Discovered
	evidence.PeerstorePeers = sourceCounts.Peerstore
	evidence.InboundPeers = sourceCounts.Inbound
	evidence.UnknownSourcePeers = sourceCounts.Unknown

	for _, snapshot := range snapshots {
		if !discoveryPeerSnapshotIncluded(snapshot) {
			continue
		}
		active := true
		if s != nil && s.sy != nil && s.Peers() != nil {
			active = s.Peers().IsActiveID(snapshot.PeerID)
		}
		source := s.peerSourceForStats(snapshot, active)
		assessment := p2ppeers.AssessPeerCompatibility(snapshot.Fork, now)

		evidence.CompatibilityTotal++
		switch assessment.Compatibility {
		case "compatible":
			evidence.CompatiblePeers++
		case "stale":
			evidence.StalePeers++
		case "wrong-fork":
			evidence.WrongForkPeers++
		case "wrong-dataset":
			evidence.WrongDatasetPeers++
		case "unsafe":
			evidence.UnsafePeers++
		default:
			evidence.UnknownCompatibility++
		}

		evidence.UsefulnessTotal++
		if assessment.UsefulForTarget {
			evidence.UsefulForTargetPeers++
		}
		switch assessment.Usefulness {
		case "sync-mining-preferred":
			evidence.PreferredPeers++
		case "sync-mining-usable":
			evidence.UsablePeers++
			if !assessment.ProofFresh {
				evidence.NonFreshUsablePeers++
			}
		case "discovery-relay-only":
			evidence.RelayOnlyPeers++
		case "deprioritized":
			evidence.DeprioritizedPeers++
		case "avoid":
			evidence.AvoidPeers++
		default:
			evidence.UnknownUsefulness++
		}
		evidence.PeerEvidence = append(evidence.PeerEvidence, discoveryPeerEvidenceFromSnapshot(snapshot, source, assessment))
	}
	return evidence
}

func discoveryPeerEvidenceFromSnapshot(snapshot *p2ppeers.StatsSnap, source peerSourceInfo, assessment p2ppeers.PeerCompatibilityAssessment) *json.PeerDiscoveryPeerEvidence {
	if snapshot == nil {
		return nil
	}
	result := &json.PeerDiscoveryPeerEvidence{
		ID:                      snapshot.PeerID.String(),
		Address:                 snapshot.Address,
		PeerSource:              source.Source,
		PeerSourceHint:          source.Hint,
		SeedPeer:                source.Seed,
		RecentPeer:              source.Recent,
		ForkStatus:              "unknown",
		ForkReason:              "not observed yet",
		PeerCompatibility:       assessment.Compatibility,
		CompatibilityReason:     assessment.CompatibilityReason,
		CompatibilityProofAge:   assessment.ProofAgeSeconds,
		CompatibilityProofFresh: assessment.ProofFresh,
		PeerUsefulness:          assessment.Usefulness,
		PeerUsefulnessReason:    assessment.UsefulnessReason,
		UsefulForTarget:         assessment.UsefulForTarget,
		DatasetStatus:           assessment.DatasetStatus,
		DatasetReason:           assessment.DatasetReason,
		DatasetProof:            assessment.DatasetProof,
	}
	observation := snapshot.Fork
	if observation == nil {
		return result
	}
	result.ForkStatus = string(observation.Status)
	result.ForkReason = observation.Reason
	result.EVMCheckpointNumber = observation.EVMCheckpointNumber
	result.EVMCheckpointHash = hashForJSON(observation.EVMCheckpointHash)
	result.EVMCheckpointRoot = hashForJSON(observation.EVMCheckpointRoot)
	result.SampledBlockOrder = observation.SampledBlockOrder
	result.SampledStateRootGot = hashForJSON(observation.SampledStateRootGot)
	result.SampledStateRootWant = hashForJSON(observation.SampledStateRootWant)
	result.PeerMainOrder = observation.MainOrder
	result.LocalMainOrder = observation.LocalMainOrder
	return result
}

func discoveryPeerSnapshotIncluded(snapshot *p2ppeers.StatsSnap) bool {
	return snapshot != nil &&
		snapshot.State &&
		snapshot.IsTheSameNetwork() &&
		p2ppeers.HasConsensusService(snapshot.Services)
}

func discoveryNonSeedPeerCount(evidence discoveryPeerEvidenceStatus) int {
	if evidence.SourceTotalPeers <= evidence.SeedPeers {
		return 0
	}
	return evidence.SourceTotalPeers - evidence.SeedPeers
}

func discoveryIsolatedToSeeds(evidence discoveryPeerEvidenceStatus) bool {
	return evidence.SourceTotalPeers > 0 && discoveryNonSeedPeerCount(evidence) == 0
}

func discoveryPeerStarvedReason(status discoveryPeerTargetStatus) string {
	switch {
	case status.LivePeerStarved && status.UsefulPeerStarved:
		return "live_and_useful_peer_targets_unmet"
	case status.LivePeerStarved:
		return "live_peer_target_unmet"
	case status.UsefulPeerStarved:
		return "useful_peer_target_unmet"
	default:
		return ""
	}
}

func (s *Service) publishDiscoveryTargetMetrics(status discoveryPeerTargetStatus) {
	common.PeerDiscoveryLiveConsensusGauge.Update(int64(status.LiveConsensusPeers))
	common.PeerDiscoveryTargetLivePeersGauge.Update(int64(status.TargetLivePeers))
	common.PeerDiscoveryLivePeerStarvedGauge.Update(discoveryBoolGauge(status.LivePeerStarved))
	common.PeerDiscoveryUsefulConsensusGauge.Update(int64(status.UsefulConsensusPeers))
	common.PeerDiscoveryTargetUsefulPeersGauge.Update(int64(status.TargetUsefulPeers))
	common.PeerDiscoveryUsefulPeerStarvedGauge.Update(discoveryBoolGauge(status.UsefulPeerStarved))
}

func resetDiscoveryTicker(ticker *time.Ticker, interval time.Duration) {
	if ticker == nil {
		return
	}
	if interval <= 0 {
		interval = discoveryFindInterval
	}
	ticker.Reset(interval)
}

func discoveryBoolGauge(value bool) int64 {
	if value {
		return 1
	}
	return 0
}
