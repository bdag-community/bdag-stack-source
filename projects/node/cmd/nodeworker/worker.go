package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"math/big"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	corejson "github.com/BlockdagNetworkLabs/bdag/core/json"
	"github.com/BlockdagNetworkLabs/bdag/log"
	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/ethereum/go-ethereum/metrics"
)

// -----------------------------
// Persist/Promotion parameters
// -----------------------------

const (
	defaultPersistRoot                = "/opt/bdag"
	healthSoak                        = 15 * time.Second // brief soak to prove health after restart
	confirmationSafetyMargin   uint64 = 1000
	dagRPCRequestTimeout              = 1500 * time.Millisecond
	defaultShutdownTimeout            = 5 * time.Minute
	defaultLivenessJoinTimeout        = 5 * time.Second
)

var (
	persistDir  = defaultPersistRoot // mounted named volume in the container (overridable via flag)
	activePath  = filepath.Join(persistDir, "bdag")
	stagingRoot = filepath.Join(persistDir, "staging")

	confirmationPollInterval  = time.Second
	livenessPollInterval      = time.Second
	upgradeRestartOffsetGauge = metrics.NewRegisteredGaugeFloat64("nodeworker/upgrade_restart_offset_seconds", nil)
	dagPeerCountRequest       = []byte(`{"jsonrpc":"2.0","id":1,"method":"getNetworkInfo","params":[]}`)
	dagTemplateHealthRequest  = []byte(`{"jsonrpc":"2.0","id":1,"method":"getTemplateHealth","params":[]}`)
)

func configurePersistRoot(root string) {
	if root == "" {
		root = defaultPersistRoot
	}
	clean := filepath.Clean(root)
	persistDir = clean
	activePath = filepath.Join(clean, "bdag")
	stagingRoot = filepath.Join(clean, "staging")
}

// stagePathFromSHA returns a staging path under the configured stagingRoot (e.g. /opt/bdag/staging/abcdef12/bdag).
func stagePathFromSHA(sha string) string {
	tag := sha
	if len(tag) > 8 {
		tag = sha[:8]
	}
	return filepath.Join(stagingRoot, tag, "bdag")
}

func copyFile(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	defer func() { _ = out.Close() }()
	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Sync()
}

// promoteAndPrune atomically promotes the staged binary to activePath
// and prunes everything else in persist dir (keeps only the "bdag" file).
func promoteAndPrune(staged string) error {
	if err := os.MkdirAll(persistDir, 0o755); err != nil {
		return err
	}
	tmp := activePath + ".new"
	if err := copyFile(staged, tmp, 0o755); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("copy staged->tmp failed: %w", err)
	}
	if err := os.Rename(tmp, activePath); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("rename tmp->active failed: %w", err)
	}
	// prune everything except the promoted 'bdag'
	entries, _ := os.ReadDir(persistDir)
	for _, e := range entries {
		if e.Name() == "bdag" {
			continue
		}
		_ = os.RemoveAll(filepath.Join(persistDir, e.Name()))
	}
	return nil
}

