// Package snapshot implements node bootstrap snapshots.
//
// A snapshot is a self-describing, integrity-checked archive of the chain
// database at a specific tip. It is designed so a fresh node can import a
// trusted snapshot and come online without replaying every block from
// genesis ("fast bootstrap"). Snapshots are chunked so peers (or HTTP
// mirrors) can stream them with per-chunk verification and resumable
// downloads.
//
// File layout (little-endian except where noted):
//
//	+-------------------------------------------------------------+
//	|  Magic       (8  bytes)  "BDAGSNP\x00"                      |
//	|  Version     (4  bytes)  uint32 BE                          |
//	|  ManifestLen (4  bytes)  uint32 BE                          |
//	|  Reserved    (16 bytes)  zero-padded                        |
//	+-------------------------------------------------------------+
//	|  Manifest    (ManifestLen bytes)  JSON-encoded Manifest     |
//	+-------------------------------------------------------------+
//	|  Chunks ...  repeated N times                               |
//	|  (v4 optional) EVM state ancient/ raw .tar.gz appendix      |
//	|    ChunkID        (4  bytes) uint32 BE                      |
//	|    UncompressedSz (4  bytes) uint32 BE                      |
//	|    CompressedSz   (4  bytes) uint32 BE                      |
//	|    SHA256         (32 bytes) of uncompressed body           |
//	|    CompressedBody (CompressedSz bytes) gzip-encoded records |
//	+-------------------------------------------------------------+
//
// Chunk body (after gzip decompression) is a sequence of records:
//
//	KeyLen (4 bytes BE) | Key | ValLen (4 bytes BE) | Val
//
// The manifest enumerates every chunk with its sha256 so an entire snapshot
// can be authenticated with a single root digest (the manifest's own
// sha256). This allows a peer to validate a single manifest from a trusted
// source, then fetch chunks in any order, verifying each one against the
// expected digest in the manifest before committing it to the local DB.
package snapshot

import "encoding/binary"

const (
	// FormatVersion is the snapshot on-disk format version. Incremented on
	// any incompatible format change.
	//
	// v2: EVM source archives embed geth chain-freezer tables (headers,
	// bodies, …) that are invisible to the KV iterator; v1 exports omitted
	// them and broke import for pruned EVM chaindata. The chain source embeds
	// BDAG freezer tables (headers/blocks/dagblocks), distinct from geth.
	//
	// v3: EVM source also embeds pathdb state-history freezer tables under
	// ancient/state (history.meta, account.*, storage.*). Without them, path-
	// scheme chaindata fails repair with an empty state freezer vs non-zero
	// disk state id.
	//
	// v4: When the state-history freezer has Tail()>0, synthetic ancient keys
	// cannot be imported onto an empty destination (indices are global). Those
	// exports carry a raw gzip-tar of ancient/state after all chunks; see
	// Manifest.EVMStateAncientGzipBytes / EVMStateAncientSha256.
	FormatVersion uint32 = 4

	// OldestSupportedFormatVersion is the minimum file/manifest version this
	// package can still read.
	OldestSupportedFormatVersion uint32 = 1

	// Magic identifies a BDAG snapshot file.
	Magic = "BDAGSNP\x00"

	// HeaderLen is the fixed length of the file header in bytes.
	HeaderLen = 32

	// ChunkHeaderLen is the fixed length of the per-chunk header in bytes.
	ChunkHeaderLen = 44

	// DefaultChunkTargetBytes is the soft target size (uncompressed) for a
	// single chunk. The writer emits a chunk once the accumulated
	// uncompressed key/value payload exceeds this threshold.
	DefaultChunkTargetBytes = 16 * 1024 * 1024

	// MaxChunkUncompressedBytes is an upper bound to guard against
	// malicious or corrupt inputs during import.
	MaxChunkUncompressedBytes = 256 * 1024 * 1024

	// ManifestFileSuffix is the filename suffix for the stand-alone JSON
	// manifest written next to the snapshot archive. The side-car manifest
	// is convenient for peers that want to inspect snapshot metadata
	// without downloading the full archive.
	ManifestFileSuffix = ".manifest.json"

	// ArchiveFileSuffix is the filename suffix for the binary snapshot
	// archive.
	ArchiveFileSuffix = ".bdsnap"
)

// byteOrder is the integer encoding used in the binary framing. Big endian
// is chosen for cross-architecture readability in file hex dumps.
var byteOrder = binary.BigEndian
