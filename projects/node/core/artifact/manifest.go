package artifact

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path"
	"sort"
	"strings"
	"time"
)

const (
	FormatVersion = 2

	KindCheckpoint = "checkpoint"
	KindRange      = "range"
	KindDelta      = "delta"

	LayoutArchive   = "archive"
	LayoutDirectory = "directory"

	ChunkClassData          = "data"
	ChunkClassEVMAncientGz  = "evm_ancient_gzip"
	HashSHA256              = "sha256"
	SignatureEd25519        = "ed25519"
	MaxRangeArtifactBlocks  = 8192
	DefaultMaxManifestBytes = 16 * 1024 * 1024
	DefaultMaxChunkBytes    = 260 * 1024 * 1024
	DefaultMaxChunks        = 1_000_000
)

var (
	ErrNilManifest        = errors.New("artifact: manifest is nil")
	ErrUnsignedManifest   = errors.New("artifact: manifest has no trusted signature")
	ErrExpiredManifest    = errors.New("artifact: manifest is expired")
	ErrDowngradeManifest  = errors.New("artifact: manifest is below the accepted checkpoint")
	ErrUnsupportedVersion = errors.New("artifact: unsupported manifest version")
)

type SourceInfo struct {
	Name              string `json:"name"`
	ChunkStart        uint32 `json:"chunk_start"`
	ChunkCount        uint32 `json:"chunk_count"`
	TotalRecords      uint64 `json:"total_records,omitempty"`
	TotalCompressed   uint64 `json:"total_compressed,omitempty"`
	TotalUncompressed uint64 `json:"total_uncompressed,omitempty"`
}

type ChunkDescriptor struct {
	ID                 uint32 `json:"id"`
	Source             string `json:"source"`
	Class              string `json:"class"`
	Offset             uint64 `json:"offset,omitempty"`
	CompressedSize     uint64 `json:"compressed_size"`
	UncompressedSize   uint64 `json:"uncompressed_size,omitempty"`
	CompressedSHA256   string `json:"compressed_sha256"`
	UncompressedSHA256 string `json:"uncompressed_sha256,omitempty"`
	Records            uint32 `json:"records,omitempty"`
}

type FileDescriptor struct {
	Path       string `json:"path"`
	Class      string `json:"class"`
	Size       uint64 `json:"size"`
	SHA256     string `json:"sha256"`
	ChunkStart uint32 `json:"chunk_start,omitempty"`
	ChunkCount uint32 `json:"chunk_count,omitempty"`
	Mode       uint32 `json:"mode,omitempty"`
}

type Signature struct {
	KeyID     string    `json:"key_id"`
	Algorithm string    `json:"algorithm"`
	PublicKey string    `json:"public_key,omitempty"`
	Signature string    `json:"signature"`
	SignedAt  time.Time `json:"signed_at"`
}

type TrustedSigner struct {
	PublicKey ed25519.PublicKey
	NotBefore time.Time
	NotAfter  time.Time
	RevokedAt time.Time
}

type Manifest struct {
	FormatVersion      uint32            `json:"format_version"`
	ArtifactType       string            `json:"artifact_type"`
	Network            string            `json:"network"`
	ChainID            uint64            `json:"chain_id"`
	GenesisHash        string            `json:"genesis_hash,omitempty"`
	StartOrder         uint64            `json:"start_order,omitempty"`
	EndOrder           uint64            `json:"end_order,omitempty"`
	TipOrder           uint64            `json:"tip_order"`
	TipHash            string            `json:"tip_hash"`
	TipID              uint64            `json:"tip_id,omitempty"`
	BlockTotal         uint64            `json:"block_total"`
	StateRoot          string            `json:"state_root"`
	ParentArtifactRoot string            `json:"parent_artifact_root,omitempty"`
	ChunkHashAlgo      string            `json:"chunk_hash_algo"`
	Encoding           string            `json:"encoding,omitempty"`
	Compression        string            `json:"compression,omitempty"`
	Layout             string            `json:"layout,omitempty"`
	CreatedAt          time.Time         `json:"created_at"`
	ExpiresAt          time.Time         `json:"expires_at,omitempty"`
	MinNodeVersion     string            `json:"min_node_version,omitempty"`
	Metadata           map[string]string `json:"metadata,omitempty"`
	Sources            []SourceInfo      `json:"sources"`
	Chunks             []ChunkDescriptor `json:"chunks"`
	Files              []FileDescriptor  `json:"files,omitempty"`
	ArtifactRoot       string            `json:"artifact_root,omitempty"`
	Signatures         []Signature       `json:"signatures,omitempty"`
}