func (w *NodeWorker) proveHealthy(ctx context.Context) error {
	t := time.NewTimer(healthSoak)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func fallbackBinary() string {
	if _, err := os.Stat(activePath); err == nil {
		return activePath
	}
	return "/usr/local/bin/bdag" // legacy baked into the image
}

func progressTxIndex(state *progressState) interface{} {
	if state == nil || state.TxIndex == nil {
		return nil
	}
	return *state.TxIndex
}

func (w *NodeWorker) startLivenessMonitor() error {
	if w.livenessTimeout <= 0 || w.liveURL == "" {
		return nil
	}

	// Cancel and join any previous monitor before replacing its handles.  The
	// join is deliberately outside livenessMu and bounded: a stuck observer
	// must never hold a lifecycle/client lock or silently control a successor.
	if err := w.stopLivenessMonitor(); err != nil {
		w.reportTerminal(err)
		return err
	}

	baseCtx := w.ctx
	if baseCtx == nil {
		baseCtx = context.Background()
	}
	ctx, cancel := context.WithCancel(baseCtx)
	done := make(chan struct{})
	w.livenessMu.Lock()
	w.livenessCancel = cancel
	w.livenessDone = done
	timeout := w.livenessTimeout
	probeInterval := normalizedHealthProbeInterval(w.healthProbeInterval)
	miningReadinessTimeout := w.miningReadinessTimeout
	miningReadinessGrace := w.miningReadinessGrace
	statusLogIntervals := w.statusLogIntervals
	primary := w.liveURL
	fallback := w.legacyLiveURL
	w.livenessMu.Unlock()

	go func() {
		defer close(done)
		w.monitorLiveness(ctx, timeout, probeInterval, miningReadinessTimeout, miningReadinessGrace, statusLogIntervals, primary, fallback)
	}()
	return nil
}

func (w *NodeWorker) stopLivenessMonitor() error {
	w.livenessMu.Lock()
	cancel := w.livenessCancel
	done := w.livenessDone
	w.livenessCancel = nil
	w.livenessDone = nil
	w.livenessMu.Unlock()

	if cancel != nil {
		cancel()
	}
	if done != nil {
		timer := time.NewTimer(defaultLivenessJoinTimeout)
		defer timer.Stop()
		select {
		case <-done:
		case <-timer.C:
			return fmt.Errorf("liveness monitor did not stop within %s", defaultLivenessJoinTimeout)
		}
	}
	return nil
}

func normalizedHealthProbeInterval(interval time.Duration) time.Duration {
	if interval <= 0 {
		return livenessPollInterval
	}
	if interval < 250*time.Millisecond {
		return 250 * time.Millisecond
	}
	return interval
}

func (w *NodeWorker) monitorLiveness(ctx context.Context, timeout, probeInterval, miningReadinessTimeout, miningReadinessGrace time.Duration, statusLogIntervals nodeStatusLogIntervals, primary, fallback string) {
	client := &http.Client{Timeout: 2 * time.Second}
	ticker := time.NewTicker(normalizedHealthProbeInterval(probeInterval))
	defer ticker.Stop()

	var failureStart time.Time
	warned := false
	monitorStart := time.Now()
	var miningFailureStart time.Time
	miningWarned := false

	for {
		err := probeLiveness(client, primary, fallback)
		if err != nil {
			if failureStart.IsZero() {
				failureStart = time.Now()
				warned = false
			}
			if !warned {
				log.Warn("liveness probe failed", "url", primary, "err", err)
				warned = true
			}
			if time.Since(failureStart) >= timeout {
				log.Error("liveness probe exceeded timeout; restarting inner binary", "url", primary, "timeout", timeout, "err", err)
				dumpCtx, dumpCancel := context.WithTimeout(context.Background(), livenessDumpHTTPTimeout+2*time.Second)
				w.captureLivenessDiagnostics(dumpCtx, err)
				dumpCancel()
				if cb := w.onLivenessFailure; cb != nil {
					cb()
				} else {
					go w.restartChild()
				}
				return
			}
		} else {
			if !failureStart.IsZero() && warned {
				log.Info("liveness probe recovered", "url", primary)
			}
			failureStart = time.Time{}
			warned = false
		}
		if err == nil {
			if miningReadinessTimeout > 0 {
				miningProbe := w.probeMiningReadiness(ctx)
				w.maybeLogNodeStatus(ctx, miningProbe.health, statusLogIntervals)
				if miningProbe.err != nil {
					if miningFailureStart.IsZero() {
						miningFailureStart = time.Now()
						miningWarned = false
					}
					if !miningWarned {
						log.Warn("mining readiness probe failed", "restartEligible", miningProbe.restartEligible, "err", miningProbe.err)
						miningWarned = true
					}
					if !miningProbe.restartEligible || time.Since(monitorStart) < miningReadinessGrace {
						miningFailureStart = time.Time{}
					} else if time.Since(miningFailureStart) >= miningReadinessTimeout {
						log.Error("mining readiness exceeded timeout; forcing shutdown", "timeout", miningReadinessTimeout, "err", miningProbe.err)
						w.forceLivenessShutdown(miningProbe.err)
						return
					}
				} else {
					if !miningFailureStart.IsZero() && miningWarned {
						log.Info("mining readiness probe recovered")
					}
					miningFailureStart = time.Time{}
					miningWarned = false
				}
			} else {
				w.maybeLogNodeStatus(ctx, nil, statusLogIntervals)
			}
		}

		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (w *NodeWorker) forceLivenessShutdown(lastErr error) {
	dumpCtx, dumpCancel := context.WithTimeout(context.Background(), livenessDumpHTTPTimeout+2*time.Second)
	w.captureLivenessDiagnostics(dumpCtx, lastErr)
	dumpCancel()
	w.reportTerminal(fmt.Errorf("liveness monitor forced shutdown: %w", lastErr))
	if cb := w.onLivenessFailure; cb != nil {
		cb()
		return
	}
	go func() {
		if stopErr := w.Stop(); stopErr != nil {
			log.Error("liveness monitor stop failed", "err", stopErr)
		}
	}()
}

func probeLiveness(client *http.Client, primary, fallback string) error {
	resp, err := client.Get(primary)
	if err != nil {
		return err
	}
	if resp.StatusCode == http.StatusOK {
		resp.Body.Close()
		return nil
	}
	if resp.StatusCode == http.StatusNotFound && fallback != "" {
		resp.Body.Close()
		resp, err = client.Get(fallback)
		if err != nil {
			return err
		}
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unhealthy: %d", resp.StatusCode)
	}
	return nil
}

// -----------------------------
// Worker
// -----------------------------

// childGeneration is the sole owner of one child process's Wait result.  The
// done channel is closed exactly once after result is recorded, so lifecycle
// operations and readiness observers can all broadcast on the same
// completion without racing to reap the PID or consuming a one-shot value.
type childGeneration struct {
	id     uint64
	cmd    *exec.Cmd
	done   chan struct{}
	cancel context.CancelFunc

	mu          sync.Mutex
	result      error
	completed   bool
	intentional bool
}

func newChildGeneration(id uint64, cmd *exec.Cmd, cancel context.CancelFunc) *childGeneration {
	return &childGeneration{id: id, cmd: cmd, done: make(chan struct{}), cancel: cancel}
}

func (g *childGeneration) record(result error) {
	g.mu.Lock()
	if g.completed {
		g.mu.Unlock()
		return
	}
	g.result = result
	g.completed = true
	close(g.done)
	g.mu.Unlock()
}

// expectStop must run before a signal is sent.  A completed generation cannot
// be retroactively made intentional: that would turn an already-observed
// unexpected exit into a clean stop and could apply its result to a successor.
func (g *childGeneration) expectStop() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.completed {
		return false
	}
	g.intentional = true
	return true
}

func (g *childGeneration) state() (result error, completed, intentional bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.result, g.completed, g.intentional
}

func (g *childGeneration) isIntentional() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.intentional
}

// NodeWorker manages a child bdag process and watches on-chain execution events
// to trigger upgrades.
type NodeWorker struct {
	// current and backup binaries
	binary     string
	prevBinary string
	args       []string

	// process control
	cmd                    *exec.Cmd
	ctx                    context.Context
	cancel                 context.CancelFunc
	exitCh                 chan error // retained for fixture/source compatibility; generations own completion
	lifecycleMu            sync.Mutex
	generation             *childGeneration
	nextGeneration         uint64
	upgrading              uint32 // 0 = normal, 1 = upgrade in progress
	managedUpgrade         uint32 // 0 = ordinary lifecycle, 1 = RestartWith candidate transition
	readyURL               string
	legacyReadyURL         string
	liveURL                string
	legacyLiveURL          string
	startupTimeout         time.Duration
	livenessTimeout        time.Duration
	shutdownTimeout        time.Duration
	healthProbeInterval    time.Duration
	miningReadinessTimeout time.Duration
	miningReadinessGrace   time.Duration
	statusLogIntervals     nodeStatusLogIntervals
	rolloutWindow          time.Duration
	healthMinPeers         uint64
	nodeID                 []byte
	nodeIDTrace            string
	snapshotCfg            snapshotConfig // datadir/network/chunk-size baseline for on-demand snapshot requests (control.go)

	// on-chain event subscription
	wsURL       string
	evmRPCURL   string
	client      *ethclient.Client
	contract    common.Address
	eventParser abi.ABI
	eventID     common.Hash
	mu          sync.Mutex

	terminalMu     sync.Mutex
	terminalOnce   sync.Once
	terminalErr    error
	terminalCancel context.CancelFunc

	headFn            func(context.Context) (uint64, error)
	peerCountFn       func(context.Context) (uint64, error)
	isCurrentFn       func(context.Context) (bool, error)
	templateHealthFn  func(context.Context) (*miningTemplateHealth, error)
	snapshotCommandFn func(context.Context, string, ...string) error

	progressMu sync.RWMutex
	progress   *progressState

	dagRPCURL    string
	dagRPCClient *http.Client
	dagRPCUser   string
	dagRPCPass   string

	livenessMu        sync.Mutex
	livenessCancel    context.CancelFunc
	livenessDone      chan struct{}
	onLivenessFailure func()

	statusLogMu            sync.Mutex
	statusLogLastLog       time.Time
	statusLogLastSignature string
	statusLogNative        syncProgressEstimator
	statusLogEVMBlock      syncProgressEstimator
	statusLogEVMState      syncProgressEstimator

	// When liveness fails for health.liveness-timeout, fetch child goroutine dump via pprof before SIGTERM.
	pprofBaseURL        string
	livenessDumpDir     string
	livenessDumpEnabled bool

	// External IP monitoring — periodically re-fetches the host's public IP and
	// restarts the child if it has changed so the node advertises the correct address.
	ipRefreshInterval time.Duration
	configFile        string
}

func (w *NodeWorker) bindTerminalCancel(cancel context.CancelFunc) {
	w.terminalMu.Lock()
	w.terminalCancel = cancel
	w.terminalMu.Unlock()
}

func (w *NodeWorker) reportTerminal(err error) {
	if err == nil {
		err = errors.New("nodeworker lifecycle failed")
	}
	w.terminalOnce.Do(func() {
		w.terminalMu.Lock()
		w.terminalErr = err
		cancel := w.terminalCancel
		w.terminalMu.Unlock()
		if cancel != nil {
			cancel()
		}
	})
}

func (w *NodeWorker) terminalFailure() error {
	w.terminalMu.Lock()
	defer w.terminalMu.Unlock()
	return w.terminalErr
}

func (w *NodeWorker) currentGeneration() *childGeneration {
	w.lifecycleMu.Lock()
	defer w.lifecycleMu.Unlock()
	return w.generation
}

func (w *NodeWorker) generationExit(g *childGeneration, result error) {
	g.record(result)
	_, _, intentional := g.state()

	// Generation identity is the guard against a late waiter from an old child
	// cancelling or reporting against its successor.
	w.lifecycleMu.Lock()
	current := w.generation == g
	if current && !intentional {
		if w.cancel != nil {
			w.cancel()
		}
	}
	w.lifecycleMu.Unlock()
	if !current || intentional {
		return
	}
	w.reportUnexpectedGeneration(g, result)
}

func (w *NodeWorker) reportUnexpectedGeneration(g *childGeneration, result error) {
	// A managed candidate is allowed to fail while RestartWith verifies and
	// rolls back.  The rollback path owns the terminal decision in that case.
	if atomic.LoadUint32(&w.managedUpgrade) != 0 {
		return
	}
	if result == nil {
		result = fmt.Errorf("child generation %d exited unexpectedly with status 0", g.id)
	} else {
		result = fmt.Errorf("child generation %d exited unexpectedly: %w", g.id, result)
	}
	w.reportTerminal(result)
}

func (w *NodeWorker) waitForReadiness(ctx context.Context) error {
	g := w.currentGeneration()
	if g == nil {
		return errors.New("child generation unavailable while waiting for readiness")
	}
	return w.waitForReadinessGeneration(ctx, g)
}

func (w *NodeWorker) waitForReadinessGeneration(ctx context.Context, g *childGeneration) error {
	return w.waitForReadinessGenerationMode(ctx, g, true)
}

func (w *NodeWorker) waitForReadinessGenerationMode(ctx context.Context, g *childGeneration, checkTerminal bool) error {
	if g == nil {
		return errors.New("child generation unavailable while waiting for readiness")
	}
	readyCtx, cancel := context.WithCancel(ctx)
	observerDone := make(chan struct{})
	go func() {
		defer close(observerDone)
		select {
		case <-g.done:
			cancel()
		case <-readyCtx.Done():
			cancel()
		}
	}()
	err := waitForReadiness(readyCtx, w.readyURL, w.legacyReadyURL, w.startupTimeout)
	cancel()
	<-observerDone
	if checkTerminal {
		if terminal := w.terminalFailure(); terminal != nil {
			return terminal
		}
	}
	if result, completed, _ := g.state(); completed && err == nil {
		if result == nil {
			return fmt.Errorf("child generation %d exited before readiness", g.id)
		}
		return fmt.Errorf("child generation %d exited before readiness: %w", g.id, result)
	}
	return err
}

// fetchExternalIP tries several public IP-echo services and returns the first
// valid response. Uses ctx for cancellation / deadline.
func fetchExternalIP(ctx context.Context) (string, error) {
	services := []string{
		"https://api.ipify.org",
		"https://ifconfig.me",
		"https://icanhazip.com",
	}
	client := &http.Client{Timeout: 5 * time.Second}
	for _, url := range services {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			continue
		}
		resp, err := client.Do(req)
		if err != nil {
			continue
		}
		body, err := io.ReadAll(io.LimitReader(resp.Body, 64))
		resp.Body.Close()
		if err != nil || resp.StatusCode != http.StatusOK {
			continue
		}
		ip := strings.TrimSpace(string(body))
		if net.ParseIP(ip) != nil {
			return ip, nil
		}
	}
	return "", errors.New("all external IP services failed")
}

