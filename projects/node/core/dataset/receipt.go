package dataset

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	ReceiptSchema       = "dataset-semantic-verdict/v1"
	maximumReceiptBytes = 8 << 20
)

type VerifierIdentity struct {
	BinarySHA256 string `json:"binary_sha256"`
	Version      string `json:"version"`
}

// Receipt is the exact hand-off from Core's byte and semantic verifier to the
// Stack activation transaction. It grants no authority by publisher or path:
// activation must match these bytes, this chain identity, and this verifier.
type Receipt struct {
	Schema              string             `json:"schema"`
	DatasetKind         string             `json:"dataset_kind"`
	DatasetRoot         string             `json:"dataset_root"`
	ManifestSHA256      string             `json:"manifest_sha256"`
	DirectoryRootSHA256 string             `json:"directory_root_sha256"`
	Chain               ChainIdentity      `json:"chain"`
	Native              NativeState        `json:"native"`
	EVM                 EVMState           `json:"evm"`
	Verifier            VerifierIdentity   `json:"verifier"`
	FileCount           int                `json:"file_count"`
	ByteCount           uint64             `json:"byte_count"`
	Accepted            bool               `json:"accepted"`
	Diagnostics         []Diagnostic       `json:"diagnostics,omitempty"`
	VerifiedAt          string             `json:"verified_at"`
	ManifestSource      *ProducerSignature `json:"manifest_producer_signature,omitempty"`
}

func ValidateReceipt(receipt *Receipt) error {
	if receipt == nil || receipt.Schema != ReceiptSchema {
		return validationError(ClassificationManifest, "unsupported or missing dataset receipt schema", "", ReceiptSchema, "")
	}
	if receipt.DatasetKind != "directory" || strings.TrimSpace(receipt.DatasetRoot) == "" {
		return validationError(ClassificationManifest, "receipt dataset kind or root is invalid", receipt.DatasetRoot, "directory", receipt.DatasetKind)
	}
	if !validSHA256(receipt.ManifestSHA256) || !validSHA256(receipt.DirectoryRootSHA256) || !validSHA256(receipt.Verifier.BinarySHA256) {
		return validationError(ClassificationManifest, "receipt manifest, directory, and verifier hashes must be lowercase SHA-256", "", "lowercase SHA-256", "invalid")
	}
	if receipt.Chain.ChainID == 0 || !validText(receipt.Chain.Network, 128) || !validHash32(receipt.Chain.GenesisHash) || !validHash32(receipt.Chain.EVMGenesisHash) {
		return validationError(ClassificationManifest, "receipt chain identity is invalid", "", "", "")
	}
	if err := validateState(receipt.Native, receipt.EVM); err != nil {
		return err
	}
	if !validText(receipt.Verifier.Version, 256) {
		return validationError(ClassificationManifest, "receipt verifier version is required", "", "", receipt.Verifier.Version)
	}
	if receipt.Accepted && receipt.FileCount <= 0 {
		return validationError(ClassificationManifest, "receipt file count must be positive", "", ">0", fmt.Sprint(receipt.FileCount))
	}
	verifiedAt, err := time.Parse(time.RFC3339Nano, receipt.VerifiedAt)
	if err != nil || verifiedAt.Location() != time.UTC {
		return validationError(ClassificationManifest, "receipt verification time must be canonical UTC RFC3339", "", "", receipt.VerifiedAt)
	}
	if receipt.Accepted && len(receipt.Diagnostics) != 0 {
		return validationError(ClassificationManifest, "accepted receipt contains diagnostics", "", "no diagnostics", fmt.Sprint(len(receipt.Diagnostics)))
	}
	if !receipt.Accepted && len(receipt.Diagnostics) == 0 {
		return validationError(ClassificationManifest, "rejected receipt lacks a diagnostic", "", "at least one diagnostic", "none")
	}
	for _, diagnostic := range receipt.Diagnostics {
		if !validText(diagnostic.Classification, 128) || !validText(diagnostic.Message, 4096) {
			return validationError(ClassificationManifest, "receipt diagnostic is malformed", diagnostic.Path, "classification and message", "invalid")
		}
	}
	return nil
}

