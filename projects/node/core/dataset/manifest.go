// Package dataset implements the byte-integrity half of owner-portable
// BlockDAG datasets. Consensus and native/EVM semantic checks remain owned by
// the node and are intentionally separate from filesystem provenance.
package dataset

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	Schema                   = "blockdag-dataset/v1"
	DefaultManifestFilename  = "DATASET-MANIFEST.json"
	DefaultReceiptFilename   = "DATASET-VERIFICATION-RECEIPT.json"
	maximumManifestBytes     = 64 << 20
	maximumManifestFiles     = 10_000_000
	hashBufferBytes          = 1 << 20
	directoryRootDomain      = "blockdag-dataset-directory-root/v1\x00"
	ClassificationHash       = "hash_mismatch"
	ClassificationMissing    = "incomplete_copy"
	ClassificationExtra      = "unexpected_file"
	ClassificationSymlink    = "unsafe_symlink"
	ClassificationSpecial    = "unsafe_special_file"
	ClassificationChanged    = "dataset_changed"
	ClassificationManifest   = "invalid_manifest"
	ClassificationPermission = "permission_denied"
	ClassificationCanceled   = "verification_canceled"
)

// ChainIdentity binds a dataset to consensus identity rather than to a source
// machine, path, user ID, or publisher.
type ChainIdentity struct {
	ChainID        uint64 `json:"chain_id"`
	Network        string `json:"network"`
	GenesisHash    string `json:"genesis_hash"`
	EVMGenesisHash string `json:"evm_genesis_hash"`
}

type NativeState struct {
	Order          uint64 `json:"order"`
	Hash           string `json:"hash"`
	StateRoot      string `json:"state_root"`
	Height         uint64 `json:"height"`
	DatabaseFormat string `json:"database_format"`
}

type EVMState struct {
	Number         uint64 `json:"number"`
	Hash           string `json:"hash"`
	StateRoot      string `json:"state_root"`
	DatabaseFormat string `json:"database_format"`
}

type Boundary struct {
	Native NativeState `json:"native"`
	EVM    EVMState    `json:"evm"`
}

type FileRecord struct {
	Path   string `json:"path"`
	Size   uint64 `json:"size"`
	SHA256 string `json:"sha256"`
}

// Exclusion is deliberately a small, versioned vocabulary. A manifest cannot
// invent a glob that silently removes consensus bytes from verification.
type Exclusion struct {
	Rule    string `json:"rule"`
	Pattern string `json:"pattern"`
	Reason  string `json:"reason"`
}

type ProducerSignature struct {
	Algorithm string `json:"algorithm"`
	KeyID     string `json:"key_id,omitempty"`
	Value     string `json:"value"`
}

type SourceInfo struct {
	CreatedAt       string `json:"created_at,omitempty"`
	SoftwareVersion string `json:"software_version,omitempty"`
	URI             string `json:"uri,omitempty"`
	NodeIdentity    string `json:"node_identity,omitempty"`
}

type Manifest struct {
	Schema              string             `json:"schema"`
	Chain               ChainIdentity      `json:"chain"`
	Native              NativeState        `json:"native"`
	EVM                 EVMState           `json:"evm"`
	CanonicalCheckpoint *Boundary          `json:"canonical_checkpoint,omitempty"`
	Files               []FileRecord       `json:"files"`
	Exclusions          []Exclusion        `json:"exclusions"`
	DirectoryRootSHA256 string             `json:"directory_root_sha256"`
	Source              SourceInfo         `json:"source,omitempty"`
	ProducerSignature   *ProducerSignature `json:"producer_signature,omitempty"`
}

type Diagnostic struct {
	Classification string `json:"classification"`
	Message        string `json:"message"`
	Path           string `json:"path,omitempty"`
	Expected       string `json:"expected,omitempty"`
	Actual         string `json:"actual,omitempty"`
	RepairableCopy bool   `json:"repairable_on_copy"`
}

