package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	coresnapshot "github.com/BlockdagNetworkLabs/bdag/core/snapshot"
)

func TestControlSocketPathDefaultsFromPersistRoot(t *testing.T) {
	if got, want := controlSocketPath("/opt/bdag", ""), "/opt/bdag/control.sock"; got != want {
		t.Fatalf("controlSocketPath = %q, want %q", got, want)
	}
	if got, want := controlSocketPath("/opt/bdag", "/tmp/custom.sock"), "/tmp/custom.sock"; got != want {
		t.Fatalf("controlSocketPath with explicit override = %q, want %q", got, want)
	}
}

func readyTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("ready"))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func notReadyTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestRunOnDemandSnapshotExportStopsExportsVerifiesAndRestarts(t *testing.T) {
	ready := readyTestServer(t)

	sourceDir := t.TempDir()
	sourceArchive := filepath.Join(sourceDir, "source"+coresnapshot.ArchiveFileSuffix)
	makeTestArchive(t, sourceArchive, "0xondemand", 7)

	var commands []string
	worker := &NodeWorker{
		binary:         "/bin/sleep",
		args:           []string{"60"},
		exitCh:         make(chan error, 1),
		readyURL:       ready.URL,
		healthMinPeers: 1,
		snapshotCfg: snapshotConfig{
			DataDir: "/bdag/data/mainnet",
			Network: "mainnet",
		},
		snapshotCommandFn: func(ctx context.Context, binary string, args ...string) error {
			commands = append(commands, strings.Join(args, " "))
			switch {
			case len(args) >= 2 && args[0] == "snap" && args[1] == "export":
				out := argValue(args, "--path")
				if out == "" {
					return errors.New("missing export path")
				}
				if err := copyTestFile(sourceArchive, out); err != nil {
					return err
				}
				return copyTestFile(sourceArchive+coresnapshot.ManifestFileSuffix, out+coresnapshot.ManifestFileSuffix)
			case len(args) >= 2 && args[0] == "snap" && args[1] == "verify":
				return nil
			default:
				return fmt.Errorf("unexpected command: %v", args)
			}
		},
	}
	if err := worker.Start(); err != nil {
		t.Fatalf("start worker child: %v", err)
	}
	t.Cleanup(func() { _ = worker.Stop() })

	outPath := filepath.Join(t.TempDir(), "ondemand.bdsnap")
	resp := worker.runOnDemandSnapshot(context.Background(), controlRequest{
		Action: "export",
		Path:   outPath,
	}, nil)
	if !resp.OK {
		t.Fatalf("export response not ok: %+v", resp)
	}
	if resp.TipHash != "0xondemand" {
		t.Fatalf("resp.TipHash = %q, want 0xondemand", resp.TipHash)
	}
	if len(commands) != 2 {
		t.Fatalf("commands = %v, want export then verify", commands)
	}
	if !strings.Contains(commands[0], "--datadir /bdag/data/mainnet") {
		t.Fatalf("export command missing datadir: %q", commands[0])
	}
	if worker.cmd == nil || worker.cmd.Process == nil {
		t.Fatal("expected child process to be restarted after on-demand export")
	}
	if atomic.LoadUint32(&worker.upgrading) != 0 {
		t.Fatal("expected upgrading flag to be cleared after on-demand export")
	}
}

func TestRunOnDemandSnapshotImportShellsCorrectArgs(t *testing.T) {
	ready := readyTestServer(t)

	var commands []string
	worker := &NodeWorker{
		binary:         "/bin/sleep",
		args:           []string{"60"},
		exitCh:         make(chan error, 1),
		readyURL:       ready.URL,
		healthMinPeers: 1,
		snapshotCfg: snapshotConfig{
			DataDir: "/bdag/data/testnet",
			Network: "testnet",
		},
		snapshotCommandFn: func(ctx context.Context, binary string, args ...string) error {
			commands = append(commands, strings.Join(args, " "))
			return nil
		},
	}
	if err := worker.Start(); err != nil {
		t.Fatalf("start worker child: %v", err)
	}
	t.Cleanup(func() { _ = worker.Stop() })

	inPath := filepath.Join(t.TempDir(), "in.bdsnap")
	resp := worker.runOnDemandSnapshot(context.Background(), controlRequest{
		Action: "import",
		Path:   inPath,
	}, nil)
	if !resp.OK {
		t.Fatalf("import response not ok: %+v", resp)
	}
	if len(commands) != 1 {
		t.Fatalf("commands = %v, want a single import command", commands)
	}
	want := fmt.Sprintf("snap import --datadir /bdag/data/testnet --path %s --testnet", inPath)
	if commands[0] != want {
		t.Fatalf("import command = %q, want %q", commands[0], want)
	}
	if worker.cmd == nil || worker.cmd.Process == nil {
		t.Fatal("expected child process to be restarted after on-demand import")
	}
}

