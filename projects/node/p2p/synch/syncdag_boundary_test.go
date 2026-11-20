package synch

import (
	"bytes"
	"reflect"
	"testing"

	"github.com/BlockdagNetworkLabs/bdag/p2p/encoder"
	"github.com/BlockdagNetworkLabs/bdag/p2p/peers"
	pb "github.com/BlockdagNetworkLabs/bdag/p2p/proto/v1"
	ethcommon "github.com/ethereum/go-ethereum/common"
	"github.com/libp2p/go-libp2p/core/peer"
)

func validSubDAGForBoundaryTest() *pb.SubDAG {
	return &pb.SubDAG{
		SyncPoint: &pb.Hash{Hash: bytes.Repeat([]byte{0x11}, ethcommon.HashLength)},
		GraphState: &pb.GraphState{
			MainOrder:  2,
			MainHeight: 2,
			Layer:      2,
			Total:      3,
		},
		Blocks: []*pb.Hash{{Hash: bytes.Repeat([]byte{0x22}, ethcommon.HashLength)}},
	}
}

func TestValidateSubDAGBeforeSizeRejectsBeforeGeneratedNormalization(t *testing.T) {
	encoding := &encoder.SszNetworkEncoder{}
	subDAG := validSubDAGForBoundaryTest()
	subDAG.GraphState = nil
	sizeCalled := false
	_, _, _, err := validateSubDAGBeforeSize(subDAG, func() int {
		sizeCalled = true
		return encoding.GetSize(subDAG)
	})
	if err == nil {
		t.Fatal("nil GraphState unexpectedly validated")
	}
	if sizeCalled {
		t.Fatal("generated size was called before full validation")
	}
	if subDAG.GraphState != nil {
		t.Fatal("validation normalized nil GraphState")
	}

	// The generated size method itself normalizes this malformed shape; this
	// assertion makes the ordering hazard concrete for the production wrapper.
	_ = encoding.GetSize(subDAG)
	if subDAG.GraphState == nil {
		t.Fatal("generated size fixture did not expose normalization behavior")
	}
}

func TestValidateSubDAGBeforeSizeRejectsLateBlockAndRoundTripsValidSSZ(t *testing.T) {
	encoding := &encoder.SszNetworkEncoder{}
	malformed := validSubDAGForBoundaryTest()
	malformed.Blocks = append(malformed.Blocks, &pb.Hash{Hash: bytes.Repeat([]byte{0x33}, ethcommon.HashLength-1)})
	sizeCalled := false
	if _, _, _, err := validateSubDAGBeforeSize(malformed, func() int {
		sizeCalled = true
		return encoding.GetSize(malformed)
	}); err == nil {
		t.Fatal("late malformed block unexpectedly validated")
	}
	if sizeCalled {
		t.Fatal("generated size was called for malformed block list")
	}

	valid := validSubDAGForBoundaryTest()
	var wire bytes.Buffer
	if _, err := encoding.EncodeWithMaxLength(&wire, valid); err != nil {
		t.Fatalf("encode valid SubDAG: %v", err)
	}
	decoded := new(pb.SubDAG)
	if err := encoding.DecodeWithMaxLength(&wire, decoded); err != nil {
		t.Fatalf("decode valid SubDAG: %v", err)
	}
	if _, _, size, err := validateSubDAGBeforeSize(decoded, func() int { return encoding.GetSize(decoded) }); err != nil || size <= 0 {
		t.Fatalf("valid generated SubDAG validation = size %d err %v", size, err)
	}
}

func TestApplyPeerForkSyncDAGResponseValidatesBeforeMutation(t *testing.T) {
	pe := peers.NewPeer(peer.ID("fork-peer"), nil)
	initialPoint := ethcommon.HexToHash("0x44")
	pe.UpdateSyncPoint(&initialPoint)
	initialGraph := &pb.GraphState{MainOrder: 7, MainHeight: 7, Layer: 7, Total: 7}
	pe.SetChainState(&pb.ChainState{GraphState: initialGraph})
	observation := &peers.PeerForkObservation{SyncPoint: initialPoint, LastError: ""}
	ps := &PeerSync{}
	malformed := validSubDAGForBoundaryTest()
	malformed.Blocks = []*pb.Hash{{Hash: bytes.Repeat([]byte{0x55}, ethcommon.HashLength-1)}}
	ps.applyPeerForkSyncDAGResponse(pe, observation, malformed)
	if observation.LastError == "" {
		t.Fatal("malformed fork response did not record LastError")
	}
	if got := pe.SyncPoint(); got == nil || *got != initialPoint {
		t.Fatalf("peer sync point changed after malformed response: %v", got)
	}
	if !reflect.DeepEqual(pe.ChainState().GraphState, initialGraph) {
		t.Fatal("peer graph state changed after malformed response")
	}
	if observation.SyncPoint != initialPoint {
		t.Fatalf("observation sync point changed after malformed response: %v", observation.SyncPoint)
	}
}
