package encoder

import (
	"encoding/binary"
	"strings"
	"testing"

	"github.com/BlockdagNetworkLabs/bdag/core/protocol"
	pb "github.com/BlockdagNetworkLabs/bdag/p2p/proto/v1"
)

func TestLegacyChainStateSSZRoundTrip(t *testing.T) {
	t.Parallel()

	genesis := bytesOf(0x11, 32)
	tip := bytesOf(0x22, 32)
	msg := &pb.ChainState{
		GenesisHash:     &pb.Hash{Hash: genesis},
		ProtocolVersion: protocol.BroadcastblockProtocolVersion,
		Timestamp:       12345,
		Services:        7,
		DisableRelayTx:  true,
		GraphState: &pb.GraphState{
			Total:      100,
			Layer:      90,
			MainHeight: 80,
			MainOrder:  70,
			Tips:       []*pb.Hash{{Hash: tip}},
		},
		UserAgent: []byte("BDAG:1.0.6-v45-compat"),
		EvmCheckpoint: &pb.EVMCheckpoint{
			Number:    99,
			Hash:      &pb.Hash{Hash: bytesOf(0x33, 32)},
			StateRoot: &pb.Hash{Hash: bytesOf(0x44, 32)},
		},
	}

	encoded, err := SszNetworkEncoder{}.doEncode(msg)
	if err != nil {
		t.Fatalf("encode legacy chainstate: %v", err)
	}

	if got := binary.LittleEndian.Uint32(encoded[chainStateGraphStateOffsetSlot : chainStateGraphStateOffsetSlot+4]); got != chainStateLegacyFixedSize {
		t.Fatalf("graphstate offset = %d, want %d", got, chainStateLegacyFixedSize)
	}
	userAgentOffset := int(binary.LittleEndian.Uint32(encoded[chainStateUserAgentOffsetSlot : chainStateUserAgentOffsetSlot+4]))
	if want := chainStateLegacyFixedSize + msg.GraphState.SizeSSZ(); userAgentOffset != want {
		t.Fatalf("user agent offset = %d, want %d", userAgentOffset, want)
	}
	if len(encoded) != userAgentOffset+len(msg.UserAgent) {
		t.Fatalf("encoded len = %d, want %d", len(encoded), userAgentOffset+len(msg.UserAgent))
	}
	if len(encoded) >= msg.SizeSSZ() {
		t.Fatalf("legacy encoded len = %d, current layout len = %d", len(encoded), msg.SizeSSZ())
	}

	var decoded pb.ChainState
	if err := (SszNetworkEncoder{}).doDecode(encoded, &decoded); err != nil {
		t.Fatalf("decode legacy chainstate: %v", err)
	}
	if decoded.ProtocolVersion != protocol.BroadcastblockProtocolVersion {
		t.Fatalf("protocol = %d, want %d", decoded.ProtocolVersion, protocol.BroadcastblockProtocolVersion)
	}
	if decoded.Timestamp != msg.Timestamp || decoded.Services != msg.Services || decoded.DisableRelayTx != msg.DisableRelayTx {
		t.Fatalf("decoded header mismatch: %#v", decoded)
	}
	if string(decoded.UserAgent) != string(msg.UserAgent) {
		t.Fatalf("user agent = %q, want %q", decoded.UserAgent, msg.UserAgent)
	}
	if decoded.GraphState.GetMainOrder() != msg.GraphState.GetMainOrder() || len(decoded.GraphState.GetTips()) != 1 {
		t.Fatalf("graphstate mismatch: %#v", decoded.GraphState)
	}
	if decoded.EvmCheckpoint != nil {
		t.Fatalf("legacy decode should not populate evm checkpoint: %#v", decoded.EvmCheckpoint)
	}
}

