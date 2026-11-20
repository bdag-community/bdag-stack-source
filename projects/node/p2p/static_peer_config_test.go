package p2p

import (
	"fmt"
	"strings"
	"testing"

	"github.com/BlockdagNetworkLabs/bdag/config"
	"github.com/BlockdagNetworkLabs/bdag/params"
	"github.com/libp2p/go-libp2p/core/peer"
)

func syntheticPeerAddr(t *testing.T, label, ip string) (string, peer.ID) {
	t.Helper()
	id := syntheticPeerID(t, label)
	return fmt.Sprintf("/ip4/%s/tcp/8150/p2p/%s", ip, id), id
}

func TestConfiguredPeerIDsRequiresIdentityAndDedupes(t *testing.T) {
	firstAddr, firstID := syntheticPeerAddr(t, "configured-peer-one", "192.0.2.10")
	firstAlternateAddr := fmt.Sprintf("/ip4/192.0.2.11/tcp/8150/p2p/%s", firstID)
	secondAddr, secondID := syntheticPeerAddr(t, "configured-peer-two", "192.0.2.12")

	got, err := configuredPeerIDs([]string{firstAddr, firstAddr, firstAlternateAddr, secondAddr})
	if err != nil {
		t.Fatalf("configuredPeerIDs: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("configured peer identity count = %d, want 2", len(got))
	}
	for _, id := range []peer.ID{firstID, secondID} {
		if _, ok := got[id]; !ok {
			t.Fatalf("configured peer identity %s missing", id)
		}
	}
}

func TestConfiguredPeerIDsRejectsInvalidOrIdentitylessAddress(t *testing.T) {
	for _, addr := range []string{
		"not-a-peer-address",
		"qnode://deadbeef",
		"/ip4/192.0.2.20/tcp/8150",
	} {
		t.Run(addr, func(t *testing.T) {
			if _, err := configuredPeerIDs([]string{addr}); err == nil {
				t.Fatalf("configuredPeerIDs(%q) succeeded without a valid peer identity", addr)
			}
		})
	}
}

func TestStaticPeersOnlyIgnoresNetworkDefaultBootstrap(t *testing.T) {
	defaultBootstrap, _ := syntheticPeerAddr(t, "network-default-bootstrap", "192.0.2.30")
	cfg := &config.Config{P2PStaticPeersOnly: true}
	param := &params.Params{Bootstrap: []string{defaultBootstrap}}

	_, err := NewService(cfg, nil, param)
	if err == nil {
		t.Fatal("static-only mode accepted a network default bootstrap as an explicit allowlist")
	}
	if !strings.Contains(err.Error(), "requires at least one explicit addpeer, bootstrapnode, or relaynode /p2p/<peer-id> multiaddress") {
		t.Fatalf("NewService error = %q", err)
	}
}
