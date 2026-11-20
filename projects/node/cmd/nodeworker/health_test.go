package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestWaitForReadinessSucceeds(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) < 3 {
			http.Error(w, "not ready", http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	if err := waitForReadiness(ctx, server.URL, "", time.Second); err != nil {
		t.Fatalf("waitForReadiness returned error: %v", err)
	}

	if calls.Load() < 3 {
		t.Fatalf("expected at least three readiness probes, got %d", calls.Load())
	}
}

func TestWaitForReadinessTimeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "not ready", http.StatusServiceUnavailable)
	}))
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	timeout := 400 * time.Millisecond
	start := time.Now()
	err := waitForReadiness(ctx, server.URL, "", timeout)
	if err == nil {
		t.Fatal("expected waitForReadiness to return an error when timeout is reached")
	}
	if elapsed := time.Since(start); elapsed < 150*time.Millisecond {
		t.Fatalf("expected waitForReadiness to run for at least %v, got %v", 150*time.Millisecond, elapsed)
	}
}

func TestLivenessMonitorForcesShutdown(t *testing.T) {
	originalInterval := livenessPollInterval
	livenessPollInterval = 10 * time.Millisecond
	defer func() { livenessPollInterval = originalInterval }()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer server.Close()

	worker := &NodeWorker{
		liveURL:         server.URL,
		livenessTimeout: 50 * time.Millisecond,
	}
	worker.ctx, worker.cancel = context.WithCancel(context.Background())

	triggered := make(chan struct{})
	worker.onLivenessFailure = func() {
		select {
		case <-triggered:
		default:
			close(triggered)
		}
	}

	worker.startLivenessMonitor()

	select {
	case <-triggered:
	case <-time.After(time.Second):
		t.Fatal("expected liveness monitor to trigger shutdown")
	}

	worker.stopLivenessMonitor()
	if worker.cancel != nil {
		worker.cancel()
	}
}

