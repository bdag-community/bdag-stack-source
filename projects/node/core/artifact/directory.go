package artifact

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const directoryCopyBufferSize = 1024 * 1024

type DirectoryVerifyOptions struct {
	Manifest VerifyOptions
	Now      func() time.Time
}

type DirectoryInstallOptions struct {
	DirectoryVerifyOptions
	ReplaceExisting bool
	MoveStaging     bool
	AllowExtraFiles bool
	Sync            bool
}

type DirectoryArtifactReport struct {
	ArtifactRoot string    `json:"artifact_root"`
	Layout       string    `json:"layout"`
	Files        int       `json:"files"`
	Bytes        uint64    `json:"bytes"`
	StartedAt    time.Time `json:"started_at"`
	FinishedAt   time.Time `json:"finished_at"`
	InstalledTo  string    `json:"installed_to,omitempty"`
	MovedStaging bool      `json:"moved_staging,omitempty"`
}

func VerifyDirectoryArtifact(ctx context.Context, manifest *Manifest, root string, opts DirectoryVerifyOptions) (*DirectoryArtifactReport, error) {
	if manifest == nil {
		return nil, ErrNilManifest
	}
	if ctx == nil {
		ctx = context.Background()
	}
	now := opts.Now
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	started := now()
	if opts.Manifest.Now.IsZero() {
		opts.Manifest.Now = started
	}
	if err := manifest.Validate(opts.Manifest); err != nil {
		return nil, err
	}
	layout := manifest.Layout
	if layout == "" {
		layout = LayoutArchive
	}
	if layout != LayoutDirectory {
		return nil, fmt.Errorf("artifact: manifest layout %q is not directory", layout)
	}
	artifactRoot, err := validatedManifestRoot(manifest)
	if err != nil {
		return nil, err
	}
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("artifact: resolve directory root: %w", err)
	}
	var total uint64
	for _, file := range manifest.Files {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		fullPath, err := directoryArtifactFilePath(rootAbs, file.Path)
		if err != nil {
			return nil, err
		}
		if err := rejectSymlinkAncestors(rootAbs, fullPath); err != nil {
			return nil, fmt.Errorf("artifact: verify file %s: %w", file.Path, err)
		}
		size, digest, err := hashRegularFile(ctx, fullPath)
		if err != nil {
			return nil, fmt.Errorf("artifact: verify file %s: %w", file.Path, err)
		}
		if size != file.Size {
			return nil, fmt.Errorf("artifact: file %s size mismatch: got %d want %d", file.Path, size, file.Size)
		}
		if !equalHex(hex.EncodeToString(digest[:]), file.SHA256) {
			return nil, fmt.Errorf("artifact: file %s sha256 mismatch", file.Path)
		}
		total += size
	}
	return &DirectoryArtifactReport{
		ArtifactRoot: artifactRoot,
		Layout:       LayoutDirectory,
		Files:        len(manifest.Files),
		Bytes:        total,
		StartedAt:    started.UTC(),
		FinishedAt:   now().UTC(),
	}, nil
}

func InstallDirectoryArtifact(ctx context.Context, manifest *Manifest, stagingDir, targetDir string, opts DirectoryInstallOptions) (*DirectoryArtifactReport, error) {
	report, err := VerifyDirectoryArtifact(ctx, manifest, stagingDir, opts.DirectoryVerifyOptions)
	if err != nil {
		return nil, err
	}
	if opts.Sync {
		if err := syncDir(stagingDir); err != nil {
			return nil, fmt.Errorf("artifact: sync staging dir: %w", err)
		}
	}
	stagingAbs, err := filepath.Abs(stagingDir)
	if err != nil {
		return nil, fmt.Errorf("artifact: resolve staging dir: %w", err)
	}
	targetAbs, err := filepath.Abs(targetDir)
	if err != nil {
		return nil, fmt.Errorf("artifact: resolve target dir: %w", err)
	}
	if stagingAbs == targetAbs {
		return nil, fmt.Errorf("artifact: staging and target directories must differ")
	}
	if opts.MoveStaging {
		if !opts.AllowExtraFiles {
			if err := rejectExtraDirectoryFiles(stagingAbs, manifest); err != nil {
				return nil, err
			}
		}
		if err := atomicReplaceDirectory(stagingAbs, targetAbs, opts.ReplaceExisting, opts.Sync); err != nil {
			return nil, err
		}
		report.InstalledTo = targetAbs
		report.MovedStaging = true
		return report, nil
	}
	tmpDir := filepath.Join(filepath.Dir(targetAbs), "."+filepath.Base(targetAbs)+".artifact-tmp-"+rootPrefix(report.ArtifactRoot))
	_ = os.RemoveAll(tmpDir)
	if err := os.MkdirAll(tmpDir, 0o755); err != nil {
		return nil, fmt.Errorf("artifact: create temp target: %w", err)
	}
	removeTmp := true
	defer func() {
		if removeTmp {
			_ = os.RemoveAll(tmpDir)
		}
	}()
	if err := copyDirectoryArtifactFiles(ctx, manifest, stagingAbs, tmpDir); err != nil {
		return nil, err
	}
	if opts.Sync {
		if err := syncDirectoryTree(tmpDir); err != nil {
			return nil, fmt.Errorf("artifact: sync temp target: %w", err)
		}
	}
	if err := atomicReplaceDirectory(tmpDir, targetAbs, opts.ReplaceExisting, opts.Sync); err != nil {
		return nil, err
	}
	removeTmp = false
	report.InstalledTo = targetAbs
	return report, nil
}

