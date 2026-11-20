package dataset

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestCopyResumesExactFilesAndOmitsRuntimeExclusions(t *testing.T) {
	source := t.TempDir()
	if err := os.MkdirAll(filepath.Join(source, "BdagChain"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "BdagChain", "one.sst"), []byte("one"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "BdagChain", "two.log"), []byte("two"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "network.key"), []byte("must-not-copy"), 0o600); err != nil {
		t.Fatal(err)
	}
	manifest, err := Create(context.Background(), source, testManifestTemplate())
	if err != nil {
		t.Fatal(err)
	}
	manifestPath := writeManifestFixture(t, source, manifest)
	manifestHash, _ := ManifestSHA256(manifest)
	destination := filepath.Join(t.TempDir(), "candidate")
	if err := os.MkdirAll(filepath.Join(destination, "BdagChain"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(destination, "BdagChain", "one.sst"), []byte("one"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(destination, "BdagChain", "two.log"), []byte("wrong"), 0o600); err != nil {
		t.Fatal(err)
	}

	report, err := Copy(context.Background(), source, destination, manifestPath, manifestHash)
	if err != nil {
		t.Fatal(err)
	}
	if report.ReusedFiles != 1 || report.CopiedFiles != 1 || report.Verification == nil || !report.Verification.Valid {
		t.Fatalf("copy report = %+v", report)
	}
	if _, err := os.Stat(filepath.Join(destination, "network.key")); !os.IsNotExist(err) {
		t.Fatalf("target-local credential was copied: %v", err)
	}
}

func TestCopyRejectsDestinationSymlinkComponent(t *testing.T) {
	source := t.TempDir()
	if err := os.MkdirAll(filepath.Join(source, "nested"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "nested", "data"), []byte("bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	manifest, err := Create(context.Background(), source, testManifestTemplate())
	if err != nil {
		t.Fatal(err)
	}
	manifestPath := writeManifestFixture(t, source, manifest)
	destination := filepath.Join(t.TempDir(), "candidate")
	if err := os.Mkdir(destination, 0o755); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(destination, "nested")); err != nil {
		t.Fatal(err)
	}
	if _, err := Copy(context.Background(), source, destination, manifestPath, ""); err == nil {
		t.Fatal("copy accepted a destination symlink component")
	}
	if entries, err := os.ReadDir(outside); err != nil || len(entries) != 0 {
		t.Fatalf("copy escaped through destination symlink: entries=%v err=%v", entries, err)
	}
}

func TestCopyRejectsOverlappingPathsWithoutMutatingSource(t *testing.T) {
	source := t.TempDir()
	if err := os.WriteFile(filepath.Join(source, "chain.dat"), []byte("bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	manifest, err := Create(context.Background(), source, testManifestTemplate())
	if err != nil {
		t.Fatal(err)
	}
	manifestPath := writeManifestFixture(t, source, manifest)
	nestedDestination := filepath.Join(source, "candidate")
	if _, err := Copy(context.Background(), source, nestedDestination, manifestPath, ""); err == nil {
		t.Fatal("copy accepted a destination nested inside the source")
	}
	if _, err := os.Lstat(nestedDestination); !os.IsNotExist(err) {
		t.Fatalf("overlap rejection mutated source: %v", err)
	}

	parentDestination := filepath.Dir(source)
	if _, err := Copy(context.Background(), source, parentDestination, manifestPath, ""); err == nil {
		t.Fatal("copy accepted a destination containing the source")
	}
}
