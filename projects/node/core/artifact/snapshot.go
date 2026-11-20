package artifact

import (
	"crypto/ed25519"
	"fmt"
	"time"

	"github.com/BlockdagNetworkLabs/bdag/core/snapshot"
)

const MetadataSnapshotRoot = "snapshot_root"

type SnapshotOptions struct {
	ChainID            uint64
	CompressedHashes   map[uint32]string
	EVMAncientSegments []snapshot.EVMAncientSegmentInfo
	ExpiresAt          time.Time
	SignerKeyID        string
	SignerPrivateKey   ed25519.PrivateKey
	Now                time.Time
}

func FromSnapshotManifest(m *snapshot.Manifest, opts SnapshotOptions) (*Manifest, error) {
	if m == nil {
		return nil, fmt.Errorf("artifact: snapshot manifest is nil")
	}
	if opts.ChainID == 0 {
		return nil, fmt.Errorf("artifact: snapshot chain id is required")
	}
	createdAt := m.CreatedAt.UTC()
	if createdAt.IsZero() {
		createdAt = opts.Now.UTC()
	}
	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	}
	manifest := NewManifest(KindCheckpoint, m.Network, opts.ChainID)
	manifest.GenesisHash = ""
	manifest.TipOrder = m.TipOrder
	manifest.TipHash = m.TipHash
	manifest.TipID = m.TipID
	manifest.BlockTotal = m.BlockTotal
	manifest.StateRoot = m.StateRoot
	manifest.CreatedAt = createdAt
	manifest.ExpiresAt = opts.ExpiresAt.UTC()
	manifest.Metadata = map[string]string{MetadataSnapshotRoot: m.RootHash}
	manifest.Sources = make([]SourceInfo, 0, len(m.EffectiveSources()))
	for _, src := range m.EffectiveSources() {
		manifest.Sources = append(manifest.Sources, SourceInfo{
			Name:              src.Name,
			ChunkStart:        src.ChunkStart,
			ChunkCount:        src.ChunkCount,
			TotalRecords:      src.TotalKeys,
			TotalCompressed:   src.TotalCompressed,
			TotalUncompressed: src.TotalUncompressed,
		})
	}
	manifest.Chunks = make([]ChunkDescriptor, 0, SnapshotArtifactChunkCount(m))
	for _, src := range m.EffectiveSources() {
		for id := src.ChunkStart; id < src.ChunkStart+src.ChunkCount; id++ {
			if int(id) >= len(m.Chunks) {
				return nil, fmt.Errorf("artifact: snapshot source %q references missing chunk %d", src.Name, id)
			}
			ch := m.Chunks[id]
			compressedHash := ch.CompressedSHA256
			if opts.CompressedHashes != nil && opts.CompressedHashes[ch.ID] != "" {
				compressedHash = opts.CompressedHashes[ch.ID]
			}
			if compressedHash == "" {
				return nil, fmt.Errorf("artifact: missing compressed hash for snapshot chunk %d", ch.ID)
			}
			manifest.Chunks = append(manifest.Chunks, ChunkDescriptor{
				ID:                 ch.ID,
				Source:             src.Name,
				Class:              ChunkClassData,
				CompressedSize:     uint64(ch.CompressedSize),
				UncompressedSize:   uint64(ch.UncompressedSize),
				CompressedSHA256:   compressedHash,
				UncompressedSHA256: ch.SHA256,
				Records:            ch.Records,
			})
		}
	}
	if m.EVMStateAncientGzipBytes > 0 {
		id := uint32(len(manifest.Chunks))
		if m.EVMStateAncientSha256 == "" {
			return nil, fmt.Errorf("artifact: snapshot EVM state ancient hash is required")
		}
		segments := opts.EVMAncientSegments
		if len(segments) == 0 {
			segments = []snapshot.EVMAncientSegmentInfo{{
				Offset: 0,
				Size:   m.EVMStateAncientGzipBytes,
				SHA256: m.EVMStateAncientSha256,
			}}
		}
		var totalSegmentBytes uint64
		for i, seg := range segments {
			if seg.Offset != totalSegmentBytes {
				return nil, fmt.Errorf("artifact: EVM state ancient segment %d offset %d does not match expected %d", i, seg.Offset, totalSegmentBytes)
			}
			if seg.Size == 0 {
				return nil, fmt.Errorf("artifact: EVM state ancient segment %d has zero size", i)
			}
			totalSegmentBytes += seg.Size
		}
		if totalSegmentBytes != m.EVMStateAncientGzipBytes {
			return nil, fmt.Errorf("artifact: EVM state ancient segment total %d does not match snapshot appendix %d", totalSegmentBytes, m.EVMStateAncientGzipBytes)
		}
		manifest.Sources = append(manifest.Sources, SourceInfo{
			Name:            ChunkClassEVMAncientGz,
			ChunkStart:      id,
			ChunkCount:      uint32(len(segments)),
			TotalCompressed: m.EVMStateAncientGzipBytes,
		})
		for i, seg := range segments {
			manifest.Chunks = append(manifest.Chunks, ChunkDescriptor{
				ID:               id + uint32(i),
				Source:           ChunkClassEVMAncientGz,
				Class:            ChunkClassEVMAncientGz,
				CompressedSize:   seg.Size,
				CompressedSHA256: seg.SHA256,
			})
		}
	}
	if err := manifest.Finalize(); err != nil {
		return nil, err
	}
	if len(opts.SignerPrivateKey) > 0 {
		sig, err := manifest.Sign(opts.SignerKeyID, opts.SignerPrivateKey, opts.Now)
		if err != nil {
			return nil, err
		}
		manifest.AddSignature(sig)
	}
	return &manifest, nil
}

func SnapshotArtifactChunkCount(m *snapshot.Manifest) int {
	if m == nil {
		return 0
	}
	n := len(m.Chunks)
	if m.EVMStateAncientGzipBytes > 0 {
		n++
	}
	return n
}
