package snapshot

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"
)

// Well-known source names for the DBs that a BDAG node needs to come
// online. Using stable string names (rather than numeric ids) makes the
// archive self-describing and future-proof: new sources can be added in
// a later release and older readers will simply ignore them with a
// clear "unknown source" error.
const (
	// SourceChain is the BDAG chain DB: blocks, headers, DAG metadata,
	// main chain map, tips, and indexes.
	SourceChain = "chain"

	// SourceEVM is the embedded go-ethereum chain data directory,
	// containing the EVM state trie, account snapshot, header store,
	// and freezer that bevm expects to find. Without this source a
	// freshly-imported node must still replay every block to rebuild
	// EVM state — which is what the whole snapshot mechanism is meant
	// to avoid.
	SourceEVM = "evm"
)

// SourceInfo describes one logical database carved out of the snapshot
// archive. Chunks belonging to a source form a contiguous id range
// [ChunkStart, ChunkStart+ChunkCount), so an importer can route chunks
// to the right destination DB without parsing any chunk bodies.
type SourceInfo struct {
	// Name is a stable identifier (see SourceChain / SourceEVM). The
	// importer maps names to destination databases; unknown names cause
	// a hard error rather than silent data loss.
	Name string `json:"name"`

	// ChunkStart is the id of the first chunk in this source.
	ChunkStart uint32 `json:"chunk_start"`

	// ChunkCount is the number of chunks contributed by this source.
	ChunkCount uint32 `json:"chunk_count"`

	// TotalKeys / TotalUncompressed / TotalCompressed are cumulative
	// per-source metrics; they let operators see at a glance how big
	// each component of a snapshot is.
	TotalKeys         uint64 `json:"total_keys"`
	TotalUncompressed uint64 `json:"total_uncompressed"`
	TotalCompressed   uint64 `json:"total_compressed"`
}

// ChunkInfo describes a single chunk inside a snapshot archive.
type ChunkInfo struct {
	// ID is the zero-based position of the chunk in the archive. It
	// doubles as the stable identifier peers use to request a specific
	// chunk.
	ID uint32 `json:"id"`

	// UncompressedSize is the length of the chunk body before gzip
	// compression. It bounds the amount of memory the importer must
	// allocate to inflate the chunk.
	UncompressedSize uint32 `json:"uncompressed_size"`

	// CompressedSize is the on-disk length of the gzip-encoded body.
	CompressedSize uint32 `json:"compressed_size"`

	// SHA256 is the sha256 hex digest of the uncompressed body. Importers
	// MUST reject any chunk that does not match this digest.
	SHA256 string `json:"sha256"`

	// CompressedSHA256 is the sha256 hex digest of the exact compressed
	// body stored in the archive. It is optional for legacy manifests, but
	// enables content-addressed P2P transfer without inflating the chunk.
	CompressedSHA256 string `json:"compressed_sha256,omitempty"`

	// Records is the number of key/value records encoded in the chunk.
	// It is informational but handy for progress reporting.
	Records uint32 `json:"records"`
}

