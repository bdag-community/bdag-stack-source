package synch

import (
	"bytes"
	"sync/atomic"
	"testing"
	"time"

	blockdag "github.com/BlockdagNetworkLabs/bdag/blockdag"
	"github.com/BlockdagNetworkLabs/bdag/core/types"
	"github.com/BlockdagNetworkLabs/bdag/core/types/pow"
	"github.com/BlockdagNetworkLabs/bdag/p2p/peers"
	pb "github.com/BlockdagNetworkLabs/bdag/p2p/proto/v1"
	"github.com/BlockdagNetworkLabs/bdag/services/notifymgr/notify"
	ethcommon "github.com/ethereum/go-ethereum/common"
	"github.com/libp2p/go-libp2p/core/peer"
)

func TestEstimateSyncETA(t *testing.T) {
	cur := blockdag.NewGraphState()
	cur.SetMainOrder(12397234)
	target := blockdag.NewGraphState()
	target.SetMainOrder(12402058)

	remaining, eta := estimateSyncETA(cur, target, 4824, 10*time.Second)

	if remaining != 4824 {
		t.Fatalf("remaining mismatch: got %d want %d", remaining, 4824)
	}
	if eta < 9*time.Second || eta > 11*time.Second {
		t.Fatalf("eta mismatch: got %s want about 10s", eta)
	}
}

func TestSelectBestSyncPeerChoosesMostAdvancedPeer(t *testing.T) {
	local := testGraphState(100)
	older := testSyncPeer("older-peer", 100)
	better := testSyncPeer("better-peer", 101)
	best := testSyncPeer("best-peer", 103)
	noGraph := peers.NewPeer(peer.ID("no-graph-peer"), nil)

	got, gotGraphState := selectBestSyncPeer([]*peers.Peer{older, better, noGraph, best}, local)

	if got != best {
		t.Fatalf("best peer = %v, want %v", got.GetID(), best.GetID())
	}
	if gotGraphState == nil || gotGraphState.GetMainOrder() != 103 {
		t.Fatalf("best graph state order = %v, want 103", gotGraphState)
	}
}

func TestSelectBestSyncPeerKeepsEqualBestCandidates(t *testing.T) {
	local := testGraphState(100)
	equalA := testSyncPeer("equal-a-peer", 103)
	equalB := testSyncPeer("equal-b-peer", 103)
	lesser := testSyncPeer("lesser-peer", 102)

	got, gotGraphState := selectBestSyncPeer([]*peers.Peer{equalA, lesser, equalB}, local)

	if got != equalA && got != equalB {
		t.Fatalf("best peer = %v, want one of equal best peers", got.GetID())
	}
	if gotGraphState == nil || gotGraphState.GetMainOrder() != 103 {
		t.Fatalf("best graph state order = %v, want 103", gotGraphState)
	}
}

func TestSelectBestSyncPeerSkipsDivergentCandidate(t *testing.T) {
	local := testGraphState(100)
	divergent := testSyncPeer("divergent-peer", 110)
	divergent.SetForkObservation(&peers.PeerForkObservation{Status: peers.PeerForkStatusDivergentMainChain})
	healthy := testSyncPeer("healthy-peer", 101)

	got, gotGraphState := selectBestSyncPeer([]*peers.Peer{divergent, healthy}, local)
	if got != healthy {
		t.Fatalf("selected peer = %v, want healthy peer", got)
	}
	if gotGraphState == nil || gotGraphState.GetMainOrder() != 101 {
		t.Fatalf("selected graph state = %v, want order 101", gotGraphState)
	}
}

func TestSelectBestSyncPeerUsesActualLatencyTieBreaker(t *testing.T) {
	local := testGraphState(100)
	a := testSyncPeer("latency-a", 103)
	b := testSyncPeer("latency-b", 103)
	tip := ethcommon.HexToHash("0x42")
	local.SetTips([]*ethcommon.Hash{&tip})
	for _, candidate := range []*peers.Peer{a, b} {
		candidate.SetChainState(&pb.ChainState{GraphState: &pb.GraphState{
			Total: 103, Layer: 103, MainHeight: 103, MainOrder: 103,
			Tips: []*pb.Hash{{Hash: append([]byte(nil), tip.Bytes()...)}},
		}})
	}
	selected, selectedGraphState := selectBestSyncPeerWithLatency([]*peers.Peer{a, b}, local,
		func(candidates []*peers.Peer) (*peers.Peer, time.Duration) {
			if len(candidates) != 2 {
				t.Fatalf("latency candidates = %d, want 2", len(candidates))
			}
			return b, 7 * time.Millisecond
		})
	if selected != b || selectedGraphState == nil {
		t.Fatalf("latency selection = (%v, %v), want b and graph state", selected, selectedGraphState)
	}
}

func TestGraphStateConverterRejectsOversizedAndMalformedTips(t *testing.T) {
	validHash := bytes.Repeat([]byte{0x42}, 32)
	valid := &pb.GraphState{Tips: make([]*pb.Hash, 100)}
	for i := range valid.Tips {
		valid.Tips[i] = &pb.Hash{Hash: append([]byte(nil), validHash...)}
	}
	converted, err := changePBGraphStateToGraphState(valid)
	if err != nil || converted == nil || len(converted.GetTipsList()) != 100 {
		t.Fatalf("valid 100-tip conversion = (%v, %v), want 100 tips", converted, err)
	}
	valid.Tips[0].Hash[0] = 0
	if converted.GetTipsList()[0].Bytes()[0] != 0x42 {
		t.Fatal("graph-state conversion aliased input tip bytes")
	}

	for name, state := range map[string]*pb.GraphState{
		"nil":       nil,
		"oversized": {Tips: append(append([]*pb.Hash(nil), valid.Tips...), &pb.Hash{Hash: append([]byte(nil), validHash...)})},
		"short":     {Tips: []*pb.Hash{{Hash: make([]byte, 31)}}},
		"nil-tip":   {Tips: []*pb.Hash{nil}},
	} {
		t.Run(name, func(t *testing.T) {
			if got, err := changePBGraphStateToGraphState(state); err == nil || got != nil {
				t.Fatalf("conversion = (%v, %v), want explicit error and nil result", got, err)
			}
		})
	}
}

