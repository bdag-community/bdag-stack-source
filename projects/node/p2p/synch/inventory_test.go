package synch

import (
	"testing"

	pb "github.com/BlockdagNetworkLabs/bdag/p2p/proto/v1"
)

func TestChunkInventoryBoundaries(t *testing.T) {
	invs := []*pb.InvVect{
		{Type: uint32(InvTypeTx), Hash: &pb.Hash{Hash: []byte{1}}},
		{Type: uint32(InvTypeTx), Hash: &pb.Hash{Hash: []byte{2}}},
		{Type: uint32(InvTypeTx), Hash: &pb.Hash{Hash: []byte{3}}},
		{Type: uint32(InvTypeTx), Hash: &pb.Hash{Hash: []byte{4}}},
		{Type: uint32(InvTypeTx), Hash: &pb.Hash{Hash: []byte{5}}},
	}

	messages := chunkInventory(invs, 2)
	if len(messages) != 3 {
		t.Fatalf("messages = %d, want 3", len(messages))
	}
	wantLens := []int{2, 2, 1}
	for i, want := range wantLens {
		if got := len(messages[i].Invs); got != want {
			t.Fatalf("message %d length = %d, want %d", i, got, want)
		}
		if cap(messages[i].Invs) != want {
			t.Fatalf("message %d capacity = %d, want exact %d", i, cap(messages[i].Invs), want)
		}
	}
	if messages[0].Invs[0] != invs[0] || messages[1].Invs[0] != invs[2] || messages[2].Invs[0] != invs[4] {
		t.Fatal("chunkInventory did not preserve inventory order")
	}
}

func TestChunkInventoryEmpty(t *testing.T) {
	if messages := chunkInventory(nil, 2); len(messages) != 0 {
		t.Fatalf("messages = %d, want 0", len(messages))
	}
}

func TestChunkInventoryBoundaryWindowsCopyAndPreserveOrder(t *testing.T) {
	for _, size := range []int{1, MaxInvPerMsg, MaxInvPerMsg + 1} {
		invs := make([]*pb.InvVect, size)
		for i := range invs {
			invs[i] = &pb.InvVect{Type: uint32(i), Hash: &pb.Hash{Hash: make([]byte, 32)}}
		}
		messages := chunkInventory(invs, MaxInvPerMsg)
		wantMessages := 1
		if size > MaxInvPerMsg {
			wantMessages = 2
		}
		if len(messages) != wantMessages {
			t.Fatalf("size %d produced %d messages, want %d", size, len(messages), wantMessages)
		}
		for i, msg := range messages {
			for j, inv := range msg.Invs {
				if inv != invs[i*MaxInvPerMsg+j] {
					t.Fatalf("size %d changed order at %d", size, i*MaxInvPerMsg+j)
				}
			}
		}
		invs[0] = nil
		if messages[0].Invs[0] == nil {
			t.Fatalf("size %d retained caller slice backing array", size)
		}
	}
}

func TestHaveInventoryHashTreatsMalformedHashAsKnown(t *testing.T) {
	var s Sync
	if !s.haveInventoryHash(InvTypeTx, nil) {
		t.Fatal("nil transaction hash should be treated as known to avoid malformed peer panics")
	}
	if !s.haveInventoryHash(InvTypeBlock, nil) {
		t.Fatal("nil block hash should be treated as known to avoid malformed peer panics")
	}
}
