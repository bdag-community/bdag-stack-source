package artifact

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestDownloadDirectoryArtifactFetchesManifestFiles(t *testing.T) {
	files := map[string][]byte{
		"chain/BdagChain/000001.ldb": []byte("hello world"),
		"evm/bdageth/chaindata/x":    []byte("state payload"),
	}
	m := testDirectoryManifest(t, files)
	staging := t.TempDir()

	report, err := DownloadDirectoryArtifact(context.Background(), m, testDirectoryHaves(m, "peer-a"), mapFileChunkFetcher(files), staging, DirectoryDownloadOptions{
		ChunkBytes:  5,
		Parallelism: 2,
		Now:         testNow,
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.CompletedFiles != len(files) || report.SkippedFiles != 0 || report.RequestedChunks != 6 {
		t.Fatalf("unexpected report: %+v", report)
	}
	if _, err := VerifyDirectoryArtifact(context.Background(), m, staging, DirectoryVerifyOptions{
		Manifest: unsignedTestOptions(),
		Now:      testNow,
	}); err != nil {
		t.Fatal(err)
	}
}

func TestDownloadDirectoryArtifactSkipsVerifiedFiles(t *testing.T) {
	files := map[string][]byte{
		"chain/BdagChain/000001.ldb": []byte("already present"),
	}
	m := testDirectoryManifest(t, files)
	staging := t.TempDir()
	writeDirectoryArtifactFiles(t, staging, m, files)

	var calls int
	fetcher := FileChunkFetcherFunc(func(context.Context, FileChunkFetchRequest) ([]byte, error) {
		calls++
		return nil, fmt.Errorf("unexpected fetch")
	})
	report, err := DownloadDirectoryArtifact(context.Background(), m, testDirectoryHaves(m, "peer-a"), fetcher, staging, DirectoryDownloadOptions{
		ChunkBytes: 4,
		Now:        testNow,
	})
	if err != nil {
		t.Fatal(err)
	}
	if calls != 0 || report.SkippedFiles != 1 || report.CompletedFiles != 0 || report.RequestedChunks != 0 {
		t.Fatalf("unexpected skip behavior calls=%d report=%+v", calls, report)
	}
}

func TestDownloadDirectoryArtifactDoesNotRequireProvidersForVerifiedFiles(t *testing.T) {
	files := map[string][]byte{
		"chain/BdagChain/000001.ldb": []byte("already present"),
	}
	m := testDirectoryManifest(t, files)
	staging := t.TempDir()
	writeDirectoryArtifactFiles(t, staging, m, files)

	report, err := DownloadDirectoryArtifact(context.Background(), m, nil, mapFileChunkFetcher(files), staging, DirectoryDownloadOptions{
		ChunkBytes: 4,
		Now:        testNow,
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.SkippedFiles != 1 || report.CompletedFiles != 0 {
		t.Fatalf("unexpected report: %+v", report)
	}
}

func TestDownloadDirectoryArtifactRetriesPeerFailures(t *testing.T) {
	files := map[string][]byte{
		"chain/BdagChain/000001.ldb": []byte("retry me"),
	}
	m := testDirectoryManifest(t, files)
	var mu sync.Mutex
	calls := make(map[string]int)
	fetcher := FileChunkFetcherFunc(func(ctx context.Context, req FileChunkFetchRequest) ([]byte, error) {
		mu.Lock()
		calls[req.PeerID]++
		mu.Unlock()
		if req.PeerID == "bad-peer" {
			return nil, fmt.Errorf("simulated peer failure")
		}
		return mapFileChunkFetcher(files).FetchArtifactFileChunk(ctx, req)
	})

	report, err := DownloadDirectoryArtifact(context.Background(), m, testDirectoryHaves(m, "bad-peer", "good-peer"), fetcher, t.TempDir(), DirectoryDownloadOptions{
		ChunkBytes: 4,
		RetryLimit: 2,
		Now:        testNow,
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.PeerFailures["bad-peer"] != 1 || calls["good-peer"] == 0 {
		t.Fatalf("expected retry on good peer, calls=%v report=%+v", calls, report)
	}
}

func TestDownloadDirectoryArtifactRejectsCorruptFile(t *testing.T) {
	files := map[string][]byte{
		"chain/BdagChain/000001.ldb": []byte("correct"),
	}
	m := testDirectoryManifest(t, files)
	fetcher := FileChunkFetcherFunc(func(context.Context, FileChunkFetchRequest) ([]byte, error) {
		return []byte("corrupt"), nil
	})

	_, err := DownloadDirectoryArtifact(context.Background(), m, testDirectoryHaves(m, "peer-a"), fetcher, t.TempDir(), DirectoryDownloadOptions{
		ChunkBytes: uint64(len(files["chain/BdagChain/000001.ldb"])),
		Now:        testNow,
	})
	if err == nil {
		t.Fatal("expected corrupt file to fail verification")
	}
}

func mapFileChunkFetcher(files map[string][]byte) FileChunkFetcher {
	return FileChunkFetcherFunc(func(ctx context.Context, req FileChunkFetchRequest) ([]byte, error) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		body, ok := files[req.Path]
		if !ok {
			return nil, fmt.Errorf("missing file %s", req.Path)
		}
		end := req.Offset + req.Size
		if end > uint64(len(body)) {
			return nil, fmt.Errorf("range outside file")
		}
		out := make([]byte, int(req.Size))
		copy(out, body[int(req.Offset):int(end)])
		return out, nil
	})
}

func testDirectoryHaves(m *Manifest, peers ...string) []PeerArtifactHave {
	haves := make([]PeerArtifactHave, 0, len(peers))
	for _, peer := range peers {
		haves = append(haves, PeerArtifactHave{ArtifactRoot: m.ArtifactRoot, PeerID: peer})
	}
	return haves
}

func TestDownloadDirectoryArtifactRemovesCorruptExistingFile(t *testing.T) {
	files := map[string][]byte{
		"chain/BdagChain/000001.ldb": []byte("fresh data"),
	}
	m := testDirectoryManifest(t, files)
	staging := t.TempDir()
	path := filepath.Join(staging, "chain", "BdagChain", "000001.ldb")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("stale"), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := DownloadDirectoryArtifact(context.Background(), m, testDirectoryHaves(m, "peer-a"), mapFileChunkFetcher(files), staging, DirectoryDownloadOptions{
		ChunkBytes: 3,
		Now:        testNow,
	}); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, files["chain/BdagChain/000001.ldb"]) {
		t.Fatalf("corrupt file was not replaced: %q", got)
	}
}
