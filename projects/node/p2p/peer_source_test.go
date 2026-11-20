package p2p

import (
	"testing"

	"github.com/BlockdagNetworkLabs/bdag/core/protocol"
	p2ppeers "github.com/BlockdagNetworkLabs/bdag/p2p/peers"
	"github.com/BlockdagNetworkLabs/bdag/params"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
)

func TestPeerSourcePriorityPreservesSeedOverGenericConnect(t *testing.T) {
	id, err := peer.Decode("16Uiu2HAm9UcTayJDSajjJYsWwVaN2qqGeczcs9kXse3dMdvGDRjz")
	if err != nil {
		t.Fatal(err)
	}
	s := &Service{}
	s.recordPeerSource(id, peerSourceBootstrap, "configured bootstrap seed")
	s.recordPeerSource(id, peerSourceConnect, "successful outbound connect")

	source := s.peerSourceForStats(&p2ppeers.StatsSnap{PeerID: id}, false)
	if source.Source != peerSourceBootstrap || !source.Seed || source.Recent {
		t.Fatalf("source = %#v, want bootstrap seed", source)
	}
}

func TestRegisterPeerSourcesFromAddrsMarksRecentPeer(t *testing.T) {
	id, err := peer.Decode("16Uiu2HAm9UcTayJDSajjJYsWwVaN2qqGeczcs9kXse3dMdvGDRjz")
	if err != nil {
		t.Fatal(err)
	}
	s := &Service{}
	s.registerPeerSourcesFromAddrs([]string{testRecentPeerTwo}, peerSourceRecent, "retained recent peer cache")

	source := s.peerSourceForStats(&p2ppeers.StatsSnap{PeerID: id}, false)
	if source.Source != peerSourceRecent || !source.Recent || source.Seed {
		t.Fatalf("source = %#v, want recent retained peer", source)
	}
}

func TestPeerSourcePriorityPreservesRecentOverGenericConnect(t *testing.T) {
	id, err := peer.Decode("16Uiu2HAm9UcTayJDSajjJYsWwVaN2qqGeczcs9kXse3dMdvGDRjz")
	if err != nil {
		t.Fatal(err)
	}
	s := &Service{}
	s.recordPeerSource(id, peerSourceRecent, "retained recent peer cache")
	s.recordPeerSource(id, peerSourceConnect, "successful outbound connect")

	source := s.peerSourceForStats(&p2ppeers.StatsSnap{PeerID: id}, false)
	if source.Source != peerSourceRecent || !source.Recent || source.Seed {
		t.Fatalf("source = %#v, want retained recent peer", source)
	}
}

func TestPeerSourcePriorityUpgradesDiscoveredToRecent(t *testing.T) {
	id, err := peer.Decode("16Uiu2HAm9UcTayJDSajjJYsWwVaN2qqGeczcs9kXse3dMdvGDRjz")
	if err != nil {
		t.Fatal(err)
	}
	s := &Service{}
	s.recordPeerSource(id, peerSourceDiscovered, "accepted DHT/QNR discovery candidate")
	s.recordPeerSource(id, peerSourceRecent, "retained recent peer cache")

	source := s.peerSourceForStats(&p2ppeers.StatsSnap{PeerID: id}, false)
	if source.Source != peerSourceRecent || !source.Recent || source.Seed {
		t.Fatalf("source = %#v, want retained recent peer after restart", source)
	}
}

func TestPeerSourcePriorityUpgradesConnectToPeerstore(t *testing.T) {
	id := peer.ID("peerstore-reconnect-peer")
	s := &Service{}
	s.recordPeerSource(id, peerSourceConnect, "successful outbound connect")
	s.recordPeerSource(id, peerSourcePeerstore, "reconnected from libp2p peerstore")

	source := s.peerSourceForStats(&p2ppeers.StatsSnap{PeerID: id}, false)
	if source.Source != peerSourcePeerstore || source.Seed || source.Recent {
		t.Fatalf("source = %#v, want peerstore reconnect", source)
	}
}

