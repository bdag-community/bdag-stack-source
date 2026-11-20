package Blockdag

import (
	"testing"

	"github.com/ethereum/go-ethereum/common"
)

func TestDAGSyncBoundsLegacyEntry(t *testing.T) {
	for _, name := range []string{"zero-output", "short-continuation", "nil-locator"} {
		t.Run(name, func(t *testing.T) {
			defer func() {
				if value := recover(); value != nil {
					t.Errorf("malformed request panicked: %v", value)
				}
			}()
			ds, blocks := newDagSyncTestFixture()
			locator, mode, limit := []*common.Hash{blocks[0].GetHash()}, SubDAGMode, uint(0)
			if name == "short-continuation" {
				mode, limit = ContinueMode, 10
			}
			if name == "nil-locator" {
				locator, limit = []*common.Hash{nil}, 10
			}
			got, _ := ds.CalcSyncBlocks(nil, locator, mode, limit, true)
			if len(got) != 0 {
				t.Fatal("malformed or zero-budget request produced hashes")
			}
		})
	}
}
