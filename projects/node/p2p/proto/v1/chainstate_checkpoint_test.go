package blockdag_p2p_v1

import (
	"bytes"
	"testing"

	"github.com/golang/protobuf/proto"
)

func TestChainStateEVMCheckpointRoundTrip(t *testing.T) {
	hash := bytes.Repeat([]byte{0x11}, 32)
	root := bytes.Repeat([]byte{0x22}, 32)
	msg := &ChainState{
		GenesisHash:     &Hash{Hash: bytes.Repeat([]byte{0x33}, 32)},
		ProtocolVersion: 46,
		Timestamp:       123,
		Services:        1,
		GraphState: &GraphState{
			Total:      1,
			Layer:      1,
			MainHeight: 1,
			MainOrder:  1,
		},
		UserAgent: []byte("test"),
		EvmCheckpoint: &EVMCheckpoint{
			Number:    100,
			Hash:      &Hash{Hash: hash},
			StateRoot: &Hash{Hash: root},
		},
	}

	sszBytes, err := msg.MarshalSSZ()
	if err != nil {
		t.Fatalf("MarshalSSZ: %v", err)
	}
	var decodedSSZ ChainState
	if err := decodedSSZ.UnmarshalSSZ(sszBytes); err != nil {
		t.Fatalf("UnmarshalSSZ: %v", err)
	}
	assertCheckpoint(t, decodedSSZ.GetEvmCheckpoint(), 100, hash, root)

	protoBytes, err := proto.Marshal(msg)
	if err != nil {
		t.Fatalf("proto Marshal: %v", err)
	}
	var decodedProto ChainState
	if err := proto.Unmarshal(protoBytes, &decodedProto); err != nil {
		t.Fatalf("proto Unmarshal: %v", err)
	}
	assertCheckpoint(t, decodedProto.GetEvmCheckpoint(), 100, hash, root)
}

func assertCheckpoint(t *testing.T, checkpoint *EVMCheckpoint, number uint64, hash []byte, root []byte) {
	t.Helper()
	if checkpoint == nil {
		t.Fatal("checkpoint is nil")
	}
	if checkpoint.Number != number {
		t.Fatalf("number = %d, want %d", checkpoint.Number, number)
	}
	if !bytes.Equal(checkpoint.GetHash().GetHash(), hash) {
		t.Fatalf("hash = %x, want %x", checkpoint.GetHash().GetHash(), hash)
	}
	if !bytes.Equal(checkpoint.GetStateRoot().GetHash(), root) {
		t.Fatalf("state root = %x, want %x", checkpoint.GetStateRoot().GetHash(), root)
	}
}
