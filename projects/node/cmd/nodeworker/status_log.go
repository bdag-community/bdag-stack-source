package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/ethclient"
)

const (
	nodeStatusLogEventName = "node_status_log"
	nodeStatusLogSchema    = "bdag.nodeworker.status_log.v1"
)

type nodeStatusLogPayload struct {
	Event      string               `json:"event"`
	Schema     string               `json:"schema"`
	Timestamp  string               `json:"timestamp"`
	NativeSync nodeStatusLogSection `json:"native_sync"`
	EVMSync    nodeStatusLogSection `json:"evm_sync"`
}

type nodeStatusLogSection struct {
	Status              string   `json:"status"`
	Source              string   `json:"source"`
	CurrentBlock        *uint64  `json:"current_block"`
	TargetBlock         *uint64  `json:"target_block"`
	RemainingBlocks     *uint64  `json:"remaining_blocks"`
	RateBlocksPerSecond *float64 `json:"rate_blocks_per_second"`
	ETASeconds          *float64 `json:"eta_seconds"`
	ETAText             string   `json:"eta_text"`
	Error               string   `json:"error,omitempty"`

	PeerCount                  *int   `json:"peer_count,omitempty"`
	FreshPeerCount             *int   `json:"fresh_peer_count,omitempty"`
	StalePeerCount             *int   `json:"stale_peer_count,omitempty"`
	P2PCurrent                 *bool  `json:"p2p_current,omitempty"`
	P2PNearlySynced            *bool  `json:"p2p_nearly_synced,omitempty"`
	P2PMiningFresh             *bool  `json:"p2p_mining_fresh,omitempty"`
	P2PBestPeerLeadBlocks      *int64 `json:"p2p_best_peer_lead_blocks,omitempty"`
	P2PBestPeerGraphStateAgeMs *int64 `json:"p2p_best_peer_graph_state_age_ms,omitempty"`
	P2PMiningFreshReasonCode   string `json:"p2p_mining_fresh_reason_code,omitempty"`
	ChainCurrent               *bool  `json:"chain_current,omitempty"`
	ReasonCode                 string `json:"reason_code,omitempty"`
	Reason                     string `json:"reason,omitempty"`

	EthSyncing             *bool    `json:"eth_syncing,omitempty"`
	StartingBlock          *uint64  `json:"starting_block,omitempty"`
	KnownStates            *uint64  `json:"known_states,omitempty"`
	PulledStates           *uint64  `json:"pulled_states,omitempty"`
	RemainingStates        *uint64  `json:"remaining_states,omitempty"`
	RateStatesPerSecond    *float64 `json:"rate_states_per_second,omitempty"`
	StateETASeconds        *float64 `json:"state_eta_seconds,omitempty"`
	StateETAText           string   `json:"state_eta_text,omitempty"`
	HealingTrienodes       *uint64  `json:"healing_trienodes,omitempty"`
	HealingBytecodes       *uint64  `json:"healing_bytecodes,omitempty"`
	TxIndexRemainingBlocks *uint64  `json:"tx_index_remaining_blocks,omitempty"`
	TxIndexFinishedBlocks  *uint64  `json:"tx_index_finished_blocks,omitempty"`
}

type syncProgressEstimator struct {
	lastAt        time.Time
	lastCurrent   *uint64
	lastRemaining *uint64
}

func (w *NodeWorker) maybeLogNodeStatus(ctx context.Context, health *miningTemplateHealth, intervals nodeStatusLogIntervals) {
	now := time.Now()
	w.statusLogMu.Lock()
	defer w.statusLogMu.Unlock()

	var healthErr error
	if health == nil {
		health, healthErr = w.statusLogTemplateHealth(ctx)
	}
	native := nativeSyncStatusSectionFromProbe(health, healthErr, &w.statusLogNative, now)
	evm := w.evmSyncStatusSection(ctx, now)

	interval := intervals.forStatus(native.Status, evm.Status)
	if interval <= 0 {
		return
	}
	signature := native.Status + "|" + evm.Status
	if !w.statusLogLastLog.IsZero() && now.Sub(w.statusLogLastLog) < interval && signature == w.statusLogLastSignature {
		return
	}

	payload := nodeStatusLogPayload{
		Event:      nodeStatusLogEventName,
		Schema:     nodeStatusLogSchema,
		Timestamp:  now.UTC().Format(time.RFC3339Nano),
		NativeSync: native,
		EVMSync:    evm,
	}

	data, err := json.Marshal(payload)
	if err != nil {
		fmt.Fprintf(os.Stderr, `{"event":"%s","schema":"%s","timestamp":"%s","error":"marshal failed: %s"}`+"\n",
			nodeStatusLogEventName, nodeStatusLogSchema, now.UTC().Format(time.RFC3339Nano), err)
		return
	}
	w.statusLogLastLog = now
	w.statusLogLastSignature = signature
	fmt.Fprintln(os.Stderr, string(data))
}

