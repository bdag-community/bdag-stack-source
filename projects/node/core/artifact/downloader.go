package artifact

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

const (
	DefaultDownloadParallelism    = 4
	DefaultMaxDownloadParallelism = 16
	DefaultMaxInFlightBytes       = 512 * 1024 * 1024

	inFlightByteUnit = 1024 * 1024
)

var (
	ErrChunkUnavailable = errors.New("artifact: chunk unavailable")
	ErrChunkFetchFailed = errors.New("artifact: chunk fetch failed")
)

type ChunkFetchRequest struct {
	ArtifactRoot     string
	PeerID           string
	ChunkID          uint32
	Source           string
	Class            string
	CompressedSize   uint64
	CompressedSHA256 string
	Attempt          int
}

type ChunkFetcher interface {
	FetchArtifactChunk(context.Context, ChunkFetchRequest) ([]byte, error)
}

type ChunkFetcherFunc func(context.Context, ChunkFetchRequest) ([]byte, error)

func (f ChunkFetcherFunc) FetchArtifactChunk(ctx context.Context, req ChunkFetchRequest) ([]byte, error) {
	return f(ctx, req)
}

type ChunkSink interface {
	PutArtifactChunk(context.Context, ChunkDescriptor, []byte) error
}

type ChunkPresenceVerifier interface {
	HasVerifiedArtifactChunk(context.Context, ChunkDescriptor) (bool, error)
}

type ChunkSinkFunc func(context.Context, ChunkDescriptor, []byte) error

func (f ChunkSinkFunc) PutArtifactChunk(ctx context.Context, desc ChunkDescriptor, body []byte) error {
	return f(ctx, desc, body)
}

type DownloadOptions struct {
	Parallelism      int
	MaxParallelism   int
	RetryLimit       int
	MaxPeerFailures  int
	MaxInFlightBytes uint64
	PeerLatencies    map[string]time.Duration
	Ledger           *ResumeLedger
	Now              func() time.Time
}

type DownloadReport struct {
	ArtifactRoot string         `json:"artifact_root"`
	Requested    int            `json:"requested"`
	Skipped      int            `json:"skipped"`
	Completed    int            `json:"completed"`
	Bytes        uint64         `json:"bytes"`
	PeerFailures map[string]int `json:"peer_failures,omitempty"`
	StartedAt    time.Time      `json:"started_at"`
	FinishedAt   time.Time      `json:"finished_at"`
}

type VerifiedChunk struct {
	ID               uint32    `json:"id"`
	CompressedSize   uint64    `json:"compressed_size"`
	CompressedSHA256 string    `json:"compressed_sha256"`
	VerifiedAt       time.Time `json:"verified_at"`
}

type ResumeLedger struct {
	ArtifactRoot string                   `json:"artifact_root"`
	Chunks       map[uint32]VerifiedChunk `json:"chunks"`
	PeerFailures map[string]int           `json:"peer_failures,omitempty"`
	UpdatedAt    time.Time                `json:"updated_at"`
}

func NewResumeLedger(artifactRoot string) *ResumeLedger {
	return &ResumeLedger{
		ArtifactRoot: artifactRoot,
		Chunks:       make(map[uint32]VerifiedChunk),
		PeerFailures: make(map[string]int),
	}
}

func LoadResumeLedger(path string) (*ResumeLedger, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var ledger ResumeLedger
	if err := json.Unmarshal(raw, &ledger); err != nil {
		return nil, err
	}
	if ledger.Chunks == nil {
		ledger.Chunks = make(map[uint32]VerifiedChunk)
	}
	if ledger.PeerFailures == nil {
		ledger.PeerFailures = make(map[string]int)
	}
	return &ledger, nil
}

func (l *ResumeLedger) SaveAtomic(path string) error {
	if path == "" {
		return fmt.Errorf("artifact: ledger path is required")
	}
	dir := filepath.Dir(path)
	base := filepath.Base(path)
	f, err := os.CreateTemp(dir, "."+base+".tmp-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	removeTmp := true
	defer func() {
		if removeTmp {
			_ = os.Remove(tmp)
		}
	}()
	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	if err := enc.Encode(l); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Chmod(0o644); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		return err
	}
	removeTmp = false
	return syncDir(dir)
}

func (l *ResumeLedger) IsVerified(desc ChunkDescriptor) bool {
	if l == nil {
		return false
	}
	got, ok := l.Chunks[desc.ID]
	return ok && got.CompressedSize == desc.CompressedSize && equalHex(got.CompressedSHA256, desc.CompressedSHA256)
}

