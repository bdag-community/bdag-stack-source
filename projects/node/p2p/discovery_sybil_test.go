package p2p

import (
	"context"
	"crypto/rand"
	"fmt"
	"testing"
	"time"

	dht "github.com/libp2p/go-libp2p-kad-dht"
	kb "github.com/libp2p/go-libp2p-kbucket"
	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/peer"
	routingdiscovery "github.com/libp2p/go-libp2p/p2p/discovery/routing"
	mocknet "github.com/libp2p/go-libp2p/p2p/net/mock"
	ma "github.com/multiformats/go-multiaddr"
)

// The four adversarial identities share one IP group and a routing-table CPL;
// an independent relay connects the seeker to the provider. This checks a
// specific alternate-path topology, not a general censorship-resistance proof.
func TestDHTDiversitySybilAlternatePath(t *testing.T) {
	for _, filtered := range []bool{false, true} {
		t.Run(fmt.Sprintf("filtered=%v", filtered), func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			mn := mocknet.New()
			defer mn.Close()
			seeker, err := mn.GenPeer()
			if err != nil {
				t.Fatal(err)
			}
			create := func(ip string, sameBucket bool) host.Host {
				for {
					key, _, e := crypto.GenerateEd25519Key(rand.Reader)
					if e != nil {
						t.Fatal(e)
					}
					id, e := peer.IDFromPrivateKey(key)
					if e != nil {
						t.Fatal(e)
					}
					if sameBucket && kb.CommonPrefixLen(kb.ConvertPeerID(seeker.ID()), kb.ConvertPeerID(id)) != 0 {
						continue
					}
					h, e := mn.AddPeer(key, ma.StringCast("/ip4/"+ip+"/tcp/4001"))
					if e != nil {
						t.Fatal(e)
					}
					return h
				}
			}
			relay := create("92.20.30.1", true)
			provider := create("93.20.30.1", false)
			attackers := make([]host.Host, 4)
			for i := range attackers {
				attackers[i] = create(fmt.Sprintf("91.20.30.%d", i+1), true)
			}
			all := append([]host.Host{seeker, relay, provider}, attackers...)
			nodes := make(map[peer.ID]*dht.IpfsDHT)
			for _, h := range all {
				opts := []dht.Option{dht.V1ProtocolOverride(ProtocolDHT()), dht.Mode(dht.ModeServer), dht.DisableAutoRefresh(),
					dht.QueryFilter(func(any, peer.AddrInfo) bool { return true }),
					dht.RoutingTableFilter(func(any, peer.ID) bool { return true })}
				if h.ID() == seeker.ID() && filtered {
					opts = append(dhtOptions(h, "mainnet"), opts[2:]...)
				}
				n, e := dht.New(h, opts...)
				if e != nil {
					t.Fatal(e)
				}
				nodes[h.ID()] = n
				defer n.Close()
			}
			connect := func(a, b host.Host) {
				if _, e := mn.LinkPeers(a.ID(), b.ID()); e != nil {
					t.Fatal(e)
				}
				if _, e := mn.ConnectPeers(a.ID(), b.ID()); e != nil {
					t.Fatal(e)
				}
				// Add connected peers explicitly so the test does not depend on
				// asynchronous identify scheduling. Existing entries are fine.
				for _, pair := range [][2]host.Host{{a, b}, {b, a}} {
					_, _ = nodes[pair[0].ID()].RoutingTable().TryAddPeer(pair[1].ID(), true, false)
				}
			}
			connect(provider, relay)
			for _, attacker := range attackers {
				connect(seeker, attacker)
			}
			connect(seeker, relay)
			count := 0
			for _, a := range attackers {
				if nodes[seeker.ID()].RoutingTable().Find(a.ID()) != "" {
					count++
				}
			}
			if (!filtered && count != 4) || (filtered && count > 2) {
				t.Fatalf("attacker identities admitted=%d filtered=%v", count, filtered)
			}
			if nodes[seeker.ID()].RoutingTable().Find(relay.ID()) == "" {
				t.Fatal("honest independent relay absent from routing table")
			}
			const namespace = "/bdag/test/dht-diversity-sybil"
			if _, err := routingdiscovery.NewRoutingDiscovery(nodes[provider.ID()]).Advertise(ctx, namespace); err != nil {
				t.Fatal(err)
			}
			peers, err := routingdiscovery.NewRoutingDiscovery(nodes[seeker.ID()]).FindPeers(ctx, namespace)
			if err != nil {
				t.Fatal(err)
			}
			found := false
			for info := range peers {
				if info.ID == provider.ID() {
					found = true
				}
			}
			if !found {
				t.Fatalf("provider not found through honest alternate path: %v", ctx.Err())
			}
			t.Logf("attacker identities admitted=%d; provider discovered via alternate path", count)
		})
	}
}
