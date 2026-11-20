package synch

import (
	"reflect"
	"testing"
	"time"

	blockdag "github.com/BlockdagNetworkLabs/bdag/blockdag"
	"github.com/BlockdagNetworkLabs/bdag/core/protocol"
	"github.com/BlockdagNetworkLabs/bdag/p2p/peers"
	pb "github.com/BlockdagNetworkLabs/bdag/p2p/proto/v1"
	"github.com/ethereum/go-ethereum/common"
)

type forkCurrentBlock struct {
	blockdag.IBlock
	id, order uint
}

func (b forkCurrentBlock) GetID() uint    { return b.id }
func (b forkCurrentBlock) GetOrder() uint { return b.order }

type forkCurrentDAG struct {
	blocks map[common.Hash]blockdag.IBlock
	main   map[uint]bool
}

type forkCurrentCheckpointCheck func(uint64, common.Hash, common.Hash) (bool, bool)

func (f forkCurrentCheckpointCheck) ValidateEVMCheckpoint(number uint64, hash, root common.Hash) (bool, bool) {
	return f(number, hash, root)
}

func (d forkCurrentDAG) GetBlock(hash *common.Hash) blockdag.IBlock { return d.blocks[*hash] }
func (d forkCurrentDAG) IsOnMainChain(id uint) bool                 { return d.main[id] }

func forkCurrentGraph(order uint, tip common.Hash) *blockdag.GraphState {
	gs := testGraphState(order)
	gs.SetTips([]*common.Hash{&tip})
	return gs
}

func TestCurrentPeerForkObservationReclassifiesCapturedState(t *testing.T) {
	oldTip := common.HexToHash("0xebbe6d928b1eb3c3c98396473d7f464e937e8f9be14dbbbe7ca434f650c10023")
	newTip := common.HexToHash("0x0e151e933bf461ac14c50ff429764815e49fe28894dab11082d6d9c8affa585b")
	unknownTip := common.HexToHash("0x03")
	prefix := common.HexToHash("0x04")
	hash, root := common.HexToHash("0x05"), common.HexToHash("0x06")
	for _, tc := range []struct {
		name     string
		peerTip  common.Hash
		order    uint
		cachedOK bool
		status   peers.PeerForkStatus
		known    bool
		same     bool
	}{
		{"stale_warning_current_tip_matches", newTip, 112801, false, peers.PeerForkStatusSameMainChain, true, true},
		{"stale_healthy_current_tip_diverges", oldTip, 112801, true, peers.PeerForkStatusDivergentMainChain, true, false},
		{"unknown_tip_only_shared_prefix", unknownTip, 112801, true, peers.PeerForkStatusCommonMainChainPrefix, false, false},
		{"known_tip_wrong_order", prefix, 112801, true, peers.PeerForkStatusUnknown, false, false},
		{"known_ancestor_correct_order", prefix, 100, false, peers.PeerForkStatusSameMainChain, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cached := &peers.PeerForkObservation{
				MainTip: oldTip, MainOrder: 112801, LocalMainTip: oldTip,
				SameNativeForkKnown: true, SameNativeFork: tc.cachedOK,
				LastChecked: time.Now(), CommitmentStatus: "checkpoint_valid",
				Tips: []common.Hash{oldTip}, DivergenceOrder: 1,
				EVMCheckpointNumber: 99, EVMCheckpointHash: oldTip,
			}
			before := cached.Clone()
			snapshot := &peers.StatsSnap{
				Fork: cached, GraphState: forkCurrentGraph(tc.order, tc.peerTip), SyncPoint: prefix,
				Protocol:      protocol.EVMCheckpointProtocolVersion,
				EVMCheckpoint: &pb.EVMCheckpoint{Number: 107796, Hash: &pb.Hash{Hash: hash.Bytes()}, StateRoot: &pb.Hash{Hash: root.Bytes()}},
			}
			dag := forkCurrentDAG{
				blocks: map[common.Hash]blockdag.IBlock{oldTip: forkCurrentBlock{id: 1, order: 112801}, prefix: forkCurrentBlock{id: 2, order: 100}},
				main:   map[uint]bool{1: false, 2: true},
			}
			validations := 0
			validate := func(number uint64, gotHash, gotRoot common.Hash) (bool, bool) {
				validations++
				return number == 107796 && gotHash == hash && gotRoot == root, true
			}
			got := currentPeerForkObservation(snapshot, forkCurrentGraph(112801, newTip), dag, forkCurrentCheckpointCheck(validate))
			if got.Status != tc.status || got.SameNativeForkKnown != tc.known || got.SameNativeFork != tc.same {
				t.Fatalf("current verdict = %+v; want %s known=%v same=%v", got, tc.status, tc.known, tc.same)
			}
			if got.MainTip != tc.peerTip || got.MainOrder != uint64(tc.order) || got.LocalMainTip != newTip {
				t.Fatalf("mixed old and current graph inputs: %+v", got)
			}
			if validations != 1 || got.CommitmentStatus != "checkpoint_valid" || got.EVMCheckpointNumber != 107796 {
				t.Fatalf("current checkpoint not independently validated: calls=%d observation=%+v", validations, got)
			}
			if !reflect.DeepEqual(cached, before) {
				t.Fatal("cached evidence mutated")
			}
		})
	}
}

