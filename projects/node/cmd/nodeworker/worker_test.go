package main

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
)

func withPersistRoot(t *testing.T) string {
	t.Helper()
	previous := persistDir
	dir := t.TempDir()
	configurePersistRoot(dir)
	t.Cleanup(func() {
		configurePersistRoot(previous)
	})
	return dir
}

func TestWaitForConfirmationsDelaysUntilHead(t *testing.T) {
	originalInterval := confirmationPollInterval
	interval := 10 * time.Millisecond
	confirmationPollInterval = interval
	defer func() { confirmationPollInterval = originalInterval }()

	blockNumber := uint64(100)
	required := blockNumber + confirmationSafetyMargin

	var calls atomic.Int32
	values := []uint64{blockNumber, required - 1, required}
	worker := &NodeWorker{
		headFn: func(ctx context.Context) (uint64, error) {
			idx := int(calls.Add(1) - 1)
			if idx >= len(values) {
				return values[len(values)-1], nil
			}
			return values[idx], nil
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	start := time.Now()
	if err := worker.waitForConfirmations(ctx, blockNumber); err != nil {
		t.Fatalf("waitForConfirmations returned error: %v", err)
	}

	if calls.Load() < 2 {
		t.Fatalf("expected at least two head polls, got %d", calls.Load())
	}

	if elapsed := time.Since(start); elapsed < interval {
		t.Fatalf("expected wait to last at least %v, got %v", interval, elapsed)
	}
}

func TestNetworkHealthyAllowsZeroPeers(t *testing.T) {
	worker := &NodeWorker{
		healthMinPeers: 0,
		peerCountFn: func(ctx context.Context) (uint64, error) {
			return 0, nil
		},
		headFn: func(ctx context.Context) (uint64, error) {
			return confirmationSafetyMargin + 10, nil
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	healthy, peers, shortfall, err := worker.networkHealthy(ctx)
	if err != nil {
		t.Fatalf("networkHealthy returned error: %v", err)
	}
	if !healthy {
		t.Fatalf("expected healthy network with zero peer requirement, got unhealthy (peers=%d shortfall=%d)", peers, shortfall)
	}
	if peers != 0 {
		t.Fatalf("expected zero peers, got %d", peers)
	}
	if shortfall != 0 {
		t.Fatalf("expected zero confirmation shortfall, got %d", shortfall)
	}
}

func TestNetworkHealthyConfirmationShortfall(t *testing.T) {
	blockNumber := uint64(500)
	headNum := blockNumber + confirmationSafetyMargin - 5

	worker := &NodeWorker{
		healthMinPeers: 1,
		peerCountFn: func(ctx context.Context) (uint64, error) {
			return 10, nil
		},
		headFn: func(ctx context.Context) (uint64, error) {
			return headNum, nil
		},
	}
	worker.setProgress(&progressState{BlockNumber: blockNumber})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	healthy, peers, shortfall, err := worker.networkHealthy(ctx)
	if err != nil {
		t.Fatalf("networkHealthy returned error: %v", err)
	}
	if healthy {
		t.Fatalf("expected network to be unhealthy due to confirmation shortfall")
	}
	if peers != 10 {
		t.Fatalf("unexpected peer count: got %d want %d", peers, 10)
	}
	expectedShortfall := (blockNumber + confirmationSafetyMargin) - headNum
	if shortfall != expectedShortfall {
		t.Fatalf("unexpected shortfall: got %d want %d", shortfall, expectedShortfall)
	}
}

func TestNetworkHealthyFallbackUsesDAGRSuccess(t *testing.T) {
	t.Parallel()

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		if r.Method != http.MethodPost {
			t.Fatalf("unexpected method: %s", r.Method)
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("failed to read body: %v", err)
		}
		if !strings.Contains(string(body), "getNetworkInfo") {
			t.Fatalf("expected getNetworkInfo call, got %s", string(body))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{"totalconnected":5}}`))
	})
	server := httptest.NewServer(handler)
	defer server.Close()

	client := server.Client()
	client.Timeout = time.Second

	worker := &NodeWorker{
		healthMinPeers: 2,
		headFn: func(ctx context.Context) (uint64, error) {
			return confirmationSafetyMargin + 10, nil
		},
		dagRPCURL:    server.URL,
		dagRPCClient: client,
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	healthy, peers, shortfall, err := worker.networkHealthy(ctx)
	if err != nil {
		t.Fatalf("networkHealthy returned error: %v", err)
	}
	if !healthy {
		t.Fatalf("expected healthy network, got unhealthy")
	}
	if peers != 5 {
		t.Fatalf("unexpected peer count: got %d want %d", peers, 5)
	}
	if shortfall != 0 {
		t.Fatalf("expected zero confirmation shortfall, got %d", shortfall)
	}
}

func TestNetworkHealthyFallbackHandlesDAGError(t *testing.T) {
	t.Parallel()

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"error":{"code":-1,"message":"boom"}}`))
	})
	server := httptest.NewServer(handler)
	defer server.Close()

	client := server.Client()
	client.Timeout = time.Second

	worker := &NodeWorker{
		headFn: func(ctx context.Context) (uint64, error) {
			return confirmationSafetyMargin + 10, nil
		},
		dagRPCURL:    server.URL,
		dagRPCClient: client,
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	_, _, _, err := worker.networkHealthy(ctx)
	if err == nil {
		t.Fatalf("expected error from dag rpc")
	}
	if !strings.Contains(err.Error(), "boom") {
		t.Fatalf("expected dag rpc error to propagate, got %v", err)
	}
}

func TestDagPeerCountUsesBasicAuth(t *testing.T) {
	t.Parallel()

	const user = "rpcuser"
	const pass = "rpcpass"

	var authHeader string
	var forwardedBy string
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		authHeader = r.Header.Get("Authorization")
		forwardedBy = r.Header.Get("X-Forwarded-By")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{"totalconnected":3}}`))
	})
	server := httptest.NewServer(handler)
	defer server.Close()

	client := server.Client()
	client.Timeout = time.Second

	worker := &NodeWorker{
		dagRPCURL:    server.URL,
		dagRPCClient: client,
		dagRPCUser:   user,
		dagRPCPass:   pass,
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	peers, err := worker.dagPeerCount(ctx)
	if err != nil {
		t.Fatalf("dagPeerCount returned error: %v", err)
	}
	if peers != 3 {
		t.Fatalf("unexpected peer count: got %d want %d", peers, 3)
	}

	expected := "Basic " + base64.StdEncoding.EncodeToString([]byte(user+":"+pass))
	if authHeader != expected {
		t.Fatalf("expected auth header %q, got %q", expected, authHeader)
	}
	if forwardedBy != "nodeworker" {
		t.Fatalf("expected X-Forwarded-By nodeworker, got %q", forwardedBy)
	}
}

func TestDagPeerCountSumsNetworkInfoBuckets(t *testing.T) {
	t.Parallel()

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{"infos":[{"connecteds":2},{"connecteds":4}]}}`))
	})
	server := httptest.NewServer(handler)
	defer server.Close()

	client := server.Client()
	client.Timeout = time.Second

	worker := &NodeWorker{
		dagRPCURL:    server.URL,
		dagRPCClient: client,
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	peers, err := worker.dagPeerCount(ctx)
	if err != nil {
		t.Fatalf("dagPeerCount returned error: %v", err)
	}
	if peers != 6 {
		t.Fatalf("unexpected peer count: got %d want %d", peers, 6)
	}
}

