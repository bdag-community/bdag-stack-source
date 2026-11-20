package snapshot

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/ethereum/go-ethereum/ethdb"
)

// WriteOptions tunes snapshot creation behaviour. Zero values select sane
// defaults so callers can pass an empty WriteOptions{} for typical use.
type WriteOptions struct {
	// ChunkTargetBytes is the soft upper bound on the uncompressed payload
	// size of a single chunk. A new chunk is started once the accumulated
	// payload crosses this threshold. Defaults to DefaultChunkTargetBytes.
	ChunkTargetBytes int

	// CompressionLevel is the gzip compression level to use. Values
	// outside [gzip.BestSpeed, gzip.BestCompression] fall back to
	// gzip.DefaultCompression.
	CompressionLevel int

	// KeyPrefix, if non-empty, limits snapshot export to the keys whose
	// binary prefix matches. The default (nil) dumps the entire DB.
	KeyPrefix []byte

	// ProgressInterval controls how often progress logging occurs while
	// writing a snapshot. Zero selects a sensible default (every 8s).
	ProgressInterval time.Duration
}

// BuildInfo carries the anchoring chain state that gets baked into the
// snapshot manifest. The writer is intentionally ignorant of blockchain
// internals so callers gather and pass this summary explicitly.
type BuildInfo struct {
	Network     string
	BDAGVersion string
	DBEngine    string
	TipHash     string
	TipOrder    uint64
	TipID       uint64
	BlockTotal  uint64
	StateRoot   string
}

// Source is a single logical database to include in a snapshot archive.
// Name MUST be one of the well-known source constants (SourceChain,
// SourceEVM, …) so importers can route chunks back to the right DB.
// KeyPrefix optionally restricts the export to keys sharing that prefix
// — most callers leave it nil to dump everything.
type Source struct {
	Name      string
	DB        ethdb.Database
	KeyPrefix []byte
}

// Write dumps the contents of db into a new snapshot archive at the given
// path, returning the finalized manifest. Retained for single-DB
// callers and tests; in production the CLI uses MultiWrite so the
// archive also carries EVM state.
//
// The caller is responsible for ensuring db is quiesced (i.e. no
// writers) for the duration of the call — snapshots capture a
// point-in-time view by iterating the key/value space.
func Write(path string, db ethdb.Database, info BuildInfo, opts WriteOptions) (*Manifest, error) {
	return MultiWrite(path, []Source{{Name: SourceChain, DB: db, KeyPrefix: opts.KeyPrefix}}, info, opts)
}

