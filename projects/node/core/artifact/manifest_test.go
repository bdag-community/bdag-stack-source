package artifact

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestManifestValidateAcceptsSignedCheckpoint(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	m := testManifest(t)
	sig, err := m.Sign("ops-1", priv, testNow())
	if err != nil {
		t.Fatal(err)
	}
	m.AddSignature(sig)
	if err := m.Finalize(); err != nil {
		t.Fatal(err)
	}
	if err := m.Validate(VerifyOptions{
		Network: "mainnet",
		ChainID: 1404,
		TrustedSigners: map[string]ed25519.PublicKey{
			"ops-1": pub,
		},
		Now: testNow(),
	}); err != nil {
		t.Fatalf("validate signed manifest: %v", err)
	}
}

func TestManifestRootStableAcrossSignatures(t *testing.T) {
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	m := testManifest(t)
	before, err := m.Root()
	if err != nil {
		t.Fatal(err)
	}
	sig, err := m.Sign("ops-1", priv, testNow())
	if err != nil {
		t.Fatal(err)
	}
	m.AddSignature(sig)
	after, err := m.Root()
	if err != nil {
		t.Fatal(err)
	}
	if before != after {
		t.Fatalf("root changed after signature: before=%s after=%s", before, after)
	}
}

func TestManifestValidateRejectsUnsignedByDefault(t *testing.T) {
	m := testManifest(t)
	err := m.Validate(VerifyOptions{
		Network: "mainnet",
		ChainID: 1404,
		TrustedSigners: map[string]ed25519.PublicKey{
			"ops-1": make(ed25519.PublicKey, ed25519.PublicKeySize),
		},
		Now: testNow(),
	})
	if !errors.Is(err, ErrUnsignedManifest) {
		t.Fatalf("expected ErrUnsignedManifest, got %v", err)
	}
}

func TestManifestValidateRejectsTamperedSignedManifest(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	m := testManifest(t)
	sig, err := m.Sign("ops-1", priv, testNow())
	if err != nil {
		t.Fatal(err)
	}
	m.AddSignature(sig)
	m.TipHash = "0x" + strings.Repeat("d", sha256.Size*2)
	err = m.Validate(VerifyOptions{
		Network: "mainnet",
		ChainID: 1404,
		TrustedSigners: map[string]ed25519.PublicKey{
			"ops-1": pub,
		},
		Now: testNow(),
	})
	if !errors.Is(err, ErrUnsignedManifest) {
		t.Fatalf("expected tamper to invalidate signature, got %v", err)
	}
}

func TestManifestValidateRejectsWrongNetworkAndChain(t *testing.T) {
	m := testManifest(t)
	opts := unsignedTestOptions()
	if err := m.Validate(VerifyOptions{Network: "testnet", ChainID: 1404, Now: testNow(), AllowUnsigned: true}); err == nil || !strings.Contains(err.Error(), "network mismatch") {
		t.Fatalf("expected network mismatch, got %v", err)
	}
	opts.ChainID = 1
	if err := m.Validate(opts); err == nil || !strings.Contains(err.Error(), "chain id mismatch") {
		t.Fatalf("expected chain mismatch, got %v", err)
	}
}

func TestManifestValidateRejectsDowngradeAndExpired(t *testing.T) {
	m := testManifest(t)
	opts := unsignedTestOptions()
	opts.HighestAcceptedOrder = m.TipOrder + 1
	if err := m.Validate(opts); !errors.Is(err, ErrDowngradeManifest) {
		t.Fatalf("expected downgrade, got %v", err)
	}
	m.ExpiresAt = testNow().Add(-time.Second)
	if err := m.Validate(unsignedTestOptions()); !errors.Is(err, ErrExpiredManifest) {
		t.Fatalf("expected expired, got %v", err)
	}
}

func TestManifestValidateRejectsRangeOver8192Blocks(t *testing.T) {
	m := testManifest(t)
	m.ArtifactType = KindRange
	m.StartOrder = 1
	m.EndOrder = MaxRangeArtifactBlocks + 1
	m.TipOrder = m.EndOrder
	m.ParentArtifactRoot = strings.Repeat("e", sha256.Size*2)
	err := m.Validate(unsignedTestOptions())
	if err == nil || !strings.Contains(err.Error(), "range exceeds") {
		t.Fatalf("expected range cap error, got %v", err)
	}
}

func TestManifestValidateRejectsRangeOverflow(t *testing.T) {
	m := testManifest(t)
	m.ArtifactType = KindRange
	m.StartOrder = 0
	m.EndOrder = ^uint64(0)
	m.TipOrder = ^uint64(0)
	m.ParentArtifactRoot = strings.Repeat("e", sha256.Size*2)
	err := m.Validate(unsignedTestOptions())
	if err == nil || !strings.Contains(err.Error(), "range exceeds") {
		t.Fatalf("expected range cap error, got %v", err)
	}
}