type VerifyOptions struct {
	Network              string
	ChainID              uint64
	MinTipOrder          uint64
	HighestAcceptedOrder uint64
	AcceptedTipHash      string
	AcceptedStateRoot    string
	AcceptedArtifactRoot string
	Now                  time.Time
	AllowUnsigned        bool
	AllowDelta           bool
	TrustedSigners       map[string]ed25519.PublicKey
	SignerPolicies       map[string]TrustedSigner
	RequiredSignatures   int
	MaxManifestBytes     int
	MaxChunks            int
	MaxChunkBytes        uint64
	MaxTotalCompressed   uint64
	MaxTotalUncompressed uint64
}

type PeerArtifactHave struct {
	ArtifactRoot string   `json:"artifact_root"`
	PeerID       string   `json:"peer_id"`
	Chunks       []uint32 `json:"chunks,omitempty"`
	Ranges       []Range  `json:"ranges,omitempty"`
	MaxBytes     uint64   `json:"max_bytes,omitempty"`
}

type Range struct {
	Start uint32 `json:"start"`
	Count uint32 `json:"count"`
}

func NewManifest(kind, network string, chainID uint64) Manifest {
	return Manifest{
		FormatVersion: FormatVersion,
		ArtifactType:  kind,
		Network:       network,
		ChainID:       chainID,
		ChunkHashAlgo: HashSHA256,
		CreatedAt:     time.Now().UTC(),
	}
}

