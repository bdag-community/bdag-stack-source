package shutdown

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTrackerWaitWritesStructuredMarker(t *testing.T) {
	tracker := NewTracker(t.TempDir())

	const blockHash = "0xabc123"
	const previousBestHash = "0xprev456"
	if err := tracker.WaitBlockApply(blockHash, previousBestHash); err != nil {
		t.Fatalf("WaitBlockApply returned error: %v", err)
	}

	data, err := os.ReadFile(tracker.filePath)
	if err != nil {
		t.Fatalf("read marker: %v", err)
	}

	var marker Marker
	if err := json.Unmarshal(data, &marker); err != nil {
		t.Fatalf("marker is not JSON: %v", err)
	}
	if marker.Version != markerVersion {
		t.Fatalf("version = %d, want %d", marker.Version, markerVersion)
	}
	if marker.Phase != markerPhaseBlockApply {
		t.Fatalf("phase = %q, want %q", marker.Phase, markerPhaseBlockApply)
	}
	if marker.BlockHash != blockHash {
		t.Fatalf("block hash = %q, want %q", marker.BlockHash, blockHash)
	}
	if marker.PreviousBestHash != previousBestHash {
		t.Fatalf("previous best hash = %q, want %q", marker.PreviousBestHash, previousBestHash)
	}
	if marker.StartedAt == "" {
		t.Fatal("started_at was not populated")
	}
	if len(marker.NodeBinarySHA256) != 64 || marker.NodeVersion == "" {
		t.Fatalf("node identity was not populated: hash=%q version=%q", marker.NodeBinarySHA256, marker.NodeVersion)
	}

	pending, ok, err := tracker.Pending()
	if err != nil {
		t.Fatalf("Pending returned error: %v", err)
	}
	if !ok {
		t.Fatal("Pending did not report marker")
	}
	if pending.BlockHash != blockHash {
		t.Fatalf("pending block hash = %q, want %q", pending.BlockHash, blockHash)
	}
	if pending.PreviousBestHash != previousBestHash {
		t.Fatalf("pending previous best hash = %q, want %q", pending.PreviousBestHash, previousBestHash)
	}
}

func TestTrackerWaitBindsExactPreOperationState(t *testing.T) {
	tracker := NewTracker(t.TempDir())
	previous := DatasetState{
		DatasetRootSHA256: strings.Repeat("a", 64), ManifestSHA256: strings.Repeat("b", 64),
		NativeOrder: 42, NativeHash: "0xnative", EVMNumber: 41, EVMHash: "0xevm", EVMRoot: "0xroot",
	}
	if err := tracker.WaitBlockApplyState("0xnext", previous.NativeHash, previous); err != nil {
		t.Fatal(err)
	}
	marker, pending, err := tracker.Pending()
	if err != nil || !pending {
		t.Fatalf("pending = %t err=%v", pending, err)
	}
	if marker.DatasetRootSHA256 != previous.DatasetRootSHA256 || marker.ManifestSHA256 != previous.ManifestSHA256 ||
		marker.PreNativeOrder != previous.NativeOrder || marker.PreNativeHash != previous.NativeHash ||
		marker.PreEVMNumber != previous.EVMNumber || marker.PreEVMHash != previous.EVMHash || marker.PreEVMRoot != previous.EVMRoot {
		t.Fatalf("marker did not bind pre-operation state: %+v", marker)
	}
}

func TestTrackerCheckAllowsStartupValidation(t *testing.T) {
	tracker := NewTracker(t.TempDir())
	if err := tracker.Wait("0xblock"); err != nil {
		t.Fatalf("Wait returned error: %v", err)
	}

	if err := tracker.Check(); err != nil {
		t.Fatalf("Check should allow startup validation, got %v", err)
	}
}

func TestTrackerPendingReadsLegacyMarker(t *testing.T) {
	dir := t.TempDir()
	tracker := NewTracker(dir)
	const legacyHash = "0xlegacy"
	if err := os.WriteFile(filepath.Join(dir, lockFileName), []byte(legacyHash), 0o644); err != nil {
		t.Fatalf("write legacy marker: %v", err)
	}

	marker, ok, err := tracker.Pending()
	if err != nil {
		t.Fatalf("Pending returned error: %v", err)
	}
	if !ok {
		t.Fatal("Pending did not report legacy marker")
	}
	if marker.Version != 0 {
		t.Fatalf("legacy version = %d, want 0", marker.Version)
	}
	if marker.BlockHash != legacyHash {
		t.Fatalf("legacy block hash = %q, want %q", marker.BlockHash, legacyHash)
	}
}

