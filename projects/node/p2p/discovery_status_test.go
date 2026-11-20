package p2p

import (
	"testing"
	"time"

	"github.com/BlockdagNetworkLabs/bdag/core/json"
	"github.com/BlockdagNetworkLabs/bdag/core/protocol"
	"github.com/BlockdagNetworkLabs/bdag/p2p/common"
	p2ppeers "github.com/BlockdagNetworkLabs/bdag/p2p/peers"
	"github.com/BlockdagNetworkLabs/bdag/params"
	"github.com/libp2p/go-libp2p/core/peer"
)

func TestDiscoveryPeerStatusCountsOnlyFreshCompatiblePeersAsUseful(t *testing.T) {
	now := time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)
	fresh := discoveryStatusCompatibleSnapshot(peer.ID("fresh-compatible"), now.Add(-time.Minute))
	untimestamped := discoveryStatusCompatibleSnapshot(peer.ID("untimestamped-compatible"), time.Time{})
	wrongDataset := discoveryStatusCompatibleSnapshot(peer.ID("wrong-dataset"), now.Add(-time.Minute))
	wrongDataset.Fork.CommitmentStatus = "checkpoint_mismatch"
	unknown := discoveryStatusTestSnapshot(peer.ID("unknown"), true, params.ActiveNetParams.Name, protocol.Full)

	snapshots := []*p2ppeers.StatsSnap{
		fresh,
		untimestamped,
		wrongDataset,
		unknown,
		discoveryStatusTestSnapshot(peer.ID("wrong-network"), true, "wrongnet", protocol.Full),
		discoveryStatusTestSnapshot(peer.ID("inactive"), false, params.ActiveNetParams.Name, protocol.Full),
		discoveryStatusTestSnapshot(peer.ID("no-consensus"), true, params.ActiveNetParams.Name, 0),
		nil,
	}

	service := &Service{}
	status := service.discoveryPeerTargetStatusAt(snapshots, 4, 2, now)
	if status.LiveConsensusPeers != 4 || status.LivePeerStarved {
		t.Fatalf("live status = %+v, want four live peers and satisfied live target", status)
	}
	if status.UsefulConsensusPeers != 1 || !status.UsefulPeerStarved || !status.Starved() {
		t.Fatalf("useful status = %+v, want only fresh compatible peer to satisfy useful target", status)
	}

	evidence := service.discoveryPeerEvidenceStatusAt(snapshots, now)
	if evidence.SourceTotalPeers != 4 || evidence.UnknownSourcePeers != 4 {
		t.Fatalf("source evidence = %+v, want four included unknown-source peers", evidence)
	}
	if evidence.CompatibilityTotal != 4 || evidence.CompatiblePeers != 2 ||
		evidence.WrongDatasetPeers != 1 || evidence.UnknownCompatibility != 1 {
		t.Fatalf("compatibility evidence = %+v, want compatible=2 wrong-dataset=1 unknown=1", evidence)
	}
	if evidence.UsefulnessTotal != 4 || evidence.UsefulForTargetPeers != 1 ||
		evidence.PreferredPeers != 1 || evidence.UsablePeers != 1 ||
		evidence.RelayOnlyPeers != 1 || evidence.AvoidPeers != 1 {
		t.Fatalf("usefulness evidence = %+v, want only fresh compatible peer useful", evidence)
	}

	result := discoveryPeerStatusResult(status, evidence, true, false, "/bdag/discovery/devnet", discoveryLowPeerInterval)
	if !result.DiscoveryEnabled || result.StaticPeersOnly || result.Namespace != "/bdag/discovery/devnet" {
		t.Fatalf("discovery flags = %+v, want enabled dynamic devnet discovery", result)
	}
	if !result.PeerStarved || result.PeerStarvedReason != "useful_peer_target_unmet" {
		t.Fatalf("starvation result = %+v, want useful-peer target unmet", result)
	}
	if result.FindIntervalSeconds != int64(discoveryLowPeerInterval/time.Second) {
		t.Fatalf("find interval = %d, want %d", result.FindIntervalSeconds, int64(discoveryLowPeerInterval/time.Second))
	}
	if len(result.PeerEvidence) != 4 {
		t.Fatalf("peer evidence rows = %d, want four", len(result.PeerEvidence))
	}
	byID := discoveryStatusEvidenceByID(result.PeerEvidence)
	if got := byID[fresh.PeerID.String()]; got == nil || !got.UsefulForTarget || !got.CompatibilityProofFresh {
		t.Fatalf("fresh evidence = %+v, want fresh useful peer", got)
	}
	if got := byID[untimestamped.PeerID.String()]; got == nil || got.UsefulForTarget || got.CompatibilityProofFresh {
		t.Fatalf("untimestamped evidence = %+v, want visible but not useful peer", got)
	}
}

