package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	coresnapshot "github.com/BlockdagNetworkLabs/bdag/core/snapshot"
	"github.com/ethereum/go-ethereum/core/rawdb"
)

func TestSnapshotConfigDefaultsToMountedSnapshotAndNetworkDataDir(t *testing.T) {
	ctx := newRunTestContext(t, nil)
	cfg, err := snapshotConfigFromContext(ctx, []string{"--datadir=/bdag/data"})
	if err != nil {
		t.Fatalf("snapshotConfigFromContext returned error: %v", err)
	}
	if cfg.Enabled {
		t.Fatal("snapshot should be disabled by default")
	}
	if cfg.OutputDir != defaultSnapshotOutputDir {
		t.Fatalf("OutputDir = %q, want %q", cfg.OutputDir, defaultSnapshotOutputDir)
	}
	if cfg.DataDir != "/bdag/data/mainnet" {
		t.Fatalf("DataDir = %q, want /bdag/data/mainnet", cfg.DataDir)
	}
	if cfg.Network != "mainnet" {
		t.Fatalf("Network = %q, want mainnet", cfg.Network)
	}
}

func TestSnapshotNetworkArgs(t *testing.T) {
	if got := snapshotNetworkArgs("mainnet"); len(got) != 0 {
		t.Fatalf("mainnet args = %v, want none", got)
	}
	if got := snapshotNetworkArgs("testnet"); !reflect.DeepEqual(got, []string{"--testnet"}) {
		t.Fatalf("testnet args = %v", got)
	}
}

func TestSnapshotEligibleRequiresCurrentNode(t *testing.T) {
	ready := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("ready"))
	}))
	defer ready.Close()

	worker := &NodeWorker{
		readyURL:       ready.URL,
		healthMinPeers: 1,
		isCurrentFn: func(context.Context) (bool, error) {
			return false, nil
		},
		peerCountFn: func(context.Context) (uint64, error) {
			return 1, nil
		},
	}

	ok, reason, peers, err := worker.snapshotEligible(context.Background(), snapshotConfig{})
	if err != nil {
		t.Fatalf("snapshotEligible returned error: %v", err)
	}
	if ok || reason != "not_current" || peers != 0 {
		t.Fatalf("eligibility = ok:%v reason:%q peers:%d, want not_current", ok, reason, peers)
	}
}

func TestPublishSnapshotArtifactsWritesLatestSet(t *testing.T) {
	outputDir := t.TempDir()
	runDir := filepath.Join(outputDir, ".tmp", "run")
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	archive := filepath.Join(runDir, "snapshot"+coresnapshot.ArchiveFileSuffix)
	makeTestArchive(t, archive, "0xabc", 42)

	cfg := snapshotConfig{
		OutputDir: outputDir,
		DataDir:   "/bdag/data/mainnet",
		Network:   "mainnet",
	}
	if err := publishSnapshotArtifacts(archive, outputDir, cfg, "/usr/local/bin/bdag"); err != nil {
		t.Fatalf("publishSnapshotArtifacts returned error: %v", err)
	}

	for _, name := range []string{
		"snapshot" + coresnapshot.ArchiveFileSuffix,
		"snapshot" + coresnapshot.ArchiveFileSuffix + coresnapshot.ManifestFileSuffix,
		latestSnapshotMetadata,
	} {
		if _, err := os.Stat(filepath.Join(outputDir, name)); err != nil {
			t.Fatalf("expected %s to exist: %v", name, err)
		}
	}

	raw, err := os.ReadFile(filepath.Join(outputDir, latestSnapshotMetadata))
	if err != nil {
		t.Fatalf("read latest: %v", err)
	}
	var latest snapshotLatestMetadata
	if err := json.Unmarshal(raw, &latest); err != nil {
		t.Fatalf("decode latest: %v", err)
	}
	if latest.Network != "mainnet" || latest.DataDir != "/bdag/data/mainnet" || latest.TipHash != "0xabc" || latest.TipOrder != 42 {
		t.Fatalf("unexpected latest metadata: %#v", latest)
	}
	if latest.ArchiveSHA256 == "" || latest.ManifestSHA256 == "" {
		t.Fatalf("expected hashes in latest metadata: %#v", latest)
	}
}