func TestManifestValidateRejectsRootMismatch(t *testing.T) {
	m := testManifest(t)
	m.ArtifactRoot = strings.Repeat("0", sha256.Size*2)
	err := m.Validate(unsignedTestOptions())
	if err == nil || !strings.Contains(err.Error(), "root mismatch") {
		t.Fatalf("expected root mismatch, got %v", err)
	}
}

func TestManifestValidateRejectsZeroValueOptionsForProduction(t *testing.T) {
	m := testManifest(t)
	err := m.Validate(VerifyOptions{AllowUnsigned: true, Now: testNow()})
	if err == nil || !strings.Contains(err.Error(), "verification network is required") {
		t.Fatalf("expected missing network error, got %v", err)
	}
}

func TestManifestValidateRejectsRangeWithoutParentRoot(t *testing.T) {
	m := testManifest(t)
	m.ArtifactType = KindRange
	m.StartOrder = 1
	m.EndOrder = 2
	m.TipOrder = 2
	err := m.Validate(unsignedTestOptions())
	if err == nil || !strings.Contains(err.Error(), "parent checkpoint") {
		t.Fatalf("expected parent checkpoint root error, got %v", err)
	}
}

func TestManifestValidateRejectsEqualHeightCheckpointEquivocation(t *testing.T) {
	m := testManifest(t)
	opts := unsignedTestOptions()
	opts.HighestAcceptedOrder = m.TipOrder
	opts.AcceptedTipHash = "0x" + strings.Repeat("d", sha256.Size*2)
	err := m.Validate(opts)
	if err == nil || !strings.Contains(err.Error(), "accepted checkpoint tip hash mismatch") {
		t.Fatalf("expected accepted checkpoint mismatch, got %v", err)
	}

	opts = unsignedTestOptions()
	opts.HighestAcceptedOrder = m.TipOrder
	opts.AcceptedStateRoot = "0x" + strings.Repeat("e", sha256.Size*2)
	err = m.Validate(opts)
	if err == nil || !strings.Contains(err.Error(), "accepted checkpoint state root mismatch") {
		t.Fatalf("expected accepted state root mismatch, got %v", err)
	}

	if err := m.Finalize(); err != nil {
		t.Fatal(err)
	}
	opts = unsignedTestOptions()
	opts.HighestAcceptedOrder = m.TipOrder
	opts.AcceptedArtifactRoot = strings.Repeat("f", sha256.Size*2)
	err = m.Validate(opts)
	if err == nil || !strings.Contains(err.Error(), "accepted checkpoint artifact root mismatch") {
		t.Fatalf("expected accepted artifact root mismatch, got %v", err)
	}
}

func TestManifestValidateRejectsDeltaUnlessAllowed(t *testing.T) {
	m := testManifest(t)
	m.ArtifactType = KindDelta
	if err := m.Validate(unsignedTestOptions()); err == nil || !strings.Contains(err.Error(), "delta artifacts are reserved") {
		t.Fatalf("expected reserved delta error, got %v", err)
	}
	opts := unsignedTestOptions()
	opts.AllowDelta = true
	if err := m.Validate(opts); err != nil {
		t.Fatalf("expected allowed delta to validate structurally, got %v", err)
	}
}

func TestManifestValidateAcceptsDirectoryLayoutFiles(t *testing.T) {
	m := testManifest(t)
	m.Layout = LayoutDirectory
	m.Files = []FileDescriptor{{
		Path:       "chain/BdagChain/000001.log",
		Class:      "chain_db",
		Size:       7,
		SHA256:     strings.Repeat("d", sha256.Size*2),
		ChunkStart: 0,
		ChunkCount: 1,
		Mode:       0o600,
	}}
	if err := m.Validate(unsignedTestOptions()); err != nil {
		t.Fatalf("expected directory layout to validate, got %v", err)
	}
}

func TestManifestValidateRejectsUnsafeDirectoryFilePaths(t *testing.T) {
	for _, badPath := range []string{
		"/chain/BdagChain/000001.log",
		"../chain/BdagChain/000001.log",
		"chain\\BdagChain\\000001.log",
	} {
		m := testManifest(t)
		m.Layout = LayoutDirectory
		m.Files = []FileDescriptor{{
			Path:       badPath,
			Class:      "chain_db",
			Size:       7,
			SHA256:     strings.Repeat("d", sha256.Size*2),
			ChunkStart: 0,
			ChunkCount: 1,
		}}
		if err := m.Validate(unsignedTestOptions()); err == nil || !strings.Contains(err.Error(), "path") {
			t.Fatalf("expected path validation error for %q, got %v", badPath, err)
		}
	}
}

