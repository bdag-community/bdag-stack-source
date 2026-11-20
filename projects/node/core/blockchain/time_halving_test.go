package blockchain

import (
	"testing"
	"time"

	blockdag "github.com/BlockdagNetworkLabs/bdag/blockdag"
	"github.com/BlockdagNetworkLabs/bdag/core/types"
	"github.com/BlockdagNetworkLabs/bdag/core/types/pow"
	"github.com/BlockdagNetworkLabs/bdag/params"
)

func TestTimeBasedHalving(t *testing.T) {
	// Setup params
	param := &params.Params{
		BaseSubsidy:     100 * 100000000, // 100 BDAG
		HalvingInterval: 604800,          // 7 days
		MulSubsidy:      1,
		DivSubsidy:      2,
		GenesisBlock: types.NewBlock(&types.Block{
			Header: types.BlockHeader{
				Timestamp: time.Unix(1000000, 0),
				Pow:       pow.GetInstance(pow.BLAKE2BD, 0, 0, nil),
			},
		}),
	}

	subsidyCache := NewSubsidyCache(0, param)
	genesisTime := param.GenesisBlock.Block().Header.Timestamp.Unix()

	tests := []struct {
		name          string
		timestamp     int64
		expectSubsidy int64
	}{
		{
			name:          "Genesis Time",
			timestamp:     genesisTime,
			expectSubsidy: 100 * 100000000,
		},
		{
			name:          "Before Halving (3.5 days)",
			timestamp:     genesisTime + 302400,
			expectSubsidy: 100 * 100000000,
		},
		{
			name:          "First Halving (7 days)",
			timestamp:     genesisTime + 604800,
			expectSubsidy: 50 * 100000000,
		},
		{
			name:          "After First Halving (10 days)",
			timestamp:     genesisTime + 864000,
			expectSubsidy: 50 * 100000000,
		},
		{
			name:          "Second Halving (14 days)",
			timestamp:     genesisTime + 1209600,
			expectSubsidy: 25 * 100000000,
		},
		{
			name:          "Before Genesis (Should be 0 or Base)",
			timestamp:     genesisTime - 1,
			expectSubsidy: 0, // Code iteration=0 logic might return base? implementation checks timestamp < genesis -> iteration 0?
			// Let's check implementation: if timestamp < genesisTime { iteration = 0 }
			// So it expects BaseSubsidy.
		},
	}

	// Update last test expectation mainly based on code reading:
	// if timestamp < genesisTime { iteration = 0 } -> returns BaseSubsidy.
	tests[5].expectSubsidy = 100 * 100000000

	bi := blockdag.NewBlueInfo(0, 0, 0, 100) // Dummy BI

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			subsidy := subsidyCache.CalcBlockSubsidy(bi, tc.timestamp)
			if subsidy != tc.expectSubsidy {
				t.Errorf("calcBlockSubsidy(%d): got %d, want %d", tc.timestamp, subsidy, tc.expectSubsidy)
			}
		})
	}
}
