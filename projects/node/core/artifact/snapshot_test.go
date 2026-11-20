package artifact

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"

	"github.com/BlockdagNetworkLabs/bdag/core/snapshot"
)

func TestFromSnapshotManifestBuildsCheckpointArtifact(t *testing.T) {
	sm := testSnapshotManifest()
	body := []byte("compressed-body")
	sum := sha256.Sum256(body)
	m, err := FromSnapshotManifest(sm, SnapshotOptions{
		ChainID: 1404,
		CompressedHashes: map[uint32]string{
			0: hex.EncodeToString(sum[:]),
		},
		Now: testNow(),
	})
	if err != nil {
		t.Fatalf("from snapshot: %v", err)
	}
	if m.ArtifactType != KindCheckpoint || m.Network != "mainnet" || m.ChainID != 1404 {
		t.Fatalf("unexpected manifest anchor: %+v", m)
	}
	if m.Metadata[MetadataSnapshotRoot] != sm.RootHash {
		t.Fatalf("snapshot root metadata mismatch")
	}
	if len(m.Chunks) != 1 || m.Chunks[0].CompressedSHA256 != hex.EncodeToString(sum[:]) {
		t.Fatalf("unexpected chunks: %+v", m.Chunks)
	}
	if err := m.Validate(VerifyOptions{
		Network:       "mainnet",
		ChainID:       1404,
		AllowUnsigned: true,
		Now:           testNow(),
	}); err != nil {
		t.Fatalf("validate artifact: %v", err)
	}
}

func TestFromSnapshotManifestRejectsMissingCompressedHash(t *testing.T) {
	_, err := FromSnapshotManifest(testSnapshotManifest(), SnapshotOptions{
		ChainID: 1404,
		Now:     testNow(),
	})
	if err == nil || !strings.Contains(err.Error(), "missing compressed hash") {
		t.Fatalf("expected missing compressed hash error, got %v", err)
	}
}

func TestFromSnapshotManifestUsesEmbeddedCompressedHash(t *testing.T) {
	sm := testSnapshotManifest()
	sm.Chunks[0].CompressedSHA256 = strings.Repeat("e", sha256.Size*2)
	m, err := FromSnapshotManifest(sm, SnapshotOptions{
		ChainID: 1404,
		Now:     testNow(),
	})
	if err != nil {
		t.Fatalf("from snapshot: %v", err)
	}
	if got := m.Chunks[0].CompressedSHA256; got != sm.Chunks[0].CompressedSHA256 {
		t.Fatalf("compressed hash mismatch: got %s want %s", got, sm.Chunks[0].CompressedSHA256)
	}
}

func TestFromSnapshotManifestSplitsEVMAncientSegments(t *testing.T) {
	sm := testSnapshotManifest()
	sm.Chunks[0].CompressedSHA256 = strings.Repeat("e", sha256.Size*2)
	sm.EVMStateAncientGzipBytes = 5
	sm.EVMStateAncientSha256 = strings.Repeat("f", sha256.Size*2)

	m, err := FromSnapshotManifest(sm, SnapshotOptions{
		ChainID: 1404,
		EVMAncientSegments: []snapshot.EVMAncientSegmentInfo{
			{Offset: 0, Size: 3, SHA256: strings.Repeat("1", sha256.Size*2)},
			{Offset: 3, Size: 2, SHA256: strings.Repeat("2", sha256.Size*2)},
		},
		Now: testNow(),
	})
	if err != nil {
		t.Fatalf("from snapshot: %v", err)
	}
	if len(m.Chunks) != 3 {
		t.Fatalf("expected 3 artifact chunks, got %d", len(m.Chunks))
	}
	if m.Sources[len(m.Sources)-1].Name != ChunkClassEVMAncientGz || m.Sources[len(m.Sources)-1].ChunkCount != 2 {
		t.Fatalf("unexpected EVM ancient source: %+v", m.Sources[len(m.Sources)-1])
	}
	if m.Chunks[1].Class != ChunkClassEVMAncientGz || m.Chunks[1].CompressedSize != 3 {
		t.Fatalf("unexpected first EVM segment: %+v", m.Chunks[1])
	}
	if m.Chunks[2].Class != ChunkClassEVMAncientGz || m.Chunks[2].CompressedSize != 2 {
		t.Fatalf("unexpected second EVM segment: %+v", m.Chunks[2])
	}
	if err := m.Validate(VerifyOptions{
		Network:       "mainnet",
		ChainID:       1404,
		AllowUnsigned: true,
		Now:           testNow(),
	}); err != nil {
		t.Fatalf("validate artifact: %v", err)
	}
}

func testSnapshotManifest() *snapshot.Manifest {
	return &snapshot.Manifest{
		FormatVersion: snapshot.FormatVersion,
		Network:       "mainnet",
		CreatedAt:     testNow(),
		TipHash:       "0x" + strings.Repeat("b", sha256.Size*2),
		TipOrder:      8192,
		TipID:         123,
		BlockTotal:    9000,
		StateRoot:     "0x" + strings.Repeat("c", sha256.Size*2),
		TotalKeys:     1,
		Sources: []snapshot.SourceInfo{{
			Name:       snapshot.SourceChain,
			ChunkStart: 0,
			ChunkCount: 1,
		}},
		Chunks: []snapshot.ChunkInfo{{
			ID:               0,
			UncompressedSize: 10,
			CompressedSize:   15,
			SHA256:           strings.Repeat("a", sha256.Size*2),
			Records:          1,
		}},
		RootHash: strings.Repeat("d", sha256.Size*2),
	}
}