func (l *ResumeLedger) MarkVerified(desc ChunkDescriptor, now time.Time) {
	if l.Chunks == nil {
		l.Chunks = make(map[uint32]VerifiedChunk)
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	l.Chunks[desc.ID] = VerifiedChunk{
		ID:               desc.ID,
		CompressedSize:   desc.CompressedSize,
		CompressedSHA256: desc.CompressedSHA256,
		VerifiedAt:       now.UTC(),
	}
	l.UpdatedAt = now.UTC()
}

type ProviderIndex struct {
	artifactRoot string
	providers    map[uint32][]string
	latencies    map[string]time.Duration
}

func NewProviderIndex(manifest *Manifest, haves []PeerArtifactHave) (*ProviderIndex, error) {
	if manifest == nil {
		return nil, ErrNilManifest
	}
	root, err := validatedManifestRoot(manifest)
	if err != nil {
		return nil, err
	}
	idx := &ProviderIndex{
		artifactRoot: root,
		providers:    make(map[uint32][]string, len(manifest.Chunks)),
	}
	for _, have := range haves {
		if !equalHex(have.ArtifactRoot, root) {
			continue
		}
		if have.PeerID == "" {
			return nil, fmt.Errorf("artifact: peer have for %s has empty peer id", root)
		}
		if len(have.Chunks) == 0 && len(have.Ranges) == 0 {
			for _, chunk := range manifest.Chunks {
				idx.addProvider(chunk.ID, have.PeerID)
			}
			continue
		}
		for _, id := range have.Chunks {
			if uint64(id) >= uint64(len(manifest.Chunks)) {
				return nil, fmt.Errorf("artifact: peer %s advertises chunk %d beyond manifest", have.PeerID, id)
			}
			idx.addProvider(id, have.PeerID)
		}
		for _, r := range have.Ranges {
			if r.Count == 0 {
				continue
			}
			end := uint64(r.Start) + uint64(r.Count)
			if end > uint64(len(manifest.Chunks)) {
				return nil, fmt.Errorf("artifact: peer %s advertises range %d+%d beyond manifest", have.PeerID, r.Start, r.Count)
			}
			for id := r.Start; id < uint32(end); id++ {
				idx.addProvider(id, have.PeerID)
			}
		}
	}
	for id, peers := range idx.providers {
		sort.Strings(peers)
		idx.providers[id] = peers
	}
	return idx, nil
}

func (idx *ProviderIndex) SetPeerLatencies(latencies map[string]time.Duration) {
	if idx == nil || len(latencies) == 0 {
		return
	}
	idx.latencies = make(map[string]time.Duration, len(latencies))
	for peerID, latency := range latencies {
		if peerID == "" || latency <= 0 {
			continue
		}
		idx.latencies[peerID] = latency
	}
}

func (idx *ProviderIndex) Providers(chunkID uint32, peerFailures map[string]int, maxPeerFailures int) []string {
	if idx == nil {
		return nil
	}
	base := idx.providers[chunkID]
	if len(base) == 0 {
		return nil
	}
	out := make([]string, 0, len(base))
	for _, peerID := range base {
		if maxPeerFailures > 0 && peerFailures[peerID] >= maxPeerFailures {
			continue
		}
		out = append(out, peerID)
	}
	sort.SliceStable(out, func(i, j int) bool {
		fi := peerFailures[out[i]]
		fj := peerFailures[out[j]]
		if fi == fj {
			li, iKnown := idx.latencies[out[i]]
			lj, jKnown := idx.latencies[out[j]]
			if iKnown && jKnown && li != lj {
				return li < lj
			}
			if iKnown != jKnown {
				return iKnown
			}
			return out[i] < out[j]
		}
		return fi < fj
	})
	return out
}

func (idx *ProviderIndex) addProvider(chunkID uint32, peerID string) {
	for _, existing := range idx.providers[chunkID] {
		if existing == peerID {
			return
		}
	}
	idx.providers[chunkID] = append(idx.providers[chunkID], peerID)
}

func DownloadChunks(ctx context.Context, manifest *Manifest, haves []PeerArtifactHave, fetcher ChunkFetcher, sink ChunkSink, opts DownloadOptions) (*DownloadReport, error) {
	if manifest == nil {
		return nil, ErrNilManifest
	}
	if fetcher == nil {
		return nil, fmt.Errorf("artifact: chunk fetcher is required")
	}
	if sink == nil {
		return nil, fmt.Errorf("artifact: chunk sink is required")
	}
	root, err := validatedManifestRoot(manifest)
	if err != nil {
		return nil, err
	}
	idx, err := NewProviderIndex(manifest, haves)
	if err != nil {
		return nil, err
	}
	idx.SetPeerLatencies(opts.PeerLatencies)
	parallelism := opts.Parallelism
	if parallelism <= 0 {
		parallelism = DefaultDownloadParallelism
	}
	maxParallelism := opts.MaxParallelism
	if maxParallelism <= 0 {
		maxParallelism = DefaultMaxDownloadParallelism
	}
	if parallelism > maxParallelism {
		parallelism = maxParallelism
	}
	if parallelism > len(manifest.Chunks) {
		parallelism = len(manifest.Chunks)
	}
	if parallelism <= 0 {
		parallelism = 1
	}
	retryLimit := opts.RetryLimit
	if retryLimit <= 0 {
		retryLimit = 3
	}
	maxInFlightBytes := opts.MaxInFlightBytes
	if maxInFlightBytes == 0 {
		maxInFlightBytes = DefaultMaxInFlightBytes
	}
	ledger := opts.Ledger
	if ledger == nil {
		ledger = NewResumeLedger(root)
	}
	if ledger.ArtifactRoot == "" {
		ledger.ArtifactRoot = root
	}
	if !equalHex(ledger.ArtifactRoot, root) {
		return nil, fmt.Errorf("artifact: resume ledger root mismatch: ledger=%s manifest=%s", ledger.ArtifactRoot, root)
	}
	now := opts.Now
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}

	state := &downloadState{
		ledger:          ledger,
		report:          DownloadReport{ArtifactRoot: root, StartedAt: now(), PeerFailures: make(map[string]int)},
		peerFailures:    copyPeerFailures(ledger.PeerFailures),
		maxPeerFailures: opts.MaxPeerFailures,
		limiter:         newByteLimiter(maxInFlightBytes),
		now:             now,
	}
	verifier, _ := sink.(ChunkPresenceVerifier)
	pending := make([]ChunkDescriptor, 0, len(manifest.Chunks))
	for _, desc := range manifest.Chunks {
		state.report.Requested++
		ok, err := chunkVerifiedForResume(ctx, ledger, verifier, desc)
		if err != nil {
			return state.finish(), err
		}
		if ok {
			state.report.Skipped++
			continue
		}
		if len(idx.Providers(desc.ID, state.peerFailures, state.maxPeerFailures)) == 0 {
			return state.finish(), fmt.Errorf("%w: chunk %d has no providers", ErrChunkUnavailable, desc.ID)
		}
		pending = append(pending, desc)
	}
	if len(pending) == 0 {
		return state.finish(), nil
	}

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	jobs := make(chan ChunkDescriptor)
	errCh := make(chan error, 1)
	var wg sync.WaitGroup
	for i := 0; i < parallelism; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for desc := range jobs {
				if err := downloadOne(runCtx, root, desc, idx, fetcher, sink, retryLimit, state); err != nil {
					select {
					case errCh <- err:
						cancel()
					default:
					}
					return
				}
			}
		}()
	}
	for _, desc := range pending {
		select {
		case err := <-errCh:
			close(jobs)
			wg.Wait()
			return state.finish(), err
		case <-runCtx.Done():
			close(jobs)
			wg.Wait()
			select {
			case err := <-errCh:
				return state.finish(), err
			default:
			}
			if err := runCtx.Err(); err != nil {
				return state.finish(), err
			}
			return state.finish(), ctx.Err()
		case jobs <- desc:
		}
	}
	close(jobs)
	wg.Wait()
	select {
	case err := <-errCh:
		return state.finish(), err
	default:
		return state.finish(), nil
	}
}

