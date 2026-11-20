package p2p

import (
	"crypto/rand"
	"fmt"
	"testing"

	dht "github.com/libp2p/go-libp2p-kad-dht"
	kb "github.com/libp2p/go-libp2p-kbucket"
	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/peer"
	mocknet "github.com/libp2p/go-libp2p/p2p/net/mock"
	ma "github.com/multiformats/go-multiaddr"
)

func TestDHTDiversityNetworkScopeAndConnectedPeers(t *testing.T) {
	for _, tc := range []struct {
		name     string
		filtered bool
	}{
		{"mainnet", true}, {"testnet", true}, {"mainnet-isolated", false},
		{"privnet", false}, {"mixnet", false}, {"devnet", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mn := mocknet.New()
			defer mn.Close()
			observer, err := mn.GenPeer()
			if err != nil {
				t.Fatal(err)
			}
			options := append(dhtOptions(observer, tc.name), dht.DisableAutoRefresh())
			node, err := dht.New(observer, options...)
			if err != nil {
				t.Fatal(err)
			}
			defer node.Close()
			// Connect all peers before attempting admission: the libp2p filter
			// deliberately groups their actual connection addresses.
			hosts := make([]host.Host, 0, 4)
			for i := 0; i < 4; i++ {
				var key crypto.PrivKey
				for {
					key, _, err = crypto.GenerateEd25519Key(rand.Reader)
					if err != nil {
						t.Fatal(err)
					}
					id, e := peer.IDFromPrivateKey(key)
					if e != nil {
						t.Fatal(e)
					}
					if kb.CommonPrefixLen(kb.ConvertPeerID(observer.ID()), kb.ConvertPeerID(id)) == 0 {
						break
					}
				}
				ip := fmt.Sprintf("91.20.30.%d", i+1)
				if i == 3 {
					ip = "92.20.30.1"
				}
				h, err := mn.AddPeer(key, ma.StringCast("/ip4/"+ip+"/tcp/4001"))
				if err != nil {
					t.Fatal(err)
				}
				if _, err = mn.LinkPeers(observer.ID(), h.ID()); err != nil {
					t.Fatal(err)
				}
				if _, err = mn.ConnectPeers(observer.ID(), h.ID()); err != nil {
					t.Fatal(err)
				}
				hosts = append(hosts, h)
			}
			for i, h := range hosts {
				added, err := node.RoutingTable().TryAddPeer(h.ID(), true, false)
				want := !(tc.filtered && i == 2)
				if added != want || (want && err != nil) || (!want && err == nil) {
					t.Errorf("peer %d: added=%v err=%v, want added=%v", i, added, err, want)
				}
			}
		})
	}
}
