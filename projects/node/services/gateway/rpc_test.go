package gateway

import (
	"testing"
)

func TestPeekMethod(t *testing.T) {
	tests := []struct {
		input    string
		expected string
		wantErr  bool
	}{
		{`{"jsonrpc":"2.0","method":"eth_blockNumber","params":[]}`, "eth_blockNumber", false},
		{`{ "method" : "web3_clientVersion" }`, "web3_clientVersion", false},
		{`{"id":1, "method":"get_balance"}`, "get_balance", false},
		{`{"nomethod": true}`, "", true},
		{`malformed`, "", true},
	}

	for _, tt := range tests {
		got, err := PeekMethod([]byte(tt.input))
		if (err != nil) != tt.wantErr {
			t.Errorf("PeekMethod(%s) error = %v, wantErr %v", tt.input, err, tt.wantErr)
			continue
		}
		if got != tt.expected {
			t.Errorf("PeekMethod(%s) = %v, want %v", tt.input, got, tt.expected)
		}
	}
}
