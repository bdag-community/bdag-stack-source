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
	"time"

	ethrawdb "github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/ethdb"
)

// ReadOptions tunes snapshot consumption. Zero values select defaults.
type ReadOptions struct {
	// BatchBytes is the maximum size of a single ethdb.Batch before it is
	// flushed to disk during import. Larger batches are faster but use
	// more memory. Defaults to 64 MiB.
	BatchBytes int

	// AncientBatchBytes is the approximate maximum synthetic ancient/freezer
	// payload held before it is appended to the destination freezer. Defaults
	// to 64 MiB.
	AncientBatchBytes int

	// ProgressInterval controls how often progress logging occurs while
	// reading a snapshot. Zero selects 8s.
	ProgressInterval time.Duration

	// Network, if non-empty, is compared against the manifest's network
	// string and the import aborts on mismatch. Callers are strongly
	// encouraged to set this.
	Network string

	// RequireEmpty, when true, aborts the import if the destination db
	// already contains any keys in the snapshot's prefix range. This is
	// the safe default for bootstrapping a fresh node.
	RequireEmpty bool
}

// OpenManifest reads and validates just the manifest from a snapshot
// archive without decoding any chunks. This is the fast-path used by RPC
// handlers and tooling that only needs to inspect snapshot metadata.
func OpenManifest(path string) (*Manifest, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	m, _, err := readHeaderAndManifest(f)
	return m, err
}

// Verify reads the entire archive and checks that every chunk hash
// matches the manifest. It never touches any database — use it before
// handing a snapshot to another operator, or as part of CI.
func Verify(path string) (*Manifest, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	m, _, err := readHeaderAndManifest(f)
	if err != nil {
		return nil, err
	}
	if err := m.Validate(""); err != nil {
		return nil, err
	}
	for i, expected := range m.Chunks {
		ci, _, err := readChunk(f, expected.CompressedSize, expected.UncompressedSize)
		if err != nil {
			return nil, fmt.Errorf("snapshot: read chunk %d: %w", i, err)
		}
		if ci.ID != expected.ID || ci.SHA256 != expected.SHA256 || ci.UncompressedSize != expected.UncompressedSize {
			return nil, fmt.Errorf("snapshot: chunk %d mismatch (manifest=%+v got=%+v)", i, expected, ci)
		}
		if expected.CompressedSHA256 != "" && ci.CompressedSHA256 != expected.CompressedSHA256 {
			return nil, fmt.Errorf("snapshot: chunk %d mismatch (manifest=%+v got=%+v)", i, expected, ci)
		}
	}
	if err := verifyEVMStateAncientTarGz(f, m.EVMStateAncientGzipBytes, m.EVMStateAncientSha256); err != nil {
		return nil, err
	}
	return m, nil
}

// Read imports a single-source snapshot archive into db. It is
// equivalent to MultiRead with a one-entry destination map keyed by
// whatever source name the archive advertises (typically SourceChain).
//
// It is an error to call Read on a multi-source archive that contains
// anything beyond SourceChain — those require MultiRead so the caller
// can supply the extra destinations explicitly. This makes it
// impossible to silently drop EVM state during an import.
func Read(path string, db ethdb.Database, opts ReadOptions) (*Manifest, error) {
	if db == nil {
		return nil, fmt.Errorf("snapshot: nil database")
	}
	m, err := OpenManifest(path)
	if err != nil {
		return nil, err
	}
	srcs := m.EffectiveSources()
	if len(srcs) != 1 {
		names := make([]string, len(srcs))
		for i, s := range srcs {
			names[i] = s.Name
		}
		return nil, fmt.Errorf("snapshot: archive has %d sources (%v); use MultiRead", len(srcs), names)
	}
	return MultiRead(path, map[string]ethdb.Database{srcs[0].Name: db}, opts)
}

