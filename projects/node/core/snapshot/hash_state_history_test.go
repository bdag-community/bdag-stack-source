package snapshot

import (
	"bytes"
	"compress/gzip"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/ethdb"
)

type historyTestDB struct {
	ethdb.Database
	ancient string
}

func (db *historyTestDB) AncientDatadir() (string, error) { return db.ancient, nil }

func newHistoryTestDB(t *testing.T, scheme string) *historyTestDB {
	t.Helper()
	db := &historyTestDB{Database: rawdb.NewMemoryDatabase(), ancient: t.TempDir()}
	t.Cleanup(func() { db.Close() })
	switch scheme {
	case rawdb.HashScheme:
		root := common.HexToHash("0x1234")
		header := &types.Header{Number: big.NewInt(0), Difficulty: big.NewInt(1), Root: root}
		rawdb.WriteHeader(db, header)
		rawdb.WriteCanonicalHash(db, header.Hash(), 0)
		rawdb.WriteLegacyTrieNode(db, root, []byte("genesis-state"))
	case rawdb.PathScheme:
		rawdb.WriteAccountTrieNode(db, nil, []byte("path-state"))
	}
	if got := rawdb.ReadStateScheme(db); got != scheme {
		t.Fatalf("fixture scheme %q != %q", got, scheme)
	}
	return db
}

func TestHashStateOptionalHistory(t *testing.T) {
	for _, scheme := range []string{rawdb.HashScheme, rawdb.PathScheme, ""} {
		for _, layout := range []string{"absent", "empty", "flock", "nonzero-lock", "corrupt", "symlink", "lock-symlink", "not-directory"} {
			t.Run(scheme+"/"+layout, func(t *testing.T) {
				db := newHistoryTestDB(t, scheme)
				dir := filepath.Join(db.ancient, "state")
				if layout != "absent" && layout != "symlink" && layout != "not-directory" {
					if err := os.Mkdir(dir, 0700); err != nil {
						t.Fatal(err)
					}
				}
				var err error
				switch layout {
				case "flock":
					err = os.WriteFile(filepath.Join(dir, "FLOCK"), nil, 0600)
				case "nonzero-lock":
					err = os.WriteFile(filepath.Join(dir, "FLOCK"), []byte("evidence"), 0600)
				case "corrupt":
					err = os.WriteFile(filepath.Join(dir, "history.meta.ridx"), []byte("bad"), 0600)
				case "symlink":
					err = os.Symlink(t.TempDir(), dir)
				case "lock-symlink":
					err = os.Symlink(filepath.Join(db.ancient, "missing"), filepath.Join(dir, "FLOCK"))
				case "not-directory":
					err = os.WriteFile(dir, nil, 0600)
				}
				if err != nil {
					t.Fatal(err)
				}
				wantSkip := scheme == rawdb.HashScheme && (layout == "absent" || layout == "empty" || layout == "flock")
				skip, guardErr := emptyHashStateHistory(db, db.ancient)
				if skip != wantSkip {
					t.Fatalf("skip=%v err=%v want=%v", skip, guardErr, wantSkip)
				}
				// Exercise the original failing call, not just the new predicate.
				useTar, err := exportEVMStateHistoryFreezer(db, func(_, _ []byte) error { t.Fatal("unexpected row"); return nil })
				if useTar || (err == nil) != wantSkip {
					t.Fatalf("tar=%v err=%v want success=%v", useTar, err, wantSkip)
				}
				if wantSkip {
					entries, err := os.ReadDir(dir)
					if layout == "absent" && !os.IsNotExist(err) {
						t.Fatal("created absent history")
					}
					if layout == "empty" && (err != nil || len(entries) != 0) {
						t.Fatal("created history files")
					}
					if layout == "flock" && (err != nil || len(entries) != 1 || entries[0].Name() != "FLOCK") {
						t.Fatal("modified lock-only history")
					}
				}
			})
		}
	}
}