func TestManifestValidateRejectsMalformedDirectoryFiles(t *testing.T) {
	tests := []struct {
		name string
		edit func(*Manifest)
		want string
	}{
		{
			name: "archive files",
			edit: func(m *Manifest) {
				m.Files = []FileDescriptor{testDirectoryFile()}
			},
			want: "archive layout must not include directory files",
		},
		{
			name: "directory without files",
			edit: func(m *Manifest) {
				m.Layout = LayoutDirectory
			},
			want: "directory layout requires files",
		},
		{
			name: "duplicate path",
			edit: func(m *Manifest) {
				m.Layout = LayoutDirectory
				m.Files = []FileDescriptor{testDirectoryFile(), testDirectoryFile()}
			},
			want: "duplicate file path",
		},
		{
			name: "missing class",
			edit: func(m *Manifest) {
				file := testDirectoryFile()
				file.Class = ""
				m.Layout = LayoutDirectory
				m.Files = []FileDescriptor{file}
			},
			want: "class is required",
		},
		{
			name: "zero size",
			edit: func(m *Manifest) {
				file := testDirectoryFile()
				file.Size = 0
				m.Layout = LayoutDirectory
				m.Files = []FileDescriptor{file}
			},
			want: "zero size",
		},
		{
			name: "bad sha",
			edit: func(m *Manifest) {
				file := testDirectoryFile()
				file.SHA256 = "bad"
				m.Layout = LayoutDirectory
				m.Files = []FileDescriptor{file}
			},
			want: "malformed sha256",
		},
		{
			name: "start without count",
			edit: func(m *Manifest) {
				file := testDirectoryFile()
				file.ChunkStart = 1
				file.ChunkCount = 0
				m.Layout = LayoutDirectory
				m.Files = []FileDescriptor{file}
			},
			want: "chunk_start without chunk_count",
		},
		{
			name: "chunk span",
			edit: func(m *Manifest) {
				file := testDirectoryFile()
				file.ChunkStart = 1
				file.ChunkCount = 1
				m.Layout = LayoutDirectory
				m.Files = []FileDescriptor{file}
			},
			want: "spans beyond chunk list",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := testManifest(t)
			tt.edit(m)
			if err := m.Validate(unsignedTestOptions()); err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("expected %q error, got %v", tt.want, err)
			}
		})
	}
}

func TestManifestValidateRejectsMalformedAnchors(t *testing.T) {
	m := testManifest(t)
	m.TipHash = "0x01"
	if err := m.Validate(unsignedTestOptions()); err == nil || !strings.Contains(err.Error(), "tip_hash") {
		t.Fatalf("expected malformed tip hash, got %v", err)
	}
	m = testManifest(t)
	m.StateRoot = "state"
	if err := m.Validate(unsignedTestOptions()); err == nil || !strings.Contains(err.Error(), "state_root") {
		t.Fatalf("expected malformed state root, got %v", err)
	}
}

func TestManifestValidateRequiresSignatureThreshold(t *testing.T) {
	pub1, priv1, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	pub2, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	m := testManifest(t)
	sig, err := m.Sign("ops-1", priv1, testNow())
	if err != nil {
		t.Fatal(err)
	}
	m.AddSignature(sig)
	err = m.Validate(VerifyOptions{
		Network:            "mainnet",
		ChainID:            1404,
		RequiredSignatures: 2,
		TrustedSigners: map[string]ed25519.PublicKey{
			"ops-1": pub1,
			"ops-2": pub2,
		},
		Now: testNow(),
	})
	if !errors.Is(err, ErrUnsignedManifest) {
		t.Fatalf("expected signature threshold failure, got %v", err)
	}
}

func TestManifestValidateRejectsRevokedSigner(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	m := testManifest(t)
	sig, err := m.Sign("ops-1", priv, testNow())
	if err != nil {
		t.Fatal(err)
	}
	m.AddSignature(sig)
	err = m.Validate(VerifyOptions{
		Network: "mainnet",
		ChainID: 1404,
		SignerPolicies: map[string]TrustedSigner{
			"ops-1": {
				PublicKey: pub,
				RevokedAt: testNow().Add(-time.Second),
			},
		},
		Now: testNow(),
	})
	if !errors.Is(err, ErrUnsignedManifest) {
		t.Fatalf("expected revoked signer to fail, got %v", err)
	}
}