func TestNormalizePeerSourceCanonicalizesAliases(t *testing.T) {
	tests := map[string]string{
		"BOOTSTRAP_PEER":        peerSourceBootstrap,
		"bootstrap-node":        peerSourceBootstrap,
		"seed":                  peerSourceBootstrap,
		"static_peer":           peerSourceStatic,
		"add-peer":              peerSourceStatic,
		"relay_node":            peerSourceRelay,
		"recent_peer":           peerSourceRecent,
		"retained recent peer":  peerSourceRecent,
		"addpeer_rpc":           peerSourceManual,
		"peer_store":            peerSourcePeerstore,
		"libp2p-peerstore":      peerSourcePeerstore,
		"dht_discovered":        peerSourceDiscovered,
		"qnr":                   peerSourceDiscovered,
		"outbound_connect":      peerSourceConnect,
		"incoming":              peerSourceInbound,
		"  custom_source_name ": "custom-source-name",
	}

	for input, want := range tests {
		if got := normalizePeerSource(input); got != want {
			t.Fatalf("normalizePeerSource(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestPeerSourceMetricCountsCanonicalizesAliases(t *testing.T) {
	seedA := peer.ID("seed-alias-peer-a")
	seedB := peer.ID("seed-alias-peer-b")
	seedC := peer.ID("seed-alias-peer-c")
	recent := peer.ID("recent-alias-peer")
	discovered := peer.ID("discovered-alias-peer")
	peerstore := peer.ID("peerstore-alias-peer")

	s := &Service{}
	s.recordPeerSource(seedA, "bootstrap_peer", "configured bootstrap seed")
	s.recordPeerSource(seedB, "static-peer", "configured addpeer seed")
	s.recordPeerSource(seedC, "relay node", "configured relay seed")
	s.recordPeerSource(recent, "retained_recent_peer", "retained recent peer cache")
	s.recordPeerSource(discovered, "dht_discovered", "accepted DHT/QNR discovery candidate")
	s.recordPeerSource(peerstore, "peer_store", "known from libp2p peerstore")

	counts := s.peerSourceMetricCountsFromSnapshots([]*p2ppeers.StatsSnap{
		testPeerSourceMetricSnapshot(seedA, network.DirOutbound, true),
		testPeerSourceMetricSnapshot(seedB, network.DirOutbound, true),
		testPeerSourceMetricSnapshot(seedC, network.DirOutbound, true),
		testPeerSourceMetricSnapshot(recent, network.DirOutbound, true),
		testPeerSourceMetricSnapshot(discovered, network.DirOutbound, true),
		testPeerSourceMetricSnapshot(peerstore, network.DirOutbound, true),
	})

	if counts.Total != 6 || counts.Seed != 3 || counts.NonSeed != 3 || counts.SeedOnly || counts.Recent != 1 || counts.Discovered != 1 || counts.Peerstore != 1 {
		t.Fatalf("counts = %+v, want aliased sources in canonical buckets", counts)
	}
}

func TestPeerForkInfoIncludesPeerSource(t *testing.T) {
	id := peer.ID("source-peer")
	s := &Service{}
	s.recordPeerSource(id, peerSourceDiscovered, "accepted DHT/QNR discovery candidate")

	info := peerForkInfoFromStats(s, &p2ppeers.StatsSnap{PeerID: id}, true, "", false)
	if info.PeerSource != peerSourceDiscovered || info.PeerSourceHint == "" {
		t.Fatalf("peer fork source = (%q, %q), want discovered with hint", info.PeerSource, info.PeerSourceHint)
	}
}

func TestPeerSourceFallsBackToInbound(t *testing.T) {
	source := (&Service{}).peerSourceForStats(&p2ppeers.StatsSnap{
		PeerID:    peer.ID("inbound-peer"),
		Direction: network.DirInbound,
	}, true)
	if source.Source != peerSourceInbound {
		t.Fatalf("source = %#v, want inbound fallback", source)
	}
}

func TestPeerSourceMetricCountsFromSnapshots(t *testing.T) {
	seed := peer.ID("seed-peer")
	recent := peer.ID("recent-peer")
	discovered := peer.ID("discovered-peer")
	inbound := peer.ID("inbound-peer")
	unknown := peer.ID("unknown-peer")
	inactive := peer.ID("inactive-peer")

	s := &Service{}
	s.recordPeerSource(seed, peerSourceStatic, "configured addpeer seed")
	s.recordPeerSource(recent, peerSourceRecent, "retained recent peer cache")
	s.recordPeerSource(discovered, peerSourceDiscovered, "accepted DHT/QNR discovery candidate")

	counts := s.peerSourceMetricCountsFromSnapshots([]*p2ppeers.StatsSnap{
		testPeerSourceMetricSnapshot(seed, network.DirOutbound, true),
		testPeerSourceMetricSnapshot(recent, network.DirOutbound, true),
		testPeerSourceMetricSnapshot(discovered, network.DirOutbound, true),
		testPeerSourceMetricSnapshot(inbound, network.DirInbound, true),
		testPeerSourceMetricSnapshot(unknown, network.DirOutbound, true),
		testPeerSourceMetricSnapshot(inactive, network.DirOutbound, false),
		{PeerID: peer.ID("wrong-network"), State: true, Network: "wrongnet", Services: protocol.Full},
	})

	if counts.Total != 5 || counts.Seed != 1 || counts.NonSeed != 4 || counts.SeedOnly || counts.Recent != 1 || counts.Discovered != 1 || counts.Inbound != 1 || counts.Unknown != 1 {
		t.Fatalf("counts = %+v, want one peer in each live source bucket", counts)
	}
}

func TestPeerSourceMetricCountsFlagsSeedOnlyIsolation(t *testing.T) {
	seedA := peer.ID("seed-peer-a")
	seedB := peer.ID("seed-peer-b")
	inactiveRecent := peer.ID("inactive-recent-peer")

	s := &Service{}
	s.recordPeerSource(seedA, peerSourceStatic, "configured addpeer seed")
	s.recordPeerSource(seedB, peerSourceBootstrap, "configured bootstrap seed")
	s.recordPeerSource(inactiveRecent, peerSourceRecent, "retained recent peer cache")

	counts := s.peerSourceMetricCountsFromSnapshots([]*p2ppeers.StatsSnap{
		testPeerSourceMetricSnapshot(seedA, network.DirOutbound, true),
		testPeerSourceMetricSnapshot(seedB, network.DirOutbound, true),
		testPeerSourceMetricSnapshot(inactiveRecent, network.DirOutbound, false),
	})

	if counts.Total != 2 || counts.Seed != 2 || counts.NonSeed != 0 || !counts.SeedOnly {
		t.Fatalf("counts = %+v, want two live seed peers and seed-only isolation", counts)
	}
	if peerSourceBoolGauge(counts.SeedOnly) != 1 {
		t.Fatalf("seed-only gauge = %d, want 1", peerSourceBoolGauge(counts.SeedOnly))
	}
}

func testPeerSourceMetricSnapshot(id peer.ID, direction network.Direction, state bool) *p2ppeers.StatsSnap {
	return &p2ppeers.StatsSnap{
		PeerID:    id,
		State:     state,
		Network:   params.ActiveNetParams.Name,
		Services:  protocol.Full,
		Direction: direction,
	}
}