func TestNonemptyStateHistoryPreserved(t *testing.T) {
	for _, scheme := range []string{rawdb.HashScheme, rawdb.PathScheme} {
		for _, tail := range []uint64{0, 1} {
			t.Run(fmt.Sprintf("%s/tail%d", scheme, tail), func(t *testing.T) {
				db := newHistoryTestDB(t, scheme)
				freezer, err := rawdb.NewStateFreezer(db.ancient, false, false)
				if err != nil {
					t.Fatal(err)
				}
				_, err = freezer.ModifyAncients(func(op ethdb.AncientWriteOp) error {
					for n := uint64(0); n < 3; n++ {
						for _, kind := range evmStateHistoryExportOrder {
							if err := op.AppendRaw(kind, n, []byte(fmt.Sprintf("%s/%d", kind, n))); err != nil {
								return err
							}
						}
					}
					return nil
				})
				if err != nil {
					t.Fatal(err)
				}
				if tail != 0 {
					if _, err := freezer.TruncateTail(tail); err != nil {
						t.Fatal(err)
					}
				}
				if err := freezer.Close(); err != nil {
					t.Fatal(err)
				}
				if skip, err := emptyHashStateHistory(db, db.ancient); skip || err != nil {
					t.Fatalf("nonempty skip=%v err=%v", skip, err)
				}
				count := 0
				useTar, err := exportEVMStateHistoryFreezer(db, func(key, value []byte) error {
					kind, n, ok := decodeAncientSnapKey(key)
					if !ok || string(value) != fmt.Sprintf("%s/%d", kind, n) {
						t.Fatal("history changed")
					}
					count++
					return nil
				})
				if err != nil || useTar != (tail > 0) {
					t.Fatalf("tar=%v err=%v", useTar, err)
				}
				if (tail == 0 && count != 15) || (tail > 0 && count != 0) {
					t.Fatalf("history count=%d", count)
				}
				archive := filepath.Join(t.TempDir(), "history.bdsnap")
				manifest, err := MultiWrite(archive, []Source{{Name: SourceEVM, DB: db}}, BuildInfo{Network: "unittest"}, WriteOptions{CompressionLevel: gzip.BestSpeed})
				if err != nil {
					t.Fatal(err)
				}
				if (manifest.EVMStateAncientGzipBytes > 0) != (tail > 0) {
					t.Fatal("state-history appendix handling changed")
				}
				if _, err := Verify(archive); err != nil {
					t.Fatal(err)
				}
			})
		}
	}
}

// Both native and EVM chain-freezer records pass through the unchanged v4
// writer/reader at BestSpeed, alongside every KV record and hash-state root.
type roundTripAncientDB struct {
	*historyTestDB
	kinds []string
	rows  map[string][]byte
}

func (db *roundTripAncientDB) Ancients() (uint64, error) {
	if len(db.rows) == 0 {
		return 0, nil
	}
	return 2, nil
}
func (db *roundTripAncientDB) Tail() (uint64, error) { return 0, nil }
func (db *roundTripAncientDB) Ancient(kind string, n uint64) ([]byte, error) {
	v, ok := db.rows[fmt.Sprintf("%s/%d", kind, n)]
	if !ok {
		return nil, fmt.Errorf("missing ancient %s/%d", kind, n)
	}
	return v, nil
}
func (db *roundTripAncientDB) ModifyAncients(fn func(ethdb.AncientWriteOp) error) (int64, error) {
	return 0, fn(roundTripAncientOp{db})
}

type roundTripAncientOp struct{ db *roundTripAncientDB }

func (op roundTripAncientOp) Append(string, uint64, interface{}) error {
	return fmt.Errorf("unexpected encoded append")
}
func (op roundTripAncientOp) AppendRaw(kind string, n uint64, value []byte) error {
	op.db.rows[fmt.Sprintf("%s/%d", kind, n)] = bytes.Clone(value)
	return nil
}

func TestHashStateBestSpeedFullRoundTrip(t *testing.T) {
	sources := []Source{}
	dests := map[string]ethdb.Database{}
	for _, name := range []string{SourceChain, SourceEVM} {
		kinds := bdagChainFreezerExportOrder
		if name == SourceEVM {
			kinds = evmChainFreezerExportOrder
		}
		src := &roundTripAncientDB{newHistoryTestDB(t, rawdb.HashScheme), kinds, map[string][]byte{}}
		dst := &roundTripAncientDB{newHistoryTestDB(t, ""), kinds, map[string][]byte{}}
		for n := uint64(0); n < 2; n++ {
			for _, kind := range kinds {
				key := fmt.Sprintf("%s/%d", kind, n)
				src.rows[key] = []byte(name + "/" + key)
			}
		}
		if err := src.Put([]byte(name+"-key"), []byte(name+"-value")); err != nil {
			t.Fatal(err)
		}
		sources = append(sources, Source{Name: name, DB: src})
		dests[name] = dst
	}
	archive := filepath.Join(t.TempDir(), "best-speed.bdsnap")
	if _, err := MultiWrite(archive, sources, BuildInfo{Network: "unittest"}, WriteOptions{CompressionLevel: gzip.BestSpeed, ChunkTargetBytes: 64}); err != nil {
		t.Fatal(err)
	}
	if _, err := Verify(archive); err != nil {
		t.Fatal(err)
	}
	if _, err := MultiRead(archive, dests, ReadOptions{Network: "unittest", RequireEmpty: true}); err != nil {
		t.Fatal(err)
	}
	for _, source := range sources {
		src := source.DB.(*roundTripAncientDB)
		dst := dests[source.Name].(*roundTripAncientDB)
		iter := src.NewIterator(nil, nil)
		for iter.Next() {
			got, err := dst.Get(iter.Key())
			if err != nil || !bytes.Equal(got, iter.Value()) {
				t.Fatalf("KV mismatch: %x", iter.Key())
			}
		}
		if err := iter.Error(); err != nil {
			t.Fatal(err)
		}
		iter.Release()
		if len(src.rows) != len(dst.rows) {
			t.Fatal("freezer row count changed")
		}
		for key, val := range src.rows {
			if !bytes.Equal(val, dst.rows[key]) {
				t.Fatalf("freezer mismatch %s", key)
			}
		}
	}
}