func TestRunSnapshotOnceRestartsAndPublishes(t *testing.T) {
	ready := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("ready"))
	}))
	defer ready.Close()

	sourceDir := t.TempDir()
	sourceArchive := filepath.Join(sourceDir, "source"+coresnapshot.ArchiveFileSuffix)
	makeTestArchive(t, sourceArchive, "0xdef", 99)

	var commands []string
	worker := &NodeWorker{
		binary:         "/bin/sleep",
		args:           []string{"60"},
		exitCh:         make(chan error, 1),
		readyURL:       ready.URL,
		healthMinPeers: 1,
		isCurrentFn: func(context.Context) (bool, error) {
			return true, nil
		},
		peerCountFn: func(context.Context) (uint64, error) {
			return 2, nil
		},
		snapshotCommandFn: func(ctx context.Context, binary string, args ...string) error {
			commands = append(commands, strings.Join(args, " "))
			if len(args) >= 2 && args[0] == "snap" && args[1] == "export" {
				out := argValue(args, "--path")
				if out == "" {
					return errors.New("missing export path")
				}
				if err := copyTestFile(sourceArchive, out); err != nil {
					return err
				}
				return copyTestFile(sourceArchive+coresnapshot.ManifestFileSuffix, out+coresnapshot.ManifestFileSuffix)
			}
			if len(args) >= 2 && args[0] == "snap" && args[1] == "verify" {
				return nil
			}
			return fmt.Errorf("unexpected command: %v", args)
		},
	}
	if err := worker.Start(); err != nil {
		t.Fatalf("start worker child: %v", err)
	}
	t.Cleanup(func() {
		_ = worker.Stop()
	})

	outputDir := t.TempDir()
	cfg := snapshotConfig{
		Enabled:   true,
		Interval:  time.Hour,
		OutputDir: outputDir,
		DataDir:   "/bdag/data/mainnet",
		Network:   "mainnet",
	}
	if err := worker.runSnapshotOnce(context.Background(), cfg); err != nil {
		t.Fatalf("runSnapshotOnce returned error: %v", err)
	}
	if worker.cmd == nil || worker.cmd.Process == nil {
		t.Fatal("expected child process to be restarted")
	}
	if len(commands) != 2 {
		t.Fatalf("commands = %v, want export and verify", commands)
	}
	if !strings.Contains(commands[0], "--datadir /bdag/data/mainnet") {
		t.Fatalf("export command missing exact datadir: %q", commands[0])
	}
	raw, err := os.ReadFile(filepath.Join(outputDir, latestSnapshotMetadata))
	if err != nil {
		t.Fatalf("read latest: %v", err)
	}
	if !bytes.Contains(raw, []byte(`"tip_hash": "0xdef"`)) {
		t.Fatalf("latest metadata did not contain exported tip: %s", string(raw))
	}
}

func TestRunSnapshotOnceFailureKeepsPreviousSnapshotAndRestarts(t *testing.T) {
	ready := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("ready"))
	}))
	defer ready.Close()

	worker := &NodeWorker{
		binary:         "/bin/sleep",
		args:           []string{"60"},
		exitCh:         make(chan error, 1),
		readyURL:       ready.URL,
		healthMinPeers: 1,
		isCurrentFn: func(context.Context) (bool, error) {
			return true, nil
		},
		peerCountFn: func(context.Context) (uint64, error) {
			return 2, nil
		},
		snapshotCommandFn: func(context.Context, string, ...string) error {
			return errors.New("export failed")
		},
	}
	if err := worker.Start(); err != nil {
		t.Fatalf("start worker child: %v", err)
	}
	t.Cleanup(func() {
		_ = worker.Stop()
	})

	outputDir := t.TempDir()
	finalArchive := filepath.Join(outputDir, "snapshot"+coresnapshot.ArchiveFileSuffix)
	if err := os.WriteFile(finalArchive, []byte("old archive"), 0o644); err != nil {
		t.Fatalf("write old archive: %v", err)
	}
	err := worker.runSnapshotOnce(context.Background(), snapshotConfig{
		Enabled:   true,
		Interval:  time.Hour,
		OutputDir: outputDir,
		DataDir:   "/bdag/data/mainnet",
		Network:   "mainnet",
	})
	if err == nil || !strings.Contains(err.Error(), "export failed") {
		t.Fatalf("expected export failure, got %v", err)
	}
	if worker.cmd == nil || worker.cmd.Process == nil {
		t.Fatal("expected child process to be restarted after failed export")
	}
	raw, err := os.ReadFile(finalArchive)
	if err != nil {
		t.Fatalf("read final archive: %v", err)
	}
	if string(raw) != "old archive" {
		t.Fatalf("final archive changed on failed export: %q", string(raw))
	}
}

func makeTestArchive(t *testing.T, archive, tipHash string, tipOrder uint64) {
	t.Helper()
	db := rawdb.NewMemoryDatabase()
	t.Cleanup(func() {
		db.Close()
	})
	if err := db.Put([]byte("key"), []byte("value")); err != nil {
		t.Fatalf("put memory db: %v", err)
	}
	manifest, err := coresnapshot.Write(archive, db, coresnapshot.BuildInfo{
		Network:     "mainnet",
		BDAGVersion: "test-version",
		DBEngine:    "memory",
		TipHash:     tipHash,
		TipOrder:    tipOrder,
		TipID:       tipOrder + 1,
		BlockTotal:  tipOrder + 2,
		StateRoot:   "0xstate",
	}, coresnapshot.WriteOptions{ChunkTargetBytes: 1024})
	if err != nil {
		t.Fatalf("write test archive: %v", err)
	}
	if err := coresnapshot.WriteManifestFile(archive+coresnapshot.ManifestFileSuffix, manifest); err != nil {
		t.Fatalf("write test manifest: %v", err)
	}
}

func copyTestFile(src, dst string) error {
	raw, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, raw, 0o644)
}

func argValue(args []string, name string) string {
	for i, arg := range args {
		if arg == name && i+1 < len(args) {
			return args[i+1]
		}
	}
	return ""
}