func TestRunOnDemandSnapshotRejectsUnknownActionOrMissingPath(t *testing.T) {
	worker := &NodeWorker{}

	resp := worker.runOnDemandSnapshot(context.Background(), controlRequest{Action: "wipe", Path: "/tmp/x"}, nil)
	if resp.OK || !strings.Contains(resp.Error, "unknown action") {
		t.Fatalf("expected unknown-action error, got %+v", resp)
	}

	resp = worker.runOnDemandSnapshot(context.Background(), controlRequest{Action: "export"}, nil)
	if resp.OK || !strings.Contains(resp.Error, "path is required") {
		t.Fatalf("expected missing-path error, got %+v", resp)
	}
}

func TestRunOnDemandSnapshotBusyWhenLifecycleOperationInProgress(t *testing.T) {
	worker := &NodeWorker{}
	atomic.StoreUint32(&worker.upgrading, 1)
	t.Cleanup(func() { atomic.StoreUint32(&worker.upgrading, 0) })

	resp := worker.runOnDemandSnapshot(context.Background(), controlRequest{Action: "export", Path: "/tmp/x.bdsnap"}, nil)
	if resp.OK || !strings.Contains(resp.Error, "busy") {
		t.Fatalf("expected busy error while another lifecycle op is in progress, got %+v", resp)
	}
	if worker.cmd != nil {
		t.Fatal("busy request should not have touched the (unstarted) child process")
	}
}

// TestRunOnDemandSnapshotExportRefusesWhenNodeNotReady guards against a real
// incident: an on-demand export triggered while the node was still mid-
// startup (rebuilding upgrade state from headers) didn't respond to Stop()'s
// SIGTERM in time, so nodeworker force-killed the process group -- an
// unclean kill that corrupted enough EVM state to make the next startup's
// repair-rewind refuse to proceed. Export must now refuse outright, before
// ever calling Stop(), when the readiness probe fails.
func TestRunOnDemandSnapshotExportRefusesWhenNodeNotReady(t *testing.T) {
	notReady := notReadyTestServer(t)

	worker := &NodeWorker{
		binary:   "/bin/sleep",
		args:     []string{"60"},
		exitCh:   make(chan error, 1),
		readyURL: notReady.URL + "/readyz",
	}

	resp := worker.runOnDemandSnapshot(context.Background(), controlRequest{Action: "export", Path: "/tmp/x.bdsnap"}, nil)
	if resp.OK || !strings.Contains(resp.Error, "not ready yet") {
		t.Fatalf("expected a not-ready refusal, got %+v", resp)
	}
	if worker.cmd != nil {
		t.Fatal("refused export should never have touched the child process (no Stop() call)")
	}
	if atomic.LoadUint32(&worker.upgrading) != 0 {
		t.Fatal("expected upgrading flag to be untouched by a pre-lock refusal")
	}
}

