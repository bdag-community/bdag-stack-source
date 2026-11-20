package snapshot

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
)

// EVMAncientSegmentInfo describes a bounded byte range within the optional
// EVM state ancient gzip appendix.
type EVMAncientSegmentInfo struct {
	Offset uint64
	Size   uint64
	SHA256 string
}

// CompressedChunkLocation records where a chunk body lives in an archive.
type CompressedChunkLocation struct {
	HeaderOffset int64
	BodyOffset   int64
	Info         ChunkInfo
}

// ReadCompressedChunk returns the exact compressed chunk body stored in a
// snapshot archive. Chunk readers can transfer the stored bytes without
// inflating and re-compressing them on the serving peer.
func ReadCompressedChunk(path string, id uint32) (*Manifest, ChunkInfo, []byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, ChunkInfo{}, nil, err
	}
	defer f.Close()

	m, _, err := readHeaderAndManifest(f)
	if err != nil {
		return nil, ChunkInfo{}, nil, err
	}
	if err := m.Validate(""); err != nil {
		return nil, ChunkInfo{}, nil, err
	}
	if int(id) >= len(m.Chunks) {
		return nil, ChunkInfo{}, nil, fmt.Errorf("snapshot: chunk id %d out of range %d", id, len(m.Chunks))
	}

	for i := uint32(0); i <= id; i++ {
		var hdr [ChunkHeaderLen]byte
		if _, err := io.ReadFull(f, hdr[:]); err != nil {
			return nil, ChunkInfo{}, nil, fmt.Errorf("snapshot: read chunk %d header: %w", i, err)
		}
		got := ChunkInfo{
			ID:               byteOrder.Uint32(hdr[0:4]),
			UncompressedSize: byteOrder.Uint32(hdr[4:8]),
			CompressedSize:   byteOrder.Uint32(hdr[8:12]),
			SHA256:           hex.EncodeToString(hdr[12:44]),
		}
		if got.ID != i {
			return nil, ChunkInfo{}, nil, fmt.Errorf("snapshot: chunk id mismatch at %d: got %d", i, got.ID)
		}
		expected := m.Chunks[i]
		if got.ID != expected.ID || got.UncompressedSize != expected.UncompressedSize ||
			got.CompressedSize != expected.CompressedSize || got.SHA256 != expected.SHA256 {
			return nil, ChunkInfo{}, nil, fmt.Errorf("snapshot: chunk %d header mismatch", i)
		}
		if i != id {
			if _, err := f.Seek(int64(got.CompressedSize), io.SeekCurrent); err != nil {
				return nil, ChunkInfo{}, nil, fmt.Errorf("snapshot: skip chunk %d: %w", i, err)
			}
			continue
		}
		body := make([]byte, got.CompressedSize)
		if _, err := io.ReadFull(f, body); err != nil {
			return nil, ChunkInfo{}, nil, fmt.Errorf("snapshot: read chunk %d body: %w", i, err)
		}
		compressedSum := sha256.Sum256(body)
		got.CompressedSHA256 = hex.EncodeToString(compressedSum[:])
		if expected.CompressedSHA256 != "" && got.CompressedSHA256 != expected.CompressedSHA256 {
			return nil, ChunkInfo{}, nil, fmt.Errorf("snapshot: chunk %d compressed sha256 mismatch", i)
		}
		got.Records = expected.Records
		return m, got, body, nil
	}
	return nil, ChunkInfo{}, nil, fmt.Errorf("snapshot: chunk id %d not found", id)
}

