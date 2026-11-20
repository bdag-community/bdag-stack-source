package artifact

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestDownloadChunksFetchesFromMultiplePeers(t *testing.T) {
	m := testMultiChunkManifest(t)
	ledger := NewResumeLedger(m.ArtifactRoot)
	got := make(map[uint32][]byte)
	var requests []ChunkFetchRequest
	var mu sync.Mutex
	report, err := DownloadChunks(context.Background(), m, []PeerArtifactHave{
		{ArtifactRoot: m.ArtifactRoot, PeerID: "peer-a", Chunks: []uint32{0}},
		{ArtifactRoot: m.ArtifactRoot, PeerID: "peer-b", Chunks: []uint32{1, 2}},
	}, ChunkFetcherFunc(func(ctx context.Context, req ChunkFetchRequest) ([]byte, error) {
		mu.Lock()
		requests = append(requests, req)
		mu.Unlock()
		return []byte("chunk-" + string(rune('0'+req.ChunkID))), nil
	}), ChunkSinkFunc(func(ctx context.Context, desc ChunkDescriptor, body []byte) error {
		mu.Lock()
		defer mu.Unlock()
		got[desc.ID] = append([]byte(nil), body...)
		return nil
	}), DownloadOptions{Parallelism: 2, Ledger: ledger, Now: testNow})
	if err != nil {
		t.Fatalf("download chunks: %v", err)
	}
	if report.Completed != 3 || report.Skipped != 0 || report.Bytes == 0 {
		t.Fatalf("unexpected report: %+v", report)
	}
	if len(got) != 3 {
		t.Fatalf("expected 3 stored chunks, got %d", len(got))
	}
	if !ledger.IsVerified(m.Chunks[0]) || !ledger.IsVerified(m.Chunks[1]) || !ledger.IsVerified(m.Chunks[2]) {
		t.Fatalf("ledger did not record verified chunks: %+v", ledger.Chunks)
	}
	seenPeers := map[string]bool{}
	mu.Lock()
	for _, req := range requests {
		seenPeers[req.PeerID] = true
	}
	mu.Unlock()
	if !seenPeers["peer-a"] || !seenPeers["peer-b"] {
		t.Fatalf("expected requests to both peers, got %+v", requests)
	}
}