// TestRunOnDemandSnapshotImportBypassesReadinessGate confirms import is
// deliberately exempt from the new export-only gate above -- it's normally
// run against a fresh or stalled node specifically because the destination
// chain DB must be empty, a state in which readiness would never pass.
func TestRunOnDemandSnapshotImportBypassesReadinessGate(t *testing.T) {
	notReady := notReadyTestServer(t)

	var commands []string
	worker := &NodeWorker{
		binary:   "/bin/sleep",
		args:     []string{"60"},
		exitCh:   make(chan error, 1),
		readyURL: notReady.URL + "/readyz",
		// The post-restart wait-for-readiness step (unrelated to the gate
		// under test) would otherwise retry against notReady indefinitely
		// -- bound it so this test can't hang.
		startupTimeout: 200 * time.Millisecond,
		snapshotCommandFn: func(ctx context.Context, binary string, args ...string) error {
			commands = append(commands, strings.Join(args, " "))
			return nil
		},
	}
	if err := worker.Start(); err != nil {
		t.Fatalf("start worker child: %v", err)
	}
	t.Cleanup(func() { _ = worker.Stop() })

	// The overall response ends up not-OK because the post-restart
	// readiness wait fails against notReady -- that's expected and not
	// what this test is checking. What matters is that the import command
	// itself ran at all, proving the pre-Stop() readiness gate (export-only)
	// didn't block it.
	worker.runOnDemandSnapshot(context.Background(), controlRequest{Action: "import", Path: "/tmp/in.bdsnap"}, nil)
	if len(commands) != 1 {
		t.Fatalf("commands = %v, want a single import command (import must not be blocked by the export-only readiness gate)", commands)
	}
}

func TestRunOnDemandStopStopsChildAndDoesNotRestart(t *testing.T) {
	ready := readyTestServer(t)

	worker := &NodeWorker{
		binary:         "/bin/sleep",
		args:           []string{"60"},
		exitCh:         make(chan error, 1),
		readyURL:       ready.URL,
		healthMinPeers: 1,
	}
	if err := worker.Start(); err != nil {
		t.Fatalf("start worker child: %v", err)
	}
	t.Cleanup(func() { _ = worker.Stop() })

	resp := worker.runOnDemandStop()
	if !resp.OK {
		t.Fatalf("stop response not ok: %+v", resp)
	}
	if worker.cmd != nil {
		t.Fatal("expected child process to be stopped (worker.cmd nil), not restarted")
	}
	if atomic.LoadUint32(&worker.upgrading) != 0 {
		t.Fatal("expected upgrading flag to be cleared after on-demand stop")
	}
}

func TestRunOnDemandStopBusyWhenLifecycleOperationInProgress(t *testing.T) {
	worker := &NodeWorker{}
	atomic.StoreUint32(&worker.upgrading, 1)
	t.Cleanup(func() { atomic.StoreUint32(&worker.upgrading, 0) })

	resp := worker.runOnDemandStop()
	if resp.OK || !strings.Contains(resp.Error, "busy") {
		t.Fatalf("expected busy error while another lifecycle op is in progress, got %+v", resp)
	}
}

func TestControlListenerRoundTripStop(t *testing.T) {
	ready := readyTestServer(t)

	worker := &NodeWorker{
		binary:         "/bin/sleep",
		args:           []string{"60"},
		exitCh:         make(chan error, 1),
		readyURL:       ready.URL,
		healthMinPeers: 1,
	}
	if err := worker.Start(); err != nil {
		t.Fatalf("start worker child: %v", err)
	}
	t.Cleanup(func() { _ = worker.Stop() })

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	socketPath := filepath.Join(t.TempDir(), "control.sock")
	if err := worker.startControlListener(ctx, socketPath); err != nil {
		t.Fatalf("startControlListener: %v", err)
	}

	reqCtx, reqCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer reqCancel()
	resp, err := sendControlRequest(reqCtx, socketPath, controlRequest{Action: "stop"}, nil)
	if err != nil {
		t.Fatalf("sendControlRequest: %v", err)
	}
	if !resp.OK {
		t.Fatalf("response not ok: %+v", resp)
	}
	if worker.cmd != nil {
		t.Fatal("expected child process to be stopped via the control socket")
	}
}