type ValidationError struct{ Diagnostic Diagnostic }

func (e *ValidationError) Error() string {
	if e == nil {
		return "dataset validation failed"
	}
	return e.Diagnostic.Message
}

type Verification struct {
	DatasetRoot         string       `json:"dataset_root"`
	ManifestSHA256      string       `json:"manifest_sha256"`
	DirectoryRootSHA256 string       `json:"directory_root_sha256"`
	FileCount           int          `json:"file_count"`
	ByteCount           uint64       `json:"byte_count"`
	Valid               bool         `json:"valid"`
	Diagnostics         []Diagnostic `json:"diagnostics,omitempty"`
}

// ContentIdentity is the stable, path-independent identity carried by an
// accepted dataset manifest. It remains useful after the live databases
// advance because the native/EVM pre-state in each operation marker binds the
// exact mutable boundary while these hashes bind the activated dataset lineage.
type ContentIdentity struct {
	DirectoryRootSHA256 string
	ManifestSHA256      string
}

// DefaultExclusions returns the only exclusions accepted by schema v1. The
// rules omit locks, diagnostic logs, target-local credentials, transfer
// receipts, peerstore state, and transient compaction files. Numeric database
// WAL files are not excluded.
func DefaultExclusions() []Exclusion {
	return []Exclusion{
		{Rule: "exact", Pattern: DefaultManifestFilename, Reason: "manifest self-reference"},
		{Rule: "exact", Pattern: DefaultReceiptFilename, Reason: "verification receipt"},
		{Rule: "basename", Pattern: "LOCK", Reason: "database runtime lock"},
		{Rule: "basename", Pattern: "LOG", Reason: "database diagnostic log"},
		{Rule: "basename_prefix", Pattern: "LOG.old.", Reason: "rotated database diagnostic log"},
		{Rule: "basename", Pattern: "network.key", Reason: "target-local node credential"},
		{Rule: "basename", Pattern: "rpc.key", Reason: "target-local RPC credential"},
		{Rule: "basename", Pattern: "rpc.cert", Reason: "target-local RPC credential material"},
		{Rule: "exact", Pattern: "shutdown.lock", Reason: "dataset-relative operation marker"},
		{Rule: "path_prefix", Pattern: "peerstore/", Reason: "runtime peerstore state"},
		{Rule: "basename_suffix", Pattern: ".tmp", Reason: "transient copy or compaction file"},
	}
}

func validationError(classification, message, filePath, expected, actual string) error {
	return &ValidationError{Diagnostic: Diagnostic{
		Classification: classification,
		Message:        message,
		Path:           filePath,
		Expected:       expected,
		Actual:         actual,
		RepairableCopy: true,
	}}
}

func canonicalRoot(root string) (string, error) {
	if strings.TrimSpace(root) == "" {
		return "", validationError(ClassificationManifest, "dataset directory is required", "", "", "")
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		class := ClassificationMissing
		if errors.Is(err, fs.ErrPermission) {
			class = ClassificationPermission
		}
		return "", validationError(class, fmt.Sprintf("resolve dataset directory: %v", err), abs, "", "")
	}
	info, err := os.Stat(resolved)
	if err != nil || !info.IsDir() {
		return "", validationError(ClassificationMissing, "dataset path is not a usable directory", resolved, "directory", "unavailable")
	}
	return filepath.Clean(resolved), nil
}

func validHash32(value string) bool {
	value = strings.TrimSpace(value)
	value = strings.TrimPrefix(value, "0x")
	value = strings.TrimPrefix(value, "0X")
	raw, err := hex.DecodeString(value)
	return err == nil && len(raw) == sha256.Size
}

func validSHA256(value string) bool {
	if value != strings.ToLower(value) || len(value) != sha256.Size*2 {
		return false
	}
	raw, err := hex.DecodeString(value)
	return err == nil && len(raw) == sha256.Size
}