func TestDagTemplateHealthUsesBasicAuth(t *testing.T) {
	t.Parallel()

	const user = "rpcuser"
	const pass = "rpcpass"

	var authHeader string
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		authHeader = r.Header.Get("Authorization")
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("failed to read body: %v", err)
		}
		if !strings.Contains(string(body), "getTemplateHealth") {
			t.Fatalf("expected getTemplateHealth call, got %s", string(body))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{"mineable_now":true,"submit_ready":true,"reason_code":"ok","reason":"ready","template_usable":true,"template_age_ms":12,"last_template_build_age_ms":56,"last_template_build_duration_ms":34,"last_template_build_reason":"periodic","pending_template_build":true,"pending_template_build_reason":"mempool_change","pending_template_build_priority":10,"latest_template_seq":42,"p2p_mining_fresh":false,"p2p_mining_fresh_reason_code":"sync_peer_stale","p2p_consensus_peer_count":3,"p2p_fresh_consensus_peer_count":2,"p2p_stale_consensus_peer_count":1,"p2p_best_peer_lead_blocks":4,"p2p_sync_peer_present":true,"p2p_sync_peer_fresh":false,"p2p_sync_peer_graph_state_age_ms":5000}}`))
	})
	server := httptest.NewServer(handler)
	defer server.Close()

	client := server.Client()
	client.Timeout = time.Second

	worker := &NodeWorker{
		dagRPCURL:    server.URL,
		dagRPCClient: client,
		dagRPCUser:   user,
		dagRPCPass:   pass,
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	health, err := worker.dagTemplateHealth(ctx)
	if err != nil {
		t.Fatalf("dagTemplateHealth returned error: %v", err)
	}
	if health == nil || !health.MineableNow || !health.SubmitReady || health.LatestTemplateSeq != 42 {
		t.Fatalf("unexpected template health: %#v", health)
	}
	if health.TemplateAgeMs != 12 ||
		health.LastTemplateBuildAgeMs != 56 ||
		health.LastTemplateBuildDurationMs != 34 ||
		health.LastTemplateBuildReason != "periodic" ||
		!health.PendingTemplateBuild ||
		health.PendingTemplateBuildReason != "mempool_change" ||
		health.PendingTemplateBuildPriority != 10 ||
		health.P2PMiningFresh ||
		health.P2PMiningFreshReasonCode != "sync_peer_stale" ||
		health.P2PConsensusPeerCount != 3 ||
		health.P2PFreshConsensusPeerCount != 2 ||
		health.P2PStaleConsensusPeerCount != 1 ||
		health.P2PBestPeerLeadBlocks != 4 ||
		!health.P2PSyncPeerPresent ||
		health.P2PSyncPeerFresh ||
		health.P2PSyncPeerGraphStateAgeMs != 5000 {
		t.Fatalf("unexpected advisory template health fields: %#v", health)
	}

	expected := "Basic " + base64.StdEncoding.EncodeToString([]byte(user+":"+pass))
	if authHeader != expected {
		t.Fatalf("expected auth header %q, got %q", expected, authHeader)
	}
}

func TestProbeMiningReadinessUnsupportedHealthIsWarningOnly(t *testing.T) {
	t.Parallel()

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"error":{"code":-32601,"message":"method not found"}}`))
	})
	server := httptest.NewServer(handler)
	defer server.Close()

	client := server.Client()
	client.Timeout = time.Second

	worker := &NodeWorker{
		dagRPCURL:    server.URL,
		dagRPCClient: client,
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	probe := worker.probeMiningReadiness(ctx)
	if probe.err == nil {
		t.Fatal("expected unsupported getTemplateHealth probe error")
	}
	if probe.restartEligible {
		t.Fatalf("unsupported getTemplateHealth should be warning-only, got %#v", probe)
	}
}

