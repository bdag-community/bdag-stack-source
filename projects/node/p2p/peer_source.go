package p2p

import (
	"fmt"
	"strings"

	"github.com/BlockdagNetworkLabs/bdag/core/json"
	"github.com/BlockdagNetworkLabs/bdag/p2p/common"
	p2ppeers "github.com/BlockdagNetworkLabs/bdag/p2p/peers"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
)

const (
	peerSourceBootstrap  = "bootstrap"
	peerSourceStatic     = "static"
	peerSourceRelay      = "relay"
	peerSourceRecent     = "recent"
	peerSourceManual     = "manual"
	peerSourcePeerstore  = "peerstore"
	peerSourceDiscovered = "discovered"
	peerSourceConnect    = "connect"
	peerSourceInbound    = "inbound"
	peerSourceUnknown    = "unknown"
)

type peerSourceInfo struct {
	Source string
	Hint   string
	Seed   bool
	Recent bool
}

type peerSourceMetricCounts struct {
	Total      int
	Seed       int
	NonSeed    int
	SeedOnly   bool
	Recent     int
	Discovered int
	Peerstore  int
	Inbound    int
	Unknown    int
}

func (s *Service) registerPeerSourcesFromAddrs(addrs []string, source, hint string) {
	if s == nil || len(addrs) == 0 {
		return
	}
	infos, err := peerInfosFromStringAddrs(addrs)
	if err != nil {
		log.Debug(fmt.Sprintf("Could not parse peer source addrs source=%s: %v", source, err))
		return
	}
	for _, info := range infos {
		s.recordPeerSource(info.ID, source, hint)
	}
}

func (s *Service) recordPeerSource(id peer.ID, source, hint string) {
	if s == nil || id == "" {
		return
	}
	source = normalizePeerSource(source)
	hint = strings.TrimSpace(hint)
	if source == "" {
		source = peerSourceUnknown
	}

	s.peerSourcesMu.Lock()
	defer s.peerSourcesMu.Unlock()
	if s.peerSources == nil {
		s.peerSources = make(map[peer.ID]peerSourceInfo)
	}
	next := peerSourceInfo{
		Source: source,
		Hint:   hint,
		Seed:   peerSourceIsSeed(source),
		Recent: source == peerSourceRecent,
	}
	current, exists := s.peerSources[id]
	if exists && peerSourcePriority(current.Source) >= peerSourcePriority(next.Source) {
		if current.Hint == "" && next.Hint != "" {
			current.Hint = next.Hint
			s.peerSources[id] = current
		}
		return
	}
	s.peerSources[id] = next
}

func (s *Service) peerSourceForStats(snapshot *p2ppeers.StatsSnap, active bool) peerSourceInfo {
	if snapshot == nil {
		return peerSourceInfo{Source: peerSourceUnknown, Hint: "missing peer snapshot"}
	}
	if s != nil {
		s.peerSourcesMu.RLock()
		source, ok := s.peerSources[snapshot.PeerID]
		s.peerSourcesMu.RUnlock()
		if ok {
			return source
		}
		if s.host != nil && len(s.host.Peerstore().Addrs(snapshot.PeerID)) > 0 {
			return peerSourceInfo{Source: peerSourcePeerstore, Hint: "known from libp2p peerstore"}
		}
	}
	if active && snapshot.Direction == network.DirInbound {
		return peerSourceInfo{Source: peerSourceInbound, Hint: "accepted inbound peer connection"}
	}
	return peerSourceInfo{Source: peerSourceUnknown, Hint: "peer source has not been classified"}
}

func applyPeerSourceInfo(info *json.GetPeerInfoResult, source peerSourceInfo) {
	if info == nil {
		return
	}
	info.PeerSource = source.Source
	info.PeerSourceHint = source.Hint
	info.SeedPeer = source.Seed
	info.RecentPeer = source.Recent
}

func applyPeerSourceForkInfo(info *json.PeerForkInfo, source peerSourceInfo) {
	if info == nil {
		return
	}
	info.PeerSource = source.Source
	info.PeerSourceHint = source.Hint
	info.SeedPeer = source.Seed
	info.RecentPeer = source.Recent
}