// patchExternalIP rewrites the externalip= line in the node config file.
// Handles active lines ("externalip=...") and the commented placeholder
// ("# externalip="). Appends if neither is present.
func patchExternalIP(configFile, ip string) error {
	data, err := os.ReadFile(configFile)
	if err != nil {
		return err
	}
	lines := strings.Split(string(data), "\n")
	found := false
	for i, line := range lines {
		stripped := strings.TrimSpace(strings.TrimLeft(line, "#"))
		stripped = strings.TrimSpace(stripped)
		if strings.HasPrefix(stripped, "externalip=") {
			lines[i] = "externalip=" + ip
			found = true
			break
		}
	}
	if !found {
		lines = append(lines, "externalip="+ip)
	}
	return os.WriteFile(configFile, []byte(strings.Join(lines, "\n")), 0o644)
}

// monitorExternalIP periodically fetches the host's public IP and restarts the
// inner binary if it has changed, so the node always advertises the correct address.
func (w *NodeWorker) monitorExternalIP(ctx context.Context) {
	if w.ipRefreshInterval <= 0 || w.configFile == "" {
		return
	}

	current, err := fetchExternalIP(ctx)
	if err != nil {
		log.Warn("ip monitor: initial fetch failed", "err", err)
	} else {
		log.Info("ip monitor: started", "ip", current, "interval", w.ipRefreshInterval)
	}

	ticker := time.NewTicker(w.ipRefreshInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			ip, err := fetchExternalIP(ctx)
			if err != nil {
				log.Warn("ip monitor: fetch failed", "err", err)
				continue
			}
			if ip == current {
				continue
			}
			log.Warn("ip monitor: external IP changed", "old", current, "new", ip)
			if err := patchExternalIP(w.configFile, ip); err != nil {
				log.Error("ip monitor: failed to update config file", "file", w.configFile, "err", err)
				continue
			}
			current = ip
			log.Info("ip monitor: config updated, restarting inner binary", "ip", ip)
			w.restartChild()
		}
	}
}

