package dataset

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func testManifestTemplate() Manifest {
	return Manifest{
		Chain:  ChainIdentity{ChainID: 1404, Network: "mainnet", GenesisHash: "0x" + repeatHex("11"), EVMGenesisHash: "0x" + repeatHex("66")},
		Native: NativeState{Order: 7, Height: 5, Hash: "0x" + repeatHex("22"), StateRoot: "0x" + repeatHex("33"), DatabaseFormat: "pebble-v1"},
		EVM:    EVMState{Number: 6, Hash: "0x" + repeatHex("44"), StateRoot: "0x" + repeatHex("55"), DatabaseFormat: "geth-pebble-v1"},
		Source: SourceInfo{CreatedAt: "2026-09-04T00:00:00Z", SoftwareVersion: "test"},
	}
}

func repeatHex(pair string) string {
	value := ""
	for len(value) < 64 {
		value += pair
	}
	return value[:64]
}

func writeManifestFixture(t *testing.T, root string, manifest *Manifest) string {
	t.Helper()
	encoded, err := CanonicalBytes(manifest)
	if err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(root, DefaultManifestFilename)
	if err := os.WriteFile(manifestPath, encoded, 0o644); err != nil {
		t.Fatal(err)
	}
	return manifestPath
}

func diagnosticClass(t *testing.T, err error) string {
	t.Helper()
	var validation *ValidationError
	if !errors.As(err, &validation) {
		t.Fatalf("error type = %T, want ValidationError: %v", err, err)
	}
	return validation.Diagnostic.Classification
}

func TestCreateAndVerifyDeterministicManifest(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "BdagChain", "folder with spaces"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "BdagChain", "000001.sst"), []byte("native"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "BdagChain", "folder with spaces", "000002.log"), []byte("wal"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "LOCK"), []byte("runtime"), 0o600); err != nil {
		t.Fatal(err)
	}
	manifest, err := Create(context.Background(), root, testManifestTemplate())
	if err != nil {
		t.Fatal(err)
	}
	if len(manifest.Files) != 2 {
		t.Fatalf("file count = %d, want 2", len(manifest.Files))
	}
	if manifest.Files[0].Path != "BdagChain/000001.sst" || manifest.Files[1].Path != "BdagChain/folder with spaces/000002.log" {
		t.Fatalf("unexpected canonical order: %+v", manifest.Files)
	}
	hash1, err := ManifestSHA256(manifest)
	if err != nil {
		t.Fatal(err)
	}
	manifest2, err := Create(context.Background(), root, testManifestTemplate())
	if err != nil {
		t.Fatal(err)
	}
	hash2, _ := ManifestSHA256(manifest2)
	if hash1 != hash2 || manifest.DirectoryRootSHA256 != manifest2.DirectoryRootSHA256 {
		t.Fatalf("manifest not deterministic: %s %s", hash1, hash2)
	}
	manifestPath := writeManifestFixture(t, root, manifest)
	report, err := Verify(context.Background(), root, manifestPath, hash1)
	if err != nil {
		t.Fatal(err)
	}
	if !report.Valid || report.ManifestSHA256 != hash1 {
		t.Fatalf("verification = %+v", report)
	}
}

func TestWriteFileIsCanonicalAndRejectsSymlinkDestination(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "data"), []byte("bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	manifest, err := Create(context.Background(), root, testManifestTemplate())
	if err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(root, DefaultManifestFilename)
	writtenHash, err := WriteFile(manifestPath, manifest)
	if err != nil {
		t.Fatal(err)
	}
	_, _, loadedHash, err := Load(manifestPath)
	if err != nil || loadedHash != writtenHash {
		t.Fatalf("loaded hash = %s, written = %s, err = %v", loadedHash, writtenHash, err)
	}
	if err := os.Remove(manifestPath); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "outside")
	if err := os.WriteFile(outside, []byte("unchanged"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, manifestPath); err != nil {
		t.Fatal(err)
	}
	if _, err := WriteFile(manifestPath, manifest); err == nil {
		t.Fatal("WriteFile replaced a symbolic-link destination")
	}
	if got, err := os.ReadFile(outside); err != nil || string(got) != "unchanged" {
		t.Fatalf("outside file changed: %q err=%v", got, err)
	}
}