// ReadCompressedChunkIndex validates archive chunk headers and returns body
// offsets for direct serving. When includeCompressedHashes is true, it also
// hashes each compressed body and enriches the returned manifest.
func ReadCompressedChunkIndex(path string, includeCompressedHashes bool) (*Manifest, map[uint32]CompressedChunkLocation, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, err
	}
	defer f.Close()

	m, _, err := readHeaderAndManifest(f)
	if err != nil {
		return nil, nil, err
	}
	if err := m.Validate(""); err != nil {
		return nil, nil, err
	}
	locations := make(map[uint32]CompressedChunkLocation, len(m.Chunks))
	for i := range m.Chunks {
		headerOffset, err := f.Seek(0, io.SeekCurrent)
		if err != nil {
			return nil, nil, fmt.Errorf("snapshot: locate chunk %d header: %w", i, err)
		}
		var hdr [ChunkHeaderLen]byte
		if _, err := io.ReadFull(f, hdr[:]); err != nil {
			return nil, nil, fmt.Errorf("snapshot: read chunk %d header: %w", i, err)
		}
		bodyOffset, err := f.Seek(0, io.SeekCurrent)
		if err != nil {
			return nil, nil, fmt.Errorf("snapshot: locate chunk %d body: %w", i, err)
		}
		got := ChunkInfo{
			ID:               byteOrder.Uint32(hdr[0:4]),
			UncompressedSize: byteOrder.Uint32(hdr[4:8]),
			CompressedSize:   byteOrder.Uint32(hdr[8:12]),
			SHA256:           hex.EncodeToString(hdr[12:44]),
		}
		expected := m.Chunks[i]
		if got.ID != expected.ID || got.UncompressedSize != expected.UncompressedSize ||
			got.CompressedSize != expected.CompressedSize || got.SHA256 != expected.SHA256 {
			return nil, nil, fmt.Errorf("snapshot: chunk %d header mismatch", i)
		}
		if includeCompressedHashes {
			h := sha256.New()
			if _, err := io.CopyN(h, f, int64(got.CompressedSize)); err != nil {
				return nil, nil, fmt.Errorf("snapshot: hash chunk %d body: %w", i, err)
			}
			got.CompressedSHA256 = hex.EncodeToString(h.Sum(nil))
			if expected.CompressedSHA256 != "" && got.CompressedSHA256 != expected.CompressedSHA256 {
				return nil, nil, fmt.Errorf("snapshot: chunk %d compressed sha256 mismatch", i)
			}
			m.Chunks[i].CompressedSHA256 = got.CompressedSHA256
		} else {
			got.CompressedSHA256 = expected.CompressedSHA256
			if _, err := f.Seek(int64(got.CompressedSize), io.SeekCurrent); err != nil {
				return nil, nil, fmt.Errorf("snapshot: skip chunk %d: %w", i, err)
			}
		}
		got.Records = expected.Records
		locations[got.ID] = CompressedChunkLocation{
			HeaderOffset: headerOffset,
			BodyOffset:   bodyOffset,
			Info:         got,
		}
	}
	return m, locations, nil
}

