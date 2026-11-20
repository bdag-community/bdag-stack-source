package shutdown

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/BlockdagNetworkLabs/bdag/version"
)

const lockFileName = "shutdown.lock"
const markerVersion = 2
const markerPhaseBlockApply = "block-apply"
const maxMarkerSize = 512 * 1024

type Tracker struct {
	filePath        string
	datasetIdentity DatasetState
	mu              sync.Mutex
}

var (
	nodeBinaryHashOnce sync.Once
	nodeBinaryHash     string
	nodeBinaryHashErr  error
)

type Marker struct {
	Version           int    `json:"version"`
	Phase             string `json:"phase"`
	OperationType     string `json:"operation_type,omitempty"`
	OperationID       string `json:"operation_id,omitempty"`
	DatasetRootSHA256 string `json:"dataset_root_sha256,omitempty"`
	ManifestSHA256    string `json:"manifest_sha256,omitempty"`
	BlockHash         string `json:"block_hash"`
	PreviousBestHash  string `json:"previous_best_hash,omitempty"`
	PreNativeOrder    uint64 `json:"pre_native_order,omitempty"`
	PreNativeHash     string `json:"pre_native_hash,omitempty"`
	PreEVMNumber      uint64 `json:"pre_evm_number,omitempty"`
	PreEVMHash        string `json:"pre_evm_hash,omitempty"`
	PreEVMRoot        string `json:"pre_evm_root,omitempty"`
	NodeBinarySHA256  string `json:"node_binary_sha256,omitempty"`
	NodeVersion       string `json:"node_version,omitempty"`
	StartedAt         string `json:"started_at"`
}

type DatasetState struct {
	DatasetRootSHA256 string
	ManifestSHA256    string
	NativeOrder       uint64
	NativeHash        string
	EVMNumber         uint64
	EVMHash           string
	EVMRoot           string
}

type MarkerClassification string

const (
	MarkerApplicable MarkerClassification = "applicable_interrupted_operation"
	MarkerForeign    MarkerClassification = "foreign_marker"
)

type MalformedMarkerError struct {
	Path string
	Err  error
}

func (e *MalformedMarkerError) Error() string {
	return fmt.Sprintf("malformed recovery marker %s: %v", e.Path, e.Err)
}

func (e *MalformedMarkerError) Unwrap() error { return e.Err }

func (m *Marker) Description() string {
	if m == nil {
		return "none"
	}
	parts := make([]string, 0, 3)
	if m.Phase != "" {
		parts = append(parts, "phase="+m.Phase)
	}
	if m.OperationID != "" {
		parts = append(parts, "operation_id="+m.OperationID)
	}
	if m.BlockHash != "" {
		parts = append(parts, "block="+m.BlockHash)
	}
	if m.PreviousBestHash != "" {
		parts = append(parts, "previous_best="+m.PreviousBestHash)
	}
	if m.StartedAt != "" {
		parts = append(parts, "started_at="+m.StartedAt)
	}
	if len(parts) == 0 {
		return "empty marker"
	}
	return strings.Join(parts, " ")
}

func (t *Tracker) Check() error {
	marker, pending, err := t.Pending()
	if err != nil {
		return err
	}
	if pending {
		log.Warn("Unclean block application marker found; startup will validate the chain database before clearing it", "marker", marker.Description(), "path", t.filePath)
	}
	return nil
}

func (t *Tracker) Wait(info string) error {
	return t.WaitBlockApply(info, "")
}

func (t *Tracker) WaitBlockApply(blockHash string, previousBestHash string) error {
	return t.WaitBlockApplyState(blockHash, previousBestHash, DatasetState{NativeHash: previousBestHash})
}

