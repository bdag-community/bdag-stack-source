package p2p

import (
	"testing"
	"time"

	"github.com/BlockdagNetworkLabs/bdag/core/protocol"
	"github.com/BlockdagNetworkLabs/bdag/p2p/peers"
	pb "github.com/BlockdagNetworkLabs/bdag/p2p/proto/v1"
	"github.com/libp2p/go-libp2p/core/peer"
)

func TestBlockRelayScoreOrdersPeersWithinFreshnessTier(t *testing.T) {
	fastID := peer.ID("fast")
	flakyID := peer.ID("flaky")
	fast := newBlockRelayScoreTestPeer(fastID, 100)
	flaky := newBlockRelayScoreTestPeer(flakyID, 100)
	now := time.Now()
	candidates := []*peers.Peer{flaky, fast}
	freshness := peers.SortMiningRelayPeers(candidates, now, 3*time.Second, 10, 100)

	tracker := newBlockRelayScoreTracker()
	tracker.recordSuccess(fastID, 10*time.Millisecond, now.Add(-time.Second))
	tracker.recordFailure(flakyID, 300*time.Millisecond, now.Add(-time.Second))
	tracker.sortBroadcastPeers(candidates, freshness.Scores, now)

	if got := candidates[0].GetID(); got != fastID {
		t.Fatalf("first peer = %s, want fast peer", got)
	}
}

func TestBlockRelayScoreDoesNotOutrankFreshnessTier(t *testing.T) {
	freshID := peer.ID("fresh")
	farAheadID := peer.ID("far-ahead")
	fresh := newBlockRelayScoreTestPeer(freshID, 100)
	farAhead := newBlockRelayScoreTestPeer(farAheadID, 130)
	now := time.Now()
	candidates := []*peers.Peer{farAhead, fresh}
	freshness := peers.SortMiningRelayPeers(candidates, now, 3*time.Second, 10, 100)

	tracker := newBlockRelayScoreTracker()
	tracker.recordSuccess(farAheadID, time.Millisecond, now.Add(-time.Second))
	tracker.recordSuccess(farAheadID, time.Millisecond, now)
	tracker.recordFailure(freshID, 500*time.Millisecond, now.Add(-time.Second))
	tracker.sortBroadcastPeers(candidates, freshness.Scores, now)

	if got := candidates[0].GetID(); got != freshID {
		t.Fatalf("first peer = %s, want fresh peer despite far-ahead peer history", got)
	}
}

func newBlockRelayScoreTestPeer(id peer.ID, mainOrder uint64) *peers.Peer {
	p := peers.NewPeer(id, nil)
	p.SetConnectionState(peers.PeerConnected)
	p.SetChainState(&pb.ChainState{
		Services: uint64(protocol.Full),
		GraphState: &pb.GraphState{
			MainOrder: uint32(mainOrder),
		},
	})
	return p
}
