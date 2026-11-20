package artifact

import (
	"context"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"sync"
	"time"
)

const DefaultDirectoryFileChunkBytes = 8 * 1024 * 1024

type FileChunkFetchRequest struct {
	ArtifactRoot string
	PeerID       string
	Path         string
	Class        string
	Offset       uint64
	Size         uint64
	Attempt      int
}

type FileChunkFetcher interface {
	FetchArtifactFileChunk(context.Context, FileChunkFetchRequest) ([]byte, error)
}

type FileChunkFetcherFunc func(context.Context, FileChunkFetchRequest) ([]byte, error)

func (f FileChunkFetcherFunc) FetchArtifactFileChunk(ctx context.Context, req FileChunkFetchRequest) ([]byte, error) {
	return f(ctx, req)
}

type DirectoryDownloadOptions struct {
	Parallelism      int
	MaxParallelism   int
	RetryLimit       int
	MaxPeerFailures  int
	MaxInFlightBytes uint64
	ChunkBytes       uint64
	Sync             bool
	Now              func() time.Time
}

type DirectoryDownloadReport struct {
	ArtifactRoot    string         `json:"artifact_root"`
	RequestedFiles  int            `json:"requested_files"`
	SkippedFiles    int            `json:"skipped_files"`
	CompletedFiles  int            `json:"completed_files"`
	RequestedChunks int            `json:"requested_chunks"`
	Bytes           uint64         `json:"bytes"`
	PeerFailures    map[string]int `json:"peer_failures,omitempty"`
	StartedAt       time.Time      `json:"started_at"`
	FinishedAt      time.Time      `json:"finished_at"`
}

