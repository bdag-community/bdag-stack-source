package params

import (
	"testing"

	"github.com/BlockdagNetworkLabs/bdag/core/protocol"
)

func TestRequiredP2PProtocolVersion(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		params *Params
		layer  uint64
		want   uint32
	}{
		{
			name:   "nil params requires current protocol",
			params: nil,
			layer:  99,
			want:   protocol.ProtocolVersion,
		},
		{
			name:   "zero activation layer requires 46 from genesis",
			params: &Params{P2PProtocol46ActivationLayer: 0},
			layer:  0,
			want:   protocol.EVMCheckpointProtocolVersion,
		},
		{
			name:   "before activation requires 45",
			params: &Params{P2PProtocol46ActivationLayer: 100},
			layer:  99,
			want:   protocol.BroadcastblockProtocolVersion,
		},
		{
			name:   "at activation requires 46",
			params: &Params{P2PProtocol46ActivationLayer: 100},
			layer:  100,
			want:   protocol.EVMCheckpointProtocolVersion,
		},
		{
			name:   "after activation requires 46",
			params: &Params{P2PProtocol46ActivationLayer: 100},
			layer:  101,
			want:   protocol.EVMCheckpointProtocolVersion,
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if got := test.params.RequiredP2PProtocolVersion(test.layer); got != test.want {
				t.Fatalf("required version = %d, want %d", got, test.want)
			}
		})
	}
}