// MultiWrite dumps the contents of every source, in order, into a
// single snapshot archive. Each source contributes a contiguous range
// of chunks; the manifest's Sources field records the range boundaries
// so importers know which destination DB each chunk should land in.
//
// The archive is first written to path+".tmp" and atomically renamed on
// success so a concurrent importer never observes a half-written file.
func MultiWrite(path string, sources []Source, info BuildInfo, opts WriteOptions) (*Manifest, error) {
	if len(sources) == 0 {
		return nil, fmt.Errorf("snapshot: no sources to export")
	}
	for i, s := range sources {
		if s.Name == "" {
			return nil, fmt.Errorf("snapshot: source %d has empty name", i)
		}
		if s.DB == nil {
			return nil, fmt.Errorf("snapshot: source %q has nil database", s.Name)
		}
	}
	if path == "" {
		return nil, fmt.Errorf("snapshot: empty output path")
	}
	if opts.ChunkTargetBytes <= 0 {
		opts.ChunkTargetBytes = DefaultChunkTargetBytes
	}
	if opts.CompressionLevel < gzip.BestSpeed || opts.CompressionLevel > gzip.BestCompression {
		opts.CompressionLevel = gzip.DefaultCompression
	}
	if opts.ProgressInterval <= 0 {
		opts.ProgressInterval = 8 * time.Second
	}

	tmpPath := path + ".tmp"
	f, err := os.OpenFile(tmpPath, os.O_CREATE|os.O_TRUNC|os.O_RDWR, 0o644)
	if err != nil {
		return nil, fmt.Errorf("snapshot: open output: %w", err)
	}
	// Best-effort cleanup of the temp file on any failure path.
	defer func() {
		if f != nil {
			_ = f.Close()
			_ = os.Remove(tmpPath)
		}
	}()

	manifest := &Manifest{
		FormatVersion: FormatVersion,
		Network:       info.Network,
		BDAGVersion:   info.BDAGVersion,
		DBEngine:      info.DBEngine,
		CreatedAt:     time.Now().UTC(),
		TipHash:       info.TipHash,
		TipOrder:      info.TipOrder,
		TipID:         info.TipID,
		BlockTotal:    info.BlockTotal,
		StateRoot:     info.StateRoot,
	}

	// Reserve space for the header + manifest placeholder. We don't know
	// the manifest length until we've emitted every chunk (because each
	// chunk's sha256 ends up in the manifest), so write chunks first then
	// seek back to fill in the header + manifest at the end.
	placeholder := make([]byte, HeaderLen)
	if _, err := f.Write(placeholder); err != nil {
		return nil, fmt.Errorf("snapshot: reserve header: %w", err)
	}

	// Stream chunks directly into the archive, tracking their offsets
	// implicitly by emission order.
	chunksStart := int64(HeaderLen)
	if _, err := f.Seek(chunksStart, io.SeekStart); err != nil {
		return nil, err
	}

	chunkOffset := chunksStart
	cb := newChunkBuilder(opts.ChunkTargetBytes, opts.CompressionLevel)

	// Walk each source to completion before starting the next so the
	// chunk list reflects source boundaries without any interleaving.
	// Per-source chunk counts are recorded on the manifest as we go;
	// totals are filled in by Finalize().
	var (
		lastReport       = time.Now()
		totalKeysAll     uint64
		sourceStartIdx   int
		sourcePrefixKey  []byte
		needEvmStateTar  bool
		evmDBForStateTar ethdb.Database
		evmStateTarPath  string
	)
	defer func() {
		if evmStateTarPath != "" {
			_ = os.Remove(evmStateTarPath)
		}
	}()
	for si, src := range sources {
		sourceStartIdx = len(manifest.Chunks)
		sourcePrefixKey = src.KeyPrefix
		it := src.DB.NewIterator(sourcePrefixKey, nil)
		sourceKeys := uint64(0)
		for it.Next() {
			if err := cb.Add(it.Key(), it.Value()); err != nil {
				it.Release()
				return nil, err
			}
			sourceKeys++
			totalKeysAll++
			if cb.ShouldFlush() {
				ci, err := cb.Flush(f, uint32(len(manifest.Chunks)))
				if err != nil {
					it.Release()
					return nil, err
				}
				manifest.Chunks = append(manifest.Chunks, ci)
				chunkOffset += int64(ChunkHeaderLen) + int64(ci.CompressedSize)
			}
			if time.Since(lastReport) > opts.ProgressInterval {
				log.Info("Snapshot export in progress",
					"source", src.Name,
					"source_keys", sourceKeys,
					"total_keys", totalKeysAll,
					"chunks", len(manifest.Chunks),
					"bytes", chunkOffset,
				)
				lastReport = time.Now()
			}
		}
		iterErr := it.Error()
		it.Release()
		if iterErr != nil {
			return nil, fmt.Errorf("snapshot: iterate source %q: %w", src.Name, iterErr)
		}
		// Chain-freezer segments (geth ancients) are not returned by the KV
		// iterator; embed them as synthetic keys so import can call
		// ModifyAncients (see ancient.go).
		useRawTar, err := exportAncientTables(src.Name, src.DB, func(k, v []byte) error {
			if err := cb.Add(k, v); err != nil {
				return err
			}
			sourceKeys++
			totalKeysAll++
			if cb.ShouldFlush() {
				ci, err := cb.Flush(f, uint32(len(manifest.Chunks)))
				if err != nil {
					return err
				}
				manifest.Chunks = append(manifest.Chunks, ci)
				chunkOffset += int64(ChunkHeaderLen) + int64(ci.CompressedSize)
			}
			if time.Since(lastReport) > opts.ProgressInterval {
				log.Info("Snapshot export in progress",
					"source", src.Name,
					"source_keys", sourceKeys,
					"total_keys", totalKeysAll,
					"chunks", len(manifest.Chunks),
					"bytes", chunkOffset,
				)
				lastReport = time.Now()
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
		if useRawTar {
			if src.Name != SourceEVM {
				return nil, fmt.Errorf("snapshot: internal error: raw state tar flagged for non-evm source")
			}
			needEvmStateTar = true
			evmDBForStateTar = src.DB
		}
		// Flush any trailing buffered keys so the source boundary
		// aligns with a chunk boundary — otherwise a chunk would span
		// two sources and we couldn't route it on import.
		if cb.HasData() {
			ci, err := cb.Flush(f, uint32(len(manifest.Chunks)))
			if err != nil {
				return nil, err
			}
			manifest.Chunks = append(manifest.Chunks, ci)
			chunkOffset += int64(ChunkHeaderLen) + int64(ci.CompressedSize)
		}
		count := uint32(len(manifest.Chunks) - sourceStartIdx)
		if count == 0 {
			// A source that yielded zero keys is almost always an
			// operator error (wrong datadir, wrong prefix, empty DB),
			// so fail loudly rather than silently producing a
			// partial snapshot that would fail at import time anyway.
			return nil, fmt.Errorf("snapshot: source %q (index %d) yielded no keys", src.Name, si)
		}
		manifest.Sources = append(manifest.Sources, SourceInfo{
			Name:       src.Name,
			ChunkStart: uint32(sourceStartIdx),
			ChunkCount: count,
			TotalKeys:  sourceKeys,
		})
	}
	if len(manifest.Chunks) == 0 {
		return nil, fmt.Errorf("snapshot: no sources produced any keys")
	}
	manifest.Finalize()

	if needEvmStateTar {
		ad, ok := evmDBForStateTar.(interface {
			AncientDatadir() (string, error)
		})
		if !ok {
			return nil, fmt.Errorf("snapshot: evm database has no ancient datadir for state tar export")
		}
		ancientRoot, err := ad.AncientDatadir()
		if err != nil {
			return nil, fmt.Errorf("snapshot: evm ancient datadir: %w", err)
		}
		if ancientRoot == "" {
			return nil, fmt.Errorf("snapshot: evm ancient datadir is empty")
		}
		stateDir := filepath.Join(ancientRoot, "state")
		st, err := os.Stat(stateDir)
		if err != nil {
			return nil, fmt.Errorf("snapshot: state freezer directory %q: %w", stateDir, err)
		}
		if !st.IsDir() {
			return nil, fmt.Errorf("snapshot: %q is not a directory", stateDir)
		}
		path, size, sha, err := materializeEVMStateAncientTarGz(stateDir, opts.CompressionLevel)
		if err != nil {
			return nil, fmt.Errorf("snapshot: materialize evm state ancient tar: %w", err)
		}
		evmStateTarPath = path
		manifest.EVMStateAncientGzipBytes = uint64(size)
		manifest.EVMStateAncientSha256 = sha
	}

	manifestBytes, err := manifest.MarshalIndented()
	if err != nil {
		return nil, fmt.Errorf("snapshot: encode manifest: %w", err)
	}
	// The manifest length must fit in uint32. In practice it is small
	// (O(number of chunks)) but we assert explicitly to surface any future
	// blow-up early.
	if len(manifestBytes) > int(^uint32(0)) {
		return nil, fmt.Errorf("snapshot: manifest too large (%d bytes)", len(manifestBytes))
	}

	// Slide existing chunk data forward by len(manifestBytes) so we can
	// inject the manifest after the header. Rather than rewriting the
	// whole file, we take advantage of the fact that chunks are a single
	// contiguous blob: we first append the manifest, then overwrite the
	// header to include its length. This keeps IO strictly sequential.
	//
	// Layout after this step:
	//   [header placeholder][chunks][manifest]
	// We then rewrite the header *and* swap the manifest into place.
	manifestEnd := chunkOffset
	if _, err := f.Seek(0, io.SeekEnd); err != nil {
		return nil, err
	}
	if _, err := f.Write(manifestBytes); err != nil {
		return nil, fmt.Errorf("snapshot: write manifest: %w", err)
	}
	// Move the chunks region so the manifest lives directly after the
	// header. This keeps random-access readers (who index into the file
	// using ChunkInfo.ID + cumulative sizes) straightforward.
	if err := relocateChunks(f, chunksStart, manifestEnd, manifestBytes); err != nil {
		return nil, err
	}

	if evmStateTarPath != "" {
		tf, err := os.Open(evmStateTarPath)
		if err != nil {
			return nil, fmt.Errorf("snapshot: open state ancient tar: %w", err)
		}
		if _, err := f.Seek(0, io.SeekEnd); err != nil {
			_ = tf.Close()
			return nil, err
		}
		if _, err := io.Copy(f, tf); err != nil {
			_ = tf.Close()
			return nil, fmt.Errorf("snapshot: append state ancient tar: %w", err)
		}
		if err := tf.Close(); err != nil {
			return nil, err
		}
	}

	// Write the final header now that we know the manifest length.
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	if err := writeHeader(f, uint32(len(manifestBytes))); err != nil {
		return nil, err
	}

	if err := f.Sync(); err != nil {
		return nil, fmt.Errorf("snapshot: fsync: %w", err)
	}
	if err := f.Close(); err != nil {
		return nil, fmt.Errorf("snapshot: close: %w", err)
	}
	f = nil // suppress deferred cleanup

	if err := os.Rename(tmpPath, path); err != nil {
		return nil, fmt.Errorf("snapshot: rename: %w", err)
	}
	log.Info("Snapshot export complete", "path", path, "chunks", len(manifest.Chunks), "keys", manifest.TotalKeys, "compressed", manifest.TotalCompressed)
	return manifest, nil
}

// WriteManifestFile writes the stand-alone JSON manifest next to an
// archive. Peers can fetch this much smaller file first to decide whether
// a snapshot is worth downloading, and to cross-check chunk hashes as
// they come in over the wire.
func WriteManifestFile(path string, m *Manifest) error {
	b, err := m.MarshalIndented()
	if err != nil {
		return fmt.Errorf("snapshot: encode manifest: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// writeHeader encodes the fixed-size file header. The format is stable and
// versioned by FormatVersion.
func writeHeader(w io.Writer, manifestLen uint32) error {
	buf := make([]byte, HeaderLen)
	copy(buf[0:8], []byte(Magic))
	byteOrder.PutUint32(buf[8:12], FormatVersion)
	byteOrder.PutUint32(buf[12:16], manifestLen)
	// buf[16:32] is reserved and left zero for forward compatibility.
	_, err := w.Write(buf)
	return err
}

// relocateChunks moves a block of chunk bytes that currently live at
// [chunksStart, chunksEnd) to sit *after* the manifest that was just
// appended at chunksEnd. It does so in-place using a windowed copy to
// avoid allocating a buffer the size of the entire chunks region.
//
// Resulting layout:
//
//	[HeaderLen .. HeaderLen+manifestLen)   -> manifest
//	[HeaderLen+manifestLen .. EOF)         -> chunks (in original order)
func relocateChunks(f *os.File, chunksStart, chunksEnd int64, manifestBytes []byte) error {
	manifestLen := int64(len(manifestBytes))
	if manifestLen == 0 {
		return fmt.Errorf("snapshot: empty manifest")
	}
	if chunksEnd == chunksStart {
		// No chunks: just move the manifest into position and truncate.
		if _, err := f.Seek(chunksStart, io.SeekStart); err != nil {
			return err
		}
		if _, err := f.Write(manifestBytes); err != nil {
			return err
		}
		return f.Truncate(chunksStart + manifestLen)
	}

	// Copy chunk data from back to front so the source region does not
	// overlap with a yet-to-be-read part of the destination. Windowed IO
	// keeps memory bounded regardless of archive size.
	const window = 4 * 1024 * 1024
	buf := make([]byte, window)
	src := chunksEnd
	dst := chunksEnd + manifestLen
	remaining := chunksEnd - chunksStart
	for remaining > 0 {
		n := int64(window)
		if remaining < n {
			n = remaining
		}
		src -= n
		dst -= n
		if _, err := f.Seek(src, io.SeekStart); err != nil {
			return err
		}
		if _, err := io.ReadFull(f, buf[:n]); err != nil {
			return err
		}
		if _, err := f.Seek(dst, io.SeekStart); err != nil {
			return err
		}
		if _, err := f.Write(buf[:n]); err != nil {
			return err
		}
		remaining -= n
	}
	// Install the manifest in the gap we just opened up.
	if _, err := f.Seek(chunksStart, io.SeekStart); err != nil {
		return err
	}
	if _, err := f.Write(manifestBytes); err != nil {
		return err
	}
	return nil
}

// chunkBuilder accumulates KV pairs in memory, flushing them to the
// archive as gzip-compressed chunks when the target size is reached.
type chunkBuilder struct {
	target int
	level  int

	buf     *bytes.Buffer
	records uint32
}

func newChunkBuilder(target, level int) *chunkBuilder {
	return &chunkBuilder{
		target: target,
		level:  level,
		buf:    bytes.NewBuffer(make([]byte, 0, target)),
	}
}

// Add appends a single KV record to the in-progress chunk.
func (cb *chunkBuilder) Add(key, val []byte) error {
	if len(key) > int(^uint32(0)) || len(val) > int(^uint32(0)) {
		return fmt.Errorf("snapshot: record too large")
	}
	var hdr [4]byte
	byteOrder.PutUint32(hdr[:], uint32(len(key)))
	cb.buf.Write(hdr[:])
	cb.buf.Write(key)
	byteOrder.PutUint32(hdr[:], uint32(len(val)))
	cb.buf.Write(hdr[:])
	cb.buf.Write(val)
	cb.records++
	return nil
}

// ShouldFlush reports whether the buffered payload has reached the target.
func (cb *chunkBuilder) ShouldFlush() bool {
	return cb.buf.Len() >= cb.target
}

// HasData reports whether the builder has any buffered records at all.
func (cb *chunkBuilder) HasData() bool {
	return cb.records > 0
}

// Flush emits the current chunk to w and resets the builder for the next
// chunk. Returns a ChunkInfo suitable for insertion into a Manifest.
func (cb *chunkBuilder) Flush(w io.Writer, id uint32) (ChunkInfo, error) {
	uncompressed := cb.buf.Bytes()
	sum := sha256.Sum256(uncompressed)

	// Compress the payload into a scratch buffer so we know the on-disk
	// size before writing the chunk header.
	var gzBuf bytes.Buffer
	gz, err := gzip.NewWriterLevel(&gzBuf, cb.level)
	if err != nil {
		return ChunkInfo{}, fmt.Errorf("snapshot: gzip writer: %w", err)
	}
	if _, err := gz.Write(uncompressed); err != nil {
		return ChunkInfo{}, fmt.Errorf("snapshot: gzip write: %w", err)
	}
	if err := gz.Close(); err != nil {
		return ChunkInfo{}, fmt.Errorf("snapshot: gzip close: %w", err)
	}
	compressedSum := sha256.Sum256(gzBuf.Bytes())

	hdr := make([]byte, ChunkHeaderLen)
	byteOrder.PutUint32(hdr[0:4], id)
	byteOrder.PutUint32(hdr[4:8], uint32(len(uncompressed)))
	byteOrder.PutUint32(hdr[8:12], uint32(gzBuf.Len()))
	copy(hdr[12:44], sum[:])
	if _, err := w.Write(hdr); err != nil {
		return ChunkInfo{}, err
	}
	if _, err := w.Write(gzBuf.Bytes()); err != nil {
		return ChunkInfo{}, err
	}

	info := ChunkInfo{
		ID:               id,
		UncompressedSize: uint32(len(uncompressed)),
		CompressedSize:   uint32(gzBuf.Len()),
		SHA256:           hex.EncodeToString(sum[:]),
		CompressedSHA256: hex.EncodeToString(compressedSum[:]),
		Records:          cb.records,
	}
	cb.buf.Reset()
	cb.records = 0
	return info, nil
}

// MarshalManifest is exposed for callers that want to embed a manifest
// inside another payload (e.g. an RPC response).
func MarshalManifest(m *Manifest) ([]byte, error) {
	return json.MarshalIndent(m, "", "  ")
}
