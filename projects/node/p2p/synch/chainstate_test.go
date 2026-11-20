package synch

import (
	"context"
	"strings"
	"testing"

	"github.com/BlockdagNetworkLabs/bdag/core/protocol"
	p2pcommon "github.com/BlockdagNetworkLabs/bdag/p2p/common"
	pb "github.com/BlockdagNetworkLabs/bdag/p2p/proto/v1"
	"github.com/BlockdagNetworkLabs/bdag/params"
	ethcommon "github.com/ethereum/go-ethereum/common"
)

func TestValidateChainStateProtocolVersionActivation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		activation  uint64
		peerVersion uint32
		wantErr     string
	}{
		{
			name:        "protocol 45 accepted before activation",
			activation:  100,
			peerVersion: protocol.BroadcastblockProtocolVersion,
		},
		{
			name:        "protocol below 45 rejected before activation",
			activation:  100,
			peerVersion: protocol.ConSyncDAGProtocolVersion,
			wantErr:     "protocol version must be 45 or greater",
		},
		{
			name:        "protocol 45 rejected at activation",
			activation:  0,
			peerVersion: protocol.BroadcastblockProtocolVersion,
			wantErr:     "protocol version must be 46 or greater",
		},
		{
			name:        "protocol 46 accepted at activation",
			activation:  0,
			peerVersion: protocol.EVMCheckpointProtocolVersion,
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			sy, genesis := newProtocolVersionTestSync(t, test.activation)
			msg := testChainState(genesis, test.peerVersion)

			err := sy.validateChainStateMessage(msg, nil)
			if test.wantErr == "" {
				if err != nil {
					t.Fatalf("validate chain state: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("validate chain state succeeded, want %q", test.wantErr)
			}
			if !strings.Contains(err.String(), test.wantErr) {
				t.Fatalf("error = %q, want %q", err.String(), test.wantErr)
			}
		})
	}
}

func TestEnforcePeerEVMCheckpointFollowsActivation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		activation  uint64
		peerVersion uint32
		want        bool
	}{
		{
			name:        "protocol 45 before activation does not require checkpoint",
			activation:  100,
			peerVersion: protocol.BroadcastblockProtocolVersion,
			want:        false,
		},
		{
			name:        "protocol 46 before activation does not require checkpoint",
			activation:  100,
			peerVersion: protocol.EVMCheckpointProtocolVersion,
			want:        false,
		},
		{
			name:        "protocol 45 at activation requires checkpoint",
			activation:  0,
			peerVersion: protocol.BroadcastblockProtocolVersion,
			want:        true,
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			sy, genesis := newProtocolVersionTestSync(t, test.activation)
			pe := sy.Peers().Fetch(testCheckpointPeerID(t))
			pe.SetChainState(testChainState(genesis, test.peerVersion))

			if got := sy.enforcePeerEVMCheckpoint(pe); got != test.want {
				t.Fatalf("enforce checkpoint = %v, want %v", got, test.want)
			}
		})
	}
}

func TestRequiredP2PProtocolVersionFollowsActivation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		activation uint64
		layer      uint64
		want       uint32
	}{
		{
			name:       "zero activation advertises 46 from genesis",
			activation: 0,
			layer:      0,
			want:       protocol.EVMCheckpointProtocolVersion,
		},
		{
			name:       "before activation requires 45",
			activation: 100,
			layer:      99,
			want:       protocol.BroadcastblockProtocolVersion,
		},
		{
			name:       "at activation requires 46",
			activation: 100,
			layer:      100,
			want:       protocol.EVMCheckpointProtocolVersion,
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			sy, _ := newProtocolVersionTestSync(t, test.activation)

			if got := sy.p2pProtocolVersionAtLayer(test.layer); got != test.want {
				t.Fatalf("required protocol version at layer %d = %d, want %d", test.layer, got, test.want)
			}
		})
	}
}

func TestAdvertisedP2PProtocolVersionIsLatest(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		activation uint64
	}{
		{name: "before activation", activation: 100},
		{name: "at genesis activation", activation: 0},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			sy, _ := newProtocolVersionTestSync(t, test.activation)

			if got := sy.advertisedP2PProtocolVersion(); got != protocol.ProtocolVersion {
				t.Fatalf("advertised protocol version = %d, want %d", got, protocol.ProtocolVersion)
			}
			if got := sy.advertisedP2PProtocolVersion(); got != protocol.StreamReuseProtocolVersion {
				t.Fatalf("advertised protocol version = %d, want StreamReuseProtocolVersion %d", got, protocol.StreamReuseProtocolVersion)
			}
		})
	}
}

func newProtocolVersionTestSync(t *testing.T, activation uint64) (*Sync, ethcommon.Hash) {
	t.Helper()
	genesis := ethcommon.HexToHash("0x1234")
	p := newTestP2P(context.Background(), nil, &p2pcommon.Config{
		Params: &params.Params{P2PProtocol46ActivationLayer: activation},
	})
	p.genesis = genesis
	return NewSync(p), genesis
}

func testChainState(genesis ethcommon.Hash, version uint32) *pb.ChainState {
	return &pb.ChainState{
		GenesisHash:     &pb.Hash{Hash: genesis.Bytes()},
		ProtocolVersion: version,
		Services:        uint64(protocol.Full),
		GraphState:      &pb.GraphState{Total: 1},
	}
}