func TestCurrentPeerForkObservationRejectsStaleHealthyAfterLocalReorg(t *testing.T) {
	tip, other := common.HexToHash("0x01"), common.HexToHash("0x02")
	snapshot := &peers.StatsSnap{GraphState: forkCurrentGraph(100, tip)}
	dag := forkCurrentDAG{blocks: map[common.Hash]blockdag.IBlock{tip: forkCurrentBlock{id: 1, order: 100}}, main: map[uint]bool{1: true}}
	first := currentPeerForkObservation(snapshot, forkCurrentGraph(101, other), dag, nil)
	if !first.SameNativeForkKnown || !first.SameNativeFork {
		t.Fatal("fixture ancestor is not verified on main chain")
	}
	snapshot.Fork = first
	dag.main[1] = false // The peer graph stays identical; only local membership changes.
	second := currentPeerForkObservation(snapshot, forkCurrentGraph(101, other), dag, nil)
	if second.Status != peers.PeerForkStatusDivergentMainChain || second.SameNativeFork {
		t.Fatalf("cached healthy verdict survived a local reorg: %+v", second)
	}
	if !first.SameNativeFork {
		t.Fatal("reclassification mutated cached observation")
	}
}

func TestCurrentPeerForkObservationDoesNotTrustCachedCheckpoint(t *testing.T) {
	tip, hash, root := common.HexToHash("0x01"), common.HexToHash("0x02"), common.HexToHash("0x03")
	for _, tc := range []struct {
		name, want   string
		valid, known bool
	}{
		{"mismatched_hash_or_root", "checkpoint_mismatch", false, true},
		{"unknown_checkpoint", "checkpoint_unknown", false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := &peers.StatsSnap{
				GraphState: forkCurrentGraph(100, tip), Protocol: protocol.EVMCheckpointProtocolVersion,
				Fork:          &peers.PeerForkObservation{CommitmentStatus: "checkpoint_valid"},
				EVMCheckpoint: &pb.EVMCheckpoint{Number: 95, Hash: &pb.Hash{Hash: hash.Bytes()}, StateRoot: &pb.Hash{Hash: root.Bytes()}},
			}
			got := currentPeerForkObservation(p, forkCurrentGraph(100, tip), forkCurrentDAG{}, forkCurrentCheckpointCheck(func(uint64, common.Hash, common.Hash) (bool, bool) { return tc.valid, tc.known }))
			if got.CommitmentStatus != tc.want {
				t.Fatalf("checkpoint status = %s, want %s", got.CommitmentStatus, tc.want)
			}
		})
	}
	for _, tc := range []struct {
		name       string
		protocol   uint32
		checkpoint *pb.EVMCheckpoint
		want       string
	}{
		{"missing", protocol.EVMCheckpointProtocolVersion, nil, "missing_checkpoint"},
		{"malformed", protocol.EVMCheckpointProtocolVersion, &pb.EVMCheckpoint{Hash: &pb.Hash{Hash: []byte{1}}, StateRoot: &pb.Hash{Hash: root.Bytes()}}, "invalid_checkpoint"},
		{"legacy", protocol.EVMCheckpointProtocolVersion - 1, nil, "legacy_v45"},
		{"no_protocol", 0, nil, "unknown"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := currentPeerForkObservation(&peers.StatsSnap{Protocol: tc.protocol, EVMCheckpoint: tc.checkpoint}, nil, nil, nil)
			if got.CommitmentStatus != tc.want || got.SameNativeForkKnown {
				t.Fatalf("missing verification context accepted: %+v", got)
			}
		})
	}
}

func TestCurrentPeerForkObservationLeavesWireProbeIntervalUnchanged(t *testing.T) {
	if PeerForkObservationInterval != 30*time.Second {
		t.Fatal("wire probe rate limit changed")
	}
}