func TestMiningTemplateHealthReadinessError(t *testing.T) {
	ready := &miningTemplateHealth{MineableNow: true, SubmitReady: true}
	if probe := ready.readinessFailure(); probe.err != nil || probe.restartEligible {
		t.Fatalf("ready health returned probe: %#v", probe)
	}

	readyButStaleP2P := &miningTemplateHealth{
		MineableNow:              true,
		SubmitReady:              true,
		ReasonCode:               "ok",
		Reason:                   "submit-without-sync override is enabled",
		P2PMiningFresh:           false,
		P2PMiningFreshReasonCode: "no_consensus_peers",
		P2PConsensusPeerCount:    0,
	}
	probe := readyButStaleP2P.readinessFailure()
	if probe.err == nil || !strings.Contains(probe.err.Error(), "p2p_mining_fresh=false") || probe.restartEligible {
		t.Fatalf("expected warning-only stale p2p readiness error, got %#v", probe)
	}

	unready := &miningTemplateHealth{
		MineableNow: false,
		SubmitReady: true,
		ReasonCode:  "template_expired",
		Reason:      "template expired",
	}
	probe = unready.readinessFailure()
	if probe.err == nil || !strings.Contains(probe.err.Error(), "template_expired") || !probe.restartEligible {
		t.Fatalf("expected restart-eligible template_expired readiness error, got %#v", probe)
	}

	syncing := &miningTemplateHealth{
		MineableNow: false,
		SubmitReady: false,
		ReasonCode:  "node_syncing",
		Reason:      "node syncing",
	}
	probe = syncing.readinessFailure()
	if probe.err == nil || probe.restartEligible {
		t.Fatalf("expected warning-only node_syncing readiness error, got %#v", probe)
	}

	nonBlockingBuild := &miningTemplateHealth{
		MineableNow:               false,
		SubmitReady:               false,
		ReasonCode:                "template_build_failed",
		Reason:                    "transient build error",
		LastTemplateBuildBlocking: false,
	}
	probe = nonBlockingBuild.readinessFailure()
	if probe.err == nil || probe.restartEligible {
		t.Fatalf("expected non-blocking build error to be warning-only, got %#v", probe)
	}
	if strings.Contains(probe.err.Error(), "last_template_build_duration_ms") {
		t.Fatalf("zero duration should not add noisy context, got %v", probe.err)
	}

	blockingBuild := &miningTemplateHealth{
		MineableNow:               false,
		SubmitReady:               false,
		ReasonCode:                "template_build_failed",
		Reason:                    "blocking build error",
		LastTemplateBuildBlocking: true,
	}
	probe = blockingBuild.readinessFailure()
	if probe.err == nil || !probe.restartEligible {
		t.Fatalf("expected blocking build error to be restart-eligible, got %#v", probe)
	}

	nonBlockingPending := &miningTemplateHealth{
		MineableNow:               false,
		SubmitReady:               false,
		ReasonCode:                "evm_pending_template_unavailable",
		Reason:                    "fresh last-good template remains usable",
		LastTemplateBuildBlocking: false,
	}
	probe = nonBlockingPending.readinessFailure()
	if probe.err == nil || probe.restartEligible {
		t.Fatalf("expected non-blocking EVM pending error to be warning-only, got %#v", probe)
	}

	blockingPending := &miningTemplateHealth{
		MineableNow:               false,
		SubmitReady:               false,
		ReasonCode:                "evm_pending_template_unavailable",
		Reason:                    "no usable EVM pending template",
		LastTemplateBuildBlocking: true,
	}
	probe = blockingPending.readinessFailure()
	if probe.err == nil || !probe.restartEligible {
		t.Fatalf("expected blocking EVM pending error to be restart-eligible, got %#v", probe)
	}

	advisoryContext := &miningTemplateHealth{
		MineableNow:                  false,
		SubmitReady:                  false,
		ReasonCode:                   "node_syncing",
		Reason:                       "node syncing",
		TemplateAgeMs:                1500,
		LastGoodTemplateAgeMs:        900,
		LastTemplateBuildAgeMs:       2100,
		LastTemplateBuildDurationMs:  87,
		LastTemplateBuildReason:      "periodic refresh",
		LastTemplateBuildErrorCode:   "mempool_not_ready",
		PendingTemplateBuild:         true,
		PendingTemplateBuildReason:   "parents_changed",
		PendingTemplateBuildPriority: 30,
		P2PMiningFresh:               false,
		P2PMiningFreshReasonCode:     "sync_peer_stale",
		P2PConsensusPeerCount:        3,
		P2PFreshConsensusPeerCount:   2,
		P2PStaleConsensusPeerCount:   1,
		P2PBestPeerLeadBlocks:        4,
		P2PBestPeerGraphStateAgeMs:   500,
		P2PAvgGraphStateAgeMs:        750,
		P2PMaxGraphStateAgeMs:        1200,
		P2PSyncPeerPresent:           true,
		P2PSyncPeerFresh:             false,
		P2PSyncPeerGraphStateAgeMs:   1400,
	}
	probe = advisoryContext.readinessFailure()
	if probe.err == nil || probe.restartEligible {
		t.Fatalf("expected advisory context to remain warning-only, got %#v", probe)
	}
	for _, want := range []string{
		"template_age_ms=1500",
		"last_good_template_age_ms=900",
		"last_template_build_age_ms=2100",
		"last_template_build_duration_ms=87",
		"last_template_build_reason=periodic refresh",
		"last_template_build_error_code=mempool_not_ready",
		"pending_template_build=true",
		"pending_template_build_reason=parents_changed",
		"pending_template_build_priority=30",
		"p2p_mining_fresh=false",
		"p2p_mining_fresh_reason_code=sync_peer_stale",
		"p2p_consensus_peer_count=3",
		"p2p_sync_peer_fresh=false",
		"p2p_sync_peer_graph_state_age_ms=1400",
	} {
		if !strings.Contains(probe.err.Error(), want) {
			t.Fatalf("readiness context missing %q in %q", want, probe.err.Error())
		}
	}
}