func (t *Tracker) WaitBlockApplyState(blockHash string, previousBestHash string, previous DatasetState) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	var err error
	previous, err = t.bindDatasetIdentity(previous)
	if err != nil {
		return err
	}
	var operationID [16]byte
	if _, err := rand.Read(operationID[:]); err != nil {
		return fmt.Errorf("create block-apply operation id: %w", err)
	}
	binaryHash, err := currentNodeBinarySHA256()
	if err != nil {
		return fmt.Errorf("identify block-apply node binary: %w", err)
	}
	if strings.TrimSpace(previous.NativeHash) == "" {
		previous.NativeHash = strings.TrimSpace(previousBestHash)
	}
	nodeVersion := strings.TrimSpace(version.String())
	if nodeVersion == "" {
		nodeVersion = "development"
	}
	marker := Marker{
		Version:           markerVersion,
		Phase:             markerPhaseBlockApply,
		OperationType:     markerPhaseBlockApply,
		OperationID:       fmt.Sprintf("%x", operationID[:]),
		DatasetRootSHA256: strings.TrimSpace(previous.DatasetRootSHA256),
		ManifestSHA256:    strings.TrimSpace(previous.ManifestSHA256),
		BlockHash:         strings.TrimSpace(blockHash),
		PreviousBestHash:  strings.TrimSpace(previousBestHash),
		PreNativeOrder:    previous.NativeOrder,
		PreNativeHash:     strings.TrimSpace(previous.NativeHash),
		PreEVMNumber:      previous.EVMNumber,
		PreEVMHash:        strings.TrimSpace(previous.EVMHash),
		PreEVMRoot:        strings.TrimSpace(previous.EVMRoot),
		NodeBinarySHA256:  binaryHash,
		NodeVersion:       nodeVersion,
		StartedAt:         time.Now().UTC().Format(time.RFC3339Nano),
	}
	data, err := json.MarshalIndent(marker, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')

	dir := filepath.Dir(t.filePath)
	tmpFile, err := os.CreateTemp(dir, "."+lockFileName+".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmpFile.Name()
	cleanupTmp := true
	defer func() {
		if cleanupTmp {
			_ = os.Remove(tmpName)
		}
	}()
	if _, err = tmpFile.Write(data); err != nil {
		_ = tmpFile.Close()
		return err
	}
	if err = tmpFile.Chmod(0o644); err != nil {
		_ = tmpFile.Close()
		return err
	}
	if err = tmpFile.Sync(); err != nil {
		_ = tmpFile.Close()
		return err
	}
	if err = tmpFile.Close(); err != nil {
		return err
	}
	if err = os.Rename(tmpName, t.filePath); err != nil {
		return err
	}
	cleanupTmp = false
	return syncDir(dir)
}

func currentNodeBinarySHA256() (string, error) {
	nodeBinaryHashOnce.Do(func() {
		executable, err := os.Executable()
		if err != nil {
			nodeBinaryHashErr = err
			return
		}
		file, err := os.Open(executable)
		if err != nil {
			nodeBinaryHashErr = err
			return
		}
		defer file.Close()
		info, err := file.Stat()
		if err != nil || !info.Mode().IsRegular() {
			nodeBinaryHashErr = fmt.Errorf("node executable is not a regular file")
			return
		}
		hash := sha256.New()
		if _, err := io.Copy(hash, file); err != nil {
			nodeBinaryHashErr = err
			return
		}
		nodeBinaryHash = hex.EncodeToString(hash.Sum(nil))
	})
	return nodeBinaryHash, nodeBinaryHashErr
}

func (t *Tracker) Done() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	err := os.Remove(t.filePath)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	return syncDir(filepath.Dir(t.filePath))
}

