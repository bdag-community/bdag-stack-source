package synch

import (
	"testing"

	pb "github.com/BlockdagNetworkLabs/bdag/p2p/proto/v1"
)

func TestBlockDatasIncrementalSizeMatchesGeneratedSSZ(t *testing.T) {
	bd := &pb.BlockDatas{}
	size := blockDatasSSZBaseSize

	for _, blockBytes := range [][]byte{
		nil,
		[]byte{0x01, 0x02},
		make([]byte, 128),
	} {
		size += blockDatasEntrySSZSize(blockBytes)
		bd.Locator = append(bd.Locator, &pb.BlockData{BlockBytes: blockBytes})
		if got := bd.SizeSSZ(); got != size {
			t.Fatalf("incremental size = %d, generated SizeSSZ = %d", size, got)
		}
	}
}

func TestBlockDatasBudgetCutoffMatchesGeneratedSSZCheck(t *testing.T) {
	cases := []struct {
		name   string
		limit  uint64
		blocks [][]byte
	}{
		{name: "empty first block under limit", limit: 32, blocks: [][]byte{nil}},
		{name: "first block reaches limit", limit: 12, blocks: [][]byte{nil}},
		{name: "second block reaches limit", limit: 30, blocks: [][]byte{{0x01}, {0x02, 0x03}, {0x04}}},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			oldBD := &pb.BlockDatas{}
			newSize := blockDatasSSZBaseSize
			oldAccepted := 0
			newAccepted := 0

			for _, blockBytes := range tt.blocks {
				pbbd := &pb.BlockData{BlockBytes: blockBytes}
				if uint64(oldBD.SizeSSZ()+pbbd.SizeSSZ()+BLOCKDATA_SSZ_HEAD_SIZE) >= tt.limit {
					break
				}
				oldBD.Locator = append(oldBD.Locator, pbbd)
				oldAccepted++
			}

			for _, blockBytes := range tt.blocks {
				entrySize := blockDatasEntrySSZSize(blockBytes)
				if uint64(newSize+entrySize) >= tt.limit {
					break
				}
				newSize += entrySize
				newAccepted++
			}

			if newAccepted != oldAccepted {
				t.Fatalf("accepted blocks = %d, want old behavior %d", newAccepted, oldAccepted)
			}
			if newSize != oldBD.SizeSSZ() {
				t.Fatalf("final incremental size = %d, generated SizeSSZ = %d", newSize, oldBD.SizeSSZ())
			}
		})
	}
}