func validText(value string, maximum int) bool {
	value = strings.TrimSpace(value)
	return value != "" && len(value) <= maximum && utf8.ValidString(value) && !strings.ContainsAny(value, "\x00\r\n")
}

func validateState(native NativeState, evm EVMState) error {
	if !validHash32(native.Hash) || !validHash32(native.StateRoot) {
		return validationError(ClassificationManifest, "native hash and state root must be 32-byte hexadecimal values", "", "", "")
	}
	if !validHash32(evm.Hash) || !validHash32(evm.StateRoot) {
		return validationError(ClassificationManifest, "EVM hash and state root must be 32-byte hexadecimal values", "", "", "")
	}
	if !validText(native.DatabaseFormat, 128) || !validText(evm.DatabaseFormat, 128) {
		return validationError(ClassificationManifest, "native and EVM database formats are required", "", "", "")
	}
	return nil
}

func validateRelativePath(value string) error {
	if !utf8.ValidString(value) || value == "" || value != path.Clean(value) || !fs.ValidPath(value) || strings.Contains(value, "\\") {
		return validationError(ClassificationManifest, "manifest contains an unsafe or non-canonical relative path", value, "", "")
	}
	return nil
}

func ValidateManifest(m *Manifest) error {
	if m == nil || m.Schema != Schema {
		return validationError(ClassificationManifest, "unsupported or missing dataset manifest schema", "", Schema, "")
	}
	if m.Chain.ChainID == 0 || !validText(m.Chain.Network, 128) || !validHash32(m.Chain.GenesisHash) || !validHash32(m.Chain.EVMGenesisHash) {
		return validationError(ClassificationManifest, "chain ID, network, native genesis, and EVM genesis hashes are required", "", "", "")
	}
	if err := validateState(m.Native, m.EVM); err != nil {
		return err
	}
	if m.CanonicalCheckpoint != nil {
		if err := validateState(m.CanonicalCheckpoint.Native, m.CanonicalCheckpoint.EVM); err != nil {
			return err
		}
	}
	if len(m.Files) == 0 || len(m.Files) > maximumManifestFiles {
		return validationError(ClassificationManifest, "manifest file list is empty or exceeds the schema bound", "", "", fmt.Sprint(len(m.Files)))
	}
	previous := ""
	for index, record := range m.Files {
		if err := validateRelativePath(record.Path); err != nil {
			return err
		}
		if index > 0 && record.Path <= previous {
			return validationError(ClassificationManifest, "manifest file records are duplicated or not strictly sorted", record.Path, "strict path order", previous)
		}
		if !validSHA256(record.SHA256) {
			return validationError(ClassificationManifest, "manifest file SHA-256 is malformed", record.Path, "lowercase SHA-256", record.SHA256)
		}
		previous = record.Path
	}
	if !reflect.DeepEqual(m.Exclusions, DefaultExclusions()) {
		return validationError(ClassificationManifest, "manifest exclusion policy is not the schema v1 policy", "", "blockdag-dataset/v1 exclusions", "different exclusions")
	}
	root, err := DirectoryRoot(m.Files)
	if err != nil {
		return err
	}
	if m.DirectoryRootSHA256 != root {
		return validationError(ClassificationHash, "manifest directory-root digest does not match its ordered file records", "", root, m.DirectoryRootSHA256)
	}
	if m.Source.CreatedAt != "" {
		parsed, err := time.Parse(time.RFC3339Nano, m.Source.CreatedAt)
		if err != nil || parsed.Location() != time.UTC {
			return validationError(ClassificationManifest, "source creation time must be canonical UTC RFC3339", "", "", m.Source.CreatedAt)
		}
	}
	return nil
}

func excluded(relative string, exclusions []Exclusion) bool {
	base := path.Base(relative)
	for _, rule := range exclusions {
		switch rule.Rule {
		case "exact":
			if relative == rule.Pattern {
				return true
			}
		case "basename":
			if base == rule.Pattern {
				return true
			}
		case "basename_prefix":
			if strings.HasPrefix(base, rule.Pattern) {
				return true
			}
		case "basename_suffix":
			if strings.HasSuffix(base, rule.Pattern) {
				return true
			}
		case "path_prefix":
			if strings.HasPrefix(relative, rule.Pattern) {
				return true
			}
		}
	}
	return false
}