func TestManifestValidateRejectsSignatureOutsideSignerWindow(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	m := testManifest(t)
	sig, err := m.Sign("ops-1", priv, testNow())
	if err != nil {
		t.Fatal(err)
	}
	m.AddSignature(sig)
	err = m.Validate(VerifyOptions{
		Network: "mainnet",
		ChainID: 1404,
		SignerPolicies: map[string]TrustedSigner{
			"ops-1": {
				PublicKey: pub,
				NotAfter:  testNow().Add(-time.Second),
			},
		},
		Now: testNow(),
	})
	if !errors.Is(err, ErrUnsignedManifest) {
		t.Fatalf("expected signer window failure, got %v", err)
	}
}

func TestVerifyChunkBody(t *testing.T) {
	body := []byte("compressed chunk body")
	desc := testChunkDescriptor("chain", 0, body)
	if err := VerifyChunkBody(desc, body); err != nil {
		t.Fatalf("verify chunk: %v", err)
	}
	if err := VerifyChunkBody(desc, []byte("bad body")); err == nil {
		t.Fatal("expected bad body to fail")
	}
}

func TestDecodeManifestRejectsOversizedAndUnknownFields(t *testing.T) {
	if _, err := DecodeManifest([]byte(`{"format_version":2}`), VerifyOptions{MaxManifestBytes: 2}); err == nil || !strings.Contains(err.Error(), "exceeds max size") {
		t.Fatalf("expected manifest size error, got %v", err)
	}
	raw := []byte(`{"format_version":2,"unknown":true}`)
	if _, err := DecodeManifest(raw, VerifyOptions{MaxManifestBytes: 1024}); err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("expected unknown field error, got %v", err)
	}
}

func TestDecodeManifestValidatesParsedManifest(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	m := testManifest(t)
	sig, err := m.Sign("ops-1", priv, testNow())
	if err != nil {
		t.Fatal(err)
	}
	m.AddSignature(sig)
	if err := m.Finalize(); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	got, err := DecodeManifest(raw, VerifyOptions{
		Network: "mainnet",
		ChainID: 1404,
		TrustedSigners: map[string]ed25519.PublicKey{
			"ops-1": pub,
		},
		Now: testNow(),
	})
	if err != nil {
		t.Fatalf("decode valid manifest: %v", err)
	}
	if got.ArtifactRoot != m.ArtifactRoot {
		t.Fatalf("root mismatch: got %s want %s", got.ArtifactRoot, m.ArtifactRoot)
	}
}

func testManifest(t *testing.T) *Manifest {
	t.Helper()
	body := []byte("chunk-0")
	m := NewManifest(KindCheckpoint, "mainnet", 1404)
	m.GenesisHash = "0x" + strings.Repeat("a", sha256.Size*2)
	m.TipOrder = 8192
	m.TipHash = "0x" + strings.Repeat("b", sha256.Size*2)
	m.BlockTotal = 9000
	m.StateRoot = "0x" + strings.Repeat("c", sha256.Size*2)
	m.CreatedAt = testNow().Add(-time.Minute)
	m.ExpiresAt = testNow().Add(time.Hour)
	m.Sources = []SourceInfo{{
		Name:       "chain",
		ChunkStart: 0,
		ChunkCount: 1,
	}}
	m.Chunks = []ChunkDescriptor{testChunkDescriptor("chain", 0, body)}
	return &m
}

func testChunkDescriptor(source string, id uint32, body []byte) ChunkDescriptor {
	compressed := sha256.Sum256(body)
	uncompressed := sha256.Sum256([]byte("uncompressed"))
	return ChunkDescriptor{
		ID:                 id,
		Source:             source,
		Class:              ChunkClassData,
		CompressedSize:     uint64(len(body)),
		UncompressedSize:   uint64(len("uncompressed")),
		CompressedSHA256:   hex.EncodeToString(compressed[:]),
		UncompressedSHA256: hex.EncodeToString(uncompressed[:]),
		Records:            1,
	}
}

func testDirectoryFile() FileDescriptor {
	return FileDescriptor{
		Path:       "chain/BdagChain/000001.log",
		Class:      "chain_db",
		Size:       7,
		SHA256:     strings.Repeat("d", sha256.Size*2),
		ChunkStart: 0,
		ChunkCount: 1,
		Mode:       0o600,
	}
}

func testNow() time.Time {
	return time.Date(2026, 5, 23, 18, 30, 0, 0, time.UTC)
}

func unsignedTestOptions() VerifyOptions {
	return VerifyOptions{
		Network:       "mainnet",
		ChainID:       1404,
		AllowUnsigned: true,
		Now:           testNow(),
	}
}