type nodeStatusLogIntervals struct {
	Syncing time.Duration
	Synced  time.Duration
}

func (i nodeStatusLogIntervals) forStatus(nativeStatus, evmStatus string) time.Duration {
	if syncStatusActive(nativeStatus) || syncStatusActive(evmStatus) {
		return i.Syncing
	}
	return i.Synced
}

func syncStatusActive(status string) bool {
	switch status {
	case "syncing", "catching_up", "unknown":
		return true
	default:
		return false
	}
}

func (w *NodeWorker) statusLogTemplateHealth(ctx context.Context) (*miningTemplateHealth, error) {
	if w.templateHealthFn != nil {
		return w.templateHealthFn(ctx)
	}
	probeCtx, cancel := context.WithTimeout(ctx, dagRPCRequestTimeout)
	defer cancel()
	return w.dagTemplateHealth(probeCtx)
}

func nativeSyncStatusSection(health *miningTemplateHealth, estimator *syncProgressEstimator, now time.Time) nodeStatusLogSection {
	return nativeSyncStatusSectionFromProbe(health, nil, estimator, now)
}

func nativeSyncStatusSectionFromProbe(health *miningTemplateHealth, probeErr error, estimator *syncProgressEstimator, now time.Time) nodeStatusLogSection {
	section := nodeStatusLogSection{
		Status:  "unknown",
		Source:  "getTemplateHealth",
		ETAText: "unknown",
	}
	if health == nil {
		if probeErr != nil {
			section.Error = probeErr.Error()
		} else {
			section.Error = "missing getTemplateHealth result"
		}
		return section
	}
	if probeErr != nil {
		section.Error = probeErr.Error()
	}

	current := health.MainOrder
	target := health.P2PBestPeerMainOrder
	if health.P2PBestPeerLeadBlocks > 0 {
		lead := uint64(health.P2PBestPeerLeadBlocks)
		if target < current || target == 0 {
			target = current + lead
		}
	}
	if target < current {
		target = current
	}

	remaining := uint64(0)
	if health.P2PBestPeerLeadBlocks > 0 {
		remaining = uint64(health.P2PBestPeerLeadBlocks)
	} else if target > current {
		remaining = target - current
	}

	status := "synced"
	if remaining > 0 || !health.ChainCurrent || !health.P2PCurrent || !health.P2PMiningFresh {
		status = "syncing"
	}

	currentPtr := uint64Ptr(current)
	remainingPtr := uint64Ptr(remaining)
	rate, eta, etaText := estimator.estimate(now, currentPtr, remainingPtr)

	section.Status = status
	section.CurrentBlock = currentPtr
	section.TargetBlock = uint64Ptr(target)
	section.RemainingBlocks = remainingPtr
	section.RateBlocksPerSecond = rate
	section.ETASeconds = eta
	section.ETAText = etaText
	section.PeerCount = intPtr(health.P2PConsensusPeerCount)
	section.FreshPeerCount = intPtr(health.P2PFreshConsensusPeerCount)
	section.StalePeerCount = intPtr(health.P2PStaleConsensusPeerCount)
	section.P2PCurrent = boolPtr(health.P2PCurrent)
	section.P2PNearlySynced = boolPtr(health.P2PNearlySynced)
	section.P2PMiningFresh = boolPtr(health.P2PMiningFresh)
	section.P2PBestPeerLeadBlocks = int64Ptr(health.P2PBestPeerLeadBlocks)
	section.P2PBestPeerGraphStateAgeMs = int64Ptr(health.P2PBestPeerGraphStateAgeMs)
	section.P2PMiningFreshReasonCode = health.P2PMiningFreshReasonCode
	section.ChainCurrent = boolPtr(health.ChainCurrent)
	section.ReasonCode = health.ReasonCode
	section.Reason = health.Reason
	return section
}