// Manifest is the JSON metadata block embedded in a snapshot archive (and
// also written out as a side-car file). It enumerates every chunk and
// records chain-level anchor information so an importer can sanity-check a
// snapshot before applying it.
type Manifest struct {
	// FormatVersion matches the binary header version. Included in JSON so
	// tooling that only has the side-car manifest can still refuse to
	// parse an incompatible snapshot.
	FormatVersion uint32 `json:"format_version"`

	// Network is the human-readable network name (mainnet, testnet, ...)
	// the snapshot was produced on. Importers MUST refuse a snapshot from
	// a different network than the one they are configured for.
	Network string `json:"network"`

	// BDAGVersion is the software version string of the node that
	// produced the snapshot. Purely informational.
	BDAGVersion string `json:"bdag_version"`

	// DBEngine is the chain DB engine ("pebble", "leveldb", ...). Mostly
	// informational — snapshots are engine-agnostic on the wire.
	DBEngine string `json:"db_engine"`

	// CreatedAt is the UTC timestamp the snapshot was produced.
	CreatedAt time.Time `json:"created_at"`

	// TipHash is the hex-encoded hash of the DAG main tip at the moment
	// the snapshot was taken.
	TipHash string `json:"tip_hash"`

	// TipOrder is the main-chain order of the tip block.
	TipOrder uint64 `json:"tip_order"`

	// TipID is the blockdag internal block id of the tip.
	TipID uint64 `json:"tip_id"`

	// BlockTotal is the total number of blocks in the DAG at snapshot
	// time. Importers use it (together with TipOrder) to detect obviously
	// truncated snapshots.
	BlockTotal uint64 `json:"block_total"`

	// StateRoot is the EVM state root associated with the tip. Informs
	// downstream components (e.g. bevm) which state to load after import.
	StateRoot string `json:"state_root"`

	// TotalKeys is the total number of key/value pairs across all chunks.
	TotalKeys uint64 `json:"total_keys"`

	// TotalUncompressed is the sum of all chunks' uncompressed sizes.
	TotalUncompressed uint64 `json:"total_uncompressed"`

	// TotalCompressed is the sum of all chunks' compressed sizes.
	TotalCompressed uint64 `json:"total_compressed"`

	// Sources carves up the chunk list into per-database ranges. A
	// snapshot with only a chain DB will have a single source named
	// "chain"; a full bootstrap snapshot also includes "evm" so a
	// fresh node can skip EVM state replay. Sources are applied in
	// order on import.
	//
	// Archives produced before this field existed (format v1 prior to
	// multi-source support) will have an empty slice here; loaders
	// must treat that case as a single implicit "chain" source.
	Sources []SourceInfo `json:"sources,omitempty"`

	// Chunks enumerates every chunk in the archive in emission order.
	Chunks []ChunkInfo `json:"chunks"`

	// RootHash is the sha256 hex digest of all chunk hashes concatenated
	// in id order. A trusted source can publish just this string to
	// vouch for an entire snapshot.
	RootHash string `json:"root_hash"`

	// EVMStateAncientGzipBytes, when non-zero (format v4+), is the length of a
	// gzip-compressed tar appended after the last chunk. The tar unpacks under
	// the EVM ancient root with a top-level "state/" directory (pathdb state
	// freezer files). Used when the source state freezer had Tail()>0 so
	// synthetic ancient keys are not used.
	EVMStateAncientGzipBytes uint64 `json:"evm_state_ancient_gzip_bytes,omitempty"`

	// EVMStateAncientSha256 is the lowercase hex sha256 of the gzip appendix
	// bytes (the exact blob on disk), not of the uncompressed tar.
	EVMStateAncientSha256 string `json:"evm_state_ancient_sha256,omitempty"`
}

// Validate performs structural checks on the manifest. It does NOT verify
// chunk contents — that happens as chunks are read. Use it to refuse a
// manifest before committing any IO or DB state to the import.
func (m *Manifest) Validate(network string) error {
	if m == nil {
		return fmt.Errorf("snapshot: manifest is nil")
	}
	if m.FormatVersion < OldestSupportedFormatVersion || m.FormatVersion > FormatVersion {
		return fmt.Errorf("snapshot: unsupported format version %d (supported %d–%d)", m.FormatVersion, OldestSupportedFormatVersion, FormatVersion)
	}
	if network != "" && m.Network != "" && m.Network != network {
		return fmt.Errorf("snapshot: network mismatch: manifest=%q local=%q", m.Network, network)
	}
	if len(m.Chunks) == 0 {
		return fmt.Errorf("snapshot: manifest has no chunks")
	}
	for i, c := range m.Chunks {
		if c.ID != uint32(i) {
			return fmt.Errorf("snapshot: chunk %d has non-sequential id %d", i, c.ID)
		}
		if c.UncompressedSize == 0 {
			return fmt.Errorf("snapshot: chunk %d has zero uncompressed size", i)
		}
		if c.UncompressedSize > MaxChunkUncompressedBytes {
			return fmt.Errorf("snapshot: chunk %d exceeds max size (%d > %d)", i, c.UncompressedSize, MaxChunkUncompressedBytes)
		}
		if len(c.SHA256) != 2*sha256.Size {
			return fmt.Errorf("snapshot: chunk %d has malformed sha256 digest", i)
		}
		if c.CompressedSHA256 != "" {
			if raw, err := hex.DecodeString(c.CompressedSHA256); err != nil || len(raw) != sha256.Size {
				return fmt.Errorf("snapshot: chunk %d has malformed compressed sha256 digest", i)
			}
		}
	}
	if err := m.validateSources(); err != nil {
		return err
	}
	if m.EVMStateAncientGzipBytes > 0 {
		if len(m.EVMStateAncientSha256) != 2*sha256.Size {
			return fmt.Errorf("snapshot: evm state ancient appendix present but sha256 missing or malformed")
		}
	}
	computed := ComputeRootHash(m.Chunks)
	if m.RootHash != "" && m.RootHash != computed {
		return fmt.Errorf("snapshot: root hash mismatch (manifest=%s computed=%s)", m.RootHash, computed)
	}
	return nil
}

