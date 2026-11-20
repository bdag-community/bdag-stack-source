package main

import (
	"strings"
	"testing"

	bdagrawdb "github.com/BlockdagNetworkLabs/bdag/database/rawdb"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/ethdb"
)

func snapshotAnchorFixture(t *testing.T, id, order uint64) (ethdb.Database, common.Hash) {
	t.Helper()
	db := bdagrawdb.NewMemoryDatabase()
	t.Cleanup(func() { db.Close() })
	hash := common.HexToHash("0x1234")
	writeNativeProbeBlock(t, db, nativeProbeBlock{id: id, order: order, hash: hash,
		stateRoot: common.HexToHash("0x2345"), evmRoot: common.HexToHash("0x3456")})
	if err := bdagrawdb.WriteDAGTips(db, []uint64{id}); err != nil {
		t.Fatal(err)
	}
	bdagrawdb.WriteBlockID(db, &hash, id)
	if err := bdagrawdb.WriteBlockOrderSnapshot(db, order, id); err != nil {
		t.Fatal(err)
	}
	return db, hash
}

func TestSnapshotNativeAnchorSeparatesOrderIDAndTotal(t *testing.T) {
	for _, tt := range []struct{ id, order, total uint64 }{{42, 40, 43}, {0, 0, 1}} {
		db, hash := snapshotAnchorFixture(t, tt.id, tt.order)
		order, id, err := snapshotNativeAnchor(db, hash, tt.total)
		if err != nil || order != tt.order || id != tt.id {
			t.Fatalf("anchor = (%d,%d,%v), want order %d ID %d", order, id, err, tt.order, tt.id)
		}
	}
}

func TestSnapshotNativeAnchorRejectsMissingOrInconsistentMetadata(t *testing.T) {
	for _, tt := range []struct {
		name, want string
		mutate     func(ethdb.Database, common.Hash) (common.Hash, uint64)
	}{
		{"missing-block", "or its state", func(db ethdb.Database, h common.Hash) (common.Hash, uint64) {
			if err := bdagrawdb.WriteDAGTips(db, []uint64{99}); err != nil {
				t.Fatal(err)
			}
			return h, 100
		}},
		{"wrong-best-hash", "best-chain hash", func(db ethdb.Database, h common.Hash) (common.Hash, uint64) { return common.HexToHash("0xabcd"), 43 }},
		{"wrong-hash-index", "hash index", func(db ethdb.Database, h common.Hash) (common.Hash, uint64) {
			bdagrawdb.WriteBlockID(db, &h, 41)
			return h, 43
		}},
		{"missing-hash-index", "hash index", func(db ethdb.Database, h common.Hash) (common.Hash, uint64) {
			bdagrawdb.DeleteBlockID(db, &h)
			return h, 43
		}},
		{"wrong-order-index", "order index", func(db ethdb.Database, h common.Hash) (common.Hash, uint64) {
			if err := bdagrawdb.WriteBlockOrderSnapshot(db, 40, 41); err != nil {
				t.Fatal(err)
			}
			return h, 43
		}},
		{"invalid-total", "outside DAG total", func(db ethdb.Database, h common.Hash) (common.Hash, uint64) { return h, 40 }},
	} {
		t.Run(tt.name, func(t *testing.T) {
			db, hash := snapshotAnchorFixture(t, 42, 40)
			hash, total := tt.mutate(db, hash)
			if _, _, err := snapshotNativeAnchor(db, hash, total); err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v, want %q", err, tt.want)
			}
		})
	}
	t.Run("missing-main-tip", func(t *testing.T) {
		db := bdagrawdb.NewMemoryDatabase()
		defer db.Close()
		if _, _, err := snapshotNativeAnchor(db, common.Hash{}, 1); err == nil || !strings.Contains(err.Error(), "designated native main tip") {
			t.Fatalf("error = %v", err)
		}
	})
}