func (w *NodeWorker) evmSyncStatusSection(ctx context.Context, now time.Time) nodeStatusLogSection {
	var httpProbeErr error
	if w.evmRPCURL != "" {
		syncCtx, cancel := context.WithTimeout(ctx, dagRPCRequestTimeout)
		progress, blockNumber, err := evmSyncProgressFromHTTP(syncCtx, w.evmRPCURL)
		cancel()
		if err == nil {
			return evmSyncStatusSectionFromProgress(progress, blockNumber, &w.statusLogEVMBlock, &w.statusLogEVMState, now, nil)
		}
		httpProbeErr = err
	}

	client := w.client
	var temp *ethclient.Client
	if client == nil {
		if w.wsURL == "" {
			err := fmt.Errorf("EVM websocket URL not configured")
			if httpProbeErr != nil {
				err = fmt.Errorf("EVM HTTP sync probe failed: %w; %w", httpProbeErr, err)
			}
			return evmSyncStatusSectionFromProgress(nil, nil, &w.statusLogEVMBlock, &w.statusLogEVMState, now, err)
		}
		dialCtx, cancel := context.WithTimeout(ctx, dagRPCRequestTimeout)
		var err error
		temp, err = ethclient.DialContext(dialCtx, w.wsURL)
		cancel()
		if err != nil {
			if httpProbeErr != nil {
				err = fmt.Errorf("EVM HTTP sync probe failed: %w; websocket dial failed: %w", httpProbeErr, err)
			}
			return evmSyncStatusSectionFromProgress(nil, nil, &w.statusLogEVMBlock, &w.statusLogEVMState, now, err)
		}
		client = temp
		defer temp.Close()
	}

	syncCtx, syncCancel := context.WithTimeout(ctx, dagRPCRequestTimeout)
	progress, syncErr := client.SyncProgress(syncCtx)
	syncCancel()
	if syncErr != nil {
		if httpProbeErr != nil {
			syncErr = fmt.Errorf("EVM HTTP sync probe failed: %w; websocket sync probe failed: %w", httpProbeErr, syncErr)
		}
		return evmSyncStatusSectionFromProgress(nil, nil, &w.statusLogEVMBlock, &w.statusLogEVMState, now, syncErr)
	}

	blockCtx, blockCancel := context.WithTimeout(ctx, dagRPCRequestTimeout)
	blockNumber, blockErr := client.BlockNumber(blockCtx)
	blockCancel()
	var blockNumberPtr *uint64
	if blockErr == nil {
		blockNumberPtr = uint64Ptr(blockNumber)
	}

	section := evmSyncStatusSectionFromProgress(progress, blockNumberPtr, &w.statusLogEVMBlock, &w.statusLogEVMState, now, nil)
	if blockErr != nil && section.Error == "" {
		section.Error = "EVM block number probe failed: " + blockErr.Error()
	}
	if httpProbeErr != nil && section.Error == "" {
		section.Error = "EVM HTTP sync probe failed: " + httpProbeErr.Error()
	}
	return section
}

