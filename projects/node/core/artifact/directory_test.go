package artifact

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestVerifyDirectoryArtifactHashesManifestFiles(t *testing.T) {
	staging := t.TempDir()
	body := []byte("chain-data")
	m := testDirectoryManifest(t, map[string][]byte{
		"chain/BdagChain/000001.log": body,
	})
	writeDirectoryArtifactFiles(t, staging, m, map[string][]byte{
		"chain/BdagChain/000001.log": body,
	})

	report, err := VerifyDirectoryArtifact(context.Background(), m, staging, DirectoryVerifyOptions{
		Manifest: unsignedTestOptions(),
		Now:      testNow,
	})
	if err != nil {
		t.Fatalf("verify directory artifact: %v", err)
	}
	if report.Files != 1 || report.Bytes != uint64(len(body)) || report.Layout != LayoutDirectory {
		t.Fatalf("unexpected report: %+v", report)
	}
}

func TestInstallDirectoryArtifactCopiesFilesAtomically(t *testing.T) {
	staging := t.TempDir()
	target := filepath.Join(t.TempDir(), "chain")
	files := map[string][]byte{
		"chain/BdagChain/000001.log": []byte("chain-data"),
		"state/ancient/000002.cdat":  []byte("ancient-data"),
	}
	m := testDirectoryManifest(t, files)
	writeDirectoryArtifactFiles(t, staging, m, files)

	report, err := InstallDirectoryArtifact(context.Background(), m, staging, target, DirectoryInstallOptions{
		DirectoryVerifyOptions: DirectoryVerifyOptions{Manifest: unsignedTestOptions(), Now: testNow},
	})
	if err != nil {
		t.Fatalf("install directory artifact: %v", err)
	}
	if report.InstalledTo != target || report.MovedStaging {
		t.Fatalf("unexpected install report: %+v", report)
	}
	for rel, want := range files {
		got, err := os.ReadFile(filepath.Join(target, filepath.FromSlash(rel)))
		if err != nil {
			t.Fatalf("read installed file %s: %v", rel, err)
		}
		if string(got) != string(want) {
			t.Fatalf("installed file %s = %q want %q", rel, got, want)
		}
	}
	if _, err := os.Stat(staging); err != nil {
		t.Fatalf("copy install should leave staging directory in place: %v", err)
	}
}

func TestInstallDirectoryArtifactMoveRejectsExtraFiles(t *testing.T) {
	staging := t.TempDir()
	target := filepath.Join(t.TempDir(), "chain")
	files := map[string][]byte{
		"chain/BdagChain/000001.log": []byte("chain-data"),
	}
	m := testDirectoryManifest(t, files)
	writeDirectoryArtifactFiles(t, staging, m, files)
	if err := os.WriteFile(filepath.Join(staging, "extra.tmp"), []byte("extra"), 0o644); err != nil {
		t.Fatalf("write extra file: %v", err)
	}

	_, err := InstallDirectoryArtifact(context.Background(), m, staging, target, DirectoryInstallOptions{
		DirectoryVerifyOptions: DirectoryVerifyOptions{Manifest: unsignedTestOptions(), Now: testNow},
		MoveStaging:            true,
	})
	if err == nil || !strings.Contains(err.Error(), "unmanifested file") {
		t.Fatalf("expected unmanifested file error, got %v", err)
	}
}

func TestVerifyDirectoryArtifactRejectsSymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink privileges are not guaranteed on Windows")
	}
	staging := t.TempDir()
	body := []byte("chain-data")
	m := testDirectoryManifest(t, map[string][]byte{
		"chain/BdagChain/000001.log": body,
	})
	fullPath := filepath.Join(staging, "chain", "BdagChain", "000001.log")
	if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.Symlink("/etc/passwd", fullPath); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	_, err := VerifyDirectoryArtifact(context.Background(), m, staging, DirectoryVerifyOptions{
		Manifest: unsignedTestOptions(),
		Now:      testNow,
	})
	if err == nil || !strings.Contains(err.Error(), "symlinks are not allowed") {
		t.Fatalf("expected symlink rejection, got %v", err)
	}
}

func TestVerifyDirectoryArtifactRejectsSymlinkAncestor(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink privileges are not guaranteed on Windows")
	}
	staging := t.TempDir()
	outside := t.TempDir()
	body := []byte("chain-data")
	m := testDirectoryManifest(t, map[string][]byte{
		"chain/BdagChain/000001.log": body,
	})
	if err := os.MkdirAll(filepath.Join(outside, "BdagChain"), 0o755); err != nil {
		t.Fatalf("mkdir outside: %v", err)
	}
	if err := os.WriteFile(filepath.Join(outside, "BdagChain", "000001.log"), body, 0o600); err != nil {
		t.Fatalf("write outside file: %v", err)
	}
	if err := os.Symlink(outside, filepath.Join(staging, "chain")); err != nil {
		t.Fatalf("symlink ancestor: %v", err)
	}

	_, err := VerifyDirectoryArtifact(context.Background(), m, staging, DirectoryVerifyOptions{
		Manifest: unsignedTestOptions(),
		Now:      testNow,
	})
	if err == nil || !strings.Contains(err.Error(), "symlink path component") {
		t.Fatalf("expected symlink ancestor rejection, got %v", err)
	}
}

func testDirectoryManifest(t *testing.T, files map[string][]byte) *Manifest {
	t.Helper()
	m := testManifest(t)
	m.Layout = LayoutDirectory
	m.Files = make([]FileDescriptor, 0, len(files))
	for rel, body := range files {
		sum := sha256.Sum256(body)
		m.Files = append(m.Files, FileDescriptor{
			Path:       rel,
			Class:      "chain_db",
			Size:       uint64(len(body)),
			SHA256:     hex.EncodeToString(sum[:]),
			ChunkStart: 0,
			ChunkCount: 1,
			Mode:       0o600,
		})
	}
	if err := m.Finalize(); err != nil {
		t.Fatalf("finalize manifest: %v", err)
	}
	return m
}

func writeDirectoryArtifactFiles(t *testing.T, root string, m *Manifest, files map[string][]byte) {
	t.Helper()
	for _, file := range m.Files {
		body, ok := files[file.Path]
		if !ok {
			t.Fatalf("missing body for %s", file.Path)
		}
		fullPath := filepath.Join(root, filepath.FromSlash(file.Path))
		if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", filepath.Dir(fullPath), err)
		}
		if err := os.WriteFile(fullPath, body, 0o600); err != nil {
			t.Fatalf("write %s: %v", file.Path, err)
		}
	}
}