// Start launches the child process (gracefully managed).
func (w *NodeWorker) Start() error {
	w.lifecycleMu.Lock()
	defer w.lifecycleMu.Unlock()
	return w.startLocked()
}

func (w *NodeWorker) startLocked() error {
	if terminal := w.terminalFailure(); terminal != nil {
		return fmt.Errorf("lifecycle is terminal: %w", terminal)
	}
	if w.generation != nil {
		_, completed, intentional := w.generation.state()
		if !completed {
			return errors.New("child already running")
		}
		if !intentional {
			err := fmt.Errorf("refusing successor start after unexpected child generation %d exit", w.generation.id)
			w.reportTerminal(err)
			return err
		}
		w.generation = nil
		w.cmd = nil
	}
	if w.cmd != nil && (w.cmd.ProcessState == nil || !w.cmd.ProcessState.Exited()) {
		return errors.New("child already running")
	}
	if err := validateNodeBinary(w.binary); err != nil {
		return fmt.Errorf("binary validation failed: %w", err)
	}

	if err := w.stopLivenessMonitor(); err != nil {
		w.reportTerminal(err)
		return err
	}
	childCtx, childCancel := context.WithCancel(context.Background())
	w.ctx, w.cancel = childCtx, childCancel

	cmd := exec.Command(w.binary, w.args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	// Put child in its own process group so we can kill the whole tree on timeout.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	if err := cmd.Start(); err != nil {
		childCancel()
		w.ctx, w.cancel = nil, nil
		return err
	}
	w.cmd = cmd
	w.nextGeneration++
	g := newChildGeneration(w.nextGeneration, cmd, childCancel)
	w.generation = g
	// This goroutine is the only caller of cmd.Wait for this generation.
	go func() {
		w.generationExit(g, cmd.Wait())
	}()

	// Liveness monitoring starts only after waitForReadiness succeeds (see run.go
	// and RestartWith). Starting it here would count "connection refused" while
	// the node is still opening databases and hit liveness-timeout before /healthz
	// is bound.

	log.Info("child started", "binary", w.binary, "pid", cmd.Process.Pid, "argv", append([]string{w.binary}, w.args...))
	return nil
}

// Stop tears down the child and closes any active client.
func (w *NodeWorker) Stop() error {
	w.lifecycleMu.Lock()
	err := w.stopLocked()
	w.lifecycleMu.Unlock()
	w.closeClient()
	return err
}

// stopLocked marks the active generation intentional before signaling it and
// waits on its recorded completion.  It must only be called with
// lifecycleMu held; it never calls cmd.Wait itself.
func (w *NodeWorker) stopLocked() error {
	log.Info("stopping worker and inner binary")
	g := w.generation
	if g == nil || w.cmd == nil {
		log.Warn("Stop called but no child process running")
		return nil
	}

	// Mark intent before cancellation or signaling so a fast exit is never
	// mistaken for an unexpected generation failure.
	expected := g.expectStop()
	if expected && g.cancel != nil {
		g.cancel()
	}
	monitorErr := w.stopLivenessMonitor()
	if monitorErr != nil {
		w.reportTerminal(monitorErr)
	}

	if expected {
		if _, completed, _ := g.state(); !completed && g.cmd.Process != nil {
			if err := g.cmd.Process.Signal(syscall.SIGTERM); err != nil {
				log.Warn("failed to signal inner binary", "err", err)
			}
		}
	}
	if _, completed, intentional := g.state(); completed && !intentional {
		// cmd.Wait has already completed, but its watcher may be blocked on
		// lifecycleMu. Publish the fatal result before retiring this generation;
		// otherwise Stop could clear it and permit a successor after a crash.
		result, _, _ := g.state()
		w.reportUnexpectedGeneration(g, result)
	}
	if _, completed, _ := g.state(); !completed {
		if !waitGeneration(g, w.effectiveShutdownTimeout()) {
			log.Warn("timeout waiting for inner binary shutdown; killing process group")
			if g.cmd.Process != nil {
				// Kill the whole process group (negative PID == pgid).
				_ = syscall.Kill(-g.cmd.Process.Pid, syscall.SIGKILL)
			}
			if !waitGeneration(g, defaultLivenessJoinTimeout) {
				err := fmt.Errorf("inner binary generation %d did not exit after kill", g.id)
				w.reportTerminal(err)
				return errors.Join(monitorErr, err)
			}
		}
	}
	if result, _, _ := g.state(); result != nil {
		log.Warn("inner binary exited", "err", result, "intentional", expected)
	} else {
		log.Info("inner binary exited cleanly", "intentional", expected)
	}
	if w.generation == g {
		w.generation = nil
		w.cmd = nil
		w.ctx, w.cancel = nil, nil
	}
	log.Info("worker stopped")
	return monitorErr
}

func waitGeneration(g *childGeneration, timeout time.Duration) bool {
	if timeout <= 0 {
		<-g.done
		return true
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-g.done:
		return true
	case <-timer.C:
		return false
	}
}

func (w *NodeWorker) closeClient() {
	w.mu.Lock()
	client := w.client
	w.client = nil
	w.mu.Unlock()
	if client != nil {
		client.Close()
	}
}

func (w *NodeWorker) effectiveShutdownTimeout() time.Duration {
	if w.shutdownTimeout <= 0 {
		return defaultShutdownTimeout
	}
	return w.shutdownTimeout
}

// restartChild stops the inner binary then starts it again and re-enables
// liveness monitoring. Called in a goroutine from monitorLiveness on timeout.
func (w *NodeWorker) restartChild() {
	w.lifecycleMu.Lock()
	defer w.lifecycleMu.Unlock()

	if w.terminalFailure() != nil {
		return
	}
	log.Info("liveness restart: stopping inner binary")
	if err := w.stopLocked(); err != nil {
		log.Error("liveness restart: stop failed", "err", err)
		w.reportTerminal(fmt.Errorf("liveness restart stop failed: %w", err))
		return
	}

	const restartDelay = 5 * time.Second
	log.Info("liveness restart: waiting before restart", "delay", restartDelay)
	time.Sleep(restartDelay)

	if w.terminalFailure() != nil {
		return
	}
	if err := w.startLocked(); err != nil {
		log.Error("liveness restart: failed to start inner binary", "err", err)
		w.reportTerminal(fmt.Errorf("liveness restart start failed: %w", err))
		return
	}

	g := w.generation
	if err := w.waitForReadinessGeneration(w.ctx, g); err != nil {
		log.Error("liveness restart: inner binary never became ready", "err", err)
		_ = w.stopLocked()
		w.reportTerminal(fmt.Errorf("liveness restart readiness failed: %w", err))
		return
	}

	if err := w.startLivenessMonitor(); err != nil {
		w.reportTerminal(fmt.Errorf("liveness restart monitor failed: %w", err))
		_ = w.stopLocked()
		return
	}
	log.Info("liveness restart: inner binary restarted successfully")
}