func DecodeManifest(raw []byte, opts VerifyOptions) (*Manifest, error) {
	max := opts.MaxManifestBytes
	if max <= 0 {
		max = DefaultMaxManifestBytes
	}
	if len(raw) > max {
		return nil, fmt.Errorf("artifact: manifest exceeds max size: %d > %d", len(raw), max)
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var m Manifest
	if err := dec.Decode(&m); err != nil {
		return nil, fmt.Errorf("artifact: decode manifest: %w", err)
	}
	var trailing struct{}
	if err := dec.Decode(&trailing); err != io.EOF {
		return nil, fmt.Errorf("artifact: trailing manifest data")
	}
	if err := m.Validate(opts); err != nil {
		return nil, err
	}
	return &m, nil
}

func (m *Manifest) Finalize() error {
	root, err := m.Root()
	if err != nil {
		return err
	}
	m.ArtifactRoot = root
	return nil
}

func (m *Manifest) Root() (string, error) {
	digest, err := m.Digest()
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(digest[:]), nil
}

func (m *Manifest) Digest() ([sha256.Size]byte, error) {
	var zero [sha256.Size]byte
	b, err := m.CanonicalBytes()
	if err != nil {
		return zero, err
	}
	return sha256.Sum256(b), nil
}

func (m *Manifest) CanonicalBytes() ([]byte, error) {
	if m == nil {
		return nil, ErrNilManifest
	}
	c := *m
	c.ArtifactRoot = ""
	c.Signatures = nil
	c.CreatedAt = canonicalTime(c.CreatedAt)
	c.ExpiresAt = canonicalTime(c.ExpiresAt)
	c.Sources = append([]SourceInfo(nil), m.Sources...)
	c.Chunks = append([]ChunkDescriptor(nil), m.Chunks...)
	c.Files = append([]FileDescriptor(nil), m.Files...)
	c.Metadata = canonicalMetadata(m.Metadata)
	return json.Marshal(c)
}

func (m *Manifest) Validate(opts VerifyOptions) error {
	if m == nil {
		return ErrNilManifest
	}
	if opts.Now.IsZero() {
		opts.Now = time.Now().UTC()
	}
	if opts.MaxManifestBytes <= 0 {
		opts.MaxManifestBytes = DefaultMaxManifestBytes
	}
	if opts.MaxChunks <= 0 {
		opts.MaxChunks = DefaultMaxChunks
	}
	if opts.MaxChunkBytes == 0 {
		opts.MaxChunkBytes = DefaultMaxChunkBytes
	}
	if err := validateManifestSize(m, opts.MaxManifestBytes); err != nil {
		return err
	}
	if m.FormatVersion != FormatVersion {
		return fmt.Errorf("%w: %d", ErrUnsupportedVersion, m.FormatVersion)
	}
	if !validArtifactKind(m.ArtifactType) {
		return fmt.Errorf("artifact: unsupported artifact type %q", m.ArtifactType)
	}
	if m.ArtifactType == KindDelta && !opts.AllowDelta {
		return fmt.Errorf("artifact: delta artifacts are reserved")
	}
	if m.Network == "" {
		return fmt.Errorf("artifact: network is required")
	}
	if opts.Network == "" {
		return fmt.Errorf("artifact: verification network is required")
	}
	if m.Network != opts.Network {
		return fmt.Errorf("artifact: network mismatch: manifest=%q local=%q", m.Network, opts.Network)
	}
	if m.ChainID == 0 {
		return fmt.Errorf("artifact: chain_id is required")
	}
	if opts.ChainID == 0 {
		return fmt.Errorf("artifact: verification chain id is required")
	}
	if m.ChainID != opts.ChainID {
		return fmt.Errorf("artifact: chain id mismatch: manifest=%d local=%d", m.ChainID, opts.ChainID)
	}
	if m.TipOrder < opts.MinTipOrder {
		return fmt.Errorf("artifact: tip order %d below minimum %d", m.TipOrder, opts.MinTipOrder)
	}
	if opts.HighestAcceptedOrder > 0 && m.TipOrder < opts.HighestAcceptedOrder {
		return fmt.Errorf("%w: manifest=%d accepted=%d", ErrDowngradeManifest, m.TipOrder, opts.HighestAcceptedOrder)
	}
	if !validHashHex(m.TipHash) {
		return fmt.Errorf("artifact: tip_hash must be a 32-byte hex hash")
	}
	if m.GenesisHash != "" && !validHashHex(m.GenesisHash) {
		return fmt.Errorf("artifact: genesis_hash must be a 32-byte hex hash")
	}
	if m.ParentArtifactRoot != "" && !validSHA256Hex(m.ParentArtifactRoot) {
		return fmt.Errorf("artifact: parent_artifact_root must be a sha256 hex digest")
	}
	if !validHashHex(m.StateRoot) {
		return fmt.Errorf("artifact: state_root must be a 32-byte hex hash")
	}
	if m.BlockTotal == 0 {
		return fmt.Errorf("artifact: block_total is required")
	}
	if m.ChunkHashAlgo != HashSHA256 {
		return fmt.Errorf("artifact: unsupported chunk hash algorithm %q", m.ChunkHashAlgo)
	}
	if err := validateLayout(m.Layout); err != nil {
		return err
	}
	if m.CreatedAt.IsZero() {
		return fmt.Errorf("artifact: created_at is required")
	}
	if !m.ExpiresAt.IsZero() {
		if !m.ExpiresAt.After(m.CreatedAt) {
			return fmt.Errorf("artifact: expires_at must be after created_at")
		}
		if !opts.Now.Before(m.ExpiresAt) {
			return ErrExpiredManifest
		}
	}
	if err := validateRangeBounds(m); err != nil {
		return err
	}
	if err := validateChunks(m.Chunks, opts); err != nil {
		return err
	}
	if err := validateSources(m.Sources, m.Chunks); err != nil {
		return err
	}
	if err := validateDirectoryFiles(m.Layout, m.Files, len(m.Chunks)); err != nil {
		return err
	}
	if m.ArtifactRoot != "" {
		root, err := m.Root()
		if err != nil {
			return err
		}
		if !equalHex(m.ArtifactRoot, root) {
			return fmt.Errorf("artifact: root mismatch: manifest=%s computed=%s", m.ArtifactRoot, root)
		}
	}
	if err := m.validateAcceptedCheckpoint(opts); err != nil {
		return err
	}
	if !opts.AllowUnsigned {
		if err := m.VerifySignaturesWithPolicy(opts, opts.RequiredSignatures); err != nil {
			return err
		}
	}
	return nil
}

func (m *Manifest) Sign(keyID string, priv ed25519.PrivateKey, signedAt time.Time) (Signature, error) {
	if keyID == "" {
		return Signature{}, fmt.Errorf("artifact: key id is required")
	}
	if len(priv) != ed25519.PrivateKeySize {
		return Signature{}, fmt.Errorf("artifact: invalid ed25519 private key size")
	}
	digest, err := m.Digest()
	if err != nil {
		return Signature{}, err
	}
	pub := priv.Public().(ed25519.PublicKey)
	if signedAt.IsZero() {
		signedAt = time.Now().UTC()
	}
	return Signature{
		KeyID:     keyID,
		Algorithm: SignatureEd25519,
		PublicKey: hex.EncodeToString(pub),
		Signature: hex.EncodeToString(ed25519.Sign(priv, digest[:])),
		SignedAt:  signedAt.UTC(),
	}, nil
}

func (m *Manifest) AddSignature(sig Signature) {
	m.Signatures = append(m.Signatures, sig)
}

func (m *Manifest) VerifySignatures(trusted map[string]ed25519.PublicKey, required int) error {
	policies := make(map[string]TrustedSigner, len(trusted))
	for keyID, pub := range trusted {
		policies[keyID] = TrustedSigner{PublicKey: pub}
	}
	return m.verifySignatures(policies, time.Time{}, required)
}

func (m *Manifest) VerifySignaturesWithPolicy(opts VerifyOptions, required int) error {
	policies := opts.SignerPolicies
	if len(policies) == 0 && len(opts.TrustedSigners) > 0 {
		policies = make(map[string]TrustedSigner, len(opts.TrustedSigners))
		for keyID, pub := range opts.TrustedSigners {
			policies[keyID] = TrustedSigner{PublicKey: pub}
		}
	}
	return m.verifySignatures(policies, opts.Now, required)
}

func (m *Manifest) verifySignatures(trusted map[string]TrustedSigner, now time.Time, required int) error {
	if len(trusted) == 0 {
		return ErrUnsignedManifest
	}
	if required <= 0 {
		required = 1
	}
	digest, err := m.Digest()
	if err != nil {
		return err
	}
	valid := 0
	seen := make(map[string]struct{}, required)
	for _, sig := range m.Signatures {
		if sig.Algorithm != SignatureEd25519 {
			continue
		}
		policy, ok := trusted[sig.KeyID]
		if !ok {
			continue
		}
		if len(policy.PublicKey) != ed25519.PublicKeySize {
			continue
		}
		if !validSignatureTime(sig.SignedAt, policy, now) {
			continue
		}
		if sig.PublicKey != "" && !equalHex(sig.PublicKey, hex.EncodeToString(policy.PublicKey)) {
			continue
		}
		rawSig, err := hex.DecodeString(sig.Signature)
		if err != nil {
			continue
		}
		if ed25519.Verify(policy.PublicKey, digest[:], rawSig) {
			if _, ok := seen[sig.KeyID]; ok {
				continue
			}
			seen[sig.KeyID] = struct{}{}
			valid++
			if valid >= required {
				return nil
			}
		}
	}
	return ErrUnsignedManifest
}

func VerifyChunkBody(desc ChunkDescriptor, body []byte) error {
	if uint64(len(body)) != desc.CompressedSize {
		return fmt.Errorf("artifact: chunk %d compressed size mismatch: body=%d manifest=%d", desc.ID, len(body), desc.CompressedSize)
	}
	sum := sha256.Sum256(body)
	got := hex.EncodeToString(sum[:])
	if !equalHex(got, desc.CompressedSHA256) {
		return fmt.Errorf("artifact: chunk %d compressed hash mismatch", desc.ID)
	}
	return nil
}

func validateManifestSize(m *Manifest, max int) error {
	b, err := json.Marshal(m)
	if err != nil {
		return err
	}
	if len(b) > max {
		return fmt.Errorf("artifact: manifest exceeds max size: %d > %d", len(b), max)
	}
	return nil
}

func validateRangeBounds(m *Manifest) error {
	switch m.ArtifactType {
	case KindRange:
		if m.EndOrder == 0 {
			return fmt.Errorf("artifact: range artifact requires start_order and end_order")
		}
		if !validSHA256Hex(m.ParentArtifactRoot) {
			return fmt.Errorf("artifact: range artifact requires parent checkpoint artifact root")
		}
		if m.EndOrder < m.StartOrder {
			return fmt.Errorf("artifact: range end_order %d before start_order %d", m.EndOrder, m.StartOrder)
		}
		if m.EndOrder-m.StartOrder >= MaxRangeArtifactBlocks {
			return fmt.Errorf("artifact: range exceeds %d blocks", MaxRangeArtifactBlocks)
		}
		if m.TipOrder < m.EndOrder {
			return fmt.Errorf("artifact: tip_order %d before range end_order %d", m.TipOrder, m.EndOrder)
		}
	case KindCheckpoint:
		if m.EndOrder != 0 && m.TipOrder < m.EndOrder {
			return fmt.Errorf("artifact: checkpoint tip_order %d before end_order %d", m.TipOrder, m.EndOrder)
		}
	}
	return nil
}

func validateChunks(chunks []ChunkDescriptor, opts VerifyOptions) error {
	if len(chunks) == 0 {
		return fmt.Errorf("artifact: manifest has no chunks")
	}
	if len(chunks) > opts.MaxChunks {
		return fmt.Errorf("artifact: too many chunks: %d > %d", len(chunks), opts.MaxChunks)
	}
	var totalCompressed, totalUncompressed uint64
	for i, c := range chunks {
		if c.ID != uint32(i) {
			return fmt.Errorf("artifact: chunk %d has non-sequential id %d", i, c.ID)
		}
		if c.Source == "" {
			return fmt.Errorf("artifact: chunk %d source is required", i)
		}
		if !validChunkClass(c.Class) {
			return fmt.Errorf("artifact: chunk %d has unsupported class %q", i, c.Class)
		}
		if c.CompressedSize == 0 {
			return fmt.Errorf("artifact: chunk %d has zero compressed size", i)
		}
		if c.CompressedSize > opts.MaxChunkBytes {
			return fmt.Errorf("artifact: chunk %d exceeds max compressed size: %d > %d", i, c.CompressedSize, opts.MaxChunkBytes)
		}
		if !validSHA256Hex(c.CompressedSHA256) {
			return fmt.Errorf("artifact: chunk %d has malformed compressed sha256", i)
		}
		if c.UncompressedSize > 0 && !validSHA256Hex(c.UncompressedSHA256) {
			return fmt.Errorf("artifact: chunk %d has malformed uncompressed sha256", i)
		}
		totalCompressed += c.CompressedSize
		totalUncompressed += c.UncompressedSize
	}
	if opts.MaxTotalCompressed > 0 && totalCompressed > opts.MaxTotalCompressed {
		return fmt.Errorf("artifact: compressed total exceeds max: %d > %d", totalCompressed, opts.MaxTotalCompressed)
	}
	if opts.MaxTotalUncompressed > 0 && totalUncompressed > opts.MaxTotalUncompressed {
		return fmt.Errorf("artifact: uncompressed total exceeds max: %d > %d", totalUncompressed, opts.MaxTotalUncompressed)
	}
	return nil
}

func validateSources(sources []SourceInfo, chunks []ChunkDescriptor) error {
	if len(sources) == 0 {
		return fmt.Errorf("artifact: manifest has no sources")
	}
	var covered uint32
	seen := make(map[string]struct{}, len(sources))
	for i, s := range sources {
		if s.Name == "" {
			return fmt.Errorf("artifact: source %d has empty name", i)
		}
		if _, ok := seen[s.Name]; ok {
			return fmt.Errorf("artifact: duplicate source %q", s.Name)
		}
		seen[s.Name] = struct{}{}
		if s.ChunkStart != covered {
			return fmt.Errorf("artifact: source %q starts at %d but previous source ended at %d", s.Name, s.ChunkStart, covered)
		}
		if uint64(s.ChunkStart)+uint64(s.ChunkCount) > uint64(len(chunks)) {
			return fmt.Errorf("artifact: source %q spans beyond chunk list", s.Name)
		}
		for id := s.ChunkStart; id < s.ChunkStart+s.ChunkCount; id++ {
			if chunks[id].Source != s.Name {
				return fmt.Errorf("artifact: chunk %d source %q outside source range %q", id, chunks[id].Source, s.Name)
			}
		}
		covered = s.ChunkStart + s.ChunkCount
	}
	if covered != uint32(len(chunks)) {
		return fmt.Errorf("artifact: sources cover %d chunks but manifest has %d", covered, len(chunks))
	}
	return nil
}

func validateLayout(layout string) error {
	if layout == "" {
		return nil
	}
	switch layout {
	case LayoutArchive, LayoutDirectory:
		return nil
	default:
		return fmt.Errorf("artifact: unsupported layout %q", layout)
	}
}

func validateDirectoryFiles(layout string, files []FileDescriptor, chunkCount int) error {
	if layout == "" || layout == LayoutArchive {
		if len(files) > 0 {
			return fmt.Errorf("artifact: archive layout must not include directory files")
		}
		return nil
	}
	if layout != LayoutDirectory {
		return nil
	}
	if len(files) == 0 {
		return fmt.Errorf("artifact: directory layout requires files")
	}
	seen := make(map[string]struct{}, len(files))
	for i, file := range files {
		cleanPath, err := validateDirectoryFilePath(file.Path)
		if err != nil {
			return fmt.Errorf("artifact: file %d path: %w", i, err)
		}
		if _, ok := seen[cleanPath]; ok {
			return fmt.Errorf("artifact: duplicate file path %q", cleanPath)
		}
		seen[cleanPath] = struct{}{}
		if file.Class == "" {
			return fmt.Errorf("artifact: file %q class is required", cleanPath)
		}
		if file.Size == 0 {
			return fmt.Errorf("artifact: file %q has zero size", cleanPath)
		}
		if !validSHA256Hex(file.SHA256) {
			return fmt.Errorf("artifact: file %q has malformed sha256", cleanPath)
		}
		if file.ChunkStart > 0 && file.ChunkCount == 0 {
			return fmt.Errorf("artifact: file %q has chunk_start without chunk_count", cleanPath)
		}
		if file.ChunkCount > 0 {
			if uint64(file.ChunkStart)+uint64(file.ChunkCount) > uint64(chunkCount) {
				return fmt.Errorf("artifact: file %q spans beyond chunk list", cleanPath)
			}
		}
	}
	return nil
}

func validateDirectoryFilePath(value string) (string, error) {
	if value == "" {
		return "", fmt.Errorf("empty path")
	}
	if strings.Contains(value, "\\") {
		return "", fmt.Errorf("must use slash separators")
	}
	if strings.HasPrefix(value, "/") {
		return "", fmt.Errorf("must be relative")
	}
	clean := path.Clean(value)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
		return "", fmt.Errorf("must stay inside artifact root")
	}
	return clean, nil
}

