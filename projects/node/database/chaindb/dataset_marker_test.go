package chaindb

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BlockdagNetworkLabs/bdag/core/dataset"
	"github.com/BlockdagNetworkLabs/bdag/core/shutdown"
)

func writeDatasetMarkerManifest(t *testing.T, datadir string) (string, string) {
	t.Helper()
	dataPath := filepath.Join(datadir, "BdagChain", "000001.sst")
	if err := os.MkdirAll(filepath.Dir(dataPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dataPath, []byte("fixture chain bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	hash := strings.Repeat("1", 64)
	root := strings.Repeat("2", 64)
	manifest, err := dataset.Create(context.Background(), datadir, dataset.Manifest{
		Chain: dataset.ChainIdentity{
			ChainID: 1404, Network: "fixture", GenesisHash: hash, EVMGenesisHash: hash,
		},
		Native: dataset.NativeState{
			Order: 10, Height: 9, Hash: hash, StateRoot: root, DatabaseFormat: "pebble",
		},
		EVM: dataset.EVMState{
			Number: 8, Hash: hash, StateRoot: root, DatabaseFormat: "pebble",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	manifestHash, err := dataset.WriteFile(filepath.Join(datadir, dataset.DefaultManifestFilename), manifest)
	if err != nil {
		t.Fatal(err)
	}
	return manifest.DirectoryRootSHA256, manifestHash
}

func TestProductionShutdownTrackerBindsManifestIdentity(t *testing.T) {
	datadir := t.TempDir()
	directoryRoot, manifestHash := writeDatasetMarkerManifest(t, datadir)
	tracker, err := newShutdownTracker(datadir)
	if err != nil {
		t.Fatal(err)
	}
	previous := shutdown.DatasetState{
		NativeOrder: 10,
		NativeHash:  "0xnative",
		EVMNumber:   8,
		EVMHash:     "0xevm",
		EVMRoot:     "0xroot",
	}
	if err := tracker.WaitBlockApplyState("0xnext", previous.NativeHash, previous); err != nil {
		t.Fatal(err)
	}
	marker, pending, err := tracker.Pending()
	if err != nil || !pending {
		t.Fatalf("pending=%t err=%v", pending, err)
	}
	if marker.DatasetRootSHA256 != directoryRoot || marker.ManifestSHA256 != manifestHash {
		t.Fatalf("marker identity root=%q manifest=%q", marker.DatasetRootSHA256, marker.ManifestSHA256)
	}
}

func TestProductionShutdownTrackerAllowsLegacyDatasetWithoutManifest(t *testing.T) {
	tracker, err := newShutdownTracker(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := tracker.WaitBlockApply("0xnext", "0xprevious"); err != nil {
		t.Fatal(err)
	}
	marker, pending, err := tracker.Pending()
	if err != nil || !pending {
		t.Fatalf("pending=%t err=%v", pending, err)
	}
	if marker.DatasetRootSHA256 != "" || marker.ManifestSHA256 != "" {
		t.Fatalf("legacy marker unexpectedly has dataset identity: %+v", marker)
	}
}

func TestProductionRecoveryClassifiesAndCompletesApplicableMarker(t *testing.T) {
	datadir := t.TempDir()
	tracker := shutdown.NewTracker(datadir)
	cdb := &ChainDB{shutdownTracker: tracker}
	current := shutdown.DatasetState{NativeOrder: 10, NativeHash: "0xprevious", EVMNumber: 9, EVMHash: "0xevm", EVMRoot: "0xroot"}
	if err := tracker.WaitBlockApplyState("0xnext", current.NativeHash, current); err != nil {
		t.Fatal(err)
	}
	if _, classification, err := cdb.ClassifyBlockApplyRecovery(current); err != nil || classification != shutdown.MarkerApplicable {
		t.Fatalf("classification=%q err=%v, want applicable", classification, err)
	}
	if err := cdb.CompleteBlockApplyRecovery(); err != nil {
		t.Fatal(err)
	}
	if _, pending, err := tracker.Pending(); err != nil || pending {
		t.Fatalf("pending=%t err=%v after completion", pending, err)
	}
}

func TestProductionRecoveryQuarantinesForeignMarkerAndDoesNotCompleteIt(t *testing.T) {
	datadir := t.TempDir()
	identity := shutdown.DatasetState{DatasetRootSHA256: strings.Repeat("a", 64), ManifestSHA256: strings.Repeat("b", 64)}
	tracker := shutdown.NewTrackerWithDatasetState(datadir, identity)
	cdb := &ChainDB{shutdownTracker: tracker}
	previous := shutdown.DatasetState{NativeHash: "0xprevious", DatasetRootSHA256: identity.DatasetRootSHA256, ManifestSHA256: identity.ManifestSHA256}
	if err := tracker.WaitBlockApplyState("0xnext", previous.NativeHash, previous); err != nil {
		t.Fatal(err)
	}
	current := previous
	current.DatasetRootSHA256 = strings.Repeat("c", 64)
	if _, classification, err := cdb.ClassifyBlockApplyRecovery(current); err != nil || classification != shutdown.MarkerForeign {
		t.Fatalf("classification=%q err=%v, want foreign", classification, err)
	}
	if _, pending, err := tracker.Pending(); err != nil || pending {
		t.Fatalf("pending=%t err=%v after quarantine", pending, err)
	}
	if err := cdb.CompleteBlockApplyRecovery(); err != nil {
		t.Fatal(err)
	}
}

func TestProductionRecoveryRejectsMalformedMarkerWithoutConsumingIt(t *testing.T) {
	datadir := t.TempDir()
	tracker := shutdown.NewTracker(datadir)
	cdb := &ChainDB{shutdownTracker: tracker}
	if err := os.WriteFile(filepath.Join(datadir, "shutdown.lock"), []byte(`{"version":2,"operation_type":"block-apply"`), 0o644); err != nil {
		t.Fatal(err)
	}
	_, _, err := cdb.ClassifyBlockApplyRecovery(shutdown.DatasetState{})
	var malformed *shutdown.MalformedMarkerError
	if !errors.As(err, &malformed) {
		t.Fatalf("error=%T %v, want MalformedMarkerError", err, err)
	}
	if _, _, pendingErr := tracker.Pending(); pendingErr == nil {
		t.Fatal("malformed marker was unexpectedly consumed")
	}
}

func TestProductionRecoveryClassifiesMatchingLegacyMarker(t *testing.T) {
	datadir := t.TempDir()
	tracker := shutdown.NewTracker(datadir)
	cdb := &ChainDB{shutdownTracker: tracker}
	marker := shutdown.Marker{Version: 1, Phase: "block-apply", BlockHash: "0xnext", PreviousBestHash: "0xprevious"}
	data, err := json.Marshal(marker)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(datadir, "shutdown.lock"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, classification, err := cdb.ClassifyBlockApplyRecovery(shutdown.DatasetState{NativeHash: "0xprevious"}); err != nil || classification != shutdown.MarkerApplicable {
		t.Fatalf("classification=%q err=%v, want applicable", classification, err)
	}
	if err := cdb.CompleteBlockApplyRecovery(); err != nil {
		t.Fatal(err)
	}
}