func TestDeterministicOffsetVariesByUpgrade(t *testing.T) {
	nodeSum := sha256.Sum256([]byte("node-1"))
	worker := &NodeWorker{
		rolloutWindow: 30 * time.Minute,
		nodeID:        nodeSum[:],
	}
	upgradeA := sha256.Sum256([]byte("upgrade-A"))
	upgradeB := sha256.Sum256([]byte("upgrade-B"))

	offsetA1 := worker.deterministicOffset(upgradeA)
	offsetA2 := worker.deterministicOffset(upgradeA)
	if offsetA1 != offsetA2 {
		t.Fatalf("expected deterministic offset, got %v and %v", offsetA1, offsetA2)
	}
	if offsetA1 < 0 || offsetA1 >= worker.rolloutWindow {
		t.Fatalf("offset %v out of window %v", offsetA1, worker.rolloutWindow)
	}

	offsetB := worker.deterministicOffset(upgradeB)
	if offsetB < 0 || offsetB >= worker.rolloutWindow {
		t.Fatalf("offset %v out of window %v", offsetB, worker.rolloutWindow)
	}
	if offsetA1 == offsetB {
		t.Fatalf("expected different offsets for different upgrades, got %v", offsetA1)
	}
}

func TestDeterministicOffsetZeroWindow(t *testing.T) {
	worker := &NodeWorker{}
	upgrade := sha256.Sum256([]byte("upgrade"))
	if offset := worker.deterministicOffset(upgrade); offset != 0 {
		t.Fatalf("expected zero offset for disabled window, got %v", offset)
	}
}