func TestMarkerClassificationBindsDatasetAndPreState(t *testing.T) {
	current := DatasetState{
		DatasetRootSHA256: strings.Repeat("a", 64), ManifestSHA256: strings.Repeat("b", 64),
		NativeOrder: 10, NativeHash: "0xnative", EVMNumber: 9, EVMHash: "0xevm", EVMRoot: "0xroot",
	}
	marker := &Marker{
		Version: 2, OperationType: "dataset-activate", OperationID: "operation",
		DatasetRootSHA256: current.DatasetRootSHA256, ManifestSHA256: current.ManifestSHA256,
		PreNativeOrder: 10, PreNativeHash: current.NativeHash,
		PreEVMNumber: 9, PreEVMHash: current.EVMHash, PreEVMRoot: current.EVMRoot,
	}
	if got := ClassifyMarker(marker, current); got != MarkerApplicable {
		t.Fatalf("classification = %s, want applicable", got)
	}
	marker.DatasetRootSHA256 = strings.Repeat("c", 64)
	if got := ClassifyMarker(marker, current); got != MarkerForeign {
		t.Fatalf("classification = %s, want foreign", got)
	}
	marker.DatasetRootSHA256 = current.DatasetRootSHA256
	marker.PreNativeHash = "0xother"
	if got := ClassifyMarker(marker, current); got != MarkerForeign {
		t.Fatalf("pre-state classification = %s, want foreign", got)
	}
}

func TestMarkerClassificationRequiresCurrentIdentityForBoundMarker(t *testing.T) {
	marker := &Marker{
		DatasetRootSHA256: strings.Repeat("a", 64),
		ManifestSHA256:    strings.Repeat("b", 64),
		PreNativeHash:     "0xnative",
	}
	current := DatasetState{NativeHash: "0xnative"}
	if got := ClassifyMarker(marker, current); got != MarkerForeign {
		t.Fatalf("classification = %q, want %q", got, MarkerForeign)
	}
}

func TestMarkerClassificationLegacyRequiresMatchingPreviousState(t *testing.T) {
	marker := &Marker{Version: 1, Phase: markerPhaseBlockApply, BlockHash: "0xnext", PreviousBestHash: "0xprev"}
	current := DatasetState{NativeHash: "0xprev"}
	if got := ClassifyMarker(marker, current); got != MarkerApplicable {
		t.Fatalf("matching legacy classification = %q, want %q", got, MarkerApplicable)
	}
	current.NativeHash = "0xother"
	if got := ClassifyMarker(marker, current); got != MarkerForeign {
		t.Fatalf("nonmatching legacy classification = %q, want %q", got, MarkerForeign)
	}
}

func TestTrackerQuarantineForeignPreservesMarkerAtomically(t *testing.T) {
	dir := t.TempDir()
	tracker := NewTracker(dir)
	marker := &Marker{Version: 2, Phase: markerPhaseBlockApply, OperationType: markerPhaseBlockApply,
		OperationID: "operation", BlockHash: "0xnext", PreNativeHash: "0xprev", StartedAt: "now"}
	data, err := json.Marshal(marker)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(tracker.filePath, data, 0o644); err != nil {
		t.Fatal(err)
	}
	pending, ok, err := tracker.Pending()
	if err != nil || !ok {
		t.Fatalf("pending=%t err=%v", ok, err)
	}
	if got := ClassifyMarker(pending, DatasetState{NativeHash: "0xother"}); got != MarkerForeign {
		t.Fatalf("classification = %q, want %q", got, MarkerForeign)
	}
	if err := tracker.QuarantineForeign(pending); err != nil {
		t.Fatalf("quarantine: %v", err)
	}
	if Exists(tracker.filePath) {
		t.Fatal("foreign marker remained at active path")
	}
	digest := sha256.Sum256(data)
	quarantined := filepath.Join(dir, lockFileName+".foreign-"+fmt.Sprintf("%x", digest[:8]))
	got, err := os.ReadFile(quarantined)
	if err != nil {
		t.Fatalf("read quarantined marker: %v", err)
	}
	if string(got) != string(data) {
		t.Fatal("quarantined marker bytes changed")
	}
}

