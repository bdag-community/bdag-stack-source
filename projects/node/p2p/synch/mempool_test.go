package synch

import (
	"math/big"
	"testing"

	ethcommon "github.com/ethereum/go-ethereum/common"
	ethtypes "github.com/ethereum/go-ethereum/core/types"
)

func TestShouldSendMempoolInventory(t *testing.T) {
	tests := []struct {
		name           string
		requestedCount uint64
		currentCount   uint64
		want           bool
	}{
		{name: "both empty does not send", requestedCount: 0, currentCount: 0},
		{name: "empty local mempool does not send", requestedCount: 10, currentCount: 0},
		{name: "same count does not send", requestedCount: 10, currentCount: 10},
		{name: "remote empty local nonzero sends", requestedCount: 0, currentCount: 10, want: true},
		{name: "different nonzero count sends", requestedCount: 9, currentCount: 10, want: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := shouldSendMempoolInventory(tc.requestedCount, tc.currentCount); got != tc.want {
				t.Fatalf("shouldSendMempoolInventory(%d, %d) = %v, want %v", tc.requestedCount, tc.currentCount, got, tc.want)
			}
		})
	}
}

func TestBuildMempoolInventoryUsesCapacityHintAndIncludesPendingQueued(t *testing.T) {
	addr1 := ethcommon.HexToAddress("0x0000000000000000000000000000000000000001")
	addr2 := ethcommon.HexToAddress("0x0000000000000000000000000000000000000002")
	tx1 := ethtypes.NewTransaction(1, addr1, big.NewInt(1), 21000, big.NewInt(1), nil)
	tx2 := ethtypes.NewTransaction(2, addr2, big.NewInt(1), 21000, big.NewInt(1), nil)
	tx3 := ethtypes.NewTransaction(3, addr1, big.NewInt(1), 21000, big.NewInt(1), nil)

	invs := buildMempoolInventory(
		map[ethcommon.Address][]*ethtypes.Transaction{addr1: {tx1, tx2}},
		map[ethcommon.Address][]*ethtypes.Transaction{addr2: {tx3}},
		8,
	)

	if len(invs) != 3 {
		t.Fatalf("inventory length = %d, want 3", len(invs))
	}
	if cap(invs) != 8 {
		t.Fatalf("inventory capacity = %d, want capacity hint 8", cap(invs))
	}

	wantHashes := map[ethcommon.Hash]bool{
		tx1.Hash(): true,
		tx2.Hash(): true,
		tx3.Hash(): true,
	}
	for _, inv := range invs {
		if InvType(inv.Type) != InvTypeTx {
			t.Fatalf("inventory type = %v, want tx", inv.Type)
		}
		hash := ethcommon.BytesToHash(inv.Hash.Hash)
		if !wantHashes[hash] {
			t.Fatalf("unexpected inventory hash %s", hash)
		}
		delete(wantHashes, hash)
	}
	if len(wantHashes) != 0 {
		t.Fatalf("missing inventory hashes: %v", wantHashes)
	}
}

func TestBuildMempoolInventoryHandlesNegativeCapacityHint(t *testing.T) {
	invs := buildMempoolInventory(nil, nil, -1)
	if len(invs) != 0 {
		t.Fatalf("inventory length = %d, want empty", len(invs))
	}
}
