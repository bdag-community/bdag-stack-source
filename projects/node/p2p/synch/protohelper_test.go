package synch

import (
	"context"
	"math/big"
	"testing"

	pb "github.com/BlockdagNetworkLabs/bdag/p2p/proto/v1"
	ethcommon "github.com/ethereum/go-ethereum/common"
)

func TestChangePBHashsToHashsPreservesOrder(t *testing.T) {
	first := ethcommon.HexToHash("0x01")
	second := ethcommon.HexToHash("0x02")

	got, err := changePBHashsToHashs([]*pb.Hash{
		{Hash: first.Bytes()},
		{Hash: second.Bytes()},
	})
	if err != nil {
		t.Fatalf("convert hashes: %v", err)
	}

	if len(got) != 2 {
		t.Fatalf("converted hashes = %d, want 2", len(got))
	}
	if *got[0] != first {
		t.Fatalf("first hash = %s, want %s", got[0], first.String())
	}
	if *got[1] != second {
		t.Fatalf("second hash = %s, want %s", got[1], second.String())
	}
}

func TestChangePBHashToHashRejectsMalformed(t *testing.T) {
	if got, err := changePBHashToHash(nil); err == nil || got != nil {
		t.Fatalf("nil protobuf hash conversion = (%v, %v), want error", got, err)
	}
}

func TestChangePBHashsToHashsRejectsMalformedWithoutPartialResult(t *testing.T) {
	first := ethcommon.HexToHash("0x01")
	second := ethcommon.HexToHash("0x02")

	got, err := changePBHashsToHashs([]*pb.Hash{
		nil,
		{Hash: first.Bytes()},
		{Hash: second.Bytes()},
	})
	if err == nil || got != nil {
		t.Fatalf("converted hashes = (%v, %v), want explicit error and no partial result", got, err)
	}
}

func TestChangePBHashToHashRequiresExactly32Bytes(t *testing.T) {
	for _, size := range []int{0, 1, 31, 33} {
		if got, err := changePBHashToHash(&pb.Hash{Hash: make([]byte, size)}); err == nil || got != nil {
			t.Fatalf("length %d conversion = (%v, %v), want error", size, got, err)
		}
	}
	if got, err := changePBHashToHash(&pb.Hash{Hash: make([]byte, 32)}); err != nil || got == nil {
		t.Fatalf("32-byte conversion = (%v, %v), want valid hash", got, err)
	}
}

func TestIncomingHashListsRejectMalformedEntriesBeforeDispatch(t *testing.T) {
	bad := &pb.Hash{Hash: []byte{1}}
	valid := &pb.Hash{Hash: make([]byte, 32)}
	if err := (&Sync{}).handleInventory(&pb.Inventory{Invs: []*pb.InvVect{
		{Type: uint32(InvTypeTx), Hash: valid},
		{Type: uint32(InvTypeBlock), Hash: bad},
	}}, nil); err == nil {
		t.Fatal("handleInventory accepted malformed hash after a valid entry")
	}
	if err := (&PeerSync{}).OnGetData(nil, []*pb.InvVect{
		{Type: uint32(InvTypeTx), Hash: valid},
		nil,
	}); err == nil {
		t.Fatal("OnGetData accepted nil inventory after a valid entry")
	}
	if err := (&Sync{}).txHandler(context.Background(), &pb.GetTxs{Txs: []*pb.Hash{valid, bad}}, nil, nil); err == nil {
		t.Fatal("txHandler accepted malformed hash after a valid entry")
	}
	if err := (&Sync{}).getBlockDataHandler(context.Background(), &pb.GetBlockDatas{Locator: []*pb.Hash{valid, bad}}, nil, nil); err == nil {
		t.Fatal("getBlockDataHandler accepted malformed locator after a valid entry")
	}
	if err := (&Sync{}).syncDAGHandler(context.Background(), &pb.SyncDAG{MainLocator: []*pb.Hash{valid, bad}}, nil, nil); err == nil {
		t.Fatal("syncDAGHandler accepted malformed locator after a valid entry")
	}
	if err := (&Sync{}).conSyncDAGHandler(context.Background(), &pb.ContinueSyncDAG{SyncPoint: valid, Start: bad}, nil, nil); err == nil {
		t.Fatal("conSyncDAGHandler accepted malformed start hash")
	}
}

func TestChangeHashsToPBHashsPreservesOrder(t *testing.T) {
	first := ethcommon.HexToHash("0x0a")
	second := ethcommon.HexToHash("0x0b")

	got := changeHashsToPBHashs([]*ethcommon.Hash{&first, &second})

	if len(got) != 2 {
		t.Fatalf("converted protobuf hashes = %d, want 2", len(got))
	}
	if ethcommon.BytesToHash(got[0].Hash) != first {
		t.Fatalf("first protobuf hash = %x, want %s", got[0].Hash, first.String())
	}
	if ethcommon.BytesToHash(got[1].Hash) != second {
		t.Fatalf("second protobuf hash = %x, want %s", got[1].Hash, second.String())
	}
}

func BenchmarkChangePBHashsToHashs(b *testing.B) {
	source := make([]*pb.Hash, 2000)
	for i := range source {
		hash := ethcommon.BigToHash(new(big.Int).SetInt64(int64(i + 1)))
		source[i] = &pb.Hash{Hash: hash.Bytes()}
	}

	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_, _ = changePBHashsToHashs(source)
	}
}
