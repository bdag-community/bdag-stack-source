package dataset

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
)

type CopyReport struct {
	Schema         string        `json:"schema"`
	Source         string        `json:"source"`
	Destination    string        `json:"destination"`
	ManifestSHA256 string        `json:"manifest_sha256"`
	CopiedFiles    int           `json:"copied_files"`
	ReusedFiles    int           `json:"reused_files"`
	CopiedBytes    uint64        `json:"copied_bytes"`
	Verification   *Verification `json:"verification,omitempty"`
}

func resolveDestination(destination string) (string, bool, error) {
	if strings.TrimSpace(destination) == "" {
		return "", false, validationError(ClassificationManifest, "copy destination is required", "", "", "")
	}
	abs, err := filepath.Abs(destination)
	if err != nil {
		return "", false, err
	}
	abs = filepath.Clean(abs)
	if info, statErr := os.Lstat(abs); statErr == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return "", false, validationError(ClassificationSymlink, "copy destination must be a real directory", abs, "directory", info.Mode().String())
		}
		resolved, err := filepath.EvalSymlinks(abs)
		if err != nil {
			return "", false, err
		}
		return filepath.Clean(resolved), true, nil
	} else if !errors.Is(statErr, fs.ErrNotExist) {
		return "", false, statErr
	}
	parent, err := filepath.EvalSymlinks(filepath.Dir(abs))
	if err != nil {
		return "", false, err
	}
	return filepath.Join(parent, filepath.Base(abs)), false, nil
}

func pathContains(parent, child string) bool {
	relative, err := filepath.Rel(parent, child)
	if err != nil {
		return false
	}
	return relative == "." || (relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)))
}

func ensureDestinationParents(root *os.Root, relative string) error {
	directory := path.Dir(relative)
	if directory == "." {
		return nil
	}
	current := ""
	for _, component := range strings.Split(directory, "/") {
		if current == "" {
			current = component
		} else {
			current += "/" + component
		}
		info, err := root.Lstat(current)
		if errors.Is(err, fs.ErrNotExist) {
			if err := root.Mkdir(current, 0o755); err != nil {
				return err
			}
			continue
		}
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return validationError(ClassificationSymlink, "copy destination contains an unsafe path component", current, "real directory", info.Mode().String())
		}
	}
	return nil
}

func existingDestinationMatches(ctx context.Context, root *os.Root, record FileRecord, buffer []byte) (bool, error) {
	info, err := root.Lstat(record.Path)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return false, validationError(ClassificationSymlink, "copy destination file is not a regular file", record.Path, "regular file", info.Mode().String())
	}
	actual, err := hashRootFile(ctx, root, record.Path, buffer)
	if err != nil {
		return false, err
	}
	return actual.Size == record.Size && actual.SHA256 == record.SHA256, nil
}

func copyManifestFile(ctx context.Context, source, destination *os.Root, record FileRecord, buffer []byte) (uint64, error) {
	before, err := source.Lstat(record.Path)
	if err != nil {
		return 0, validationError(ClassificationMissing, fmt.Sprintf("stat source file: %v", err), record.Path, record.SHA256, "missing")
	}
	if !before.Mode().IsRegular() || before.Mode()&os.ModeSymlink != 0 {
		return 0, validationError(ClassificationSymlink, "copy source file is not a regular file", record.Path, "regular file", before.Mode().String())
	}
	input, err := source.Open(record.Path)
	if err != nil {
		return 0, err
	}
	defer input.Close()
	opened, err := input.Stat()
	if err != nil || !sameStableFile(before, opened) {
		return 0, validationError(ClassificationChanged, "copy source changed while it was opened", record.Path, "stable file", "changed")
	}
	if err := ensureDestinationParents(destination, record.Path); err != nil {
		return 0, err
	}
	temporary := fmt.Sprintf("%s.bdag-copy-%d-%d.tmp", record.Path, os.Getpid(), time.Now().UnixNano())
	output, err := destination.OpenFile(temporary, os.O_WRONLY|os.O_CREATE|os.O_EXCL, before.Mode().Perm())
	if err != nil {
		return 0, err
	}
	cleanup := true
	defer func() {
		_ = output.Close()
		if cleanup {
			_ = destination.Remove(temporary)
		}
	}()
	hash := sha256.New()
	var total uint64
	for {
		if err := ctx.Err(); err != nil {
			return 0, validationError(ClassificationCanceled, err.Error(), record.Path, "", "")
		}
		count, readErr := input.Read(buffer)
		if count > 0 {
			if _, err := output.Write(buffer[:count]); err != nil {
				return 0, err
			}
			_, _ = hash.Write(buffer[:count])
			total += uint64(count)
		}
		if readErr == fs.ErrClosed {
			return 0, readErr
		}
		if readErr != nil {
			if errors.Is(readErr, fs.ErrNotExist) {
				return 0, validationError(ClassificationChanged, "copy source disappeared while reading", record.Path, record.SHA256, "missing")
			}
			if errors.Is(readErr, io.EOF) {
				break
			}
			return 0, readErr
		}
	}
	actualHash := hex.EncodeToString(hash.Sum(nil))
	if total != record.Size || actualHash != record.SHA256 {
		return 0, validationError(ClassificationHash, "copy source bytes differ from the manifest", record.Path, fmt.Sprintf("%d:%s", record.Size, record.SHA256), fmt.Sprintf("%d:%s", total, actualHash))
	}
	after, err := source.Lstat(record.Path)
	if err != nil || !sameStableFile(opened, after) {
		return 0, validationError(ClassificationChanged, "copy source changed during transfer", record.Path, "stable file", "changed")
	}
	if err := output.Sync(); err != nil {
		return 0, err
	}
	if err := output.Close(); err != nil {
		return 0, err
	}
	if existing, err := destination.Lstat(record.Path); err == nil && (!existing.Mode().IsRegular() || existing.Mode()&os.ModeSymlink != 0) {
		return 0, validationError(ClassificationSymlink, "copy destination changed to an unsafe entry", record.Path, "regular file", existing.Mode().String())
	} else if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return 0, err
	}
	if err := destination.Rename(temporary, record.Path); err != nil {
		return 0, err
	}
	cleanup = false
	parent := path.Dir(record.Path)
	directory, err := destination.Open(parent)
	if err != nil {
		return 0, err
	}
	defer directory.Close()
	if err := directory.Sync(); err != nil {
		return 0, err
	}
	return total, nil
}