func ReceiptCanonicalBytes(receipt *Receipt) ([]byte, error) {
	if err := ValidateReceipt(receipt); err != nil {
		return nil, err
	}
	encoded, err := json.MarshalIndent(receipt, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(encoded, '\n'), nil
}

func ReceiptSHA256(receipt *Receipt) (string, error) {
	encoded, err := ReceiptCanonicalBytes(receipt)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), nil
}

func writeAtomicCanonicalFile(outputPath string, encoded []byte, kind string) (string, error) {
	abs, err := filepath.Abs(outputPath)
	if err != nil {
		return "", err
	}
	parent, err := filepath.EvalSymlinks(filepath.Dir(abs))
	if err != nil {
		return "", err
	}
	name := filepath.Base(abs)
	if name == "." || name == string(filepath.Separator) || !utf8.ValidString(name) {
		return "", validationError(ClassificationManifest, kind+" output path is invalid", abs, "regular filename", name)
	}
	root, err := os.OpenRoot(parent)
	if err != nil {
		return "", err
	}
	defer root.Close()
	if existing, statErr := root.Lstat(name); statErr == nil && (!existing.Mode().IsRegular() || existing.Mode()&os.ModeSymlink != 0) {
		return "", validationError(ClassificationSpecial, kind+" output is not a regular file", abs, "regular file", existing.Mode().String())
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

func WriteReceipt(outputPath string, receipt *Receipt) (string, error) {
	encoded, err := ReceiptCanonicalBytes(receipt)
	if err != nil {
		return "", err
	}
	return writeAtomicCanonicalFile(outputPath, encoded, "receipt")
}

func LoadReceipt(receiptPath string) (*Receipt, []byte, string, error) {
	abs, err := filepath.Abs(receiptPath)
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
		return nil, nil, "", validationError(ClassificationSpecial, "dataset receipt is not a regular file", abs, "regular file", before.Mode().String())
	}
	file, err := root.Open(name)
	if err != nil {
		return nil, nil, "", err
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !sameStableFile(before, opened) {
		return nil, nil, "", validationError(ClassificationChanged, "dataset receipt changed while it was opened", abs, "stable file identity", "changed")
	}
	raw, err := io.ReadAll(io.LimitReader(file, maximumReceiptBytes+1))
	if err != nil {
		return nil, nil, "", err
	}
	if len(raw) > maximumReceiptBytes {
		return nil, nil, "", validationError(ClassificationManifest, "dataset receipt exceeds the schema size bound", receiptPath, "", fmt.Sprint(len(raw)))
	}
	after, err := root.Lstat(name)
	if err != nil || !sameStableFile(opened, after) || int64(len(raw)) != opened.Size() {
		return nil, nil, "", validationError(ClassificationChanged, "dataset receipt changed while it was read", abs, fmt.Sprint(opened.Size()), fmt.Sprint(len(raw)))
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var receipt Receipt
	if err := decoder.Decode(&receipt); err != nil {
		return nil, nil, "", validationError(ClassificationManifest, fmt.Sprintf("decode dataset receipt: %v", err), receiptPath, "", "")
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return nil, nil, "", validationError(ClassificationManifest, "dataset receipt contains trailing JSON", receiptPath, "single object", "trailing data")
	}
	canonical, err := ReceiptCanonicalBytes(&receipt)
	if err != nil {
		return nil, nil, "", err
	}
	if !bytes.Equal(raw, canonical) {
		return nil, nil, "", validationError(ClassificationManifest, "dataset receipt is not canonical JSON", receiptPath, "canonical schema bytes", "different encoding")
	}
	digest := sha256.Sum256(raw)
	return &receipt, raw, hex.EncodeToString(digest[:]), nil
}

func FileSHA256(ctx context.Context, filePath string) (string, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return "", err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return "", validationError(ClassificationSpecial, "verifier binary is not a regular file", filePath, "regular file", "unavailable")
	}
	hash := sha256.New()
	buffer := make([]byte, hashBufferBytes)
	for {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		count, readErr := file.Read(buffer)
		if count > 0 {
			_, _ = hash.Write(buffer[:count])
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			return "", readErr
		}
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}