type downloadState struct {
	mu              sync.Mutex
	ledger          *ResumeLedger
	report          DownloadReport
	peerFailures    map[string]int
	maxPeerFailures int
	limiter         chan struct{}
	now             func() time.Time
}

func (s *downloadState) providers(idx *ProviderIndex, chunkID uint32) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return idx.Providers(chunkID, s.peerFailures, s.maxPeerFailures)
}

func (s *downloadState) markPeerFailure(peerID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.peerFailures == nil {
		s.peerFailures = make(map[string]int)
	}
	s.peerFailures[peerID]++
}

func (s *downloadState) markVerified(desc ChunkDescriptor) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ledger.MarkVerified(desc, s.now())
	s.report.Completed++
	s.report.Bytes += desc.CompressedSize
}

func (s *downloadState) finish() *DownloadReport {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ledger.PeerFailures == nil {
		s.ledger.PeerFailures = make(map[string]int)
	}
	for peerID, failures := range s.peerFailures {
		s.ledger.PeerFailures[peerID] = failures
		s.report.PeerFailures[peerID] = failures
	}
	s.report.FinishedAt = s.now()
	report := s.report
	return &report
}

func downloadOne(ctx context.Context, root string, desc ChunkDescriptor, idx *ProviderIndex, fetcher ChunkFetcher, sink ChunkSink, retryLimit int, state *downloadState) error {
	var lastErr error
	for attempt := 0; attempt < retryLimit; attempt++ {
		providers := state.providers(idx, desc.ID)
		if len(providers) == 0 {
			if lastErr == nil {
				lastErr = ErrChunkUnavailable
			}
			return fmt.Errorf("%w: chunk %d: %v", ErrChunkFetchFailed, desc.ID, lastErr)
		}
		peerID := providers[0]
		tokens, err := acquireByteTokens(ctx, state.limiter, desc.CompressedSize)
		if err != nil {
			return err
		}
		body, err := fetcher.FetchArtifactChunk(ctx, ChunkFetchRequest{
			ArtifactRoot:     root,
			PeerID:           peerID,
			ChunkID:          desc.ID,
			Source:           desc.Source,
			Class:            desc.Class,
			CompressedSize:   desc.CompressedSize,
			CompressedSHA256: desc.CompressedSHA256,
			Attempt:          attempt + 1,
		})
		if err != nil && ctx.Err() != nil {
			releaseByteTokens(state.limiter, tokens)
			return ctx.Err()
		}
		if ctx.Err() != nil {
			releaseByteTokens(state.limiter, tokens)
			return ctx.Err()
		}
		if err == nil {
			err = VerifyChunkBody(desc, body)
		}
		if err == nil {
			if err := sink.PutArtifactChunk(ctx, desc, body); err != nil {
				releaseByteTokens(state.limiter, tokens)
				return fmt.Errorf("artifact: sink chunk %d: %w", desc.ID, err)
			}
			releaseByteTokens(state.limiter, tokens)
			state.markVerified(desc)
			return nil
		}
		releaseByteTokens(state.limiter, tokens)
		lastErr = err
		state.markPeerFailure(peerID)
	}
	return fmt.Errorf("%w: chunk %d after %d attempts: %v", ErrChunkFetchFailed, desc.ID, retryLimit, lastErr)
}