// RestartWith restarts the worker using the provided binary (no promotion here).
func (w *NodeWorker) RestartWith(newBin string) error {
	w.lifecycleMu.Lock()
	defer w.lifecycleMu.Unlock()

	atomic.StoreUint32(&w.upgrading, 1)
	defer atomic.StoreUint32(&w.upgrading, 0)
	defer atomic.StoreUint32(&w.managedUpgrade, 0)

	if _, err := os.Stat(newBin); err != nil {
		return fmt.Errorf("restart: new binary not found: %w", err)
	}

	oldBin := w.binary
	if err := w.stopLocked(); err != nil {
		w.reportTerminal(fmt.Errorf("restart: old process cleanup failed: %w", err))
		return err
	}
	if terminal := w.terminalFailure(); terminal != nil {
		return fmt.Errorf("restart: old process exited unexpectedly: %w", terminal)
	}
	// Only candidate and rollback generations are exempt from the ordinary
	// unexpected-exit terminal rule.  The old generation must be fully retired
	// and proven non-terminal before entering that managed transition.
	atomic.StoreUint32(&w.managedUpgrade, 1)
	w.prevBinary = oldBin
	w.binary = newBin

	if err := w.startLocked(); err != nil {
		if rollbackErr := w.rollbackLocked(oldBin); rollbackErr != nil {
			w.reportTerminal(errors.Join(fmt.Errorf("restart: failed to start new binary: %w", err), rollbackErr))
		}
		return fmt.Errorf("restart: failed to start new binary: %w", err)
	}
	g := w.generation

	// Wait for new binary to become healthy
	if err := w.waitForReadinessGeneration(w.ctx, g); err != nil {
		log.Error("restart: new binary failed health check", "err", err)
		_ = w.stopLocked()
		if rollbackErr := w.rollbackLocked(oldBin); rollbackErr != nil {
			w.reportTerminal(errors.Join(fmt.Errorf("restart: health check failed: %w", err), rollbackErr))
		}
		return fmt.Errorf("restart: health check failed: %w", err)
	}

	if err := w.startLivenessMonitor(); err != nil {
		_ = w.stopLocked()
		if rollbackErr := w.rollbackLocked(oldBin); rollbackErr != nil {
			w.reportTerminal(errors.Join(fmt.Errorf("restart: monitor start failed: %w", err), rollbackErr))
		}
		return err
	}

	log.Info("restart: upgrade successful; now running", "binary", w.binary)
	return nil
}

func (w *NodeWorker) rollbackLocked(oldBin string) error {
	w.binary = oldBin
	w.prevBinary = oldBin
	if err := w.startLocked(); err != nil {
		log.Error("restart: rollback Start() failed", "err", err)
		return fmt.Errorf("rollback start: %w", err)
	}
	g := w.generation
	if err := w.waitForReadinessGenerationMode(w.ctx, g, false); err != nil {
		log.Error("restart: rollback never became ready", "err", err)
		_ = w.stopLocked()
		return fmt.Errorf("rollback readiness: %w", err)
	}
	if err := w.startLivenessMonitor(); err != nil {
		log.Error("restart: rollback monitor failed", "err", err)
		_ = w.stopLocked()
		return fmt.Errorf("rollback monitor: %w", err)
	}
	log.Info("restart: rolled back to old binary")
	return nil
}

func (w *NodeWorker) restartWithRollout(ctx context.Context, staged string, lg types.Log) error {
	upgradeID := computeUpgradeID(lg)
	offset := w.deterministicOffset(upgradeID)
	fmt.Println("Deterministic restart offset:", offset)
	upgradeRestartOffsetGauge.Update(offset.Seconds())

	if w.rolloutWindow <= 0 {
		log.Info("restart: rollout window disabled", "node", w.nodeIDTrace, "window", w.rolloutWindow, "offset", offset, "healthDelayed", false)
		return w.RestartWith(staged)
	}

	if offset > 0 {
		if err := sleepWithContext(ctx, offset); err != nil {
			return err
		}
	}

	healthDelayed := false
	peers := uint64(0)
	shortfall := uint64(0)
	healthy, currentPeers, currentShortfall, err := w.networkHealthy(ctx)
	if err != nil {
		log.Warn("restart: network health check failed", "err", err)
	}
	peers = currentPeers
	shortfall = currentShortfall
	if !healthy {
		healthDelayed = true
		log.Warn("restart: network health gate delaying restart", "peers", peers, "confirmationShortfall", shortfall)
		if err := sleepWithContext(ctx, 3*time.Minute); err != nil {
			return err
		}
		healthy, currentPeers, currentShortfall, err = w.networkHealthy(ctx)
		if err != nil {
			log.Warn("restart: network health recheck failed", "err", err)
		}
		peers = currentPeers
		shortfall = currentShortfall
		if !healthy {
			log.Warn("restart: proceeding despite network health still below threshold", "peers", peers, "confirmationShortfall", shortfall)
		}
	}

	log.Info("restart: rollout schedule applied", "node", w.nodeIDTrace, "window", w.rolloutWindow, "offset", offset, "healthDelayed", healthDelayed, "peers", peers, "confirmationShortfall", shortfall)
	return w.RestartWith(staged)
}

func (w *NodeWorker) deterministicOffset(upgradeID [32]byte) time.Duration {
	if w.rolloutWindow <= 0 {
		return 0
	}
	window := uint64(w.rolloutWindow)
	if window == 0 {
		return 0
	}
	id := w.nodeID
	if len(id) == 0 {
		sum := sha256.Sum256([]byte("nodeworker-default"))
		id = sum[:]
	}
	buf := make([]byte, 0, len(id)+len(upgradeID))
	buf = append(buf, id...)
	buf = append(buf, upgradeID[:]...)
	sum := sha256.Sum256(buf)
	value := binary.BigEndian.Uint64(sum[:8])
	return time.Duration(value % window)
}

func computeUpgradeID(lg types.Log) [32]byte {
	buf := make([]byte, 0, len(lg.BlockHash.Bytes())+16)
	buf = append(buf, lg.BlockHash.Bytes()...)
	var tmp [8]byte
	binary.BigEndian.PutUint64(tmp[:], uint64(lg.TxIndex))
	buf = append(buf, tmp[:]...)
	binary.BigEndian.PutUint64(tmp[:], uint64(lg.Index))
	buf = append(buf, tmp[:]...)
	return sha256.Sum256(buf)
}

