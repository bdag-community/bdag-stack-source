package bdag

import (
	"math/big"
	"testing"

	mparams "github.com/BlockdagNetworkLabs/bdag/bevm/params"
)

func TestResolveEVMBlockTimestampUsesNativeHeaderTime(t *testing.T) {
	parent := uint64(1_782_929_286) // Candidate A EVM 12,916,967
	tests := []struct {
		name     string
		request  int64
		chainID  *big.Int
		expected uint64
	}{
		{
			name:     "mainnet native equal to parent is preserved",
			request:  int64(parent),
			chainID:  mparams.BdagMainnetChainConfig.ChainID,
			expected: parent,
		},
		{
			name:     "mainnet native before parent is preserved",
			request:  int64(parent - 1),
			chainID:  mparams.BdagMainnetChainConfig.ChainID,
			expected: parent - 1,
		},
		{
			name:     "mainnet Candidate A boundary gap is preserved",
			request:  1_782_929_290,
			chainID:  mparams.BdagMainnetChainConfig.ChainID,
			expected: 1_782_929_290,
		},
		{
			name:     "devnet native before parent is preserved",
			request:  int64(parent - 10),
			chainID:  big.NewInt(1043),
			expected: parent - 10,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := ResolveEVMBlockTimestamp(parent, tc.request, tc.chainID)
			if got != tc.expected {
				t.Fatalf("ResolveEVMBlockTimestamp() = %d, want %d", got, tc.expected)
			}
		})
	}
}

func TestResolveEVMBlockTimestampFallsBackForMissingTime(t *testing.T) {
	const parent = uint64(100)
	for _, request := range []int64{0, -1} {
		if got := ResolveEVMBlockTimestamp(parent, request, mparams.BdagMainnetChainConfig.ChainID); got != parent+1 {
			t.Fatalf("request %d resolved to %d, want %d", request, got, parent+1)
		}
	}
}

// TestResolveEVMBlockTimestampMatchesBdagscan pins block-to-block timestamps
// pulled live from rpc.bdagscan.com (blocks 1-9) as regression fixtures:
// canonical mainnet timestamps track the native header time verbatim, with
// irregular multi-second deltas rather than a fixed cadence.
func TestResolveEVMBlockTimestampMatchesBdagscan(t *testing.T) {
	bdagscanTimestamps := []uint64{
		1_770_761_923, // block 1
		1_770_761_928, // block 2 (+5)
		1_770_761_930, // block 3 (+2)
		1_770_761_931, // block 4 (+1)
		1_770_761_932, // block 5 (+1)
		1_770_761_936, // block 6 (+4)
		1_770_761_941, // block 7 (+5)
		1_770_761_946, // block 8 (+5)
		1_770_761_949, // block 9 (+3)
	}
	for i := 1; i < len(bdagscanTimestamps); i++ {
		parent := bdagscanTimestamps[i-1]
		want := bdagscanTimestamps[i]
		got := ResolveEVMBlockTimestamp(parent, int64(want), mparams.BdagMainnetChainConfig.ChainID)
		if got != want {
			t.Fatalf("block %d: ResolveEVMBlockTimestamp(%d, %d) = %d, want %d (bdagscan canonical)",
				i+1, parent, want, got, want)
		}
	}
}