func (m *Manifest) validateAcceptedCheckpoint(opts VerifyOptions) error {
	if opts.HighestAcceptedOrder == 0 || m.TipOrder != opts.HighestAcceptedOrder {
		return nil
	}
	if opts.AcceptedTipHash != "" && !equalHashHex(m.TipHash, opts.AcceptedTipHash) {
		return fmt.Errorf("artifact: accepted checkpoint tip hash mismatch at order %d", m.TipOrder)
	}
	if opts.AcceptedStateRoot != "" && !equalHashHex(m.StateRoot, opts.AcceptedStateRoot) {
		return fmt.Errorf("artifact: accepted checkpoint state root mismatch at order %d", m.TipOrder)
	}
	if opts.AcceptedArtifactRoot != "" {
		root := m.ArtifactRoot
		if root == "" {
			var err error
			root, err = m.Root()
			if err != nil {
				return err
			}
		}
		if !equalHex(root, opts.AcceptedArtifactRoot) {
			return fmt.Errorf("artifact: accepted checkpoint artifact root mismatch at order %d", m.TipOrder)
		}
	}
	return nil
}

func validSignatureTime(signedAt time.Time, policy TrustedSigner, now time.Time) bool {
	if signedAt.IsZero() {
		return false
	}
	signedAt = signedAt.UTC()
	if !policy.NotBefore.IsZero() && signedAt.Before(policy.NotBefore.UTC()) {
		return false
	}
	if !policy.NotAfter.IsZero() && signedAt.After(policy.NotAfter.UTC()) {
		return false
	}
	if !policy.RevokedAt.IsZero() && !signedAt.Before(policy.RevokedAt.UTC()) {
		return false
	}
	if !now.IsZero() && signedAt.After(now.UTC().Add(5*time.Minute)) {
		return false
	}
	return true
}