type miningTemplateHealth corejson.MiningTemplateHealthResult

type miningReadinessProbe struct {
	err             error
	restartEligible bool
	health          *miningTemplateHealth
}

func (h *miningTemplateHealth) readinessFailure() miningReadinessProbe {
	if h == nil {
		return miningReadinessProbe{err: errors.New("mining readiness: empty template health response")}
	}
	if h.P2PMiningFreshReasonCode != "" && !h.P2PMiningFresh {
		reason := h.P2PMiningFreshReasonCode
		if h.Reason != "" {
			reason = h.Reason
		}
		err := fmt.Errorf("mining readiness: mineable_now=%v submit_ready=%v reason_code=%s reason=%s seq=%d%s",
			h.MineableNow, h.SubmitReady, h.P2PMiningFreshReasonCode, reason, h.LatestTemplateSeq, h.readinessContext())
		return miningReadinessProbe{err: err, health: h}
	}
	if h.MineableNow && h.SubmitReady {
		return miningReadinessProbe{health: h}
	}
	reason := h.Reason
	if reason == "" {
		reason = h.ReasonCode
	}
	if reason == "" {
		reason = "template health is not mineable"
	}
	err := fmt.Errorf("mining readiness: mineable_now=%v submit_ready=%v reason_code=%s reason=%s seq=%d%s",
		h.MineableNow, h.SubmitReady, h.ReasonCode, reason, h.LatestTemplateSeq, h.readinessContext())
	return miningReadinessProbe{
		err:             err,
		restartEligible: miningReadinessRestartEligible(h),
		health:          h,
	}
}

func (h *miningTemplateHealth) readinessContext() string {
	if h == nil {
		return ""
	}
	fields := make([]string, 0, 16)
	appendInt64Context(&fields, "template_age_ms", h.TemplateAgeMs)
	appendInt64Context(&fields, "last_good_template_age_ms", h.LastGoodTemplateAgeMs)
	appendInt64Context(&fields, "last_template_build_age_ms", h.LastTemplateBuildAgeMs)
	appendInt64Context(&fields, "last_template_build_duration_ms", h.LastTemplateBuildDurationMs)
	appendStringContext(&fields, "last_template_build_reason", h.LastTemplateBuildReason)
	appendStringContext(&fields, "last_template_build_error_code", h.LastTemplateBuildErrorCode)
	if h.PendingTemplateBuild {
		fields = append(fields,
			fmt.Sprintf("pending_template_build=%v", h.PendingTemplateBuild),
			fmt.Sprintf("pending_template_build_priority=%d", h.PendingTemplateBuildPriority),
		)
		appendStringContext(&fields, "pending_template_build_reason", h.PendingTemplateBuildReason)
	}
	if h.P2PConsensusPeerCount != 0 ||
		h.P2PFreshConsensusPeerCount != 0 ||
		h.P2PStaleConsensusPeerCount != 0 ||
		h.P2PMiningFreshReasonCode != "" {
		fields = append(fields,
			fmt.Sprintf("p2p_mining_fresh=%v", h.P2PMiningFresh),
			fmt.Sprintf("p2p_mining_fresh_reason_code=%s", h.P2PMiningFreshReasonCode),
			fmt.Sprintf("p2p_consensus_peer_count=%d", h.P2PConsensusPeerCount),
			fmt.Sprintf("p2p_fresh_consensus_peer_count=%d", h.P2PFreshConsensusPeerCount),
			fmt.Sprintf("p2p_stale_consensus_peer_count=%d", h.P2PStaleConsensusPeerCount),
			fmt.Sprintf("p2p_best_peer_lead_blocks=%d", h.P2PBestPeerLeadBlocks),
		)
		appendInt64Context(&fields, "p2p_best_peer_graph_state_age_ms", h.P2PBestPeerGraphStateAgeMs)
		appendInt64Context(&fields, "p2p_avg_graph_state_age_ms", h.P2PAvgGraphStateAgeMs)
		appendInt64Context(&fields, "p2p_max_graph_state_age_ms", h.P2PMaxGraphStateAgeMs)
		if h.P2PSyncPeerPresent {
			fields = append(fields,
				fmt.Sprintf("p2p_sync_peer_present=%v", h.P2PSyncPeerPresent),
				fmt.Sprintf("p2p_sync_peer_fresh=%v", h.P2PSyncPeerFresh),
			)
			appendInt64Context(&fields, "p2p_sync_peer_graph_state_age_ms", h.P2PSyncPeerGraphStateAgeMs)
		}
	}
	if len(fields) == 0 {
		return ""
	}
	return " " + strings.Join(fields, " ")
}

func appendInt64Context(fields *[]string, key string, value int64) {
	if value != 0 {
		*fields = append(*fields, fmt.Sprintf("%s=%d", key, value))
	}
}

func appendStringContext(fields *[]string, key, value string) {
	if value != "" {
		*fields = append(*fields, fmt.Sprintf("%s=%s", key, value))
	}
}

func miningReadinessRestartEligible(h *miningTemplateHealth) bool {
	if h == nil {
		return false
	}
	switch h.ReasonCode {
	case string(corejson.MiningTemplateHealthTemplateExpired),
		string(corejson.MiningTemplateHealthTemplateStaleParent),
		string(corejson.MiningTemplateHealthTemplateUnavailable),
		string(corejson.MiningTemplateHealthWorkerUnavailable),
		string(corejson.MiningTemplateHealthWorkerStopped):
		return true
	case string(corejson.MiningTemplateHealthTemplateBuildFailed),
		string(corejson.MiningTemplateHealthEVMPendingUnavailable):
		return h.LastTemplateBuildBlocking
	default:
		return false
	}
}

func (w *NodeWorker) probeMiningReadiness(ctx context.Context) miningReadinessProbe {
	var (
		health *miningTemplateHealth
		err    error
	)
	if w.templateHealthFn != nil {
		health, err = w.templateHealthFn(ctx)
	} else {
		probeCtx, cancel := context.WithTimeout(ctx, dagRPCRequestTimeout)
		health, err = w.dagTemplateHealth(probeCtx)
		cancel()
	}
	if err != nil {
		return miningReadinessProbe{err: err}
	}
	return health.readinessFailure()
}