// Copy copies only manifest-bound chain bytes. It never stops or mutates the
// source. An interrupted destination is resumable because already-correct files
// are re-hashed and reused; mismatched files are atomically replaced.
func Copy(ctx context.Context, sourceDirectory, destinationDirectory, manifestPath, expectedManifestSHA256 string) (*CopyReport, error) {
	manifest, _, manifestHash, err := Load(manifestPath)
	if err != nil {
		return nil, err
	}
	if expected := strings.ToLower(strings.TrimSpace(expectedManifestSHA256)); expected != "" && expected != manifestHash {
		return nil, validationError(ClassificationHash, "source manifest SHA-256 differs from the owner-selected hash", manifestPath, expected, manifestHash)
	}
	sourcePath, err := canonicalRoot(sourceDirectory)
	if err != nil {
		return nil, err
	}
	destinationPath, destinationExists, err := resolveDestination(destinationDirectory)
	if err != nil {
		return nil, err
	}
	if pathContains(sourcePath, destinationPath) || pathContains(destinationPath, sourcePath) {
		return nil, validationError(ClassificationManifest, "copy source and destination must not overlap", destinationPath, "independent destination", sourcePath)
	}
	if !destinationExists {
		if err := os.Mkdir(destinationPath, 0o755); err != nil {
			return nil, err
		}
	}
	source, err := os.OpenRoot(sourcePath)
	if err != nil {
		return nil, err
	}
	defer source.Close()
	destination, err := os.OpenRoot(destinationPath)
	if err != nil {
		return nil, err
	}
	defer destination.Close()
	report := &CopyReport{Schema: "blockdag-dataset-copy/v1", Source: sourcePath, Destination: destinationPath, ManifestSHA256: manifestHash}
	buffer := make([]byte, hashBufferBytes)
	for _, record := range manifest.Files {
		if err := ctx.Err(); err != nil {
			return nil, validationError(ClassificationCanceled, err.Error(), record.Path, "", "")
		}
		matches, err := existingDestinationMatches(ctx, destination, record, buffer)
		if err != nil {
			return nil, err
		}
		if matches {
			report.ReusedFiles++
			continue
		}
		copied, err := copyManifestFile(ctx, source, destination, record, buffer)
		if err != nil {
			return nil, err
		}
		report.CopiedFiles++
		report.CopiedBytes += copied
	}
	destinationManifest := filepath.Join(destinationPath, DefaultManifestFilename)
	if _, err := WriteFile(destinationManifest, manifest); err != nil {
		return nil, err
	}
	report.Verification, err = Verify(ctx, destinationPath, destinationManifest, manifestHash)
	if err != nil {
		return nil, err
	}
	if !report.Verification.Valid {
		return report, validationError(ClassificationHash, "copied dataset failed exact destination verification", destinationPath, manifest.DirectoryRootSHA256, report.Verification.DirectoryRootSHA256)
	}
	return report, nil
}