// MultiRead imports a multi-source snapshot archive, dispatching each
// source's chunks into the matching destination database from dests.
// Every source named in the manifest MUST have a destination — a
// missing destination is treated as a hard error so we never silently
// drop part of the snapshot. Extra destinations (sources the archive
// does not provide) are ignored.
//
// The import is not atomic across sources: if the process crashes
// mid-import the destination DBs may contain partial snapshots.
// Recovery is "cleanup data dir + retry", which mirrors the behaviour
// of the existing blockchain import command.
func MultiRead(path string, dests map[string]ethdb.Database, opts ReadOptions) (*Manifest, error) {
	if len(dests) == 0 {
		return nil, fmt.Errorf("snapshot: no destinations provided")
	}
	if opts.BatchBytes <= 0 {
		opts.BatchBytes = 64 * 1024 * 1024
	}
	if opts.AncientBatchBytes <= 0 {
		opts.AncientBatchBytes = 64 * 1024 * 1024
	}
	if opts.ProgressInterval <= 0 {
		opts.ProgressInterval = 8 * time.Second
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	m, _, err := readHeaderAndManifest(f)
	if err != nil {
		return nil, err
	}
	if err := m.Validate(opts.Network); err != nil {
		return nil, err
	}

	// Resolve every manifest source to an actual destination up front
	// so a misconfigured call fails before any data is written.
	sources := m.EffectiveSources()
	resolved := make([]ethdb.Database, len(sources))
	for i, s := range sources {
		db, ok := dests[s.Name]
		if !ok || db == nil {
			return nil, fmt.Errorf("snapshot: manifest source %q has no destination", s.Name)
		}
		resolved[i] = db
	}

	// The empty-dest check runs per unique db handle so two sources
	// accidentally pointing at the same DB are still caught once.
	if opts.RequireEmpty {
		seen := make(map[ethdb.Database]struct{})
		for i, db := range resolved {
			if _, ok := seen[db]; ok {
				continue
			}
			seen[db] = struct{}{}
			empty, err := isEmpty(db)
			if err != nil {
				return nil, fmt.Errorf("snapshot: probe destination %q: %w", sources[i].Name, err)
			}
			if !empty {
				return nil, fmt.Errorf("snapshot: destination %q is not empty; refusing to import. Cleanup the data dir first.", sources[i].Name)
			}
		}
	}

	var (
		appliedTotal uint64
		lastReport   = time.Now()
	)
	for si, src := range sources {
		db := resolved[si]
		batch := db.NewBatch()
		var sourceKeys uint64
		ancientBuf := &ancientImportBuffer{
			sourceName:          src.Name,
			db:                  db,
			maxBytes:            opts.AncientBatchBytes,
			hasEVMStateAppendix: m.EVMStateAncientGzipBytes > 0,
		}
		for j := uint32(0); j < src.ChunkCount; j++ {
			idx := int(src.ChunkStart + j)
			expected := m.Chunks[idx]
			_, records, err := readChunk(f, expected.CompressedSize, expected.UncompressedSize)
			if err != nil {
				return nil, fmt.Errorf("snapshot: read chunk %d (source %q): %w", idx, src.Name, err)
			}
			for _, kv := range records {
				if _, _, ok := decodeAncientSnapKey(kv.Key); ok {
					sourceKeys++
					appliedTotal++
					if err := ancientBuf.Add(kv); err != nil {
						return nil, err
					}
					continue
				}
				if err := batch.Put(kv.Key, kv.Value); err != nil {
					return nil, fmt.Errorf("snapshot: batch put (source %q): %w", src.Name, err)
				}
				sourceKeys++
				appliedTotal++
				if batch.ValueSize() >= opts.BatchBytes {
					if err := batch.Write(); err != nil {
						return nil, fmt.Errorf("snapshot: batch write (source %q): %w", src.Name, err)
					}
					batch.Reset()
				}
			}
			if time.Since(lastReport) > opts.ProgressInterval {
				log.Info("Snapshot import in progress",
					"source", src.Name,
					"chunk", idx+1,
					"of", len(m.Chunks),
					"source_keys", sourceKeys,
					"total_keys", appliedTotal,
					"target", m.TotalKeys,
				)
				lastReport = time.Now()
			}
		}
		if batch.ValueSize() > 0 {
			if err := batch.Write(); err != nil {
				return nil, fmt.Errorf("snapshot: final batch write (source %q): %w", src.Name, err)
			}
		}

		if err := ancientBuf.Flush(); err != nil {
			return nil, err
		}
		log.Info("Snapshot source imported", "source", src.Name, "keys", sourceKeys, "chunks", src.ChunkCount)
	}
	if m.EVMStateAncientGzipBytes > 0 {
		evmDB, ok := dests[SourceEVM]
		if !ok || evmDB == nil {
			return nil, fmt.Errorf("snapshot: manifest has evm state ancient appendix but no %q destination", SourceEVM)
		}
		ad, ok := evmDB.(interface {
			AncientDatadir() (string, error)
		})
		if !ok {
			return nil, fmt.Errorf("snapshot: %q destination has no ancient datadir for state appendix", SourceEVM)
		}
		ancientRoot, err := ad.AncientDatadir()
		if err != nil {
			return nil, fmt.Errorf("snapshot: ancient datadir (%q): %w", SourceEVM, err)
		}
		if err := extractEVMStateAncientTarGz(f, ancientRoot, m.EVMStateAncientGzipBytes, m.EVMStateAncientSha256); err != nil {
			return nil, err
		}
	}
	log.Info("Snapshot import complete", "sources", len(sources), "keys", appliedTotal, "chunks", len(m.Chunks), "tip", m.TipHash, "order", m.TipOrder)
	return m, nil
}

type ancientImportBuffer struct {
	sourceName          string
	db                  ethdb.Database
	maxBytes            int
	recs                []keyValue
	bytes               int
	lastNum             uint64
	haveLastNum         bool
	hasEVMStateAppendix bool
}

func (b *ancientImportBuffer) Add(kv keyValue) error {
	if b == nil {
		return nil
	}
	kind, num, _ := decodeAncientSnapKey(kv.Key)
	if b.maxBytes > 0 && b.bytes >= b.maxBytes && len(b.recs) > 0 && b.haveLastNum && num != b.lastNum {
		if err := b.Flush(); err != nil {
			return err
		}
	}
	if b.sourceName == SourceEVM && b.hasEVMStateAppendix {
		if kindRankForSource(SourceEVM, kind) >= 5 {
			return fmt.Errorf("snapshot: archive mixes evm state ancient appendix with synthetic state keys")
		}
	}
	b.recs = append(b.recs, kv)
	b.bytes += len(kv.Key) + len(kv.Value)
	b.lastNum = num
	b.haveLastNum = true
	return nil
}

func (b *ancientImportBuffer) Flush() error {
	if b == nil || len(b.recs) == 0 {
		return nil
	}
	if err := importAncientRecords(b.sourceName, b.db, b.recs); err != nil {
		return err
	}
	b.recs = nil
	b.bytes = 0
	b.haveLastNum = false
	return nil
}

func importAncientRecords(sourceName string, db ethdb.Database, recs []keyValue) error {
	if len(recs) == 0 {
		return nil
	}
	if sourceName == SourceEVM {
		chainRecs, stateRecs := PartitionEVMAncientRecs(recs)
		if len(chainRecs) > 0 {
			aw, ok := db.(ethdb.AncientWriter)
			if !ok {
				return fmt.Errorf("snapshot: destination %q does not support ancient writes", sourceName)
			}
			if err := importAncientTables(sourceName, aw, chainRecs); err != nil {
				return fmt.Errorf("snapshot: import chain ancients (source %q): %w", sourceName, err)
			}
		}
		if len(stateRecs) > 0 {
			ad, ok := db.(interface {
				AncientDatadir() (string, error)
			})
			if !ok {
				return fmt.Errorf("snapshot: destination %q has no ancient datadir for state histories", sourceName)
			}
			ancientRoot, err := ad.AncientDatadir()
			if err != nil {
				return fmt.Errorf("snapshot: ancient datadir (source %q): %w", sourceName, err)
			}
			sf, err := ethrawdb.NewStateFreezer(ancientRoot, false, false)
			if err != nil {
				return fmt.Errorf("snapshot: open state history freezer (source %q): %w", sourceName, err)
			}
			defer sf.Close()
			if err := importAncientTables(sourceName, sf, stateRecs); err != nil {
				return fmt.Errorf("snapshot: import state histories (source %q): %w", sourceName, err)
			}
		}
		return nil
	}
	aw, ok := db.(ethdb.AncientWriter)
	if !ok {
		return fmt.Errorf("snapshot: destination %q does not support ancient writes", sourceName)
	}
	if err := importAncientTables(sourceName, aw, recs); err != nil {
		return fmt.Errorf("snapshot: import ancients (source %q): %w", sourceName, err)
	}
	return nil
}

// keyValue is a decoded record from a chunk body.
type keyValue struct {
	Key   []byte
	Value []byte
}

// readHeaderAndManifest parses the fixed file header followed by the
// JSON manifest. The returned offset is the position at which the first
// chunk begins.
func readHeaderAndManifest(f *os.File) (*Manifest, int64, error) {
	hdr := make([]byte, HeaderLen)
	if _, err := io.ReadFull(f, hdr); err != nil {
		return nil, 0, fmt.Errorf("snapshot: read header: %w", err)
	}
	if !bytes.Equal(hdr[0:8], []byte(Magic)) {
		return nil, 0, fmt.Errorf("snapshot: invalid magic")
	}
	ver := byteOrder.Uint32(hdr[8:12])
	if ver < OldestSupportedFormatVersion || ver > FormatVersion {
		return nil, 0, fmt.Errorf("snapshot: unsupported format version %d (supported %d–%d)", ver, OldestSupportedFormatVersion, FormatVersion)
	}
	manifestLen := byteOrder.Uint32(hdr[12:16])
	if manifestLen == 0 {
		return nil, 0, fmt.Errorf("snapshot: zero manifest length")
	}
	mBytes := make([]byte, manifestLen)
	if _, err := io.ReadFull(f, mBytes); err != nil {
		return nil, 0, fmt.Errorf("snapshot: read manifest: %w", err)
	}
	m := &Manifest{}
	if err := json.Unmarshal(mBytes, m); err != nil {
		return nil, 0, fmt.Errorf("snapshot: decode manifest: %w", err)
	}
	return m, int64(HeaderLen) + int64(manifestLen), nil
}

// readChunk reads the next chunk header + body, verifies its hash, and
// decodes the records inside. It enforces the declared compressed /
// uncompressed sizes as a defense against corrupt or oversized chunks.
func readChunk(f *os.File, expectedCompressed, expectedUncompressed uint32) (ChunkInfo, []keyValue, error) {
	hdr := make([]byte, ChunkHeaderLen)
	if _, err := io.ReadFull(f, hdr); err != nil {
		return ChunkInfo{}, nil, fmt.Errorf("read chunk header: %w", err)
	}
	id := byteOrder.Uint32(hdr[0:4])
	uncompressed := byteOrder.Uint32(hdr[4:8])
	compressed := byteOrder.Uint32(hdr[8:12])
	expectedSum := hdr[12:44]

	if expectedCompressed != 0 && compressed != expectedCompressed {
		return ChunkInfo{}, nil, fmt.Errorf("compressed size mismatch (header=%d manifest=%d)", compressed, expectedCompressed)
	}
	if expectedUncompressed != 0 && uncompressed != expectedUncompressed {
		return ChunkInfo{}, nil, fmt.Errorf("uncompressed size mismatch (header=%d manifest=%d)", uncompressed, expectedUncompressed)
	}
	if uncompressed > MaxChunkUncompressedBytes {
		return ChunkInfo{}, nil, fmt.Errorf("chunk exceeds max size: %d", uncompressed)
	}

	body := make([]byte, compressed)
	if _, err := io.ReadFull(f, body); err != nil {
		return ChunkInfo{}, nil, fmt.Errorf("read chunk body: %w", err)
	}
	compressedSum := sha256.Sum256(body)

	gz, err := gzip.NewReader(bytes.NewReader(body))
	if err != nil {
		return ChunkInfo{}, nil, fmt.Errorf("gzip reader: %w", err)
	}
	raw := make([]byte, 0, uncompressed)
	// LimitReader guards against a gzip bomb — we know the declared
	// uncompressed size so the decoder should never produce more bytes
	// than that.
	lr := io.LimitReader(gz, int64(uncompressed)+1)
	raw, err = io.ReadAll(lr)
	_ = gz.Close()
	if err != nil {
		return ChunkInfo{}, nil, fmt.Errorf("gzip decode: %w", err)
	}
	if uint32(len(raw)) != uncompressed {
		return ChunkInfo{}, nil, fmt.Errorf("uncompressed size mismatch (decoded=%d declared=%d)", len(raw), uncompressed)
	}
	actualSum := sha256.Sum256(raw)
	if !bytes.Equal(actualSum[:], expectedSum) {
		return ChunkInfo{}, nil, fmt.Errorf("chunk %d sha256 mismatch", id)
	}

	records, err := decodeRecords(raw)
	if err != nil {
		return ChunkInfo{}, nil, err
	}
	return ChunkInfo{
		ID:               id,
		UncompressedSize: uncompressed,
		CompressedSize:   compressed,
		SHA256:           hex.EncodeToString(actualSum[:]),
		CompressedSHA256: hex.EncodeToString(compressedSum[:]),
		Records:          uint32(len(records)),
	}, records, nil
}

// decodeRecords parses the KV record stream produced by chunkBuilder.Add.
func decodeRecords(raw []byte) ([]keyValue, error) {
	out := make([]keyValue, 0, 1024)
	r := bytes.NewReader(raw)
	for r.Len() > 0 {
		var hdr [4]byte
		if _, err := io.ReadFull(r, hdr[:]); err != nil {
			return nil, fmt.Errorf("read key len: %w", err)
		}
		klen := byteOrder.Uint32(hdr[:])
		if int(klen) > r.Len() {
			return nil, fmt.Errorf("key len %d overflows chunk", klen)
		}
		key := make([]byte, klen)
		if _, err := io.ReadFull(r, key); err != nil {
			return nil, fmt.Errorf("read key: %w", err)
		}
		if _, err := io.ReadFull(r, hdr[:]); err != nil {
			return nil, fmt.Errorf("read val len: %w", err)
		}
		vlen := byteOrder.Uint32(hdr[:])
		if int(vlen) > r.Len() {
			return nil, fmt.Errorf("val len %d overflows chunk", vlen)
		}
		val := make([]byte, vlen)
		if _, err := io.ReadFull(r, val); err != nil {
			return nil, fmt.Errorf("read val: %w", err)
		}
		out = append(out, keyValue{Key: key, Value: val})
	}
	return out, nil
}

// isEmpty reports whether an ethdb.Database contains any keys. It reads
// only a single record so the probe is O(1) regardless of destination
// size.
func isEmpty(db ethdb.Database) (bool, error) {
	it := db.NewIterator(nil, nil)
	defer it.Release()
	if it.Next() {
		return false, nil
	}
	return true, it.Error()
}
