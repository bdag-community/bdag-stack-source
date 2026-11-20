package snapshot

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/ethdb"
)

// TestRoundTrip exercises the full export -> import path on an in-memory
// database. It asserts that every key in the source DB is faithfully
// reproduced in the destination DB and that manifest integrity checks
// pass.
func TestRoundTrip(t *testing.T) {
	src := rawdb.NewMemoryDatabase()
	defer src.Close()

	// Populate the source DB with a mixture of small and medium records
	// using a few distinct key prefixes so we exercise iterator ordering.
	expected := make(map[string][]byte)
	for i := 0; i < 1024; i++ {
		k := []byte(fmt.Sprintf("blk:%08d", i))
		v := bytes.Repeat([]byte{byte(i)}, 64+(i%128))
		if err := src.Put(k, v); err != nil {
			t.Fatalf("put: %v", err)
		}
		expected[string(k)] = v
	}
	for i := 0; i < 16; i++ {
		k := []byte(fmt.Sprintf("meta:%02d", i))
		v := []byte(fmt.Sprintf("value-%d", i))
		if err := src.Put(k, v); err != nil {
			t.Fatalf("put: %v", err)
		}
		expected[string(k)] = v
	}

	dir := t.TempDir()
	archive := filepath.Join(dir, "test"+ArchiveFileSuffix)

	info := BuildInfo{
		Network:     "unittest",
		BDAGVersion: "test",
		DBEngine:    "memdb",
		TipHash:     "0xdeadbeef",
		TipOrder:    42,
		TipID:       43,
		BlockTotal:  44,
		StateRoot:   "0xabc",
	}
	// Use a small chunk target so the test actually produces multiple
	// chunks (catching any chunking-related bugs).
	manifest, err := Write(archive, src, info, WriteOptions{ChunkTargetBytes: 4096})
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	if len(manifest.Chunks) < 2 {
		t.Fatalf("expected multiple chunks, got %d", len(manifest.Chunks))
	}
	if manifest.TotalKeys != uint64(len(expected)) {
		t.Fatalf("manifest key count %d != expected %d", manifest.TotalKeys, len(expected))
	}

	// Side-car manifest roundtrip.
	sidecar := archive + ManifestFileSuffix
	if err := WriteManifestFile(sidecar, manifest); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	if _, err := os.Stat(sidecar); err != nil {
		t.Fatalf("sidecar missing: %v", err)
	}

	// Verify pass — this re-reads and re-hashes every chunk.
	if _, err := Verify(archive); err != nil {
		t.Fatalf("verify: %v", err)
	}

	dst := rawdb.NewMemoryDatabase()
	defer dst.Close()

	if _, err := Read(archive, dst, ReadOptions{Network: "unittest", RequireEmpty: true}); err != nil {
		t.Fatalf("read: %v", err)
	}
	for k, v := range expected {
		got, err := dst.Get([]byte(k))
		if err != nil {
			t.Fatalf("get %s: %v", k, err)
		}
		if !bytes.Equal(got, v) {
			t.Fatalf("value mismatch for %s", k)
		}
	}
}

// TestRejectNetworkMismatch verifies that import aborts when the caller's
// configured network does not match the snapshot.
func TestRejectNetworkMismatch(t *testing.T) {
	src := rawdb.NewMemoryDatabase()
	defer src.Close()
	if err := src.Put([]byte("k"), []byte("v")); err != nil {
		t.Fatalf("put: %v", err)
	}
	archive := filepath.Join(t.TempDir(), "n"+ArchiveFileSuffix)
	if _, err := Write(archive, src, BuildInfo{Network: "mainnet"}, WriteOptions{}); err != nil {
		t.Fatalf("write: %v", err)
	}
	dst := rawdb.NewMemoryDatabase()
	defer dst.Close()
	if _, err := Read(archive, dst, ReadOptions{Network: "testnet"}); err == nil {
		t.Fatalf("expected network mismatch error")
	}
}