// DoneExpected removes the marker only when it is still the marker that was
// classified as applicable. This prevents a newer operation from being
// consumed after a startup race.
func (t *Tracker) DoneExpected(expected *Marker) error {
	if expected == nil {
		return fmt.Errorf("cannot complete nil recovery marker")
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	data, err := ReadFile(t.filePath)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	actual, err := parseMarker(data)
	if err != nil {
		return &MalformedMarkerError{Path: t.filePath, Err: err}
	}
	if !markersEqual(expected, actual) {
		return fmt.Errorf("recovery marker changed before completion: %s", t.filePath)
	}
	if err := os.Remove(t.filePath); err != nil {
		return err
	}
	return syncDir(filepath.Dir(t.filePath))
}

func (t *Tracker) Pending() (*Marker, bool, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	bhbs, err := ReadFile(t.filePath)
	if os.IsNotExist(err) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	if len(bhbs) > maxMarkerSize {
		return nil, false, fmt.Errorf("unclean block application marker is larger than %d bytes: %s", maxMarkerSize, t.filePath)
	}
	if len(bhbs) == 0 {
		return &Marker{Phase: markerPhaseBlockApply}, true, nil
	}
	marker, err := parseMarker(bhbs)
	if err != nil {
		return nil, false, &MalformedMarkerError{Path: t.filePath, Err: err}
	}
	return marker, true, nil
}

// DatasetIdentity returns the immutable identity bound when this tracker was
// opened. Native and EVM state is intentionally supplied by the caller.
func (t *Tracker) DatasetIdentity() DatasetState {
	t.mu.Lock()
	defer t.mu.Unlock()
	return DatasetState{
		DatasetRootSHA256: t.datasetIdentity.DatasetRootSHA256,
		ManifestSHA256:    t.datasetIdentity.ManifestSHA256,
	}
}

// QuarantineForeign atomically renames a classified foreign marker to a
// content-addressed sibling, preserving its bytes without consuming it as an
// applicable recovery operation. A changed marker fails closed.
func (t *Tracker) QuarantineForeign(expected *Marker) error {
	if expected == nil {
		return fmt.Errorf("cannot quarantine nil recovery marker")
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	data, err := ReadFile(t.filePath)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	actual, err := parseMarker(data)
	if err != nil {
		return &MalformedMarkerError{Path: t.filePath, Err: err}
	}
	if !markersEqual(expected, actual) {
		return fmt.Errorf("recovery marker changed before quarantine: %s", t.filePath)
	}
	digest := sha256.Sum256(data)
	target := fmt.Sprintf("%s.foreign-%x", t.filePath, digest[:8])
	if _, err := os.Lstat(target); err == nil {
		return fmt.Errorf("foreign recovery marker quarantine already exists: %s", target)
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := os.Rename(t.filePath, target); err != nil {
		return err
	}
	return syncDir(filepath.Dir(t.filePath))
}

func markersEqual(left, right *Marker) bool {
	if left == nil || right == nil {
		return left == right
	}
	return *left == *right
}

func parseMarker(data []byte) (*Marker, error) {
	trimmed := strings.TrimSpace(string(data))
	if trimmed == "" {
		return &Marker{Phase: markerPhaseBlockApply}, nil
	}
	var marker Marker
	decoder := json.NewDecoder(bytes.NewReader([]byte(trimmed)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&marker); err == nil {
		var trailing any
		if err := decoder.Decode(&trailing); err != io.EOF {
			return nil, fmt.Errorf("marker contains trailing JSON")
		}
		if marker.Version < 1 || marker.Version > markerVersion {
			return nil, fmt.Errorf("unsupported marker version %d", marker.Version)
		}
		if marker.Phase == "" {
			marker.Phase = markerPhaseBlockApply
		}
		if marker.Version >= 2 && (marker.OperationType == "" || marker.OperationID == "" || marker.StartedAt == "") {
			return nil, fmt.Errorf("version 2 marker lacks operation type, id, or start time")
		}
		return &marker, nil
	} else if strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "[") {
		return nil, fmt.Errorf("invalid marker JSON")
	}
	return &Marker{
		Version:   0,
		Phase:     markerPhaseBlockApply,
		BlockHash: trimmed,
	}, nil
}

// ClassifyMarker decides whether a copied marker can describe the exact
// dataset state being inspected. Provenance and numeric ownership do not enter
// the decision. Legacy markers are applicable only when their recorded
// previous native state matches the opened state.
func ClassifyMarker(marker *Marker, current DatasetState) MarkerClassification {
	if marker == nil {
		return MarkerForeign
	}
	if marker.DatasetRootSHA256 != "" && !equalFoldTrim(marker.DatasetRootSHA256, current.DatasetRootSHA256) {
		return MarkerForeign
	}
	if marker.ManifestSHA256 != "" && !equalFoldTrim(marker.ManifestSHA256, current.ManifestSHA256) {
		return MarkerForeign
	}
	blockApplied := marker.BlockHash != "" && equalFoldTrim(marker.BlockHash, current.NativeHash)
	if marker.Version < 2 && (marker.PreviousBestHash == "" || !equalFoldTrim(marker.PreviousBestHash, current.NativeHash)) {
		return MarkerForeign
	}
	if marker.PreNativeHash != "" {
		if !equalFoldTrim(marker.PreNativeHash, current.NativeHash) {
			// A block-apply marker also applies when its exact block became the
			// persisted tip before the crash and only finalization remains.
			if marker.Version < 2 || !blockApplied {
				return MarkerForeign
			}
		}
		if marker.PreNativeOrder != 0 && marker.PreNativeOrder != current.NativeOrder && (marker.Version < 2 || !blockApplied) {
			return MarkerForeign
		}
	}
	if marker.PreEVMHash != "" || marker.PreEVMRoot != "" || marker.PreEVMNumber != 0 {
		if (marker.Version < 2 || !blockApplied) && (marker.PreEVMNumber != current.EVMNumber || !equalFoldTrim(marker.PreEVMHash, current.EVMHash) || !equalFoldTrim(marker.PreEVMRoot, current.EVMRoot)) {
			return MarkerForeign
		}
	}
	if marker.Version >= 2 && marker.PreNativeHash == "" && !blockApplied {
		return MarkerForeign
	}
	if marker.PreNativeHash == "" && marker.PreviousBestHash != "" &&
		!equalFoldTrim(marker.PreviousBestHash, current.NativeHash) && !equalFoldTrim(marker.BlockHash, current.NativeHash) {
		return MarkerForeign
	}
	return MarkerApplicable
}

func equalFoldTrim(left, right string) bool {
	return strings.EqualFold(strings.TrimSpace(left), strings.TrimSpace(right))
}

func NewTracker(datadir string) *Tracker {
	return &Tracker{filePath: filepath.Join(datadir, lockFileName)}
}

// NewTrackerWithDatasetState binds new operation markers to the accepted
// manifest and directory-root identity for this database. The changing native
// and EVM boundary is supplied for each operation separately.
func NewTrackerWithDatasetState(datadir string, identity DatasetState) *Tracker {
	return &Tracker{
		filePath: filepath.Join(datadir, lockFileName),
		datasetIdentity: DatasetState{
			DatasetRootSHA256: strings.TrimSpace(identity.DatasetRootSHA256),
			ManifestSHA256:    strings.TrimSpace(identity.ManifestSHA256),
		},
	}
}

func (t *Tracker) bindDatasetIdentity(previous DatasetState) (DatasetState, error) {
	for _, item := range []struct {
		name     string
		bound    string
		supplied *string
	}{
		{name: "dataset root", bound: t.datasetIdentity.DatasetRootSHA256, supplied: &previous.DatasetRootSHA256},
		{name: "manifest", bound: t.datasetIdentity.ManifestSHA256, supplied: &previous.ManifestSHA256},
	} {
		if item.bound == "" {
			continue
		}
		if *item.supplied != "" && !equalFoldTrim(*item.supplied, item.bound) {
			return DatasetState{}, fmt.Errorf("block-apply %s identity differs from the opened dataset", item.name)
		}
		*item.supplied = item.bound
	}
	return previous, nil
}

func Exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func ReadFile(path string) ([]byte, error) {
	before, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !before.Mode().IsRegular() || before.Mode()&os.ModeSymlink != 0 || before.Sys() == nil {
		return nil, fmt.Errorf("recovery marker is not a regular file: %s", path)
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !sameMarkerFile(before, opened) {
		return nil, fmt.Errorf("recovery marker changed while opening: %s", path)
	}
	data, err := io.ReadAll(io.LimitReader(file, maxMarkerSize+1))
	if err != nil {
		return nil, err
	}
	after, err := os.Lstat(path)
	if err != nil || !sameMarkerFile(opened, after) || int64(len(data)) != opened.Size() {
		return nil, fmt.Errorf("recovery marker changed while reading: %s", path)
	}
	return data, nil
}

func sameMarkerFile(left, right os.FileInfo) bool {
	return left != nil && right != nil && os.SameFile(left, right) && left.Mode() == right.Mode() && left.Size() == right.Size() && left.ModTime().Equal(right.ModTime())
}

func syncDir(path string) error {
	dir, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() {
		if cerr := dir.Close(); cerr != nil {
			log.Error(fmt.Sprintf("Error closing directory: %v", cerr))
		}
	}()
	return dir.Sync()
}