func TestLoadRejectsManifestSymlink(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "data"), []byte("bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	manifest, err := Create(context.Background(), root, testManifestTemplate())
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := CanonicalBytes(manifest)
	if err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "manifest.json")
	if err := os.WriteFile(outside, canonical, 0o644); err != nil {
		t.Fatal(err)
	}
	linked := filepath.Join(root, DefaultManifestFilename)
	if err := os.Symlink(outside, linked); err != nil {
		t.Fatal(err)
	}
	_, _, _, err = Load(linked)
	if got := diagnosticClass(t, err); got != ClassificationSpecial {
		t.Fatalf("class = %s", got)
	}
}

func TestVerifyClassifiesMutationMissingAndExtra(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(t *testing.T, root string)
		class  string
	}{
		{name: "mutation", mutate: func(t *testing.T, root string) {
			t.Helper()
			_ = os.WriteFile(filepath.Join(root, "data"), []byte("changed"), 0o644)
		}, class: ClassificationHash},
		{name: "missing", mutate: func(t *testing.T, root string) { t.Helper(); _ = os.Remove(filepath.Join(root, "data")) }, class: ClassificationMissing},
		{name: "extra", mutate: func(t *testing.T, root string) {
			t.Helper()
			_ = os.WriteFile(filepath.Join(root, "extra"), []byte("extra"), 0o644)
		}, class: ClassificationExtra},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			if err := os.WriteFile(filepath.Join(root, "data"), []byte("original"), 0o666); err != nil {
				t.Fatal(err)
			}
			manifest, err := Create(context.Background(), root, testManifestTemplate())
			if err != nil {
				t.Fatal(err)
			}
			manifestPath := writeManifestFixture(t, root, manifest)
			test.mutate(t, root)
			report, err := Verify(context.Background(), root, manifestPath, "")
			if err != nil {
				t.Fatal(err)
			}
			found := false
			for _, diagnostic := range report.Diagnostics {
				if diagnostic.Classification == test.class {
					found = true
				}
			}
			if report.Valid || !found {
				t.Fatalf("verification = %+v, want %s", report, test.class)
			}
		})
	}
}

func TestScanRejectsSymlinkAndSpecialFile(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "data"), []byte("bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("data", filepath.Join(root, "linked")); err != nil {
		t.Fatal(err)
	}
	_, _, err := Scan(context.Background(), root, DefaultExclusions())
	if got := diagnosticClass(t, err); got != ClassificationSymlink {
		t.Fatalf("class = %s", got)
	}
	if err := os.Remove(filepath.Join(root, "linked")); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(filepath.Join(root, "named-pipe"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, _, err = Scan(context.Background(), root, DefaultExclusions())
	if got := diagnosticClass(t, err); got != ClassificationSpecial {
		t.Fatalf("class = %s", got)
	}
}

func TestManifestRejectsInventedExclusionAndNonCanonicalJSON(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "data"), []byte("bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	manifest, err := Create(context.Background(), root, testManifestTemplate())
	if err != nil {
		t.Fatal(err)
	}
	manifest.Exclusions = append(manifest.Exclusions, Exclusion{Rule: "path_prefix", Pattern: "BdagChain/", Reason: "hide chain"})
	if got := diagnosticClass(t, ValidateManifest(manifest)); got != ClassificationManifest {
		t.Fatalf("class = %s", got)
	}

	manifest, err = Create(context.Background(), root, testManifestTemplate())
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := CanonicalBytes(manifest)
	if err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(root, DefaultManifestFilename)
	if err := os.WriteFile(manifestPath, append([]byte(" \n"), encoded...), 0o644); err != nil {
		t.Fatal(err)
	}
	_, _, _, err = Load(manifestPath)
	if got := diagnosticClass(t, err); got != ClassificationManifest {
		t.Fatalf("class = %s", got)
	}
}

func TestManifestHashMismatchIsOwnerSelectedTrustFailure(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "data"), []byte("bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	manifest, err := Create(context.Background(), root, testManifestTemplate())
	if err != nil {
		t.Fatal(err)
	}
	manifestPath := writeManifestFixture(t, root, manifest)
	report, err := Verify(context.Background(), root, manifestPath, repeatHex("aa"))
	if err != nil {
		t.Fatal(err)
	}
	if report.Valid || len(report.Diagnostics) == 0 || report.Diagnostics[0].Classification != ClassificationHash {
		t.Fatalf("verification = %+v", report)
	}
}
