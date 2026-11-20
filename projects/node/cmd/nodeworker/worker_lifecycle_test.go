package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestUnexpectedChildExitCancelsReadinessAndReportsTerminalFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	worker := &NodeWorker{
		binary:         "/bin/true",
		readyURL:       server.URL,
		startupTimeout: 0,
	}
	worker.bindTerminalCancel(cancel)
	if err := worker.Start(); err != nil {
		t.Fatalf("start child: %v", err)
	}

	started := time.Now()
	err := worker.waitForReadiness(ctx)
	if err == nil || worker.terminalFailure() == nil {
		t.Fatalf("waitForReadiness err=%v terminal=%v, want terminal failure", err, worker.terminalFailure())
	}
	if elapsed := time.Since(started); elapsed > 2*time.Second {
		t.Fatalf("unexpected exit took %s to cancel readiness", elapsed)
	}
	_ = worker.Stop()
}

func TestUnexpectedChildExitAfterReadinessCancelsApplication(t *testing.T) {
	ready := readyTestServer(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	worker := &NodeWorker{
		binary:   "/bin/sleep",
		args:     []string{"60"},
		readyURL: ready.URL,
	}
	worker.bindTerminalCancel(cancel)
	if err := worker.Start(); err != nil {
		t.Fatalf("start child: %v", err)
	}
	if err := worker.waitForReadiness(ctx); err != nil {
		t.Fatalf("readiness: %v", err)
	}
	g := worker.currentGeneration()
	if g == nil || g.cmd.Process == nil {
		t.Fatal("missing active generation")
	}
	if err := g.cmd.Process.Kill(); err != nil {
		t.Fatalf("kill child fixture: %v", err)
	}
	select {
	case <-ctx.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("unexpected exit did not cancel application context")
	}
	if worker.terminalFailure() == nil {
		t.Fatal("unexpected exit did not record terminal failure")
	}
	_ = worker.Stop()
}

func TestIntentionalStopDoesNotBecomeTerminalFailure(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	worker := &NodeWorker{binary: "/bin/sleep", args: []string{"60"}}
	worker.bindTerminalCancel(cancel)
	if err := worker.Start(); err != nil {
		t.Fatalf("start child: %v", err)
	}
	if err := worker.Stop(); err != nil {
		t.Fatalf("stop child: %v", err)
	}
	if err := worker.terminalFailure(); err != nil {
		t.Fatalf("intentional stop became terminal: %v", err)
	}
	select {
	case <-ctx.Done():
		t.Fatal("intentional stop cancelled application context")
	default:
	}
}

func TestRestartWithReusesGenerationCompletionWithoutSecondWait(t *testing.T) {
	ready := readyTestServer(t)
	worker := &NodeWorker{
		binary:   "/bin/sleep",
		args:     []string{"60"},
		readyURL: ready.URL,
	}
	if err := worker.Start(); err != nil {
		t.Fatalf("start child: %v", err)
	}
	old := worker.currentGeneration()
	if old == nil {
		t.Fatal("missing initial generation")
	}
	if err := worker.RestartWith("/bin/sleep"); err != nil {
		t.Fatalf("restart child: %v", err)
	}
	newGeneration := worker.currentGeneration()
	if newGeneration == nil || newGeneration == old || newGeneration.id <= old.id {
		t.Fatalf("restart did not create a successor generation: old=%v new=%v", old.id, newGeneration)
	}
	if _, completed, intentional := old.state(); !completed || !intentional {
		t.Fatalf("old generation state = completed:%v intentional:%v, want intentional completion", completed, intentional)
	}
	if err := worker.Stop(); err != nil {
		t.Fatalf("stop successor: %v", err)
	}
}

func TestStaleGenerationCompletionCannotCancelSuccessor(t *testing.T) {
	_, oldCancel := context.WithCancel(context.Background())
	defer oldCancel()
	successorCtx, successorCancel := context.WithCancel(context.Background())
	defer successorCancel()
	old := newChildGeneration(1, nil, oldCancel)
	successor := newChildGeneration(2, nil, successorCancel)
	worker := &NodeWorker{generation: successor, ctx: successorCtx, cancel: successorCancel}

	worker.generationExit(old, nil)
	if worker.terminalFailure() != nil {
		t.Fatalf("stale generation reported terminal failure: %v", worker.terminalFailure())
	}
	select {
	case <-successorCtx.Done():
		t.Fatal("stale generation cancelled successor")
	default:
	}
}

func TestStopPublishesUnexpectedCompletionBeforeRetiringGeneration(t *testing.T) {
	appCtx, appCancel := context.WithCancel(context.Background())
	defer appCancel()
	cmd := exec.Command("/bin/sleep", "60")
	g := newChildGeneration(1, cmd, appCancel)
	worker := &NodeWorker{
		binary:     "/bin/sleep",
		args:       []string{"60"},
		cmd:        cmd,
		ctx:        appCtx,
		cancel:     appCancel,
		generation: g,
	}
	worker.bindTerminalCancel(appCancel)

	// Hold lifecycleMu across completion publication.  stopLocked must publish
	// the unexpected result before it retires this generation, so a successor
	// cannot be started after a crash even if the Wait watcher was delayed.
	worker.lifecycleMu.Lock()
	g.record(nil)
	if err := worker.stopLocked(); err != nil {
		worker.lifecycleMu.Unlock()
		t.Fatalf("stop completed generation: %v", err)
	}
	worker.lifecycleMu.Unlock()

	if worker.terminalFailure() == nil {
		t.Fatal("completed unexpected generation did not become terminal")
	}
	select {
	case <-appCtx.Done():
	default:
		t.Fatal("unexpected completion did not cancel application context")
	}
	if worker.generation != nil {
		t.Fatal("unexpected generation remained current after stop")
	}
	worker.lifecycleMu.Lock()
	if err := worker.startLocked(); err == nil {
		worker.lifecycleMu.Unlock()
		t.Fatal("terminal worker accepted successor start")
	}
	worker.lifecycleMu.Unlock()
}

func TestRestartWithCrashedOldGenerationIsTerminalAndDoesNotStartSuccessor(t *testing.T) {
	appCtx, appCancel := context.WithCancel(context.Background())
	defer appCancel()
	cmd := exec.Command("/bin/sleep", "60")
	g := newChildGeneration(1, cmd, appCancel)
	worker := &NodeWorker{
		binary:     "/bin/sleep",
		args:       []string{"60"},
		cmd:        cmd,
		ctx:        appCtx,
		cancel:     appCancel,
		generation: g,
	}
	worker.bindTerminalCancel(appCancel)
	g.record(nil)

	if err := worker.RestartWith("/bin/sleep"); err == nil {
		t.Fatal("RestartWith accepted an already-crashed old generation")
	}
	if worker.terminalFailure() == nil {
		t.Fatal("crashed old generation did not become terminal")
	}
	select {
	case <-appCtx.Done():
	default:
		t.Fatal("crashed old generation did not cancel application context")
	}
	if worker.currentGeneration() != nil {
		t.Fatal("RestartWith started a successor after crashed old generation")
	}
}

func TestRestartWithCandidateExecFailureRollsBackHealthyOldGeneration(t *testing.T) {
	ready := readyTestServer(t)
	worker := &NodeWorker{
		binary:         "/bin/sleep",
		args:           []string{"60"},
		readyURL:       ready.URL,
		startupTimeout: 500 * time.Millisecond,
	}
	if err := worker.Start(); err != nil {
		t.Fatalf("start old child: %v", err)
	}
	old := worker.currentGeneration()
	if old == nil {
		t.Fatal("missing old generation")
	}
	if err := worker.waitForReadiness(context.Background()); err != nil {
		t.Fatalf("old readiness: %v", err)
	}
	t.Cleanup(func() { _ = worker.Stop() })

	candidate := filepath.Join(t.TempDir(), "not-an-executable")
	if err := os.WriteFile(candidate, []byte("not an executable"), 0o755); err != nil {
		t.Fatalf("write candidate: %v", err)
	}
	if err := worker.RestartWith(candidate); err == nil {
		t.Fatal("candidate exec failure unexpectedly succeeded")
	}
	if terminal := worker.terminalFailure(); terminal != nil {
		t.Fatalf("candidate exec failure latched terminal despite healthy rollback: %v", terminal)
	}
	if worker.binary != "/bin/sleep" {
		t.Fatalf("binary after rollback = %q, want old binary", worker.binary)
	}
	current := worker.currentGeneration()
	if current == nil || current == old || current.id <= old.id {
		t.Fatalf("rollback did not create healthy successor: old=%v current=%v", old.id, current)
	}
}

func TestRestartWithReadinessFailureRollsBackHealthyOldGeneration(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "candidate-running")
	ready := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if _, err := os.Stat(marker); err == nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte("ready"))
	}))
	defer ready.Close()

	worker := &NodeWorker{
		binary:         "/bin/sleep",
		args:           []string{"60"},
		readyURL:       ready.URL,
		startupTimeout: 500 * time.Millisecond,
	}
	if err := worker.Start(); err != nil {
		t.Fatalf("start old child: %v", err)
	}
	old := worker.currentGeneration()
	if old == nil {
		t.Fatal("missing old generation")
	}
	if err := worker.waitForReadiness(context.Background()); err != nil {
		t.Fatalf("old readiness: %v", err)
	}
	t.Cleanup(func() { _ = worker.Stop() })

	candidate := filepath.Join(t.TempDir(), "candidate.sh")
	// Seed the failure marker before launching the candidate so the first
	// readiness probe cannot race the candidate's shell startup.  The
	// candidate removes it from a signal-responsive builtin loop, allowing the
	// rollback generation to become healthy immediately after Stop.
	if err := os.WriteFile(marker, []byte("candidate"), 0o600); err != nil {
		t.Fatalf("seed candidate marker: %v", err)
	}
	script := fmt.Sprintf("#!/bin/sh\nif [ \"$1\" = --version ]; then echo test-candidate; exit 0; fi\ntrap 'rm -f %q; exit 0' TERM INT\nwhile :; do :; done\n", marker)
	if err := os.WriteFile(candidate, []byte(script), 0o755); err != nil {
		t.Fatalf("write candidate: %v", err)
	}
	if err := worker.RestartWith(candidate); err == nil {
		t.Fatal("candidate readiness failure unexpectedly succeeded")
	}
	if terminal := worker.terminalFailure(); terminal != nil {
		t.Fatalf("candidate readiness failure latched terminal despite healthy rollback: %v", terminal)
	}
	if worker.binary != "/bin/sleep" {
		t.Fatalf("binary after rollback = %q, want old binary", worker.binary)
	}
	current := worker.currentGeneration()
	if current == nil || current == old || current.id <= old.id {
		t.Fatalf("rollback did not create healthy successor: old=%v current=%v", old.id, current)
	}
}