func TestDownloadChunksFallsBackFromCorruptPeer(t *testing.T) {
	m := testMultiChunkManifest(t)
	failures := 0
	var mu sync.Mutex
	report, err := DownloadChunks(context.Background(), m, []PeerArtifactHave{
		{ArtifactRoot: m.ArtifactRoot, PeerID: "bad"},
		{ArtifactRoot: m.ArtifactRoot, PeerID: "good"},
	}, ChunkFetcherFunc(func(ctx context.Context, req ChunkFetchRequest) ([]byte, error) {
		if req.PeerID == "bad" {
			mu.Lock()
			failures++
			mu.Unlock()
			return []byte("corrupt"), nil
		}
		return []byte("chunk-" + string(rune('0'+req.ChunkID))), nil
	}), ChunkSinkFunc(func(ctx context.Context, desc ChunkDescriptor, body []byte) error {
		return nil
	}), DownloadOptions{RetryLimit: 4, MaxPeerFailures: 1, Now: testNow})
	if err != nil {
		t.Fatalf("download with fallback: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if failures == 0 {
		t.Fatal("expected corrupt peer to be tried")
	}
	if report.Completed != 3 {
		t.Fatalf("expected all chunks complete, got %+v", report)
	}
	if report.PeerFailures["bad"] == 0 {
		t.Fatalf("expected bad peer failure accounting, got %+v", report.PeerFailures)
	}
}

func TestDownloadChunksSkipsResumeLedgerChunks(t *testing.T) {
	m := testMultiChunkManifest(t)
	ledger := NewResumeLedger(m.ArtifactRoot)
	ledger.MarkVerified(m.Chunks[0], testNow())
	store := newMemoryChunkStore()
	store.chunks[0] = []byte("chunk-0")
	var requested []uint32
	var mu sync.Mutex
	report, err := DownloadChunks(context.Background(), m, []PeerArtifactHave{
		{ArtifactRoot: m.ArtifactRoot, PeerID: "peer-a"},
	}, ChunkFetcherFunc(func(ctx context.Context, req ChunkFetchRequest) ([]byte, error) {
		mu.Lock()
		requested = append(requested, req.ChunkID)
		mu.Unlock()
		return []byte("chunk-" + string(rune('0'+req.ChunkID))), nil
	}), store, DownloadOptions{Parallelism: 1, Ledger: ledger, Now: testNow})
	if err != nil {
		t.Fatalf("download with ledger: %v", err)
	}
	if report.Skipped != 1 || report.Completed != 2 {
		t.Fatalf("unexpected report: %+v", report)
	}
	mu.Lock()
	defer mu.Unlock()
	if reflect.DeepEqual(requested, []uint32{0}) {
		t.Fatalf("expected chunk 0 to be skipped, requested=%v", requested)
	}
	for _, id := range requested {
		if id == 0 {
			t.Fatalf("chunk 0 should not be fetched again: %v", requested)
		}
	}
}

func TestDownloadChunksRefetchesStaleLedgerChunk(t *testing.T) {
	m := testMultiChunkManifest(t)
	ledger := NewResumeLedger(m.ArtifactRoot)
	ledger.MarkVerified(m.Chunks[0], testNow())
	store := newMemoryChunkStore()
	var requested []uint32
	var mu sync.Mutex
	_, err := DownloadChunks(context.Background(), m, []PeerArtifactHave{
		{ArtifactRoot: m.ArtifactRoot, PeerID: "peer-a"},
	}, ChunkFetcherFunc(func(ctx context.Context, req ChunkFetchRequest) ([]byte, error) {
		mu.Lock()
		requested = append(requested, req.ChunkID)
		mu.Unlock()
		return []byte("chunk-" + string(rune('0'+req.ChunkID))), nil
	}), store, DownloadOptions{Parallelism: 1, Ledger: ledger, Now: testNow})
	if err != nil {
		t.Fatalf("download with stale ledger: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if !reflect.DeepEqual(requested, []uint32{0, 1, 2}) {
		t.Fatalf("expected stale ledger chunk to be fetched, got %v", requested)
	}
}

func TestDownloadChunksFallsBackWithoutPeerQuarantine(t *testing.T) {
	m := testSingleChunkManifest(t)
	var requested []string
	report, err := DownloadChunks(context.Background(), m, []PeerArtifactHave{
		{ArtifactRoot: m.ArtifactRoot, PeerID: "bad"},
		{ArtifactRoot: m.ArtifactRoot, PeerID: "good"},
	}, ChunkFetcherFunc(func(ctx context.Context, req ChunkFetchRequest) ([]byte, error) {
		requested = append(requested, req.PeerID)
		if req.PeerID == "bad" {
			return []byte("corrupt"), nil
		}
		return []byte("chunk-0"), nil
	}), ChunkSinkFunc(func(ctx context.Context, desc ChunkDescriptor, body []byte) error {
		return nil
	}), DownloadOptions{RetryLimit: 2, Now: testNow})
	if err != nil {
		t.Fatalf("download with fallback: %v", err)
	}
	if report.Completed != 1 {
		t.Fatalf("expected chunk complete, got %+v", report)
	}
	if !reflect.DeepEqual(requested, []string{"bad", "good"}) {
		t.Fatalf("expected bad then good peer, got %v", requested)
	}
}

func TestDownloadChunksPrefersLowerLatencyProvider(t *testing.T) {
	m := testSingleChunkManifest(t)
	var requested []string
	report, err := DownloadChunks(context.Background(), m, []PeerArtifactHave{
		{ArtifactRoot: m.ArtifactRoot, PeerID: "slow"},
		{ArtifactRoot: m.ArtifactRoot, PeerID: "fast"},
	}, ChunkFetcherFunc(func(ctx context.Context, req ChunkFetchRequest) ([]byte, error) {
		requested = append(requested, req.PeerID)
		return []byte("chunk-0"), nil
	}), ChunkSinkFunc(func(ctx context.Context, desc ChunkDescriptor, body []byte) error {
		return nil
	}), DownloadOptions{
		Now: testNow,
		PeerLatencies: map[string]time.Duration{
			"slow": 80 * time.Millisecond,
			"fast": 4 * time.Millisecond,
		},
	})
	if err != nil {
		t.Fatalf("download with latency preference: %v", err)
	}
	if report.Completed != 1 {
		t.Fatalf("expected chunk complete, got %+v", report)
	}
	if !reflect.DeepEqual(requested, []string{"fast"}) {
		t.Fatalf("expected low-latency peer first, got %v", requested)
	}
}

func TestDownloadChunksRejectsRootMismatch(t *testing.T) {
	m := testMultiChunkManifest(t)
	m.TipHash = "0x" + strings.Repeat("d", 64)
	_, err := DownloadChunks(context.Background(), m, []PeerArtifactHave{
		{ArtifactRoot: m.ArtifactRoot, PeerID: "peer-a"},
	}, ChunkFetcherFunc(func(ctx context.Context, req ChunkFetchRequest) ([]byte, error) {
		return nil, nil
	}), ChunkSinkFunc(func(ctx context.Context, desc ChunkDescriptor, body []byte) error {
		return nil
	}), DownloadOptions{Now: testNow})
	if err == nil || !strings.Contains(err.Error(), "root mismatch") {
		t.Fatalf("expected root mismatch, got %v", err)
	}
}

func TestResumeLedgerSaveAndLoad(t *testing.T) {
	m := testMultiChunkManifest(t)
	ledger := NewResumeLedger(m.ArtifactRoot)
	ledger.MarkVerified(m.Chunks[1], testNow())
	path := filepath.Join(t.TempDir(), "ledger.json")
	if err := ledger.SaveAtomic(path); err != nil {
		t.Fatalf("save ledger: %v", err)
	}
	loaded, err := LoadResumeLedger(path)
	if err != nil {
		t.Fatalf("load ledger: %v", err)
	}
	if !loaded.IsVerified(m.Chunks[1]) {
		t.Fatalf("loaded ledger lost verified chunk: %+v", loaded.Chunks)
	}
	if _, err := os.Stat(path + ".tmp"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("temporary ledger file left behind: %v", err)
	}
}

func TestDownloadChunksRejectsUnavailableChunk(t *testing.T) {
	m := testMultiChunkManifest(t)
	_, err := DownloadChunks(context.Background(), m, []PeerArtifactHave{
		{ArtifactRoot: m.ArtifactRoot, PeerID: "peer-a", Chunks: []uint32{0, 1}},
	}, ChunkFetcherFunc(func(ctx context.Context, req ChunkFetchRequest) ([]byte, error) {
		return nil, nil
	}), ChunkSinkFunc(func(ctx context.Context, desc ChunkDescriptor, body []byte) error {
		return nil
	}), DownloadOptions{Now: testNow})
	if !errors.Is(err, ErrChunkUnavailable) {
		t.Fatalf("expected ErrChunkUnavailable, got %v", err)
	}
}

func TestProviderIndexExpandsRangesAndRejectsBadAdvert(t *testing.T) {
	m := testMultiChunkManifest(t)
	idx, err := NewProviderIndex(m, []PeerArtifactHave{
		{ArtifactRoot: m.ArtifactRoot, PeerID: "peer-a", Ranges: []Range{{Start: 1, Count: 2}}},
	})
	if err != nil {
		t.Fatalf("provider index: %v", err)
	}
	if got := idx.Providers(0, nil, 0); len(got) != 0 {
		t.Fatalf("chunk 0 should have no providers, got %v", got)
	}
	if got := idx.Providers(1, nil, 0); !reflect.DeepEqual(got, []string{"peer-a"}) {
		t.Fatalf("chunk 1 providers = %v", got)
	}

	_, err = NewProviderIndex(m, []PeerArtifactHave{
		{ArtifactRoot: m.ArtifactRoot, PeerID: "peer-a", Ranges: []Range{{Start: 2, Count: 2}}},
	})
	if err == nil || !strings.Contains(err.Error(), "beyond manifest") {
		t.Fatalf("expected bad range error, got %v", err)
	}
}

func testMultiChunkManifest(t *testing.T) *Manifest {
	t.Helper()
	m := testManifest(t)
	m.Chunks = []ChunkDescriptor{
		testChunkDescriptor("chain", 0, []byte("chunk-0")),
		testChunkDescriptor("chain", 1, []byte("chunk-1")),
		testChunkDescriptor("chain", 2, []byte("chunk-2")),
	}
	m.Sources = []SourceInfo{{
		Name:       "chain",
		ChunkStart: 0,
		ChunkCount: uint32(len(m.Chunks)),
	}}
	if err := m.Finalize(); err != nil {
		t.Fatal(err)
	}
	return m
}

func testSingleChunkManifest(t *testing.T) *Manifest {
	t.Helper()
	m := testMultiChunkManifest(t)
	m.Chunks = m.Chunks[:1]
	m.Sources = []SourceInfo{{
		Name:       "chain",
		ChunkStart: 0,
		ChunkCount: 1,
	}}
	if err := m.Finalize(); err != nil {
		t.Fatal(err)
	}
	return m
}

type memoryChunkStore struct {
	mu     sync.Mutex
	chunks map[uint32][]byte
}

func newMemoryChunkStore() *memoryChunkStore {
	return &memoryChunkStore{chunks: make(map[uint32][]byte)}
}

func (s *memoryChunkStore) PutArtifactChunk(ctx context.Context, desc ChunkDescriptor, body []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.chunks[desc.ID] = append([]byte(nil), body...)
	return nil
}

func (s *memoryChunkStore) HasVerifiedArtifactChunk(ctx context.Context, desc ChunkDescriptor) (bool, error) {
	s.mu.Lock()
	body, ok := s.chunks[desc.ID]
	s.mu.Unlock()
	if !ok {
		return false, nil
	}
	return VerifyChunkBody(desc, body) == nil, nil
}