// TestRejectNonEmptyDestination verifies the RequireEmpty guard.
func TestRejectNonEmptyDestination(t *testing.T) {
	src := rawdb.NewMemoryDatabase()
	defer src.Close()
	if err := src.Put([]byte("k"), []byte("v")); err != nil {
		t.Fatalf("put: %v", err)
	}
	archive := filepath.Join(t.TempDir(), "e"+ArchiveFileSuffix)
	if _, err := Write(archive, src, BuildInfo{Network: "net"}, WriteOptions{}); err != nil {
		t.Fatalf("write: %v", err)
	}
	dst := rawdb.NewMemoryDatabase()
	defer dst.Close()
	if err := dst.Put([]byte("already"), []byte("here")); err != nil {
		t.Fatalf("put: %v", err)
	}
	if _, err := Read(archive, dst, ReadOptions{Network: "net", RequireEmpty: true}); err == nil {
		t.Fatalf("expected non-empty dst error")
	}
}

// TestMultiSourceRoundTrip exports two logical databases (chain + EVM)
// into a single archive and imports them into distinct destinations.
// It covers the boundary-alignment guarantees: chunks from source A
// MUST NOT leak into destination B.
func TestMultiSourceRoundTrip(t *testing.T) {
	chain := rawdb.NewMemoryDatabase()
	evm := rawdb.NewMemoryDatabase()
	defer chain.Close()
	defer evm.Close()

	chainExpected := map[string][]byte{}
	evmExpected := map[string][]byte{}
	for i := 0; i < 300; i++ {
		k := []byte(fmt.Sprintf("chain:%05d", i))
		v := bytes.Repeat([]byte{byte(i % 251)}, 48)
		if err := chain.Put(k, v); err != nil {
			t.Fatalf("chain put: %v", err)
		}
		chainExpected[string(k)] = v
	}
	for i := 0; i < 500; i++ {
		k := []byte(fmt.Sprintf("evm:%05d", i))
		v := bytes.Repeat([]byte{byte((i * 7) % 251)}, 60)
		if err := evm.Put(k, v); err != nil {
			t.Fatalf("evm put: %v", err)
		}
		evmExpected[string(k)] = v
	}

	dir := t.TempDir()
	archive := filepath.Join(dir, "multi"+ArchiveFileSuffix)
	// Force several chunks per source to stress boundary flushing.
	m, err := MultiWrite(archive, []Source{
		{Name: SourceChain, DB: chain},
		{Name: SourceEVM, DB: evm},
	}, BuildInfo{Network: "unittest"}, WriteOptions{ChunkTargetBytes: 2048})
	if err != nil {
		t.Fatalf("multiwrite: %v", err)
	}
	if len(m.Sources) != 2 {
		t.Fatalf("want 2 sources, got %d", len(m.Sources))
	}
	if m.Sources[0].Name != SourceChain || m.Sources[1].Name != SourceEVM {
		t.Fatalf("unexpected source order: %+v", m.Sources)
	}
	if m.Sources[0].ChunkStart != 0 || m.Sources[1].ChunkStart != m.Sources[0].ChunkCount {
		t.Fatalf("chunk ranges not contiguous: %+v", m.Sources)
	}
	if m.Sources[0].TotalKeys != uint64(len(chainExpected)) {
		t.Fatalf("chain total keys mismatch: %d vs %d", m.Sources[0].TotalKeys, len(chainExpected))
	}
	if m.Sources[1].TotalKeys != uint64(len(evmExpected)) {
		t.Fatalf("evm total keys mismatch: %d vs %d", m.Sources[1].TotalKeys, len(evmExpected))
	}

	chainOut := rawdb.NewMemoryDatabase()
	evmOut := rawdb.NewMemoryDatabase()
	defer chainOut.Close()
	defer evmOut.Close()

	if _, err := MultiRead(archive, map[string]ethdb.Database{
		SourceChain: chainOut,
		SourceEVM:   evmOut,
	}, ReadOptions{Network: "unittest", RequireEmpty: true}); err != nil {
		t.Fatalf("multiread: %v", err)
	}

	for k, v := range chainExpected {
		got, err := chainOut.Get([]byte(k))
		if err != nil {
			t.Fatalf("chain get %s: %v", k, err)
		}
		if !bytes.Equal(got, v) {
			t.Fatalf("chain value mismatch for %s", k)
		}
		if _, err := evmOut.Get([]byte(k)); err == nil {
			t.Fatalf("chain key %s leaked into evm destination", k)
		}
	}
	for k, v := range evmExpected {
		got, err := evmOut.Get([]byte(k))
		if err != nil {
			t.Fatalf("evm get %s: %v", k, err)
		}
		if !bytes.Equal(got, v) {
			t.Fatalf("evm value mismatch for %s", k)
		}
	}
}