func (s *Service) publishPeerSourceMetrics() {
	if s == nil || s.sy == nil || s.Peers() == nil {
		return
	}
	counts := s.peerSourceMetricCountsFromSnapshots(s.Peers().StatsSnapshots())
	common.PeerSourceTotalGauge.Update(int64(counts.Total))
	common.PeerSourceSeedGauge.Update(int64(counts.Seed))
	common.PeerSourceNonSeedGauge.Update(int64(counts.NonSeed))
	common.PeerSourceSeedOnlyGauge.Update(peerSourceBoolGauge(counts.SeedOnly))
	common.PeerSourceRecentGauge.Update(int64(counts.Recent))
	common.PeerSourceDiscoveredGauge.Update(int64(counts.Discovered))
	common.PeerSourcePeerstoreGauge.Update(int64(counts.Peerstore))
	common.PeerSourceInboundGauge.Update(int64(counts.Inbound))
	common.PeerSourceUnknownGauge.Update(int64(counts.Unknown))
}

func (s *Service) peerSourceMetricCountsFromSnapshots(snapshots []*p2ppeers.StatsSnap) peerSourceMetricCounts {
	var counts peerSourceMetricCounts
	for _, snapshot := range snapshots {
		if !peerSourceMetricPeerIncluded(snapshot) {
			continue
		}
		active := true
		if s != nil && s.sy != nil && s.Peers() != nil {
			active = s.Peers().IsActiveID(snapshot.PeerID)
		}
		source := s.peerSourceForStats(snapshot, active).Source
		counts.Total++
		if peerSourceIsSeed(source) {
			counts.Seed++
			continue
		}
		counts.NonSeed++
		switch source {
		case peerSourceRecent:
			counts.Recent++
		case peerSourceDiscovered:
			counts.Discovered++
		case peerSourcePeerstore:
			counts.Peerstore++
		case peerSourceInbound:
			counts.Inbound++
		default:
			counts.Unknown++
		}
	}
	counts.SeedOnly = counts.Total > 0 && counts.NonSeed == 0
	return counts
}

func peerSourceMetricPeerIncluded(snapshot *p2ppeers.StatsSnap) bool {
	return snapshot != nil &&
		snapshot.State &&
		snapshot.IsTheSameNetwork() &&
		p2ppeers.HasConsensusService(snapshot.Services)
}

func peerInfosFromStringAddrs(addrs []string) ([]peer.AddrInfo, error) {
	multiAddrs, err := peersFromStringAddrs(addrs)
	if err != nil {
		return nil, err
	}
	return peer.AddrInfosFromP2pAddrs(multiAddrs...)
}

func normalizePeerSource(source string) string {
	normalized := strings.ToLower(strings.TrimSpace(source))
	normalized = strings.ReplaceAll(normalized, "_", "-")
	normalized = strings.ReplaceAll(normalized, " ", "-")
	for strings.Contains(normalized, "--") {
		normalized = strings.ReplaceAll(normalized, "--", "-")
	}
	switch normalized {
	case "", peerSourceUnknown:
		return peerSourceUnknown
	case peerSourceBootstrap, "bootnode", "bootstrapnode", "bootstrap-node", "bootstrap-peer", "seed", "seed-peer", "seed-node":
		return peerSourceBootstrap
	case peerSourceStatic, "static-peer", "static-node", "addpeer", "add-peer", "configured", "configured-peer":
		return peerSourceStatic
	case peerSourceRelay, "relay-peer", "relay-node":
		return peerSourceRelay
	case peerSourceRecent, "recent-peer", "recent-node", "retained", "retained-peer", "retained-recent", "retained-recent-peer":
		return peerSourceRecent
	case peerSourceManual, "manual-peer", "addpeer-rpc", "rpc-addpeer":
		return peerSourceManual
	case peerSourcePeerstore, "peer-store", "peer-store-peer", "libp2p-peerstore":
		return peerSourcePeerstore
	case peerSourceDiscovered, "discovery", "dht", "dht-discovered", "qnr", "qnr-discovered":
		return peerSourceDiscovered
	case peerSourceConnect, "connected", "outbound", "outbound-connect":
		return peerSourceConnect
	case peerSourceInbound, "incoming", "inbound-connect":
		return peerSourceInbound
	default:
		return normalized
	}
}

func peerSourceIsSeed(source string) bool {
	switch normalizePeerSource(source) {
	case peerSourceBootstrap, peerSourceStatic, peerSourceRelay:
		return true
	default:
		return false
	}
}

func peerSourcePriority(source string) int {
	switch normalizePeerSource(source) {
	case peerSourceBootstrap, peerSourceStatic, peerSourceRelay:
		return 100
	case peerSourceRecent:
		return 80
	case peerSourceManual:
		return 70
	case peerSourcePeerstore, peerSourceDiscovered:
		return 50
	case peerSourceConnect:
		return 40
	case peerSourceInbound:
		return 30
	default:
		return 0
	}
}

func peerSourceBoolGauge(value bool) int64 {
	if value {
		return 1
	}
	return 0
}