func directoryArtifactFilePath(rootAbs, rel string) (string, error) {
	clean := filepath.Clean(filepath.FromSlash(rel))
	if clean == "." || filepath.IsAbs(clean) || strings.HasPrefix(clean, ".."+string(filepath.Separator)) || clean == ".." {
		return "", fmt.Errorf("artifact: unsafe file path %q", rel)
	}
	full := filepath.Join(rootAbs, clean)
	fullAbs, err := filepath.Abs(full)
	if err != nil {
		return "", err
	}
	relative, err := filepath.Rel(rootAbs, fullAbs)
	if err != nil {
		return "", err
	}
	if relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("artifact: file path escapes root: %q", rel)
	}
	return fullAbs, nil
}

func rejectSymlinkAncestors(rootAbs, fullPath string) error {
	rel, err := filepath.Rel(rootAbs, fullPath)
	if err != nil {
		return err
	}
	dir := filepath.Dir(rel)
	if dir == "." {
		return nil
	}
	current := rootAbs
	for _, part := range strings.Split(dir, string(filepath.Separator)) {
		if part == "" || part == "." {
			continue
		}
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("symlink path component is not allowed: %s", part)
		}
		if !info.IsDir() {
			return fmt.Errorf("path component is not a directory: %s", part)
		}
	}
	return nil
}

func hashRegularFile(ctx context.Context, path string) (uint64, [sha256.Size]byte, error) {
	var zero [sha256.Size]byte
	info, err := os.Lstat(path)
	if err != nil {
		return 0, zero, err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return 0, zero, fmt.Errorf("symlinks are not allowed")
	}
	if !info.Mode().IsRegular() {
		return 0, zero, fmt.Errorf("not a regular file")
	}
	f, err := os.Open(path)
	if err != nil {
		return 0, zero, err
	}
	defer f.Close()
	h := sha256.New()
	buf := make([]byte, directoryCopyBufferSize)
	var total uint64
	for {
		if err := ctx.Err(); err != nil {
			return 0, zero, err
		}
		n, readErr := f.Read(buf)
		if n > 0 {
			total += uint64(n)
			if _, err := h.Write(buf[:n]); err != nil {
				return 0, zero, err
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return 0, zero, readErr
		}
	}
	var digest [sha256.Size]byte
	copy(digest[:], h.Sum(nil))
	return total, digest, nil
}

func copyDirectoryArtifactFiles(ctx context.Context, manifest *Manifest, stagingAbs, targetAbs string) error {
	buf := make([]byte, directoryCopyBufferSize)
	for _, file := range manifest.Files {
		src, err := directoryArtifactFilePath(stagingAbs, file.Path)
		if err != nil {
			return err
		}
		if err := rejectSymlinkAncestors(stagingAbs, src); err != nil {
			return err
		}
		dst, err := directoryArtifactFilePath(targetAbs, file.Path)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		mode := os.FileMode(0o644)
		if file.Mode != 0 {
			mode = os.FileMode(file.Mode)
		}
		if err := copyFile(ctx, src, dst, mode, buf); err != nil {
			return fmt.Errorf("artifact: copy file %s: %w", file.Path, err)
		}
	}
	return nil
}

func copyFile(ctx context.Context, src, dst string, mode os.FileMode, buf []byte) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	removeDst := true
	defer func() {
		_ = out.Close()
		if removeDst {
			_ = os.Remove(dst)
		}
	}()
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		n, readErr := in.Read(buf)
		if n > 0 {
			if _, err := out.Write(buf[:n]); err != nil {
				return err
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return readErr
		}
	}
	if err := out.Sync(); err != nil {
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	removeDst = false
	return nil
}

func rejectExtraDirectoryFiles(rootAbs string, manifest *Manifest) error {
	allowed := make(map[string]struct{}, len(manifest.Files))
	for _, file := range manifest.Files {
		clean := filepath.Clean(filepath.FromSlash(file.Path))
		allowed[clean] = struct{}{}
	}
	return filepath.WalkDir(rootAbs, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path == rootAbs || d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(rootAbs, path)
		if err != nil {
			return err
		}
		if _, ok := allowed[rel]; !ok {
			return fmt.Errorf("artifact: staging directory contains unmanifested file %s", rel)
		}
		return nil
	})
}

func atomicReplaceDirectory(src, target string, replaceExisting, syncParent bool) error {
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	var backup string
	if _, err := os.Lstat(target); err == nil {
		if !replaceExisting {
			return fmt.Errorf("artifact: target directory already exists: %s", target)
		}
		backup = target + ".replaced-" + time.Now().UTC().Format("20060102T150405.000000000Z")
		if err := os.Rename(target, backup); err != nil {
			return fmt.Errorf("artifact: move existing target aside: %w", err)
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	renamed := false
	defer func() {
		if !renamed && backup != "" {
			_ = os.Rename(backup, target)
		}
	}()
	if err := os.Rename(src, target); err != nil {
		return fmt.Errorf("artifact: install directory: %w", err)
	}
	renamed = true
	if backup != "" {
		_ = os.RemoveAll(backup)
	}
	if syncParent {
		if err := syncDir(filepath.Dir(target)); err != nil && runtime.GOOS != "windows" {
			return err
		}
	}
	return nil
}

func syncDirectoryTree(root string) error {
	return filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			return nil
		}
		return syncDir(path)
	})
}

func rootPrefix(root string) string {
	if len(root) <= 12 {
		return root
	}
	return root[:12]
}
