package p2p

import (
	"testing"

	p2ppeers "github.com/BlockdagNetworkLabs/bdag/p2p/peers"
	"github.com/libp2p/go-libp2p/core/peer"
	ma "github.com/multiformats/go-multiaddr"
)

func TestDiscoveryPeerDialCandidateRejectsMissingSelfAndConnected(t *testing.T) {
	id := mustDiscoveryPeerID(t, "16Uiu2HAmVM5wT3dNGGMUeyMnF81JsmQ7ogqsrNBrkgG7beU36J2S")
	public := mustDiscoveryMultiaddr(t, "/ip4/8.8.8.8/tcp/8150")

	if _, ok, reason := discoveryPeerDialCandidate(peer.AddrInfo{}, "", false, nil, false); ok || reason != "missing peer ID" {
		t.Fatalf("missing peer ID candidate = ok %t reason %q", ok, reason)
	}
	if _, ok, reason := discoveryPeerDialCandidate(peer.AddrInfo{ID: id, Addrs: []ma.Multiaddr{public}}, id, false, nil, false); ok || reason != "self peer" {
		t.Fatalf("self candidate = ok %t reason %q", ok, reason)
	}
	if _, ok, reason := discoveryPeerDialCandidate(peer.AddrInfo{ID: id, Addrs: []ma.Multiaddr{public}}, "", true, nil, false); ok || reason != "already connected" {
		t.Fatalf("connected candidate = ok %t reason %q", ok, reason)
	}
}

func TestDiscoveryPeerDialCandidateFiltersPrivateAddresses(t *testing.T) {
	id := mustDiscoveryPeerID(t, "16Uiu2HAmNQznwWPjuAUhXnTkP6EuWsEHa4eRnb1aTm3WcZQKsrFn")
	private := mustDiscoveryMultiaddr(t, "/ip4/192.168.100.10/tcp/8150")
	public := mustDiscoveryMultiaddr(t, "/ip4/8.8.4.4/tcp/8150")

	if _, ok, reason := discoveryPeerDialCandidate(peer.AddrInfo{ID: id, Addrs: []ma.Multiaddr{private}}, "", false, nil, false); ok || reason != "no public dialable addresses" {
		t.Fatalf("private-only candidate = ok %t reason %q", ok, reason)
	}
	filtered, ok, reason := discoveryPeerDialCandidate(peer.AddrInfo{ID: id, Addrs: []ma.Multiaddr{private, public}}, "", false, nil, false)
	if !ok {
		t.Fatalf("mixed-address candidate rejected: %s", reason)
	}
	if len(filtered.Addrs) != 1 || filtered.Addrs[0].String() != public.String() {
		t.Fatalf("filtered addrs = %v, want only %s", filtered.Addrs, public)
	}
	allowed, ok, reason := discoveryPeerDialCandidate(peer.AddrInfo{ID: id, Addrs: []ma.Multiaddr{private}}, "", false, nil, true)
	if !ok {
		t.Fatalf("private candidate rejected when allowPrivate=true: %s", reason)
	}
	if len(allowed.Addrs) != 1 || allowed.Addrs[0].String() != private.String() {
		t.Fatalf("allowed addrs = %v, want %s", allowed.Addrs, private)
	}
}

func TestDiscoveryPeerDialCandidateRejectsKnownActivePeer(t *testing.T) {
	id := mustDiscoveryPeerID(t, "16Uiu2HAmDynYpWjWmgVGf9qVWvDdLnJ3ybVgDmFexizR4zMereus")
	public := mustDiscoveryMultiaddr(t, "/ip4/1.1.1.1/tcp/8150")
	known := p2ppeers.NewPeer(id, nil)
	known.SetConnectionState(p2ppeers.PeerConnected)

	if _, ok, reason := discoveryPeerDialCandidate(peer.AddrInfo{ID: id, Addrs: []ma.Multiaddr{public}}, "", false, known, false); ok || reason != "known active peer" {
		t.Fatalf("known active candidate = ok %t reason %q", ok, reason)
	}
}

func mustDiscoveryPeerID(t *testing.T, raw string) peer.ID {
	t.Helper()
	id, err := peer.Decode(raw)
	if err != nil {
		t.Fatalf("decode peer ID %q: %v", raw, err)
	}
	return id
}

func mustDiscoveryMultiaddr(t *testing.T, raw string) ma.Multiaddr {
	t.Helper()
	addr, err := ma.NewMultiaddr(raw)
	if err != nil {
		t.Fatalf("parse multiaddr %q: %v", raw, err)
	}
	return addr
}