// validateSources ensures the per-source chunk ranges tile the full
// chunk list exactly once without gaps or overlaps. Legacy single-
// source manifests (no Sources field) are accepted unchanged.
func (m *Manifest) validateSources() error {
	if len(m.Sources) == 0 {
		return nil
	}
	var covered uint32
	for i, s := range m.Sources {
		if s.Name == "" {
			return fmt.Errorf("snapshot: source %d has empty name", i)
		}
		if s.ChunkStart != covered {
			return fmt.Errorf("snapshot: source %q starts at %d but previous source ended at %d", s.Name, s.ChunkStart, covered)
		}
		if uint64(s.ChunkStart)+uint64(s.ChunkCount) > uint64(len(m.Chunks)) {
			return fmt.Errorf("snapshot: source %q spans beyond chunk list", s.Name)
		}
		covered = s.ChunkStart + s.ChunkCount
	}
	if covered != uint32(len(m.Chunks)) {
		return fmt.Errorf("snapshot: sources cover %d chunks but manifest has %d", covered, len(m.Chunks))
	}
	// Duplicate-name check: an importer maps source names to
	// destination DBs, so two chunks claiming the same name would be
	// ambiguous. Reject up front rather than overwriting state.
	seen := make(map[string]struct{}, len(m.Sources))
	for _, s := range m.Sources {
		if _, ok := seen[s.Name]; ok {
			return fmt.Errorf("snapshot: duplicate source name %q", s.Name)
		}
		seen[s.Name] = struct{}{}
	}
	return nil
}

// EffectiveSources returns m.Sources if it is populated, otherwise a
// synthesized single-source view named SourceChain that covers every
// chunk. Callers that want to route chunks on import should always use
// this method so they seamlessly support old single-source archives.
func (m *Manifest) EffectiveSources() []SourceInfo {
	if len(m.Sources) > 0 {
		return m.Sources
	}
	if len(m.Chunks) == 0 {
		return nil
	}
	total := uint32(len(m.Chunks))
	var keys, u, c uint64
	for _, ch := range m.Chunks {
		keys += uint64(ch.Records)
		u += uint64(ch.UncompressedSize)
		c += uint64(ch.CompressedSize)
	}
	return []SourceInfo{{
		Name:              SourceChain,
		ChunkStart:        0,
		ChunkCount:        total,
		TotalKeys:         keys,
		TotalUncompressed: u,
		TotalCompressed:   c,
	}}
}

// Finalize populates RootHash and totals from the current Chunks slice. It
// is called by the writer once emission completes. If the writer filled
// in Sources with boundary information but left per-source totals at
// zero, Finalize computes those totals from the chunks each source
// spans so the manifest is fully self-consistent.
func (m *Manifest) Finalize() {
	m.RootHash = ComputeRootHash(m.Chunks)
	var keys, u, c uint64
	for _, ch := range m.Chunks {
		keys += uint64(ch.Records)
		u += uint64(ch.UncompressedSize)
		c += uint64(ch.CompressedSize)
	}
	m.TotalKeys = keys
	m.TotalUncompressed = u
	m.TotalCompressed = c

	for i, s := range m.Sources {
		if s.ChunkCount == 0 {
			continue
		}
		var sk, su, sc uint64
		for j := s.ChunkStart; j < s.ChunkStart+s.ChunkCount; j++ {
			ch := m.Chunks[j]
			sk += uint64(ch.Records)
			su += uint64(ch.UncompressedSize)
			sc += uint64(ch.CompressedSize)
		}
		m.Sources[i].TotalKeys = sk
		m.Sources[i].TotalUncompressed = su
		m.Sources[i].TotalCompressed = sc
	}
}

// ComputeRootHash returns the sha256 hex digest of every chunk's sha256
// concatenated in id order. The ordering is defensive: even though Chunks
// are always emitted in order, computing the digest from the slice lets
// callers double-check that ordering before trusting it.
func ComputeRootHash(chunks []ChunkInfo) string {
	h := sha256.New()
	for _, c := range chunks {
		raw, err := hex.DecodeString(c.SHA256)
		if err != nil || len(raw) != sha256.Size {
			// A malformed per-chunk digest poisons the root digest
			// so mismatches surface loudly at Validate() time.
			h.Write([]byte(c.SHA256))
			continue
		}
		h.Write(raw)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// MarshalIndented produces a stable, human-friendly JSON rendering. The
// indented form is used for both the embedded manifest and the side-car
// file so diffs between snapshots remain reviewable.
func (m *Manifest) MarshalIndented() ([]byte, error) {
	return json.MarshalIndent(m, "", "  ")
}
