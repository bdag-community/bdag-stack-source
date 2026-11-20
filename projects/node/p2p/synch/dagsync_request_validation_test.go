package synch

import (
	"context"
	"testing"

	"github.com/BlockdagNetworkLabs/bdag/p2p/common"
	"github.com/BlockdagNetworkLabs/bdag/p2p/peers"
	pb "github.com/BlockdagNetworkLabs/bdag/p2p/proto/v1"
	ethcommon "github.com/ethereum/go-ethereum/common"
)

func TestDAGRequestValidation(t *testing.T) {
	s := &Sync{}
	pe := peers.NewPeer("requester", nil)
	for _, name := range []string{
		"nil-blocks",
		"nil-sync",
		"nil-continue",
		"short-hash",
		"zero-hash",
		"too-many",
		"cancelled-sync",
		"cancelled-continue",
		"cancelled-blocks",
	} {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			hash := ethcommon.Hash{1}
			var err *common.Error
			switch name {
			case "nil-blocks":
				err = s.getBlocksHandler(ctx, (*pb.GetBlocks)(nil), nil, pe)
			case "nil-sync":
				err = s.syncDAGHandler(ctx, (*pb.SyncDAG)(nil), nil, pe)
			case "nil-continue":
				err = s.conSyncDAGHandler(ctx, (*pb.ContinueSyncDAG)(nil), nil, pe)
			case "short-hash":
				err = s.conSyncDAGHandler(ctx, &pb.ContinueSyncDAG{SyncPoint: &pb.Hash{Hash: []byte{1}}, Start: &pb.Hash{Hash: hash.Bytes()}}, nil, pe)
			case "zero-hash":
				err = s.getBlocksHandler(ctx, &pb.GetBlocks{Locator: []*pb.Hash{{Hash: make([]byte, ethcommon.HashLength)}}}, nil, pe)
			case "too-many":
				err = s.getBlocksHandler(ctx, &pb.GetBlocks{Locator: make([]*pb.Hash, MaxBlockLocatorsPerMsg+1)}, nil, pe)
			default:
				cancelled, cancel := context.WithCancel(ctx)
				cancel()
				switch name {
				case "cancelled-sync":
					err = s.syncDAGHandler(cancelled, &pb.SyncDAG{MainLocator: []*pb.Hash{{Hash: hash.Bytes()}}, GraphState: &pb.GraphState{}}, nil, pe)
				case "cancelled-continue":
					err = s.conSyncDAGHandler(cancelled, &pb.ContinueSyncDAG{SyncPoint: &pb.Hash{Hash: hash.Bytes()}, Start: &pb.Hash{Hash: hash.Bytes()}}, nil, pe)
				case "cancelled-blocks":
					err = s.getBlocksHandler(cancelled, &pb.GetBlocks{}, nil, pe)
				}
			}
			if err == nil {
				t.Fatal("invalid request reached absent chain/transport")
			}
		})
	}
}
