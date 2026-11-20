package evmchaindb

import (
	"path/filepath"
	"testing"
)

// TestOpenCreatesAndReopens checks that Open initialises a fresh DB layout
// (with freezer) and can re-open it read-only afterwards. This implicitly
// covers the path-resolution constants too.
func TestOpenCreatesAndReopens(t *testing.T) {
	dataDir := t.TempDir()

	// Initial open in write mode creates the directory tree.
	db, err := Open(dataDir, Options{Type: "leveldb"})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := db.Put([]byte("k"), []byte("v")); err != nil {
		t.Fatalf("put: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	// Verify the on-disk layout matches what the rest of the codebase
	// (bevm, snap-tool, …) expects.
	want := filepath.Join(dataDir, "bdageth", "chaindata")
	if got := Path(dataDir); got != want {
		t.Errorf("Path: got %q want %q", got, want)
	}
	wantAnc := filepath.Join(want, "ancient")
	if got := AncientsPath(dataDir); got != wantAnc {
		t.Errorf("AncientsPath: got %q want %q", got, wantAnc)
	}

	// Re-open read-only and verify the value is still there.
	roDB, err := Open(dataDir, Options{ReadOnly: true})
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer roDB.Close()
	got, err := roDB.Get([]byte("k"))
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if string(got) != "v" {
		t.Errorf("get: got %q want %q", string(got), "v")
	}
}

// TestOpenReadOnlyMissingFails ensures that a read-only open of a
// non-existent data dir produces a clear error rather than silently
// creating an empty DB (which would mask configuration mistakes).
func TestOpenReadOnlyMissingFails(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "does-not-exist")
	if _, err := Open(dir, Options{ReadOnly: true}); err == nil {
		t.Fatal("expected error opening missing read-only DB")
	}
}