func validatedManifestRoot(m *Manifest) (string, error) {
	if m == nil {
		return "", ErrNilManifest
	}
	root, err := m.Root()
	if err != nil {
		return "", err
	}
	if m.ArtifactRoot != "" && !equalHex(m.ArtifactRoot, root) {
		return "", fmt.Errorf("artifact: root mismatch: manifest=%s computed=%s", m.ArtifactRoot, root)
	}
	return root, nil
}

func chunkVerifiedForResume(ctx context.Context, ledger *ResumeLedger, verifier ChunkPresenceVerifier, desc ChunkDescriptor) (bool, error) {
	if !ledger.IsVerified(desc) {
		return false, nil
	}
	if verifier == nil {
		return false, nil
	}
	ok, err := verifier.HasVerifiedArtifactChunk(ctx, desc)
	if err != nil {
		return false, fmt.Errorf("artifact: verify stored chunk %d: %w", desc.ID, err)
	}
	if !ok {
		delete(ledger.Chunks, desc.ID)
	}
	return ok, nil
}

func newByteLimiter(maxBytes uint64) chan struct{} {
	if maxBytes == 0 {
		return nil
	}
	tokens := int((maxBytes + inFlightByteUnit - 1) / inFlightByteUnit)
	if tokens < 1 {
		tokens = 1
	}
	return make(chan struct{}, tokens)
}

func acquireByteTokens(ctx context.Context, limiter chan struct{}, bytes uint64) (int, error) {
	if limiter == nil {
		return 0, nil
	}
	need := int((bytes + inFlightByteUnit - 1) / inFlightByteUnit)
	if need < 1 {
		need = 1
	}
	if need > cap(limiter) {
		need = cap(limiter)
	}
	acquired := 0
	for acquired < need {
		select {
		case limiter <- struct{}{}:
			acquired++
		case <-ctx.Done():
			releaseByteTokens(limiter, acquired)
			return 0, ctx.Err()
		}
	}
	return acquired, nil
}

func releaseByteTokens(limiter chan struct{}, tokens int) {
	for i := 0; limiter != nil && i < tokens; i++ {
		<-limiter
	}
}

func copyPeerFailures(in map[string]int) map[string]int {
	out := make(map[string]int, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func syncDir(path string) error {
	d, err := os.Open(path)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}
