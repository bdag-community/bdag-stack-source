package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum"
)

func TestNativeSyncETASectionEstimatesRemainingTime(t *testing.T) {
	var estimator syncProgressEstimator
	start := time.Unix(100, 0)

	first := nativeSyncStatusSection(&miningTemplateHealth{
		MainOrder:                  100,
		P2PBestPeerMainOrder:       200,
		P2PBestPeerLeadBlocks:      100,
		P2PConsensusPeerCount:      10,
		P2PFreshConsensusPeerCount: 8,
		P2PCurrent:                 false,
		P2PNearlySynced:            false,
		P2PMiningFresh:             false,
		ChainCurrent:               false,
	}, &estimator, start)
	if first.Status != "syncing" {
		t.Fatalf("first status = %q, want syncing", first.Status)
	}
	if first.ETASeconds != nil {
		t.Fatalf("first ETA should need a second sample, got %v", *first.ETASeconds)
	}

	second := nativeSyncStatusSection(&miningTemplateHealth{
		MainOrder:                  150,
		P2PBestPeerMainOrder:       200,
		P2PBestPeerLeadBlocks:      50,
		P2PConsensusPeerCount:      10,
		P2PFreshConsensusPeerCount: 8,
		P2PCurrent:                 false,
		P2PNearlySynced:            true,
		P2PMiningFresh:             false,
		ChainCurrent:               false,
	}, &estimator, start.Add(10*time.Second))
	if got := derefUint64(second.RemainingBlocks); got != 50 {
		t.Fatalf("remaining blocks = %d, want 50", got)
	}
	if second.RateBlocksPerSecond == nil || *second.RateBlocksPerSecond != 5 {
		t.Fatalf("rate blocks/s = %v, want 5", second.RateBlocksPerSecond)
	}
	if second.ETASeconds == nil || *second.ETASeconds != 10 {
		t.Fatalf("ETA seconds = %v, want 10", second.ETASeconds)
	}
	if second.ETAText != "10s" {
		t.Fatalf("ETA text = %q, want 10s", second.ETAText)
	}
}

func TestEVMSyncStatusSectionIncludesBlockAndStateETA(t *testing.T) {
	var blockEstimator syncProgressEstimator
	var stateEstimator syncProgressEstimator
	start := time.Unix(200, 0)

	first := evmSyncStatusSectionFromProgress(&ethereum.SyncProgress{
		CurrentBlock: 100,
		HighestBlock: 200,
		KnownStates:  1000,
		PulledStates: 100,
	}, nil, &blockEstimator, &stateEstimator, start, nil)
	if first.Status != "catching_up" {
		t.Fatalf("first status = %q, want catching_up", first.Status)
	}
	if got := derefUint64(first.RemainingStates); got != 900 {
		t.Fatalf("first remaining states = %d, want 900", got)
	}

	second := evmSyncStatusSectionFromProgress(&ethereum.SyncProgress{
		CurrentBlock: 150,
		HighestBlock: 200,
		KnownStates:  1000,
		PulledStates: 600,
	}, nil, &blockEstimator, &stateEstimator, start.Add(10*time.Second), nil)
	if second.RateBlocksPerSecond == nil || *second.RateBlocksPerSecond != 5 {
		t.Fatalf("block rate = %v, want 5", second.RateBlocksPerSecond)
	}
	if second.ETASeconds == nil || *second.ETASeconds != 10 {
		t.Fatalf("block ETA = %v, want 10", second.ETASeconds)
	}
	if second.RateStatesPerSecond == nil || *second.RateStatesPerSecond != 50 {
		t.Fatalf("state rate = %v, want 50", second.RateStatesPerSecond)
	}
	if second.StateETASeconds == nil || *second.StateETASeconds != 8 {
		t.Fatalf("state ETA = %v, want 8", second.StateETASeconds)
	}
}

func TestEVMSyncStatusDoesNotUseNativeTargetWhenEthSyncingIsFalse(t *testing.T) {
	var blockEstimator syncProgressEstimator
	var stateEstimator syncProgressEstimator
	now := time.Unix(300, 0)
	block := uint64(150)

	section := evmSyncStatusSectionFromProgress(nil, &block, &blockEstimator, &stateEstimator, now, nil)
	if section.Status != "synced" {
		t.Fatalf("status = %q, want synced", section.Status)
	}
	if got := derefUint64(section.TargetBlock); got != 150 {
		t.Fatalf("target block = %d, want 150", got)
	}
	if got := derefUint64(section.RemainingBlocks); got != 0 {
		t.Fatalf("remaining blocks = %d, want 0", got)
	}
	if section.EthSyncing == nil || *section.EthSyncing {
		t.Fatalf("eth_syncing = %v, want false", section.EthSyncing)
	}
}