// TestReadRejectsMultiSource ensures the simple single-DB Read refuses
// to quietly drop sources it does not know about.
func TestReadRejectsMultiSource(t *testing.T) {
	chain := rawdb.NewMemoryDatabase()
	evm := rawdb.NewMemoryDatabase()
	defer chain.Close()
	defer evm.Close()
	_ = chain.Put([]byte("c"), []byte("1"))
	_ = evm.Put([]byte("e"), []byte("2"))

	archive := filepath.Join(t.TempDir(), "r"+ArchiveFileSuffix)
	if _, err := MultiWrite(archive, []Source{
		{Name: SourceChain, DB: chain},
		{Name: SourceEVM, DB: evm},
	}, BuildInfo{Network: "net"}, WriteOptions{}); err != nil {
		t.Fatalf("multiwrite: %v", err)
	}

	dst := rawdb.NewMemoryDatabase()
	defer dst.Close()
	if _, err := Read(archive, dst, ReadOptions{Network: "net"}); err == nil {
		t.Fatalf("expected Read to refuse multi-source archive")
	}
}

// TestMultiReadMissingDestination makes sure a caller that forgets to
// provide a dest for a declared source sees a hard failure rather than
// silent data loss.
func TestMultiReadMissingDestination(t *testing.T) {
	chain := rawdb.NewMemoryDatabase()
	evm := rawdb.NewMemoryDatabase()
	defer chain.Close()
	defer evm.Close()
	_ = chain.Put([]byte("c"), []byte("1"))
	_ = evm.Put([]byte("e"), []byte("2"))

	archive := filepath.Join(t.TempDir(), "m"+ArchiveFileSuffix)
	if _, err := MultiWrite(archive, []Source{
		{Name: SourceChain, DB: chain},
		{Name: SourceEVM, DB: evm},
	}, BuildInfo{Network: "net"}, WriteOptions{}); err != nil {
		t.Fatalf("multiwrite: %v", err)
	}

	only := rawdb.NewMemoryDatabase()
	defer only.Close()
	if _, err := MultiRead(archive, map[string]ethdb.Database{SourceChain: only}, ReadOptions{Network: "net", RequireEmpty: true}); err == nil {
		t.Fatalf("expected missing-destination error")
	}
}

// TestManifestValidate rejects malformed chunk lists.
func TestManifestValidate(t *testing.T) {
	m := &Manifest{
		FormatVersion: FormatVersion,
		Network:       "net",
		Chunks: []ChunkInfo{
			{ID: 0, UncompressedSize: 1, SHA256: "00"}, // bad sha256 length
		},
	}
	if err := m.Validate("net"); err == nil {
		t.Fatalf("expected sha256 length error")
	}

	m.Chunks[0].SHA256 = "00000000000000000000000000000000000000000000000000000000000000ff"
	m.Chunks = append(m.Chunks, ChunkInfo{ID: 5, UncompressedSize: 1, SHA256: m.Chunks[0].SHA256})
	if err := m.Validate("net"); err == nil {
		t.Fatalf("expected non-sequential id error")
	}
}