func evmSyncStatusSectionFromProgress(progress *ethereum.SyncProgress, blockNumber *uint64, blockEstimator, stateEstimator *syncProgressEstimator, now time.Time, probeErr error) nodeStatusLogSection {
	section := nodeStatusLogSection{
		Status:       "unknown",
		Source:       "eth_syncing",
		ETAText:      "unknown",
		StateETAText: "unknown",
	}
	if probeErr != nil {
		section.Error = probeErr.Error()
		return section
	}

	ethSyncing := progress != nil
	section.EthSyncing = boolPtr(ethSyncing)
	if progress == nil {
		if blockNumber == nil {
			section.Error = "missing EVM block number"
			return section
		}
		target := *blockNumber
		remaining := uint64(0)
		section.Status = "synced"
		section.CurrentBlock = uint64Ptr(*blockNumber)
		section.TargetBlock = uint64Ptr(target)
		section.RemainingBlocks = uint64Ptr(remaining)
		section.RemainingStates = uint64Ptr(0)
		section.KnownStates = uint64Ptr(0)
		section.PulledStates = uint64Ptr(0)
		section.RateBlocksPerSecond, section.ETASeconds, section.ETAText = blockEstimator.estimate(now, section.CurrentBlock, section.RemainingBlocks)
		section.RateStatesPerSecond, section.StateETASeconds, section.StateETAText = stateEstimator.estimate(now, nil, section.RemainingStates)
		return section
	}

	current := progress.CurrentBlock
	target := progress.HighestBlock
	if target < current {
		target = current
	}
	remaining := uint64(0)
	if target > current {
		remaining = target - current
	}

	section.Status = "catching_up"
	if progress.Done() && remaining == 0 {
		section.Status = "synced"
	}
	section.StartingBlock = uint64Ptr(progress.StartingBlock)
	section.CurrentBlock = uint64Ptr(current)
	section.TargetBlock = uint64Ptr(target)
	section.RemainingBlocks = uint64Ptr(remaining)
	section.TxIndexRemainingBlocks = uint64Ptr(progress.TxIndexRemainingBlocks)
	section.TxIndexFinishedBlocks = uint64Ptr(progress.TxIndexFinishedBlocks)

	knownStates := progress.KnownStates
	pulledStates := progress.PulledStates
	remainingStates := uint64(0)
	if knownStates > pulledStates {
		remainingStates = knownStates - pulledStates
	}
	if progress.HealingTrienodes > 0 || progress.HealingBytecode > 0 {
		remainingStates = saturatingAdd(remainingStates, progress.HealingTrienodes)
		remainingStates = saturatingAdd(remainingStates, progress.HealingBytecode)
	}
	section.KnownStates = uint64Ptr(knownStates)
	section.PulledStates = uint64Ptr(pulledStates)
	section.RemainingStates = uint64Ptr(remainingStates)
	section.HealingTrienodes = uint64Ptr(progress.HealingTrienodes)
	section.HealingBytecodes = uint64Ptr(progress.HealingBytecode)

	section.RateBlocksPerSecond, section.ETASeconds, section.ETAText = blockEstimator.estimate(now, section.CurrentBlock, section.RemainingBlocks)
	section.RateStatesPerSecond, section.StateETASeconds, section.StateETAText = stateEstimator.estimate(now, section.PulledStates, section.RemainingStates)
	return section
}

type evmRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type evmRPCResponse struct {
	Result json.RawMessage `json:"result"`
	Error  *evmRPCError    `json:"error"`
}

type evmSyncProgressJSON struct {
	StartingBlock          hexutil.Uint64 `json:"startingBlock"`
	CurrentBlock           hexutil.Uint64 `json:"currentBlock"`
	HighestBlock           hexutil.Uint64 `json:"highestBlock"`
	KnownStates            hexutil.Uint64 `json:"knownStates"`
	PulledStates           hexutil.Uint64 `json:"pulledStates"`
	HealingTrienodes       hexutil.Uint64 `json:"healingTrienodes"`
	HealingBytecode        hexutil.Uint64 `json:"healingBytecode"`
	TxIndexRemainingBlocks hexutil.Uint64 `json:"txIndexRemainingBlocks"`
	TxIndexFinishedBlocks  hexutil.Uint64 `json:"txIndexFinishedBlocks"`
}

func evmSyncProgressFromHTTP(ctx context.Context, url string) (*ethereum.SyncProgress, *uint64, error) {
	result, err := callEVMJSONRPC(ctx, url, "eth_syncing")
	if err != nil {
		return nil, nil, err
	}
	if bytes.Equal(bytes.TrimSpace(result), []byte("false")) {
		blockNumber, err := evmBlockNumberFromHTTP(ctx, url)
		if err != nil {
			return nil, nil, err
		}
		return nil, uint64Ptr(blockNumber), nil
	}

	var decoded evmSyncProgressJSON
	if err := json.Unmarshal(result, &decoded); err != nil {
		return nil, nil, fmt.Errorf("decode eth_syncing result: %w", err)
	}
	progress := &ethereum.SyncProgress{
		StartingBlock:          uint64(decoded.StartingBlock),
		CurrentBlock:           uint64(decoded.CurrentBlock),
		HighestBlock:           uint64(decoded.HighestBlock),
		KnownStates:            uint64(decoded.KnownStates),
		PulledStates:           uint64(decoded.PulledStates),
		HealingTrienodes:       uint64(decoded.HealingTrienodes),
		HealingBytecode:        uint64(decoded.HealingBytecode),
		TxIndexRemainingBlocks: uint64(decoded.TxIndexRemainingBlocks),
		TxIndexFinishedBlocks:  uint64(decoded.TxIndexFinishedBlocks),
	}
	return progress, nil, nil
}