func TestCurrentChainStateSSZKeepsEVMCheckpointLayout(t *testing.T) {
	t.Parallel()

	msg := &pb.ChainState{
		GenesisHash:     &pb.Hash{Hash: bytesOf(0x11, 32)},
		ProtocolVersion: protocol.EVMCheckpointProtocolVersion,
		Timestamp:       12345,
		Services:        7,
		GraphState:      &pb.GraphState{Total: 1, Tips: []*pb.Hash{{Hash: bytesOf(0x22, 32)}}},
		UserAgent:       []byte("BDAG:1.0.6-v45-compat"),
		EvmCheckpoint: &pb.EVMCheckpoint{
			Number:    99,
			Hash:      &pb.Hash{Hash: bytesOf(0x33, 32)},
			StateRoot: &pb.Hash{Hash: bytesOf(0x44, 32)},
		},
	}

	encoded, err := SszNetworkEncoder{}.doEncode(msg)
	if err != nil {
		t.Fatalf("encode current chainstate: %v", err)
	}
	if got := binary.LittleEndian.Uint32(encoded[chainStateGraphStateOffsetSlot : chainStateGraphStateOffsetSlot+4]); got != chainStateCurrentFixedSize {
		t.Fatalf("graphstate offset = %d, want %d", got, chainStateCurrentFixedSize)
	}

	var decoded pb.ChainState
	if err := (SszNetworkEncoder{}).doDecode(encoded, &decoded); err != nil {
		t.Fatalf("decode current chainstate: %v", err)
	}
	if decoded.EvmCheckpoint == nil || decoded.EvmCheckpoint.GetNumber() != msg.EvmCheckpoint.GetNumber() {
		t.Fatalf("decoded evm checkpoint mismatch: %#v", decoded.EvmCheckpoint)
	}
}

func TestCurrentLayoutChainStateWithProtocol45StillDecodes(t *testing.T) {
	t.Parallel()

	msg := &pb.ChainState{
		GenesisHash:     &pb.Hash{Hash: bytesOf(0x11, 32)},
		ProtocolVersion: protocol.BroadcastblockProtocolVersion,
		Timestamp:       12345,
		Services:        7,
		GraphState:      &pb.GraphState{Total: 1, Tips: []*pb.Hash{{Hash: bytesOf(0x22, 32)}}},
		UserAgent:       []byte("BDAG:1.0.5"),
		EvmCheckpoint: &pb.EVMCheckpoint{
			Number:    99,
			Hash:      &pb.Hash{Hash: bytesOf(0x33, 32)},
			StateRoot: &pb.Hash{Hash: bytesOf(0x44, 32)},
		},
	}

	// Previous rollout builds could advertise v45 while still using the new
	// ChainState layout. Decode must remain permissive during the hotfix window.
	encoded, err := msg.MarshalSSZ()
	if err != nil {
		t.Fatalf("marshal current layout chainstate: %v", err)
	}
	if got := binary.LittleEndian.Uint32(encoded[chainStateGraphStateOffsetSlot : chainStateGraphStateOffsetSlot+4]); got != chainStateCurrentFixedSize {
		t.Fatalf("graphstate offset = %d, want %d", got, chainStateCurrentFixedSize)
	}

	var decoded pb.ChainState
	if err := (SszNetworkEncoder{}).doDecode(encoded, &decoded); err != nil {
		t.Fatalf("decode current layout with protocol 45: %v", err)
	}
	if decoded.ProtocolVersion != protocol.BroadcastblockProtocolVersion {
		t.Fatalf("protocol = %d, want %d", decoded.ProtocolVersion, protocol.BroadcastblockProtocolVersion)
	}
}

func TestChainStateSSZDecodeErrorIncludesLayoutDiagnostics(t *testing.T) {
	t.Parallel()

	payload := make([]byte, chainStateCurrentFixedSize)
	binary.LittleEndian.PutUint32(payload[chainStateProtocolVersionOffset:chainStateProtocolVersionOffset+4], 45)
	binary.LittleEndian.PutUint32(payload[chainStateGraphStateOffsetSlot:chainStateGraphStateOffsetSlot+4], chainStateLegacyFixedSize)
	binary.LittleEndian.PutUint32(payload[chainStateUserAgentOffsetSlot:chainStateUserAgentOffsetSlot+4], chainStateLegacyFixedSize)

	err := SszNetworkEncoder{}.doDecode(payload, &pb.ChainState{})
	if err == nil {
		t.Fatal("expected ChainState SSZ decode error")
	}

	errText := err.Error()
	for _, want := range []string{
		"legacy chainstate graphstate",
		"chainstate_ssz_payload_bytes=133",
		"expected_fixed_bytes=133",
		"legacy_without_evm_checkpoint_fixed_bytes=61",
		"partial_protocol_version=45",
		"graphstate_offset=61",
		"graphstate_offset_in_fixed_section=true",
		"looks_like_legacy_chainstate_without_evm_checkpoint=true",
		"useragent_offset=61",
		"evm_checkpoint_bytes_nonzero=false",
		"prefix_hex=",
	} {
		if !strings.Contains(errText, want) {
			t.Fatalf("expected error to contain %q, got %q", want, errText)
		}
	}
}

func bytesOf(value byte, size int) []byte {
	b := make([]byte, size)
	for i := range b {
		b[i] = value
	}
	return b
}