func TestRelayInventoryToPeersSkipsEmptyPeerAndContinues(t *testing.T) {
	filteredPeer := peers.NewPeer(peer.ID("filtered-peer"), nil)
	relayPeer := peers.NewPeer(peer.ID("relay-peer"), nil)
	header := types.BlockHeader{Pow: pow.GetInstance(pow.BLAKE2BD, 0, 0, nil)}
	nds := []*notify.NotifyData{{Data: header, Filters: []peer.ID{filteredPeer.GetID()}}}
	sent := make(map[peer.ID]int)
	var gotHash []byte

	for _, pe := range []*peers.Peer{filteredPeer, relayPeer} {
		invs, running := relayInventoryForPeer(pe, nds, func() bool { return true })
		if !running {
			t.Fatal("relay stopped unexpectedly")
		}
		if len(invs) > 0 {
			sent[pe.GetID()] = len(invs)
			gotHash = append([]byte(nil), invs[0].Hash.Hash...)
		}
	}

	if _, ok := sent[filteredPeer.GetID()]; ok {
		t.Fatalf("sent an empty inventory batch to filtered peer")
	}
	if sent[relayPeer.GetID()] != 1 {
		t.Fatalf("expected one inventory vector for relay peer, got %d", sent[relayPeer.GetID()])
	}
	wantHash := header.BlockHash()
	if !bytes.Equal(gotHash, wantHash.Bytes()) {
		t.Fatalf("relayed hash mismatch: got %x want %x", gotHash, wantHash.Bytes())
	}
}

func TestRelayInventoryToPeersStopsWhenNotRunning(t *testing.T) {
	relayPeer := peers.NewPeer(peer.ID("relay-peer"), nil)
	nds := []*notify.NotifyData{{Data: types.BlockHeader{Pow: pow.GetInstance(pow.BLAKE2BD, 0, 0, nil)}}}

	invs, running := relayInventoryForPeer(relayPeer, nds, func() bool { return false })
	if running || len(invs) != 0 {
		t.Fatalf("relay result = (%d, %v), want empty stopped result", len(invs), running)
	}
}

func TestRelayInventoryUsesProductionFanoutAndContinuesPastEmptyPeer(t *testing.T) {
	filteredPeer := peers.NewPeer(peer.ID("filtered-peer"), nil)
	relayPeer := peers.NewPeer(peer.ID("relay-peer"), nil)
	header := types.BlockHeader{Pow: pow.GetInstance(pow.BLAKE2BD, 0, 0, nil)}
	nds := []*notify.NotifyData{{Data: header, Filters: []peer.ID{filteredPeer.GetID()}}}
	var sent []peer.ID
	var ps *PeerSync
	ps = &PeerSync{
		started: 1,
		relayInventoryPeers: func() []*peers.Peer {
			return []*peers.Peer{filteredPeer, relayPeer}
		},
		relayInventorySend: func(pe *peers.Peer, invs []*pb.InvVect) error {
			sent = append(sent, pe.GetID())
			if len(invs) != 1 {
				t.Fatalf("inventory count = %d, want 1", len(invs))
			}
			return nil
		},
	}
	ps.RelayInventory(nds)
	if len(sent) != 1 || sent[0] != relayPeer.GetID() {
		t.Fatalf("sent peers = %v, want [%s]", sent, relayPeer.GetID())
	}
}

func TestRelayInventoryProductionFanoutStopsAfterShutdown(t *testing.T) {
	first := peers.NewPeer(peer.ID("first-peer"), nil)
	second := peers.NewPeer(peer.ID("second-peer"), nil)
	header := types.BlockHeader{Pow: pow.GetInstance(pow.BLAKE2BD, 0, 0, nil)}
	nds := []*notify.NotifyData{{Data: header}}
	var sent []peer.ID
	var ps *PeerSync
	ps = &PeerSync{
		started:             1,
		relayInventoryPeers: func() []*peers.Peer { return []*peers.Peer{first, second} },
		relayInventorySend: func(pe *peers.Peer, _ []*pb.InvVect) error {
			sent = append(sent, pe.GetID())
			atomic.StoreInt32(&ps.shutdown, 1)
			return nil
		},
	}
	ps.RelayInventory(nds)
	if len(sent) != 1 || sent[0] != first.GetID() {
		t.Fatalf("sent peers = %v, want only first peer", sent)
	}
}

func testSyncPeer(name string, mainOrder uint32) *peers.Peer {
	pe := peers.NewPeer(peer.ID(name), nil)
	pe.SetConnectionState(peers.PeerConnected)
	pe.SetChainState(&pb.ChainState{
		GraphState: &pb.GraphState{
			Total:      mainOrder,
			Layer:      mainOrder,
			MainHeight: mainOrder,
			MainOrder:  mainOrder,
		},
	})
	return pe
}

func testGraphState(mainOrder uint) *blockdag.GraphState {
	gs := blockdag.NewGraphState()
	gs.SetTotal(mainOrder)
	gs.SetLayer(mainOrder)
	gs.SetMainHeight(mainOrder)
	gs.SetMainOrder(mainOrder)
	return gs
}