// ReadCompressedChunkBodyAt returns a chunk body from a known archive body
// offset. The caller is responsible for validating the body hash.
func ReadCompressedChunkBodyAt(path string, bodyOffset int64, compressedSize uint64) ([]byte, error) {
	if bodyOffset < 0 {
		return nil, fmt.Errorf("snapshot: negative chunk body offset")
	}
	if compressedSize > uint64(int(^uint(0)>>1)) {
		return nil, fmt.Errorf("snapshot: compressed chunk too large: %d", compressedSize)
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	if _, err := f.Seek(bodyOffset, io.SeekStart); err != nil {
		return nil, fmt.Errorf("snapshot: seek chunk body: %w", err)
	}
	body := make([]byte, int(compressedSize))
	if _, err := io.ReadFull(f, body); err != nil {
		return nil, fmt.Errorf("snapshot: read chunk body: %w", err)
	}
	return body, nil
}

// ReadCompressedChunkHashes returns sha256 digests for the exact compressed
// chunk bodies stored in the archive. It does not inflate chunks, making it the
// cheapest compatibility path for older manifests that predate
// ChunkInfo.CompressedSHA256.
func ReadCompressedChunkHashes(path string) (*Manifest, map[uint32]string, error) {
	m, locations, err := ReadCompressedChunkIndex(path, true)
	if err != nil {
		return nil, nil, err
	}
	hashes := make(map[uint32]string, len(m.Chunks))
	for id, loc := range locations {
		hashes[id] = loc.Info.CompressedSHA256
	}
	return m, hashes, nil
}

// ReadEVMStateAncientGzipSegmentHashes returns sha256 digests for bounded
// ranges of the optional v4 EVM state ancient appendix without loading the
// whole appendix into memory.
func ReadEVMStateAncientGzipSegmentHashes(path string, maxSegmentBytes uint64) (*Manifest, []EVMAncientSegmentInfo, error) {
	if maxSegmentBytes == 0 {
		return nil, nil, fmt.Errorf("snapshot: EVM state ancient segment size must be non-zero")
	}
	f, m, err := openEVMStateAncientGzip(path)
	if err != nil {
		return nil, nil, err
	}
	defer f.Close()

	segments := make([]EVMAncientSegmentInfo, 0, (m.EVMStateAncientGzipBytes+maxSegmentBytes-1)/maxSegmentBytes)
	var offset uint64
	for offset < m.EVMStateAncientGzipBytes {
		size := maxSegmentBytes
		if remaining := m.EVMStateAncientGzipBytes - offset; remaining < size {
			size = remaining
		}
		h := sha256.New()
		if _, err := io.CopyN(h, f, int64(size)); err != nil {
			return nil, nil, fmt.Errorf("snapshot: hash EVM state ancient segment at offset %d: %w", offset, err)
		}
		segments = append(segments, EVMAncientSegmentInfo{
			Offset: offset,
			Size:   size,
			SHA256: hex.EncodeToString(h.Sum(nil)),
		})
		offset += size
	}
	return m, segments, nil
}

// ReadEVMStateAncientGzip returns the optional v4 EVM state ancient appendix
// exactly as stored after the archive chunks.
func ReadEVMStateAncientGzip(path string) (*Manifest, []byte, error) {
	f, m, err := openEVMStateAncientGzip(path)
	if err != nil {
		return nil, nil, err
	}
	defer f.Close()

	if m.EVMStateAncientGzipBytes > uint64(int(^uint(0)>>1)) {
		return nil, nil, fmt.Errorf("snapshot: EVM state ancient appendix too large: %d", m.EVMStateAncientGzipBytes)
	}
	body := make([]byte, int(m.EVMStateAncientGzipBytes))
	if _, err := io.ReadFull(f, body); err != nil {
		return nil, nil, fmt.Errorf("snapshot: read EVM state ancient appendix: %w", err)
	}
	got := sha256.Sum256(body)
	if hex.EncodeToString(got[:]) != m.EVMStateAncientSha256 {
		return nil, nil, fmt.Errorf("snapshot: EVM state ancient appendix sha256 mismatch")
	}
	return m, body, nil
}

// ReadEVMStateAncientGzipSegment returns a bounded byte range from the optional
// v4 EVM state ancient appendix.
func ReadEVMStateAncientGzipSegment(path string, offset, size uint64) (*Manifest, []byte, error) {
	f, m, err := openEVMStateAncientGzip(path)
	if err != nil {
		return nil, nil, err
	}
	defer f.Close()
	if size == 0 {
		return nil, nil, fmt.Errorf("snapshot: EVM state ancient segment size must be non-zero")
	}
	if offset > m.EVMStateAncientGzipBytes || size > m.EVMStateAncientGzipBytes-offset {
		return nil, nil, fmt.Errorf("snapshot: EVM state ancient segment range out of bounds")
	}
	if _, err := f.Seek(int64(offset), io.SeekCurrent); err != nil {
		return nil, nil, fmt.Errorf("snapshot: seek EVM state ancient segment: %w", err)
	}
	if size > uint64(int(^uint(0)>>1)) {
		return nil, nil, fmt.Errorf("snapshot: EVM state ancient segment too large: %d", size)
	}
	body := make([]byte, int(size))
	if _, err := io.ReadFull(f, body); err != nil {
		return nil, nil, fmt.Errorf("snapshot: read EVM state ancient segment: %w", err)
	}
	return m, body, nil
}

func openEVMStateAncientGzip(path string) (*os.File, *Manifest, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, err
	}
	m, _, err := readHeaderAndManifest(f)
	if err != nil {
		_ = f.Close()
		return nil, nil, err
	}
	if err := m.Validate(""); err != nil {
		_ = f.Close()
		return nil, nil, err
	}
	if m.EVMStateAncientGzipBytes == 0 {
		_ = f.Close()
		return nil, nil, fmt.Errorf("snapshot: archive has no EVM state ancient appendix")
	}
	for i := range m.Chunks {
		var hdr [ChunkHeaderLen]byte
		if _, err := io.ReadFull(f, hdr[:]); err != nil {
			_ = f.Close()
			return nil, nil, fmt.Errorf("snapshot: read chunk %d header: %w", i, err)
		}
		got := ChunkInfo{
			ID:               byteOrder.Uint32(hdr[0:4]),
			UncompressedSize: byteOrder.Uint32(hdr[4:8]),
			CompressedSize:   byteOrder.Uint32(hdr[8:12]),
			SHA256:           hex.EncodeToString(hdr[12:44]),
		}
		expected := m.Chunks[i]
		if got.ID != expected.ID || got.UncompressedSize != expected.UncompressedSize ||
			got.CompressedSize != expected.CompressedSize || got.SHA256 != expected.SHA256 {
			_ = f.Close()
			return nil, nil, fmt.Errorf("snapshot: chunk %d header mismatch", i)
		}
		if _, err := f.Seek(int64(got.CompressedSize), io.SeekCurrent); err != nil {
			_ = f.Close()
			return nil, nil, fmt.Errorf("snapshot: skip chunk %d: %w", i, err)
		}
	}
	return f, m, nil
}