func canonicalTime(t time.Time) time.Time {
	if t.IsZero() {
		return time.Time{}
	}
	return t.UTC()
}

func canonicalMetadata(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	keys := make([]string, 0, len(in))
	for k := range in {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		out[k] = in[k]
	}
	return out
}

func validArtifactKind(kind string) bool {
	switch kind {
	case KindCheckpoint, KindRange, KindDelta:
		return true
	default:
		return false
	}
}

func validChunkClass(class string) bool {
	switch class {
	case ChunkClassData, ChunkClassEVMAncientGz:
		return true
	default:
		return false
	}
}

func validSHA256Hex(s string) bool {
	if len(s) != sha256.Size*2 {
		return false
	}
	if strings.ToLower(s) != s {
		return false
	}
	_, err := hex.DecodeString(s)
	return err == nil
}

func validHashHex(s string) bool {
	if strings.HasPrefix(s, "0x") {
		s = s[2:]
	}
	return validSHA256Hex(s)
}

func equalHex(a, b string) bool {
	da, errA := hex.DecodeString(strings.ToLower(a))
	db, errB := hex.DecodeString(strings.ToLower(b))
	if errA != nil || errB != nil {
		return false
	}
	return bytes.Equal(da, db)
}

func equalHashHex(a, b string) bool {
	a = strings.TrimPrefix(strings.ToLower(a), "0x")
	b = strings.TrimPrefix(strings.ToLower(b), "0x")
	return equalHex(a, b)
}