func (w *NodeWorker) dagTemplateHealth(ctx context.Context) (*miningTemplateHealth, error) {
	if w.dagRPCClient == nil {
		return nil, errors.New("mining readiness: dag rpc client unavailable")
	}
	if w.dagRPCURL == "" {
		return nil, errors.New("mining readiness: dag rpc url not configured")
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, w.dagRPCURL, bytes.NewReader(dagTemplateHealthRequest))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-Forwarded-By", "nodeworker")
	if w.dagRPCUser != "" || w.dagRPCPass != "" {
		req.SetBasicAuth(w.dagRPCUser, w.dagRPCPass)
	}

	resp, err := w.dagRPCClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("mining readiness: dag rpc status %s: %s", resp.Status, string(body))
	}

	var env struct {
		Result *miningTemplateHealth `json:"result"`
		Error  *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
		return nil, fmt.Errorf("mining readiness: decode getTemplateHealth response: %w", err)
	}
	if env.Error != nil {
		msg := env.Error.Message
		if msg == "" {
			msg = "dag rpc error"
		}
		return nil, errors.New(msg)
	}
	if env.Result == nil {
		return nil, errors.New("mining readiness: missing getTemplateHealth result")
	}
	return env.Result, nil
}

func (w *NodeWorker) dagPeerCount(ctx context.Context) (uint64, error) {
	if w.dagRPCClient == nil {
		return 0, errors.New("network healthy: dag rpc client unavailable")
	}
	if w.dagRPCURL == "" {
		return 0, errors.New("network healthy: dag rpc url not configured")
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, w.dagRPCURL, bytes.NewReader(dagPeerCountRequest))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-Forwarded-By", "nodeworker")
	if w.dagRPCUser != "" || w.dagRPCPass != "" {
		req.SetBasicAuth(w.dagRPCUser, w.dagRPCPass)
	}

	resp, err := w.dagRPCClient.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return 0, fmt.Errorf("network healthy: dag rpc status %s: %s", resp.Status, string(body))
	}

	var env struct {
		Result *struct {
			TotalConnected *int64 `json:"totalconnected"`
			Connections    *int64 `json:"connections"`
			Infos          []struct {
				Connecteds *int64 `json:"connecteds"`
			} `json:"infos"`
		} `json:"result"`
		Error *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
		return 0, fmt.Errorf("network healthy: decode getNetworkInfo response: %w", err)
	}
	if env.Error != nil {
		msg := env.Error.Message
		if msg == "" {
			msg = "dag rpc error"
		}
		return 0, errors.New(msg)
	}
	if env.Result == nil {
		return 0, errors.New("network healthy: missing getNetworkInfo result")
	}
	var peerCount *int64
	if env.Result.TotalConnected != nil {
		peerCount = env.Result.TotalConnected
	} else if env.Result.Connections != nil {
		peerCount = env.Result.Connections
	} else if len(env.Result.Infos) > 0 {
		var total int64
		for _, info := range env.Result.Infos {
			if info.Connecteds == nil {
				continue
			}
			if *info.Connecteds < 0 {
				return 0, fmt.Errorf("network healthy: negative peer count %d", *info.Connecteds)
			}
			total += *info.Connecteds
		}
		peerCount = &total
	}
	if peerCount == nil {
		return 0, errors.New("network healthy: missing totalconnected in getNetworkInfo result")
	}
	if *peerCount < 0 {
		return 0, fmt.Errorf("network healthy: negative peer count %d", *peerCount)
	}

	return uint64(*peerCount), nil
}

func (w *NodeWorker) networkHealthy(ctx context.Context) (bool, uint64, uint64, error) {
	checkCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	client := w.client
	var temp *ethclient.Client
	needClient := false
	if w.headFn == nil {
		needClient = true
	}
	if client == nil && needClient {
		var err error
		temp, err = ethclient.DialContext(checkCtx, w.wsURL)
		if err != nil {
			return false, 0, 0, err
		}
		client = temp
		defer temp.Close()
	}

	var peerCount uint64
	var err error
	if w.peerCountFn != nil {
		peerCount, err = w.peerCountFn(checkCtx)
	} else {
		peerCtx, peerCancel := context.WithTimeout(checkCtx, dagRPCRequestTimeout)
		peerCount, err = w.dagPeerCount(peerCtx)
		peerCancel()
	}
	if err != nil {
		return false, 0, 0, err
	}

	var headNum uint64
	if w.headFn != nil {
		headNum, err = w.headFn(checkCtx)
	} else {
		if client == nil {
			return false, peerCount, 0, errors.New("network healthy: no client for head lookup")
		}
		head, herr := client.HeaderByNumber(checkCtx, nil)
		if herr != nil {
			return false, peerCount, 0, herr
		}
		if head == nil {
			return false, peerCount, 0, errors.New("network healthy: nil head")
		}
		headNum = head.Number.Uint64()
	}
	if err != nil {
		return false, peerCount, 0, err
	}

	requiredHeight := uint64(0)
	if progress := w.getProgress(); progress != nil {
		if math.MaxUint64-progress.BlockNumber < confirmationSafetyMargin {
			requiredHeight = math.MaxUint64
		} else {
			requiredHeight = progress.BlockNumber + confirmationSafetyMargin
		}
	} else {
		if headNum > confirmationSafetyMargin {
			requiredHeight = headNum - confirmationSafetyMargin
		}
	}

	shortfall := uint64(0)
	if headNum < requiredHeight {
		shortfall = requiredHeight - headNum
	}

	healthy := peerCount >= w.healthMinPeers && shortfall == 0
	return healthy, peerCount, shortfall, nil
}

