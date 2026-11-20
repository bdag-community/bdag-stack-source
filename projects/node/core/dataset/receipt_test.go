package dataset

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func testReceipt() *Receipt {
	manifest := testManifestTemplate()
	return &Receipt{
		Schema: ReceiptSchema, DatasetKind: "directory", DatasetRoot: "/srv/blockdag/candidate with spaces",
		ManifestSHA256: strings.Repeat("1", 64), DirectoryRootSHA256: strings.Repeat("2", 64),
		Chain: manifest.Chain, Native: manifest.Native, EVM: manifest.EVM,
		Verifier:  VerifierIdentity{BinarySHA256: strings.Repeat("3", 64), Version: "v1.2.3"},
		FileCount: 2, ByteCount: 42, Accepted: true,
		VerifiedAt: time.Unix(100, 123).UTC().Format(time.RFC3339Nano),
	}
}

func TestReceiptCanonicalRoundTripAndHash(t *testing.T) {
	receipt := testReceipt()
	directory := t.TempDir()
	path := filepath.Join(directory, DefaultReceiptFilename)
	writtenHash, err := WriteReceipt(path, receipt)
	if err != nil {
		t.Fatal(err)
	}
	loaded, raw, loadedHash, err := LoadReceipt(path)
	if err != nil {
		t.Fatal(err)
	}
	if writtenHash != loadedHash || loaded.DatasetRoot != receipt.DatasetRoot || len(raw) == 0 {
		t.Fatalf("receipt round trip = hash %s/%s root %q", writtenHash, loadedHash, loaded.DatasetRoot)
	}
	expected, err := ReceiptSHA256(receipt)
	if err != nil || expected != writtenHash {
		t.Fatalf("receipt hash = %q err=%v, want %q", expected, err, writtenHash)
	}
}

func TestReceiptRejectsUnsafeOrNonCanonicalInput(t *testing.T) {
	receipt := testReceipt()
	receipt.Accepted = false
	if err := ValidateReceipt(receipt); err == nil {
		t.Fatal("rejected receipt without diagnostic was accepted")
	}
	receipt = testReceipt()
	receipt.Diagnostics = []Diagnostic{{Classification: "hash_mismatch", Message: "changed"}}
	if err := ValidateReceipt(receipt); err == nil {
		t.Fatal("accepted receipt with diagnostic was accepted")
	}
	directory := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside")
	if err := os.WriteFile(outside, []byte("outside"), 0o600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(directory, DefaultReceiptFilename)
	if err := os.Symlink(outside, path); err != nil {
		t.Fatal(err)
	}
	if _, err := WriteReceipt(path, testReceipt()); err == nil {
		t.Fatal("receipt writer followed a symlink")
	}
}

func TestFileSHA256HonorsCancellation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "binary")
	if err := os.WriteFile(path, []byte("binary"), 0o700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := FileSHA256(ctx, path); err == nil {
		t.Fatal("file hash ignored cancellation")
	}
}