func TestTrackerQuarantineForeignFailsWithoutOverwritingTarget(t *testing.T) {
	dir := t.TempDir()
	tracker := NewTracker(dir)
	marker := &Marker{Version: 2, Phase: markerPhaseBlockApply, OperationType: markerPhaseBlockApply,
		OperationID: "operation", BlockHash: "0xnext", PreNativeHash: "0xprev", StartedAt: "now"}
	data, err := json.Marshal(marker)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(tracker.filePath, data, 0o644); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(data)
	target := filepath.Join(dir, lockFileName+".foreign-"+fmt.Sprintf("%x", digest[:8]))
	if err := os.WriteFile(target, []byte("sentinel"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := tracker.QuarantineForeign(marker); err == nil {
		t.Fatal("quarantine unexpectedly overwrote existing target")
	}
	if !Exists(tracker.filePath) {
		t.Fatal("source marker was lost after failed quarantine")
	}
	got, err := os.ReadFile(target)
	if err != nil || string(got) != "sentinel" {
		t.Fatalf("target changed after failed quarantine: %q err=%v", got, err)
	}
}

func TestTrackerDoneExpectedPreservesChangedMarker(t *testing.T) {
	dir := t.TempDir()
	tracker := NewTracker(dir)
	first := &Marker{Version: 2, Phase: markerPhaseBlockApply, OperationType: markerPhaseBlockApply,
		OperationID: "first", BlockHash: "0xnext", PreNativeHash: "0xprev", StartedAt: "now"}
	second := *first
	second.OperationID = "second"
	firstData, err := json.Marshal(first)
	if err != nil {
		t.Fatal(err)
	}
	secondData, err := json.Marshal(&second)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(tracker.filePath, firstData, 0o644); err != nil {
		t.Fatal(err)
	}
	expected, pending, err := tracker.Pending()
	if err != nil || !pending {
		t.Fatalf("pending=%t err=%v", pending, err)
	}
	if err := os.WriteFile(tracker.filePath, secondData, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := tracker.DoneExpected(expected); err == nil {
		t.Fatal("completion unexpectedly consumed a changed marker")
	}
	if _, err := os.Stat(tracker.filePath); err != nil {
		t.Fatalf("changed marker was lost: %v", err)
	}
}

func TestTrackerReportsMalformedStructuredMarkerDistinctly(t *testing.T) {
	dir := t.TempDir()
	tracker := NewTracker(dir)
	if err := os.WriteFile(filepath.Join(dir, lockFileName), []byte(`{"version":2,"operation_type":"dataset-activate"`), 0o644); err != nil {
		t.Fatal(err)
	}
	_, _, err := tracker.Pending()
	var malformed *MalformedMarkerError
	if !errors.As(err, &malformed) {
		t.Fatalf("error = %T %v, want MalformedMarkerError", err, err)
	}
}

func TestTrackerDoneRemovesMarker(t *testing.T) {
	tracker := NewTracker(t.TempDir())
	if err := tracker.Wait("0xblock"); err != nil {
		t.Fatalf("Wait returned error: %v", err)
	}
	if err := tracker.Done(); err != nil {
		t.Fatalf("Done returned error: %v", err)
	}

	_, ok, err := tracker.Pending()
	if err != nil {
		t.Fatalf("Pending returned error: %v", err)
	}
	if ok {
		t.Fatal("marker still pending after Done")
	}
}

func TestTrackerRejectsOversizedMarker(t *testing.T) {
	dir := t.TempDir()
	tracker := NewTracker(dir)
	oversized := strings.Repeat("x", maxMarkerSize+1)
	if err := os.WriteFile(filepath.Join(dir, lockFileName), []byte(oversized), 0o644); err != nil {
		t.Fatalf("write oversized marker: %v", err)
	}

	_, _, err := tracker.Pending()
	if err == nil {
		t.Fatal("expected oversized marker error")
	}
	if !strings.Contains(err.Error(), "unclean block application marker is larger") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestTrackerRejectsSymlinkMarker(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(t.TempDir(), "foreign-marker")
	if err := os.WriteFile(target, []byte("0xforeign"), 0o600); err != nil {
		t.Fatal(err)
	}
	tracker := NewTracker(dir)
	if err := os.Symlink(target, tracker.filePath); err != nil {
		t.Fatal(err)
	}
	if _, _, err := tracker.Pending(); err == nil || !strings.Contains(err.Error(), "not a regular file") {
		t.Fatalf("symlink marker error = %v", err)
	}
}