func sameStableFile(left, right fs.FileInfo) bool {
	return left != nil && right != nil && os.SameFile(left, right) && left.Mode() == right.Mode() && left.Size() == right.Size() && left.ModTime().Equal(right.ModTime())
}

func hashRootFile(ctx context.Context, root *os.Root, relative string, buffer []byte) (FileRecord, error) {
	before, err := root.Lstat(relative)
	if err != nil {
		return FileRecord{}, validationError(ClassificationMissing, fmt.Sprintf("stat dataset file: %v", err), relative, "regular file", "missing")
	}
	if !before.Mode().IsRegular() {
		return FileRecord{}, validationError(ClassificationSpecial, "dataset entry is not a regular file", relative, "regular file", before.Mode().String())
	}
	file, err := root.Open(relative)
	if err != nil {
		class := ClassificationMissing
		if errors.Is(err, fs.ErrPermission) {
			class = ClassificationPermission
		}
		return FileRecord{}, validationError(class, fmt.Sprintf("open dataset file: %v", err), relative, "readable regular file", "unavailable")
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !sameStableFile(before, opened) {
		return FileRecord{}, validationError(ClassificationChanged, "dataset file changed while it was opened", relative, "stable file identity", "changed")
	}
	hash := sha256.New()
	var total uint64
	for {
		if err := ctx.Err(); err != nil {
			return FileRecord{}, validationError(ClassificationCanceled, err.Error(), relative, "", "")
		}
		count, readErr := file.Read(buffer)
		if count > 0 {
			total += uint64(count)
			_, _ = hash.Write(buffer[:count])
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return FileRecord{}, validationError(ClassificationMissing, fmt.Sprintf("read dataset file: %v", readErr), relative, "readable file", "read failure")
		}
	}
	after, err := root.Lstat(relative)
	if err != nil || !sameStableFile(opened, after) || total != uint64(opened.Size()) {
		return FileRecord{}, validationError(ClassificationChanged, "dataset file changed while it was hashed", relative, fmt.Sprint(opened.Size()), fmt.Sprint(total))
	}
	return FileRecord{Path: relative, Size: total, SHA256: hex.EncodeToString(hash.Sum(nil))}, nil
}

func Scan(ctx context.Context, directory string, exclusions []Exclusion) (string, []FileRecord, error) {
	rootPath, err := canonicalRoot(directory)
	if err != nil {
		return "", nil, err
	}
	if !reflect.DeepEqual(exclusions, DefaultExclusions()) {
		return "", nil, validationError(ClassificationManifest, "unsupported dataset exclusion policy", "", "blockdag-dataset/v1 exclusions", "different exclusions")
	}
	root, err := os.OpenRoot(rootPath)
	if err != nil {
		return "", nil, validationError(ClassificationPermission, fmt.Sprintf("open dataset directory: %v", err), rootPath, "", "")
	}
	defer root.Close()
	buffer := make([]byte, hashBufferBytes)
	records := make([]FileRecord, 0, 1024)
	err = fs.WalkDir(root.FS(), ".", func(relative string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return validationError(ClassificationPermission, fmt.Sprintf("walk dataset: %v", walkErr), relative, "", "")
		}
		if relative == "." {
			return nil
		}
		if err := ctx.Err(); err != nil {
			return validationError(ClassificationCanceled, err.Error(), relative, "", "")
		}
		relative = filepath.ToSlash(relative)
		if err := validateRelativePath(relative); err != nil {
			return err
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return validationError(ClassificationSymlink, "dataset contains a symbolic link", relative, "regular file or directory", "symlink")
		}
		info, err := entry.Info()
		if err != nil {
			return validationError(ClassificationChanged, fmt.Sprintf("inspect dataset entry: %v", err), relative, "stable entry", "changed")
		}
		if info.IsDir() {
			return nil
		}
		if !info.Mode().IsRegular() {
			return validationError(ClassificationSpecial, "dataset contains a non-regular entry", relative, "regular file", info.Mode().String())
		}
		if excluded(relative, exclusions) {
			return nil
		}
		if len(records) >= maximumManifestFiles {
			return validationError(ClassificationManifest, "dataset exceeds the schema file-count bound", relative, "", "")
		}
		record, err := hashRootFile(ctx, root, relative, buffer)
		if err != nil {
			return err
		}
		records = append(records, record)
		return nil
	})
	if err != nil {
		return "", nil, err
	}
	sort.Slice(records, func(i, j int) bool { return records[i].Path < records[j].Path })
	return rootPath, records, nil
}