func sleepWithContext(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return nil
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// watchExecuted watches on-chain events and triggers staged upgrades with promotion on success.
func (w *NodeWorker) watchExecuted(ctx context.Context) {
outer:
	for {
		if ctx.Err() != nil {
			log.Info("watchExecuted: context cancelled, exiting")
			return
		}

		if w.client != nil {
			w.client.Close()
			w.client = nil
		}
		client, err := ethclient.DialContext(ctx, w.wsURL)
		if err != nil {
			log.Error("watchExecuted: dial WS failed", "err", err)
			time.Sleep(2 * time.Second)
			continue
		}
		w.client = client

		progress := w.getProgress()
		if progress == nil {
			seeded, err := w.seedProgressFromHead(ctx)
			if err != nil {
				log.Error("watchExecuted: seed watermark failed", "err", err)
				time.Sleep(2 * time.Second)
				continue
			}
			progress = seeded
		}

		query := ethereum.FilterQuery{
			Addresses: []common.Address{w.contract},
			Topics:    [][]common.Hash{{w.eventID}},
		}
		if progress != nil {
			from := new(big.Int).SetUint64(progress.BlockNumber + 1)
			query.FromBlock = from
			log.Info("watchExecuted: resuming from watermark", "block", progress.BlockNumber, "txIndex", progressTxIndex(progress), "logIndex", progress.LogIndex)
		}

		logsCh := make(chan types.Log, 10)
		sub, err := w.client.SubscribeFilterLogs(ctx, query, logsCh)
		if err != nil {
			log.Error("watchExecuted: subscription failed", "err", err)
			time.Sleep(2 * time.Second)
			continue
		}
		log.Info("watchExecuted: subscription established")

	loop:
		for {
			select {
			case <-ctx.Done():
				sub.Unsubscribe()
				break outer
			case err := <-sub.Err():
				log.Error("watchExecuted: subscription error", "err", err)
				sub.Unsubscribe()
				break loop
			case lg := <-logsCh:
				if w.shouldSkipLog(lg) {
					log.Info("watchExecuted: skipping already-processed event", "block", lg.BlockNumber, "txIndex", lg.TxIndex, "logIndex", lg.Index)
					continue
				}
				if err := w.waitForConfirmations(ctx, lg.BlockNumber); err != nil {
					if ctx.Err() != nil {
						sub.Unsubscribe()
						break outer
					}
					log.Error("watchExecuted: confirmation wait failed", "err", err)
					sub.Unsubscribe()
					break loop
				}
				var ev struct {
					Link       string `abi:"link"`
					Sha256Hash string `abi:"sha256Hash"`
				}
				if err := w.eventParser.UnpackIntoInterface(&ev, "ProposalExecuted", lg.Data); err != nil {
					log.Error("watchExecuted: unpack failed", "err", err)
					continue
				}

				log.Info("watchExecuted: got event", "link", ev.Link, "sha256", ev.Sha256Hash)
				if err := writePending(ev.Link, ev.Sha256Hash); err != nil {
					log.Error("watchExecuted: write pending failed", "err", err)
				}

				// 1) download to temp (existing download function)
				tmpPath, err := downloadBinary(ctx, ev.Link, ev.Sha256Hash)
				if err != nil {
					log.Error("watchExecuted: download failed", "err", err)
					continue
				}
				if err := clearPending(); err != nil {
					log.Warn("watchExecuted: clear pending failed", "err", err)
				}

				// 2) stage inside the volume
				staged := stagePathFromSHA(ev.Sha256Hash)
				if err := copyFile(tmpPath, staged, 0o755); err != nil {
					log.Error("watchExecuted: stage copy failed", "err", err, "dst", staged)
					continue
				}

				// 3) restart with the staged binary
				if err := w.restartWithRollout(ctx, staged, lg); err != nil {
					if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
						log.Info("watchExecuted: restart aborted due to context cancellation", "err", err)
						sub.Unsubscribe()
						break outer
					}
					log.Error("watchExecuted: restart failed", "err", err)
					_ = os.RemoveAll(filepath.Dir(staged))
					// rollback to last good (active persisted or legacy)
					_ = w.RestartWith(fallbackBinary())
					sub.Unsubscribe()
					break loop
				}

				// 4) short soak to ensure stability
				if err := w.proveHealthy(ctx); err != nil {
					log.Error("watchExecuted: health soak failed", "err", err)
					_ = os.RemoveAll(filepath.Dir(staged))
					_ = w.RestartWith(fallbackBinary())
					sub.Unsubscribe()
					break loop
				}

				// 5) promote atomically and prune old staged dirs
				if err := promoteAndPrune(staged); err != nil {
					log.Error("watchExecuted: promote failed", "err", err)
					_ = os.RemoveAll(filepath.Dir(staged))
					_ = w.RestartWith(fallbackBinary())
					sub.Unsubscribe()
					break loop
				}

				log.Info("watchExecuted: upgrade promoted", "active", activePath)
				if err := w.recordProgress(ctx, lg); err != nil {
					log.Error("watchExecuted: persist progress failed", "err", err)
				}
				sub.Unsubscribe()
				break loop
			}
		}

		log.Info("watchExecuted: reconnecting in 2s")
		time.Sleep(2 * time.Second)
	}
}

func (w *NodeWorker) headNumber(ctx context.Context) (uint64, error) {
	if w.headFn != nil {
		return w.headFn(ctx)
	}
	if w.client == nil {
		return 0, errors.New("head number: client not initialized")
	}
	head, err := w.client.HeaderByNumber(ctx, nil)
	if err != nil {
		return 0, err
	}
	if head == nil {
		return 0, errors.New("head number: received nil head")
	}
	return head.Number.Uint64(), nil
}

func (w *NodeWorker) waitForConfirmations(ctx context.Context, blockNumber uint64) error {
	required := blockNumber
	if math.MaxUint64-blockNumber < confirmationSafetyMargin {
		required = math.MaxUint64
	} else {
		required = blockNumber + confirmationSafetyMargin
	}

	ticker := time.NewTicker(confirmationPollInterval)
	defer ticker.Stop()

	for {
		headNum, err := w.headNumber(ctx)
		if err != nil {
			log.Warn("watchExecuted: head poll failed", "err", err)
		} else if headNum >= required {
			return nil
		} else {
			log.Info("watchExecuted: waiting for confirmations", "block", blockNumber, "head", headNum, "required", required)
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func (w *NodeWorker) seedProgressFromHead(ctx context.Context) (*progressState, error) {
	if w.client == nil {
		return nil, errors.New("seed watermark: client not initialized")
	}
	head, err := w.client.HeaderByNumber(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("seed watermark: fetch head failed: %w", err)
	}
	if head == nil {
		return nil, errors.New("seed watermark: received nil head")
	}

	tip := head.Number.Uint64()
	seedBlock := uint64(0)
	if tip > confirmationSafetyMargin {
		seedBlock = tip - confirmationSafetyMargin
	}

	state := &progressState{
		BlockNumber: seedBlock,
		LogIndex:    math.MaxUint64,
		TxIndex:     uint64Ptr(math.MaxUint64),
	}
	if seedBlock == tip {
		state.BlockHash = head.Hash().Hex()
	}

	if err := saveProgress(state); err != nil {
		return nil, fmt.Errorf("seed watermark: persist failed: %w", err)
	}
	w.setProgress(state)
	log.Info("watchExecuted: seeded watermark from head", "tip", tip, "seedBlock", seedBlock, "margin", confirmationSafetyMargin)
	return state, nil
}

// validateNodeBinary checks that the binary exists and is executable.
// It is called before every child start.
func validateNodeBinary(path string) error {
	fi, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("binary not found: %w", err)
	}
	if fi.Mode()&0o100 == 0 {
		return fmt.Errorf("binary %s is not executable (mode %o)", path, fi.Mode())
	}
	// Verify the binary can execute by running --version.
	// Uses the process's PATH and toolchain to ensure compatibility.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, path, "--version")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("binary %s failed to execute: %w (output: %s)", path, err, strings.TrimSpace(string(output)))
	}
	log.Debug("child binary validated", "path", path, "version", strings.TrimSpace(string(output)))
	return nil
}
