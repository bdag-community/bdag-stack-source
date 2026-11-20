package p2p

import (
	"bytes"
	"context"
	"crypto/sha256"
	"strings"
	"testing"
	"time"

	"github.com/BlockdagNetworkLabs/bdag/p2p/common"
	"github.com/libp2p/go-libp2p"
	libp2pcrypto "github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	libp2ptcp "github.com/libp2p/go-libp2p/p2p/transport/tcp"
	ma "github.com/multiformats/go-multiaddr"
)

func syntheticPeerID(t *testing.T, label string) peer.ID {
	t.Helper()
	seed := sha256.Sum256([]byte(label))
	privateKey, _, err := libp2pcrypto.GenerateEd25519Key(bytes.NewReader(seed[:]))
	if err != nil {
		t.Fatalf("generate synthetic peer key for %q: %v", label, err)
	}
	id, err := peer.IDFromPrivateKey(privateKey)
	if err != nil {
		t.Fatalf("derive synthetic peer ID for %q: %v", label, err)
	}
	return id
}

func staticOnlyTestService(t *testing.T) (*Service, peer.ID, peer.ID) {
	t.Helper()
	configured := syntheticPeerID(t, "configured-static-peer")
	unconfigured := syntheticPeerID(t, "unconfigured-static-peer")
	return &Service{
		cfg: &common.Config{StaticPeersOnly: true},
		staticPeerIDs: map[peer.ID]struct{}{
			configured: {},
		},
	}, configured, unconfigured
}

func TestStaticPeersOnlyGatesOutboundAndAuthenticatedPeers(t *testing.T) {
	service, configured, unconfigured := staticOnlyTestService(t)
	addr := ma.StringCast("/ip4/192.0.2.1/tcp/8150")

	if !service.InterceptPeerDial(configured) {
		t.Fatal("configured outbound peer was rejected")
	}
	if service.InterceptPeerDial(unconfigured) {
		t.Fatal("unconfigured outbound peer was accepted")
	}
	if !service.InterceptAddrDial(configured, addr) {
		t.Fatal("configured outbound peer address was rejected")
	}
	if service.InterceptAddrDial(unconfigured, addr) {
		t.Fatal("unconfigured outbound peer address was accepted")
	}
	if !service.InterceptSecured(network.DirInbound, configured, nil) {
		t.Fatal("configured authenticated inbound peer was rejected")
	}
	if service.InterceptSecured(network.DirInbound, unconfigured, nil) {
		t.Fatal("unconfigured authenticated inbound peer was accepted")
	}
}

func TestStaticPeersOnlyRejectsAnEmptyIdentitySet(t *testing.T) {
	service := &Service{cfg: &common.Config{StaticPeersOnly: true}}
	if service.InterceptPeerDial(syntheticPeerID(t, "peer-with-empty-allowlist")) {
		t.Fatal("static-only mode accepted a peer with no configured identities")
	}
}

func TestDynamicPeerModePreservesOrdinaryConnections(t *testing.T) {
	service := &Service{cfg: &common.Config{StaticPeersOnly: false}}
	peerID := syntheticPeerID(t, "dynamic-peer")
	addr := ma.StringCast("/ip4/192.0.2.2/tcp/8150")

	if !service.InterceptPeerDial(peerID) {
		t.Fatal("dynamic outbound peer was rejected")
	}
	if !service.InterceptAddrDial(peerID, addr) {
		t.Fatal("dynamic outbound peer address was rejected")
	}
	if !service.InterceptSecured(network.DirInbound, peerID, nil) {
		t.Fatal("dynamic authenticated inbound peer was rejected")
	}
}

func TestStaticPeersOnlyRejectsManualPeerOutsideConfiguredSet(t *testing.T) {
	service, _, unconfigured := staticOnlyTestService(t)
	addr := "/ip4/192.0.2.3/tcp/8150/p2p/" + unconfigured.String()

	err := service.ConnectToPeer(addr, true)
	if err == nil {
		t.Fatal("manual AddPeer accepted an unconfigured peer")
	}
	if !strings.Contains(err.Error(), "not in the configured static peer set") {
		t.Fatalf("manual AddPeer error = %q", err)
	}
	if err := service.Connect(peer.AddrInfo{ID: unconfigured}); err == nil {
		t.Fatal("direct Connect accepted an unconfigured peer")
	} else if !strings.Contains(err.Error(), "not in the configured static peer set") {
		t.Fatalf("direct Connect error = %q", err)
	}
}

func TestStaticPeersOnlyEnforcesIdentityAtLiveLibp2pBoundary(t *testing.T) {
	tcpOnly := []libp2p.Option{
		libp2p.NoTransports,
		libp2p.Transport(libp2ptcp.NewTCPTransport),
		libp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/0"),
	}
	configuredHost, err := libp2p.New(tcpOnly...)
	if err != nil {
		t.Fatalf("create configured peer: %v", err)
	}
	t.Cleanup(func() { _ = configuredHost.Close() })

	unconfiguredHost, err := libp2p.New(tcpOnly...)
	if err != nil {
		t.Fatalf("create unconfigured peer: %v", err)
	}
	t.Cleanup(func() { _ = unconfiguredHost.Close() })

	service := &Service{
		cfg: &common.Config{StaticPeersOnly: true},
		staticPeerIDs: map[peer.ID]struct{}{
			configuredHost.ID(): {},
		},
	}
	protectedHost, err := libp2p.New(append(tcpOnly, libp2p.ConnectionGater(service))...)
	if err != nil {
		t.Fatalf("create protected peer: %v", err)
	}
	t.Cleanup(func() { _ = protectedHost.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	protectedInfo := peer.AddrInfo{ID: protectedHost.ID(), Addrs: protectedHost.Addrs()}
	if err := configuredHost.Connect(ctx, protectedInfo); err != nil {
		t.Fatalf("configured authenticated inbound peer was rejected: %v", err)
	}
	if err := protectedHost.Network().ClosePeer(configuredHost.ID()); err != nil {
		t.Fatalf("close configured inbound peer: %v", err)
	}

	_ = unconfiguredHost.Connect(ctx, protectedInfo)
	for deadline := time.Now().Add(time.Second); time.Now().Before(deadline); {
		if protectedHost.Network().Connectedness(unconfiguredHost.ID()) == network.NotConnected {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if protectedHost.Network().Connectedness(unconfiguredHost.ID()) != network.NotConnected {
		t.Fatal("unconfigured authenticated inbound peer remained connected across the live libp2p boundary")
	}

	configuredInfo := peer.AddrInfo{ID: configuredHost.ID(), Addrs: configuredHost.Addrs()}
	if err := protectedHost.Connect(ctx, configuredInfo); err != nil {
		t.Fatalf("configured outbound peer was rejected: %v", err)
	}
	if err := protectedHost.Network().ClosePeer(configuredHost.ID()); err != nil {
		t.Fatalf("close configured outbound peer: %v", err)
	}

	unconfiguredInfo := peer.AddrInfo{ID: unconfiguredHost.ID(), Addrs: unconfiguredHost.Addrs()}
	if err := protectedHost.Connect(ctx, unconfiguredInfo); err == nil {
		t.Fatal("unconfigured outbound peer crossed the live libp2p boundary")
	}
}