func DirectoryRoot(records []FileRecord) (string, error) {
	hash := sha256.New()
	_, _ = io.WriteString(hash, directoryRootDomain)
	var encoded [8]byte
	binary.BigEndian.PutUint64(encoded[:], uint64(len(records)))
	_, _ = hash.Write(encoded[:])
	previous := ""
	for index, record := range records {
		if err := validateRelativePath(record.Path); err != nil {
			return "", err
		}
		if index > 0 && record.Path <= previous {
			return "", validationError(ClassificationManifest, "directory-root records must be strictly sorted", record.Path, "", previous)
		}
		digest, err := hex.DecodeString(record.SHA256)
		if err != nil || len(digest) != sha256.Size || record.SHA256 != strings.ToLower(record.SHA256) {
			return "", validationError(ClassificationManifest, "directory-root file digest is malformed", record.Path, "lowercase SHA-256", record.SHA256)
		}
		binary.BigEndian.PutUint64(encoded[:], uint64(len(record.Path)))
		_, _ = hash.Write(encoded[:])
		_, _ = io.WriteString(hash, record.Path)
		binary.BigEndian.PutUint64(encoded[:], record.Size)
		_, _ = hash.Write(encoded[:])
		_, _ = hash.Write(digest)
		previous = record.Path
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func Create(ctx context.Context, directory string, template Manifest) (*Manifest, error) {
	root, records, err := Scan(ctx, directory, DefaultExclusions())
	if err != nil {
		return nil, err
	}
	_ = root
	template.Schema = Schema
	template.Files = records
	template.Exclusions = DefaultExclusions()
	template.DirectoryRootSHA256, err = DirectoryRoot(records)
	if err != nil {
		return nil, err
	}
	if template.Source.CreatedAt == "" {
		template.Source.CreatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	}
	if parsed, parseErr := time.Parse(time.RFC3339Nano, template.Source.CreatedAt); parseErr == nil {
		template.Source.CreatedAt = parsed.UTC().Format(time.RFC3339Nano)
	}
	if err := ValidateManifest(&template); err != nil {
		return nil, err
	}
	return &template, nil
}

func CanonicalBytes(m *Manifest) ([]byte, error) {
	if err := ValidateManifest(m); err != nil {
		return nil, err
	}
	encoded, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(encoded, '\n'), nil
}

func ManifestSHA256(m *Manifest) (string, error) {
	encoded, err := CanonicalBytes(m)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), nil
}

// WriteFile publishes canonical manifest bytes with an fsynced same-directory
// rename. Existing symbolic links and non-regular destinations are rejected.
func WriteFile(manifestPath string, manifest *Manifest) (string, error) {
	encoded, err := CanonicalBytes(manifest)
	if err != nil {
		return "", err
	}
	abs, err := filepath.Abs(manifestPath)
	if err != nil {
		return "", err
	}
	parent, err := filepath.EvalSymlinks(filepath.Dir(abs))
	if err != nil {
		return "", err
	}
	name := filepath.Base(abs)
	if name == "." || name == string(filepath.Separator) || !utf8.ValidString(name) {
		return "", validationError(ClassificationManifest, "manifest output path is invalid", abs, "regular filename", name)
	}
	root, err := os.OpenRoot(parent)
	if err != nil {
		return "", err
	}
	defer root.Close()
	if existing, statErr := root.Lstat(name); statErr == nil && (!existing.Mode().IsRegular() || existing.Mode()&os.ModeSymlink != 0) {
		return "", validationError(ClassificationSpecial, "manifest output is not a regular file", abs, "regular file", existing.Mode().String())
	} else if statErr != nil && !errors.Is(statErr, fs.ErrNotExist) {
		return "", statErr
	}
	temporary := fmt.Sprintf(".%s.tmp-%d-%d", name, os.Getpid(), time.Now().UnixNano())
	file, err := root.OpenFile(temporary, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return "", err
	}
	cleanup := true
	defer func() {
		_ = file.Close()
		if cleanup {
			_ = root.Remove(temporary)
		}
	}()
	if _, err := file.Write(encoded); err != nil {
		return "", err
	}
	if err := file.Sync(); err != nil {
		return "", err
	}
	if err := file.Close(); err != nil {
		return "", err
	}
	if err := root.Rename(temporary, name); err != nil {
		return "", err
	}
	cleanup = false
	directory, err := root.Open(".")
	if err != nil {
		return "", err
	}
	defer directory.Close()
	if err := directory.Sync(); err != nil {
		return "", err
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), nil
}

func Load(manifestPath string) (*Manifest, []byte, string, error) {
	abs, err := filepath.Abs(manifestPath)
	if err != nil {
		return nil, nil, "", err
	}
	parent, err := filepath.EvalSymlinks(filepath.Dir(abs))
	if err != nil {
		return nil, nil, "", err
	}
	root, err := os.OpenRoot(parent)
	if err != nil {
		return nil, nil, "", err
	}
	defer root.Close()
	name := filepath.Base(abs)
	before, err := root.Lstat(name)
	if err != nil {
		return nil, nil, "", err
	}
	if !before.Mode().IsRegular() || before.Mode()&os.ModeSymlink != 0 {
		return nil, nil, "", validationError(ClassificationSpecial, "dataset manifest is not a regular file", abs, "regular file", before.Mode().String())
	}
	file, err := root.Open(name)
	if err != nil {
		return nil, nil, "", err
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !sameStableFile(before, opened) {
		return nil, nil, "", validationError(ClassificationChanged, "dataset manifest changed while it was opened", abs, "stable file identity", "changed")
	}
	raw, err := io.ReadAll(io.LimitReader(file, maximumManifestBytes+1))
	if err != nil {
		return nil, nil, "", err
	}
	if len(raw) > maximumManifestBytes {
		return nil, nil, "", validationError(ClassificationManifest, "dataset manifest exceeds the schema size bound", manifestPath, "", fmt.Sprint(len(raw)))
	}
	after, err := root.Lstat(name)
	if err != nil || !sameStableFile(opened, after) || int64(len(raw)) != opened.Size() {
		return nil, nil, "", validationError(ClassificationChanged, "dataset manifest changed while it was read", abs, fmt.Sprint(opened.Size()), fmt.Sprint(len(raw)))
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var manifest Manifest
	if err := decoder.Decode(&manifest); err != nil {
		return nil, nil, "", validationError(ClassificationManifest, fmt.Sprintf("decode dataset manifest: %v", err), manifestPath, "", "")
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return nil, nil, "", validationError(ClassificationManifest, "dataset manifest contains trailing JSON", manifestPath, "single object", "trailing data")
	}
	if err := ValidateManifest(&manifest); err != nil {
		return nil, nil, "", err
	}
	canonical, err := CanonicalBytes(&manifest)
	if err != nil {
		return nil, nil, "", err
	}
	if !bytes.Equal(raw, canonical) {
		return nil, nil, "", validationError(ClassificationManifest, "dataset manifest is not canonical JSON", manifestPath, "canonical schema bytes", "different encoding")
	}
	digest := sha256.Sum256(raw)
	return &manifest, raw, hex.EncodeToString(digest[:]), nil
}

// LoadContentIdentity reads the canonical manifest in directory without
// hashing the live database files again. A missing manifest identifies a
// legacy dataset and is not an error; a present malformed or unsafe manifest
// fails closed.
func LoadContentIdentity(directory string) (ContentIdentity, bool, error) {
	manifest, _, manifestHash, err := Load(filepath.Join(directory, DefaultManifestFilename))
	if errors.Is(err, fs.ErrNotExist) {
		return ContentIdentity{}, false, nil
	}
	if err != nil {
		return ContentIdentity{}, false, err
	}
	return ContentIdentity{
		DirectoryRootSHA256: manifest.DirectoryRootSHA256,
		ManifestSHA256:      manifestHash,
	}, true, nil
}

func Verify(ctx context.Context, directory, manifestPath, expectedManifestSHA256 string) (*Verification, error) {
	manifest, _, manifestHash, err := Load(manifestPath)
	if err != nil {
		return nil, err
	}
	root, actual, err := Scan(ctx, directory, manifest.Exclusions)
	if err != nil {
		return nil, err
	}
	report := &Verification{DatasetRoot: root, ManifestSHA256: manifestHash, FileCount: len(actual)}
	for _, record := range actual {
		report.ByteCount += record.Size
	}
	if expected := strings.ToLower(strings.TrimSpace(expectedManifestSHA256)); expected != "" && expected != manifestHash {
		report.Diagnostics = append(report.Diagnostics, Diagnostic{Classification: ClassificationHash, Message: "manifest SHA-256 differs from the owner-selected hash", Path: manifestPath, Expected: expected, Actual: manifestHash, RepairableCopy: true})
	}
	manifestByPath := make(map[string]FileRecord, len(manifest.Files))
	for _, record := range manifest.Files {
		manifestByPath[record.Path] = record
	}
	actualByPath := make(map[string]FileRecord, len(actual))
	for _, record := range actual {
		actualByPath[record.Path] = record
		expected, ok := manifestByPath[record.Path]
		if !ok {
			report.Diagnostics = append(report.Diagnostics, Diagnostic{Classification: ClassificationExtra, Message: "dataset contains a regular file absent from the manifest", Path: record.Path, Actual: record.SHA256, RepairableCopy: true})
			continue
		}
		if expected.Size != record.Size || expected.SHA256 != record.SHA256 {
			report.Diagnostics = append(report.Diagnostics, Diagnostic{Classification: ClassificationHash, Message: "dataset file bytes differ from the manifest", Path: record.Path, Expected: fmt.Sprintf("%d:%s", expected.Size, expected.SHA256), Actual: fmt.Sprintf("%d:%s", record.Size, record.SHA256), RepairableCopy: true})
		}
	}
	for _, record := range manifest.Files {
		if _, ok := actualByPath[record.Path]; !ok {
			report.Diagnostics = append(report.Diagnostics, Diagnostic{Classification: ClassificationMissing, Message: "manifest file is missing from the dataset", Path: record.Path, Expected: record.SHA256, Actual: "missing", RepairableCopy: true})
		}
	}
	report.DirectoryRootSHA256, err = DirectoryRoot(actual)
	if err != nil {
		return nil, err
	}
	if report.DirectoryRootSHA256 != manifest.DirectoryRootSHA256 {
		report.Diagnostics = append(report.Diagnostics, Diagnostic{Classification: ClassificationHash, Message: "dataset directory-root digest differs from the manifest", Expected: manifest.DirectoryRootSHA256, Actual: report.DirectoryRootSHA256, RepairableCopy: true})
	}
	report.Valid = len(report.Diagnostics) == 0
	return report, nil
}