func DownloadDirectoryArtifact(ctx context.Context, manifest *Manifest, haves []PeerArtifactHave, fetcher FileChunkFetcher, stagingDir string, opts DirectoryDownloadOptions) (*DirectoryDownloadReport, error) {
	if manifest == nil {
		return nil, ErrNilManifest
	}
	if fetcher == nil {
		return nil, fmt.Errorf("artifact: file chunk fetcher is required")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	layout := manifest.Layout
	if layout == "" {
		layout = LayoutArchive
	}
	if layout != LayoutDirectory {
		return nil, fmt.Errorf("artifact: manifest layout %q is not directory", layout)
	}
	if err := validateDirectoryFiles(layout, manifest.Files, len(manifest.Chunks)); err != nil {
		return nil, err
	}
	root, err := validatedManifestRoot(manifest)
	if err != nil {
		return nil, err
	}
	stagingAbs, err := filepath.Abs(stagingDir)
	if err != nil {
		return nil, fmt.Errorf("artifact: resolve staging dir: %w", err)
	}
	if err := os.MkdirAll(stagingAbs, 0o755); err != nil {
		return nil, fmt.Errorf("artifact: create staging dir: %w", err)
	}
	parallelism := directoryDownloadParallelism(opts, len(manifest.Files))
	chunkBytes := opts.ChunkBytes
	if chunkBytes == 0 {
		chunkBytes = DefaultDirectoryFileChunkBytes
	}
	if chunkBytes > DefaultMaxChunkBytes {
		chunkBytes = DefaultMaxChunkBytes
	}
	maxInFlightBytes := opts.MaxInFlightBytes
	if maxInFlightBytes == 0 {
		maxInFlightBytes = DefaultMaxInFlightBytes
	}
	retryLimit := opts.RetryLimit
	if retryLimit <= 0 {
		retryLimit = 3
	}
	now := opts.Now
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	report := DirectoryDownloadReport{
		ArtifactRoot: root,
		StartedAt:    now(),
		PeerFailures: make(map[string]int),
	}
	pending := make([]FileDescriptor, 0, len(manifest.Files))
	for _, file := range manifest.Files {
		report.RequestedFiles++
		ok, err := verifiedDirectoryFile(ctx, stagingAbs, file)
		if err != nil {
			report.FinishedAt = now()
			return &report, err
		}
		if ok {
			report.SkippedFiles++
			continue
		}
		pending = append(pending, file)
	}
	if len(pending) == 0 {
		report.FinishedAt = now()
		return &report, nil
	}
	providers, err := directoryArtifactProviders(root, haves)
	if err != nil {
		report.FinishedAt = now()
		return &report, err
	}
	state := &directoryDownloadState{
		root:            root,
		stagingAbs:      stagingAbs,
		fetcher:         fetcher,
		providers:       providers,
		peerFailures:    make(map[string]int),
		maxPeerFailures: opts.MaxPeerFailures,
		chunkBytes:      chunkBytes,
		retryLimit:      retryLimit,
		sync:            opts.Sync,
		limiter:         newByteLimiter(maxInFlightBytes),
		now:             now,
		report:          report,
	}

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	jobs := make(chan FileDescriptor)
	errCh := make(chan error, 1)
	var wg sync.WaitGroup
	for i := 0; i < parallelism; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for file := range jobs {
				if err := state.downloadFile(runCtx, file); err != nil {
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
	for _, file := range pending {
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
			return state.finish(), runCtx.Err()
		case jobs <- file:
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

type directoryDownloadState struct {
	mu              sync.Mutex
	root            string
	stagingAbs      string
	fetcher         FileChunkFetcher
	providers       []string
	peerFailures    map[string]int
	maxPeerFailures int
	chunkBytes      uint64
	retryLimit      int
	sync            bool
	limiter         chan struct{}
	now             func() time.Time
	report          DirectoryDownloadReport
}

func (s *directoryDownloadState) downloadFile(ctx context.Context, file FileDescriptor) error {
	finalPath, err := directoryArtifactFilePath(s.stagingAbs, file.Path)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(finalPath), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(finalPath), "."+filepath.Base(finalPath)+".part-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	removeTmp := true
	defer func() {
		_ = tmp.Close()
		if removeTmp {
			_ = os.Remove(tmpPath)
		}
	}()
	var offset uint64
	for offset < file.Size {
		if err := ctx.Err(); err != nil {
			return err
		}
		size := s.chunkBytes
		if remaining := file.Size - offset; size > remaining {
			size = remaining
		}
		body, err := s.fetchRange(ctx, file, offset, size)
		if err != nil {
			return err
		}
		if err := copyExact(tmp, body); err != nil {
			return err
		}
		offset += size
		s.markChunk(size)
	}
	if err := tmp.Chmod(directoryFileMode(file)); err != nil {
		return err
	}
	if err := tmp.Sync(); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if ok, err := verifiedPathFile(ctx, tmpPath, file); err != nil {
		return fmt.Errorf("artifact: verify downloaded file %s: %w", file.Path, err)
	} else if !ok {
		return fmt.Errorf("artifact: verify downloaded file %s: hash or size mismatch", file.Path)
	}
	if err := os.Remove(finalPath); err != nil && !os.IsNotExist(err) {
		return err
	}
	if err := os.Rename(tmpPath, finalPath); err != nil {
		return err
	}
	removeTmp = false
	if s.sync {
		if err := syncDir(filepath.Dir(finalPath)); err != nil && runtime.GOOS != "windows" {
			return err
		}
	}
	s.markFile()
	return nil
}

func (s *directoryDownloadState) fetchRange(ctx context.Context, file FileDescriptor, offset, size uint64) ([]byte, error) {
	var lastErr error
	for attempt := 0; attempt < s.retryLimit; attempt++ {
		providers := s.availableProviders()
		if len(providers) == 0 {
			if lastErr == nil {
				lastErr = ErrChunkUnavailable
			}
			return nil, fmt.Errorf("%w: file %s offset %d: %v", ErrChunkFetchFailed, file.Path, offset, lastErr)
		}
		peerID := providers[0]
		tokens, err := acquireByteTokens(ctx, s.limiter, size)
		if err != nil {
			return nil, err
		}
		body, err := s.fetcher.FetchArtifactFileChunk(ctx, FileChunkFetchRequest{
			ArtifactRoot: s.root,
			PeerID:       peerID,
			Path:         file.Path,
			Class:        file.Class,
			Offset:       offset,
			Size:         size,
			Attempt:      attempt + 1,
		})
		releaseByteTokens(s.limiter, tokens)
		if err != nil && ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if err == nil && uint64(len(body)) != size {
			err = fmt.Errorf("artifact: file %s offset %d size mismatch: got %d want %d", file.Path, offset, len(body), size)
		}
		if err == nil {
			return body, nil
		}
		lastErr = err
		s.markPeerFailure(peerID)
	}
	return nil, fmt.Errorf("%w: file %s offset %d after %d attempts: %v", ErrChunkFetchFailed, file.Path, offset, s.retryLimit, lastErr)
}

func (s *directoryDownloadState) availableProviders() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, 0, len(s.providers))
	for _, peerID := range s.providers {
		if s.maxPeerFailures > 0 && s.peerFailures[peerID] >= s.maxPeerFailures {
			continue
		}
		out = append(out, peerID)
	}
	sort.SliceStable(out, func(i, j int) bool {
		fi := s.peerFailures[out[i]]
		fj := s.peerFailures[out[j]]
		if fi == fj {
			return out[i] < out[j]
		}
		return fi < fj
	})
	return out
}

func (s *directoryDownloadState) markPeerFailure(peerID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.peerFailures[peerID]++
}

func (s *directoryDownloadState) markChunk(bytes uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.report.RequestedChunks++
	s.report.Bytes += bytes
}

func (s *directoryDownloadState) markFile() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.report.CompletedFiles++
}

func (s *directoryDownloadState) finish() *DirectoryDownloadReport {
	s.mu.Lock()
	defer s.mu.Unlock()
	for peerID, failures := range s.peerFailures {
		s.report.PeerFailures[peerID] = failures
	}
	s.report.FinishedAt = s.now()
	report := s.report
	return &report
}

func directoryArtifactProviders(root string, haves []PeerArtifactHave) ([]string, error) {
	seen := make(map[string]struct{}, len(haves))
	providers := make([]string, 0, len(haves))
	for _, have := range haves {
		if !equalHex(have.ArtifactRoot, root) {
			continue
		}
		if have.PeerID == "" {
			return nil, fmt.Errorf("artifact: peer have for %s has empty peer id", root)
		}
		if _, ok := seen[have.PeerID]; ok {
			continue
		}
		seen[have.PeerID] = struct{}{}
		providers = append(providers, have.PeerID)
	}
	sort.Strings(providers)
	if len(providers) == 0 {
		return nil, fmt.Errorf("%w: directory artifact %s has no providers", ErrChunkUnavailable, root)
	}
	return providers, nil
}

func directoryDownloadParallelism(opts DirectoryDownloadOptions, files int) int {
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
	if parallelism > files {
		parallelism = files
	}
	if parallelism <= 0 {
		return 1
	}
	return parallelism
}

func verifiedDirectoryFile(ctx context.Context, rootAbs string, file FileDescriptor) (bool, error) {
	fullPath, err := directoryArtifactFilePath(rootAbs, file.Path)
	if err != nil {
		return false, err
	}
	return verifiedPathFile(ctx, fullPath, file)
}

func verifiedPathFile(ctx context.Context, path string, file FileDescriptor) (bool, error) {
	size, digest, err := hashRegularFile(ctx, path)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	return size == file.Size && equalHex(hex.EncodeToString(digest[:]), file.SHA256), nil
}

func directoryFileMode(file FileDescriptor) os.FileMode {
	if file.Mode != 0 {
		return os.FileMode(file.Mode)
	}
	return 0o644
}

func copyExact(dst io.Writer, body []byte) error {
	n, err := dst.Write(body)
	if err != nil {
		return err
	}
	if n != len(body) {
		return io.ErrShortWrite
	}
	return nil
}