func evmBlockNumberFromHTTP(ctx context.Context, url string) (uint64, error) {
	result, err := callEVMJSONRPC(ctx, url, "eth_blockNumber")
	if err != nil {
		return 0, err
	}
	var block hexutil.Uint64
	if err := json.Unmarshal(result, &block); err != nil {
		return 0, fmt.Errorf("decode eth_blockNumber result: %w", err)
	}
	return uint64(block), nil
}

func callEVMJSONRPC(ctx context.Context, url, method string) (json.RawMessage, error) {
	body := []byte(fmt.Sprintf(`{"jsonrpc":"2.0","id":1,"method":%q,"params":[]}`, method))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("%s returned HTTP %d: %s", method, resp.StatusCode, strings.TrimSpace(string(data)))
	}

	var decoded evmRPCResponse
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		return nil, err
	}
	if decoded.Error != nil {
		return nil, fmt.Errorf("%s RPC error %d: %s", method, decoded.Error.Code, decoded.Error.Message)
	}
	if len(decoded.Result) == 0 {
		return nil, fmt.Errorf("%s missing result", method)
	}
	return decoded.Result, nil
}

func (e *syncProgressEstimator) estimate(now time.Time, current, remaining *uint64) (*float64, *float64, string) {
	if remaining == nil {
		e.lastAt = now
		e.lastCurrent = cloneUint64Ptr(current)
		e.lastRemaining = nil
		return nil, nil, "unknown"
	}
	if *remaining == 0 {
		e.lastAt = now
		e.lastCurrent = cloneUint64Ptr(current)
		e.lastRemaining = cloneUint64Ptr(remaining)
		zero := float64(0)
		return nil, &zero, "0s"
	}

	var rate *float64
	if !e.lastAt.IsZero() {
		elapsed := now.Sub(e.lastAt).Seconds()
		if elapsed > 0 {
			var delta uint64
			if current != nil && e.lastCurrent != nil && *current > *e.lastCurrent {
				delta = *current - *e.lastCurrent
			} else if e.lastRemaining != nil && *e.lastRemaining > *remaining {
				delta = *e.lastRemaining - *remaining
			}
			if delta > 0 {
				rate = roundedFloatPtr(float64(delta) / elapsed)
			}
		}
	}

	e.lastAt = now
	e.lastCurrent = cloneUint64Ptr(current)
	e.lastRemaining = cloneUint64Ptr(remaining)

	if rate == nil || *rate <= 0 {
		return nil, nil, "estimating"
	}
	etaSeconds := float64(*remaining) / *rate
	eta := roundedFloatPtr(etaSeconds)
	return rate, eta, syncStatusHumanDuration(etaSeconds)
}

func syncStatusHumanDuration(seconds float64) string {
	if math.IsNaN(seconds) || math.IsInf(seconds, 0) || seconds < 0 {
		return "unknown"
	}
	if seconds < 1 {
		return "<1s"
	}
	return time.Duration(seconds * float64(time.Second)).Round(time.Second).String()
}

func saturatingAdd(a, b uint64) uint64 {
	if math.MaxUint64-a < b {
		return math.MaxUint64
	}
	return a + b
}

func roundedFloatPtr(v float64) *float64 {
	rounded := math.Round(v*1000) / 1000
	return &rounded
}

func cloneUint64Ptr(v *uint64) *uint64 {
	if v == nil {
		return nil
	}
	return uint64Ptr(*v)
}

func int64Ptr(v int64) *int64 {
	return &v
}

func intPtr(v int) *int {
	return &v
}

func boolPtr(v bool) *bool {
	return &v
}