func TestControlListenerRoundTrip(t *testing.T) {
	ready := readyTestServer(t)

	sourceDir := t.TempDir()
	sourceArchive := filepath.Join(sourceDir, "source"+coresnapshot.ArchiveFileSuffix)
	makeTestArchive(t, sourceArchive, "0xsocket", 3)

	worker := &NodeWorker{
		binary:         "/bin/sleep",
		args:           []string{"60"},
		exitCh:         make(chan error, 1),
		readyURL:       ready.URL,
		healthMinPeers: 1,
		snapshotCfg: snapshotConfig{
			DataDir: "/bdag/data/mainnet",
			Network: "mainnet",
		},
		snapshotCommandFn: func(ctx context.Context, binary string, args ...string) error {
			if len(args) >= 2 && args[0] == "snap" && args[1] == "export" {
				out := argValue(args, "--path")
				if err := copyTestFile(sourceArchive, out); err != nil {
					return err
				}
				return copyTestFile(sourceArchive+coresnapshot.ManifestFileSuffix, out+coresnapshot.ManifestFileSuffix)
			}
			return nil
		},
	}
	if err := worker.Start(); err != nil {
		t.Fatalf("start worker child: %v", err)
	}
	t.Cleanup(func() { _ = worker.Stop() })

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	socketPath := filepath.Join(t.TempDir(), "control.sock")
	if err := worker.startControlListener(ctx, socketPath); err != nil {
		t.Fatalf("startControlListener: %v", err)
	}

	if info, err := os.Stat(socketPath); err != nil {
		t.Fatalf("stat socket: %v", err)
	} else if info.Mode().Perm() != 0o600 {
		t.Fatalf("socket perm = %v, want 0600", info.Mode().Perm())
	}

	outPath := filepath.Join(t.TempDir(), "socket-export.bdsnap")
	reqCtx, reqCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer reqCancel()
	resp, err := sendControlRequest(reqCtx, socketPath, controlRequest{Action: "export", Path: outPath}, nil)
	if err != nil {
		t.Fatalf("sendControlRequest: %v", err)
	}
	if !resp.OK {
		t.Fatalf("response not ok: %+v", resp)
	}
	if resp.TipHash != "0xsocket" {
		t.Fatalf("resp.TipHash = %q, want 0xsocket", resp.TipHash)
	}
}

// TestControlListenerRoundTripStreamsProgress exercises the real exec path
// (no snapshotCommandFn override) so controlLineStreamer actually relays
// the child's stdout over the socket -- this is what a caller like
// bdagrig-console (via `nodeworker snapshot export`'s own os.Stdout) sees
// live during a real export/import, instead of only the terse final error
// this whole mechanism used to lose the real cause behind.
func TestControlListenerRoundTripStreamsProgress(t *testing.T) {
	ready := readyTestServer(t)

	// Doubles as both the supervised "node" child (args[0] == "60", mimics
	// /bin/sleep so the worker has something long-lived to supervise) and
	// the snap export/verify binary (args[0] == "snap", echoes lines the
	// way blockdag-node's real "Snapshot export in progress ..." logging
	// does, then exits clean without actually producing an archive -- this
	// test only cares about the streamed log lines, not the archive).
	scriptPath := filepath.Join(t.TempDir(), "fake-bdag.sh")
	script := "#!/bin/sh\nif [ \"$1\" = \"snap\" ]; then\n echo line-one\n echo line-two\n exit 0\nfi\nexec sleep \"$@\"\n"
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake binary: %v", err)
	}

	worker := &NodeWorker{
		binary:         scriptPath,
		args:           []string{"60"},
		exitCh:         make(chan error, 1),
		readyURL:       ready.URL,
		healthMinPeers: 1,
		snapshotCfg: snapshotConfig{
			DataDir: "/bdag/data/mainnet",
			Network: "mainnet",
		},
	}
	if err := worker.Start(); err != nil {
		t.Fatalf("start worker child: %v", err)
	}
	t.Cleanup(func() { _ = worker.Stop() })

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	socketPath := filepath.Join(t.TempDir(), "control.sock")
	if err := worker.startControlListener(ctx, socketPath); err != nil {
		t.Fatalf("startControlListener: %v", err)
	}

	outPath := filepath.Join(t.TempDir(), "socket-export.bdsnap")
	reqCtx, reqCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer reqCancel()
	var streamed bytes.Buffer
	resp, err := sendControlRequest(reqCtx, socketPath, controlRequest{Action: "export", Path: outPath}, &streamed)
	if err != nil {
		t.Fatalf("sendControlRequest: %v", err)
	}
	if !resp.OK {
		t.Fatalf("response not ok: %+v", resp)
	}
	// Export and verify each invoke the script once, so each line appears
	// twice.
	if got := strings.Count(streamed.String(), "line-one"); got != 2 {
		t.Fatalf("streamed output = %q, want 2 occurrences of line-one, got %d", streamed.String(), got)
	}
	if got := strings.Count(streamed.String(), "line-two"); got != 2 {
		t.Fatalf("streamed output = %q, want 2 occurrences of line-two, got %d", streamed.String(), got)
	}
}