func TestLivenessMonitorRecoversBeforeTimeout(t *testing.T) {
	originalInterval := livenessPollInterval
	livenessPollInterval = 10 * time.Millisecond
	defer func() { livenessPollInterval = originalInterval }()

	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) <= 3 {
			http.Error(w, "boom", http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	worker := &NodeWorker{
		liveURL:         server.URL,
		livenessTimeout: 200 * time.Millisecond,
	}
	worker.ctx, worker.cancel = context.WithCancel(context.Background())

	triggered := make(chan struct{}, 1)
	worker.onLivenessFailure = func() {
		triggered <- struct{}{}
	}

	worker.startLivenessMonitor()

	// Allow enough time for failures and subsequent recovery.
	time.Sleep(120 * time.Millisecond)

	select {
	case <-triggered:
		t.Fatal("liveness failure callback should not have fired after recovery")
	default:
	}

	worker.stopLivenessMonitor()
	if worker.cancel != nil {
		worker.cancel()
	}
}

func TestMiningReadinessMonitorForcesShutdown(t *testing.T) {
	originalInterval := livenessPollInterval
	livenessPollInterval = 10 * time.Millisecond
	defer func() { livenessPollInterval = originalInterval }()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	worker := &NodeWorker{
		liveURL:                server.URL,
		livenessTimeout:        time.Second,
		miningReadinessTimeout: 50 * time.Millisecond,
		miningReadinessGrace:   0,
		templateHealthFn: func(ctx context.Context) (*miningTemplateHealth, error) {
			return &miningTemplateHealth{
				MineableNow:       false,
				SubmitReady:       false,
				ReasonCode:        "template_expired",
				Reason:            "template expired",
				LatestTemplateSeq: 12,
			}, nil
		},
	}
	worker.ctx, worker.cancel = context.WithCancel(context.Background())

	triggered := make(chan struct{})
	worker.onLivenessFailure = func() {
		select {
		case <-triggered:
		default:
			close(triggered)
		}
	}

	worker.startLivenessMonitor()

	select {
	case <-triggered:
	case <-time.After(time.Second):
		t.Fatal("expected mining readiness monitor to trigger shutdown")
	}

	worker.stopLivenessMonitor()
	if worker.cancel != nil {
		worker.cancel()
	}
}

func TestNormalizedHealthProbeInterval(t *testing.T) {
	originalInterval := livenessPollInterval
	livenessPollInterval = 10 * time.Millisecond
	defer func() { livenessPollInterval = originalInterval }()

	if got := normalizedHealthProbeInterval(0); got != 10*time.Millisecond {
		t.Fatalf("zero interval should fall back to livenessPollInterval, got %v", got)
	}
	if got := normalizedHealthProbeInterval(50 * time.Millisecond); got != 250*time.Millisecond {
		t.Fatalf("small interval should be clamped, got %v", got)
	}
	if got := normalizedHealthProbeInterval(2 * time.Second); got != 2*time.Second {
		t.Fatalf("configured interval changed, got %v", got)
	}
}

func TestMiningReadinessMonitorRecoversBeforeTimeout(t *testing.T) {
	originalInterval := livenessPollInterval
	livenessPollInterval = 10 * time.Millisecond
	defer func() { livenessPollInterval = originalInterval }()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	var calls atomic.Int32
	worker := &NodeWorker{
		liveURL:                server.URL,
		livenessTimeout:        time.Second,
		miningReadinessTimeout: 200 * time.Millisecond,
		miningReadinessGrace:   0,
		templateHealthFn: func(ctx context.Context) (*miningTemplateHealth, error) {
			if calls.Add(1) <= 3 {
				return &miningTemplateHealth{
					MineableNow: false,
					SubmitReady: false,
					ReasonCode:  "template_expired",
					Reason:      "template expired",
				}, nil
			}
			return &miningTemplateHealth{
				MineableNow: true,
				SubmitReady: true,
				ReasonCode:  "ok",
				Reason:      "ready",
			}, nil
		},
	}
	worker.ctx, worker.cancel = context.WithCancel(context.Background())

	triggered := make(chan struct{}, 1)
	worker.onLivenessFailure = func() {
		triggered <- struct{}{}
	}

	worker.startLivenessMonitor()
	time.Sleep(120 * time.Millisecond)

	select {
	case <-triggered:
		t.Fatal("mining readiness failure callback should not have fired after recovery")
	default:
	}

	worker.stopLivenessMonitor()
	if worker.cancel != nil {
		worker.cancel()
	}
}

func TestMiningReadinessMonitorRespectsGrace(t *testing.T) {
	originalInterval := livenessPollInterval
	livenessPollInterval = 10 * time.Millisecond
	defer func() { livenessPollInterval = originalInterval }()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	worker := &NodeWorker{
		liveURL:                server.URL,
		livenessTimeout:        time.Second,
		miningReadinessTimeout: 30 * time.Millisecond,
		miningReadinessGrace:   time.Second,
		templateHealthFn: func(ctx context.Context) (*miningTemplateHealth, error) {
			return &miningTemplateHealth{
				MineableNow: false,
				SubmitReady: false,
				ReasonCode:  "template_expired",
				Reason:      "template expired",
			}, nil
		},
	}
	worker.ctx, worker.cancel = context.WithCancel(context.Background())

	triggered := make(chan struct{}, 1)
	worker.onLivenessFailure = func() {
		triggered <- struct{}{}
	}

	worker.startLivenessMonitor()
	time.Sleep(120 * time.Millisecond)

	select {
	case <-triggered:
		t.Fatal("mining readiness failure callback should not fire during grace")
	default:
	}

	worker.stopLivenessMonitor()
	if worker.cancel != nil {
		worker.cancel()
	}
}

func TestMiningReadinessMonitorIgnoresNonRestartEligibleState(t *testing.T) {
	originalInterval := livenessPollInterval
	livenessPollInterval = 10 * time.Millisecond
	defer func() { livenessPollInterval = originalInterval }()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	worker := &NodeWorker{
		liveURL:                server.URL,
		livenessTimeout:        time.Second,
		miningReadinessTimeout: 30 * time.Millisecond,
		miningReadinessGrace:   0,
		templateHealthFn: func(ctx context.Context) (*miningTemplateHealth, error) {
			return &miningTemplateHealth{
				MineableNow: false,
				SubmitReady: false,
				ReasonCode:  "node_syncing",
				Reason:      "node syncing",
			}, nil
		},
	}
	worker.ctx, worker.cancel = context.WithCancel(context.Background())

	triggered := make(chan struct{}, 1)
	worker.onLivenessFailure = func() {
		triggered <- struct{}{}
	}

	worker.startLivenessMonitor()
	time.Sleep(120 * time.Millisecond)

	select {
	case <-triggered:
		t.Fatal("non restart-eligible readiness state should not force shutdown")
	default:
	}

	worker.stopLivenessMonitor()
	if worker.cancel != nil {
		worker.cancel()
	}
}

func TestMiningReadinessMonitorIgnoresProbeErrors(t *testing.T) {
	originalInterval := livenessPollInterval
	livenessPollInterval = 10 * time.Millisecond
	defer func() { livenessPollInterval = originalInterval }()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	worker := &NodeWorker{
		liveURL:                server.URL,
		livenessTimeout:        time.Second,
		miningReadinessTimeout: 30 * time.Millisecond,
		miningReadinessGrace:   0,
		templateHealthFn: func(ctx context.Context) (*miningTemplateHealth, error) {
			return nil, context.DeadlineExceeded
		},
	}
	worker.ctx, worker.cancel = context.WithCancel(context.Background())

	triggered := make(chan struct{}, 1)
	worker.onLivenessFailure = func() {
		triggered <- struct{}{}
	}

	worker.startLivenessMonitor()
	time.Sleep(120 * time.Millisecond)

	select {
	case <-triggered:
		t.Fatal("probe transport errors should not force shutdown")
	default:
	}

	worker.stopLivenessMonitor()
	if worker.cancel != nil {
		worker.cancel()
	}
}