func TestDiscoveryFindIntervalTracksConfiguredPeerTargets(t *testing.T) {
	service := &Service{cfg: &common.Config{
		DiscoveryTargetLivePeers:   5,
		DiscoveryTargetUsefulPeers: 2,
	}}
	if service.discoveryTargetLivePeers() != 5 || service.discoveryTargetUsefulPeers() != 2 {
		t.Fatalf("configured targets = (%d,%d), want (5,2)", service.discoveryTargetLivePeers(), service.discoveryTargetUsefulPeers())
	}

	starved := discoveryPeerTargetStatus{UsefulPeerStarved: true}
	if got := discoveryFindIntervalForTargetStatus(starved); got != discoveryLowPeerInterval {
		t.Fatalf("starved interval = %s, want %s", got, discoveryLowPeerInterval)
	}
	if got := discoveryFindIntervalForTargetStatus(discoveryPeerTargetStatus{}); got != discoveryFindInterval {
		t.Fatalf("healthy interval = %s, want %s", got, discoveryFindInterval)
	}

	service.cfg.DiscoveryTargetLivePeers = 0
	service.cfg.DiscoveryTargetUsefulPeers = -1
	if service.discoveryTargetLivePeers() != defaultDiscoveryTargetLivePeers ||
		service.discoveryTargetUsefulPeers() != defaultDiscoveryTargetUsefulPeers {
		t.Fatalf("default targets = (%d,%d), want (%d,%d)",
			service.discoveryTargetLivePeers(), service.discoveryTargetUsefulPeers(),
			defaultDiscoveryTargetLivePeers, defaultDiscoveryTargetUsefulPeers)
	}
}

func TestGetPeerDiscoveryStatusReportsDisabledModesWithoutPanicking(t *testing.T) {
	result, err := NewPublicP2PAPI(nil).GetPeerDiscoveryStatus()
	if err != nil {
		t.Fatalf("nil-service status: %v", err)
	}
	if result.DiscoveryEnabled || result.StaticPeersOnly || result.FindIntervalSeconds != 0 {
		t.Fatalf("nil-service status = %+v, want disabled non-static status", result)
	}
	if result.TargetLivePeers != defaultDiscoveryTargetLivePeers ||
		result.TargetUsefulPeers != defaultDiscoveryTargetUsefulPeers ||
		!result.LivePeerStarved || !result.UsefulPeerStarved {
		t.Fatalf("nil-service targets = %+v, want explicit unmet defaults", result)
	}

	staticService := &Service{cfg: &common.Config{StaticPeersOnly: true}}
	result, err = NewPublicP2PAPI(staticService).GetPeerDiscoveryStatus()
	if err != nil {
		t.Fatalf("static-service status: %v", err)
	}
	if result.DiscoveryEnabled || !result.StaticPeersOnly || result.FindIntervalSeconds != 0 {
		t.Fatalf("static-service status = %+v, want disabled static-only status", result)
	}
}

func discoveryStatusCompatibleSnapshot(id peer.ID, checkedAt time.Time) *p2ppeers.StatsSnap {
	snapshot := discoveryStatusTestSnapshot(id, true, params.ActiveNetParams.Name, protocol.Full)
	snapshot.Fork = &p2ppeers.PeerForkObservation{
		Status:              p2ppeers.PeerForkStatusSameMainChain,
		SameNativeForkKnown: true,
		SameNativeFork:      true,
		CommitmentStatus:    "checkpoint_valid",
		LastChecked:         checkedAt,
	}
	return snapshot
}

func discoveryStatusTestSnapshot(id peer.ID, state bool, network string, services protocol.ServiceFlag) *p2ppeers.StatsSnap {
	return &p2ppeers.StatsSnap{
		PeerID:   id,
		State:    state,
		Network:  network,
		Services: services,
	}
}

func discoveryStatusEvidenceByID(rows []*json.PeerDiscoveryPeerEvidence) map[string]*json.PeerDiscoveryPeerEvidence {
	result := make(map[string]*json.PeerDiscoveryPeerEvidence, len(rows))
	for _, row := range rows {
		if row != nil {
			result[row.ID] = row
		}
	}
	return result
}