func TestRecordProgressWaitsForConfirmations(t *testing.T) {
	originalInterval := confirmationPollInterval
	interval := 10 * time.Millisecond
	confirmationPollInterval = interval
	defer func() { confirmationPollInterval = originalInterval }()

	root := withPersistRoot(t)
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatalf("failed to create persist dir: %v", err)
	}
	progressPath := progressFilePath()
	_ = os.Remove(progressPath)
	defer os.Remove(progressPath)

	blockNumber := uint64(200)
	required := blockNumber + confirmationSafetyMargin

	var head atomic.Uint64
	head.Store(required - 1)

	worker := &NodeWorker{
		headFn: func(ctx context.Context) (uint64, error) {
			return head.Load(), nil
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	log := types.Log{
		BlockNumber: blockNumber,
		BlockHash:   common.HexToHash("0x1"),
		Index:       0,
		TxIndex:     0,
		TxHash:      common.HexToHash("0x2"),
	}

	done := make(chan struct{})
	go func() {
		time.Sleep(2 * interval)
		head.Store(required)
		close(done)
	}()

	start := time.Now()
	if err := worker.recordProgress(ctx, log); err != nil {
		t.Fatalf("recordProgress returned error: %v", err)
	}

	if elapsed := time.Since(start); elapsed < interval {
		t.Fatalf("expected recordProgress to wait at least %v, got %v", interval, elapsed)
	}

	state := worker.getProgress()
	if state == nil {
		t.Fatal("expected progress state to be set")
	}
	if state.BlockNumber != blockNumber {
		t.Fatalf("unexpected block number: got %d want %d", state.BlockNumber, blockNumber)
	}

	select {
	case <-done:
	default:
		t.Fatal("head update goroutine did not run")
	}
}

func TestRecordProgressContextCancel(t *testing.T) {
	originalInterval := confirmationPollInterval
	interval := 10 * time.Millisecond
	confirmationPollInterval = interval
	defer func() { confirmationPollInterval = originalInterval }()

	root := withPersistRoot(t)
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatalf("failed to create persist dir: %v", err)
	}
	progressPath := progressFilePath()
	_ = os.Remove(progressPath)
	defer os.Remove(progressPath)

	blockNumber := uint64(300)
	worker := &NodeWorker{
		headFn: func(ctx context.Context) (uint64, error) {
			return blockNumber, nil
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), interval)
	defer cancel()

	log := types.Log{
		BlockNumber: blockNumber,
		BlockHash:   common.HexToHash("0x3"),
		Index:       0,
		TxIndex:     0,
		TxHash:      common.HexToHash("0x4"),
	}

	err := worker.recordProgress(ctx, log)
	if err == nil {
		t.Fatal("expected error when context cancelled")
	}
	if !errors.Is(err, context.DeadlineExceeded) && !errors.Is(err, context.Canceled) {
		t.Fatalf("unexpected error: %v", err)
	}

	if state := worker.getProgress(); state != nil {
		t.Fatalf("expected no progress to be recorded, got %+v", state)
	}

	if _, statErr := os.Stat(progressPath); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("expected no progress file, got error %v", statErr)
	}
}

func TestNodeWorkerEffectiveShutdownTimeout(t *testing.T) {
	tests := []struct {
		name string
		got  time.Duration
		want time.Duration
	}{
		{name: "default for zero", got: (&NodeWorker{}).effectiveShutdownTimeout(), want: defaultShutdownTimeout},
		{name: "default for negative", got: (&NodeWorker{shutdownTimeout: -time.Second}).effectiveShutdownTimeout(), want: defaultShutdownTimeout},
		{name: "configured", got: (&NodeWorker{shutdownTimeout: 7 * time.Minute}).effectiveShutdownTimeout(), want: 7 * time.Minute},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if test.got != test.want {
				t.Fatalf("effective shutdown timeout = %s, want %s", test.got, test.want)
			}
		})
	}
}