func TestEVMSyncStatusHTTPProbeUsesGatewayEthSyncingProgress(t *testing.T) {
	var blockEstimator syncProgressEstimator
	var stateEstimator syncProgressEstimator
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Method string `json:"method"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		switch req.Method {
		case "eth_syncing":
			_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{"startingBlock":"0x0","currentBlock":"0x96","highestBlock":"0xc8"}}`))
		default:
			t.Fatalf("unexpected method %q", req.Method)
		}
	}))
	defer server.Close()

	progress, blockNumber, err := evmSyncProgressFromHTTP(context.Background(), server.URL)
	if err != nil {
		t.Fatalf("probe EVM sync: %v", err)
	}
	if blockNumber != nil {
		t.Fatalf("blockNumber = %v, want nil while eth_syncing has progress", *blockNumber)
	}

	section := evmSyncStatusSectionFromProgress(progress, blockNumber, &blockEstimator, &stateEstimator, time.Unix(350, 0), nil)
	if section.Status != "catching_up" {
		t.Fatalf("status = %q, want catching_up", section.Status)
	}
	if got := derefUint64(section.CurrentBlock); got != 150 {
		t.Fatalf("current block = %d, want 150", got)
	}
	if got := derefUint64(section.TargetBlock); got != 200 {
		t.Fatalf("target block = %d, want 200", got)
	}
	if got := derefUint64(section.RemainingBlocks); got != 50 {
		t.Fatalf("remaining blocks = %d, want 50", got)
	}
	if section.EthSyncing == nil || !*section.EthSyncing {
		t.Fatalf("eth_syncing = %v, want true", section.EthSyncing)
	}
}

func TestEVMSyncStatusHTTPProbeFallsBackToBlockNumberWhenEthSyncingFalse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Method string `json:"method"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		switch req.Method {
		case "eth_syncing":
			_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":false}`))
		case "eth_blockNumber":
			_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":"0x96"}`))
		default:
			t.Fatalf("unexpected method %q", req.Method)
		}
	}))
	defer server.Close()

	progress, blockNumber, err := evmSyncProgressFromHTTP(context.Background(), server.URL)
	if err != nil {
		t.Fatalf("probe EVM sync: %v", err)
	}
	if progress != nil {
		t.Fatalf("progress = %#v, want nil", progress)
	}
	if blockNumber == nil || *blockNumber != 150 {
		t.Fatalf("block number = %v, want 150", blockNumber)
	}
}

func TestNodeStatusLogPayloadJSONShape(t *testing.T) {
	payload := nodeStatusLogPayload{
		Event:     nodeStatusLogEventName,
		Schema:    nodeStatusLogSchema,
		Timestamp: time.Unix(400, 0).UTC().Format(time.RFC3339Nano),
		NativeSync: nodeStatusLogSection{
			Status:          "synced",
			Source:          "getTemplateHealth",
			RemainingBlocks: uint64Ptr(0),
			ETASeconds:      float64PtrForTest(0),
			ETAText:         "0s",
		},
		EVMSync: nodeStatusLogSection{
			Status:          "synced",
			Source:          "eth_syncing",
			EthSyncing:      boolPtr(false),
			RemainingBlocks: uint64Ptr(0),
			RemainingStates: uint64Ptr(0),
			ETASeconds:      float64PtrForTest(0),
			ETAText:         "0s",
			StateETASeconds: float64PtrForTest(0),
			StateETAText:    "0s",
		},
	}
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if decoded["event"] != nodeStatusLogEventName {
		t.Fatalf("event = %v, want %s", decoded["event"], nodeStatusLogEventName)
	}
	if decoded["schema"] != nodeStatusLogSchema {
		t.Fatalf("schema = %v, want %s", decoded["schema"], nodeStatusLogSchema)
	}
	if _, ok := decoded["native_sync"].(map[string]any); !ok {
		t.Fatalf("native_sync object missing: %#v", decoded["native_sync"])
	}
	if _, ok := decoded["evm_sync"].(map[string]any); !ok {
		t.Fatalf("evm_sync object missing: %#v", decoded["evm_sync"])
	}
}

func TestNodeStatusLogIntervalsUseSyncingIntervalForNativeOrEVMProgress(t *testing.T) {
	intervals := nodeStatusLogIntervals{
		Syncing: 5 * time.Second,
		Synced:  30 * time.Second,
	}
	if got := intervals.forStatus("synced", "synced"); got != 30*time.Second {
		t.Fatalf("synced interval = %s, want 30s", got)
	}
	if got := intervals.forStatus("syncing", "synced"); got != 5*time.Second {
		t.Fatalf("native syncing interval = %s, want 5s", got)
	}
	if got := intervals.forStatus("synced", "catching_up"); got != 5*time.Second {
		t.Fatalf("EVM catching-up interval = %s, want 5s", got)
	}
}

func derefUint64(v *uint64) uint64 {
	if v == nil {
		return 0
	}
	return *v
}

func float64PtrForTest(v float64) *float64 {
	return &v
}
