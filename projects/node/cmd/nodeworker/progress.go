package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/ethereum/go-ethereum/core/types"
)

const progressFileName = "worker-progress.json"

type progressState struct {
	BlockNumber uint64  `json:"blockNumber"`
	BlockHash   string  `json:"blockHash"`
	TxIndex     *uint64 `json:"txIndex,omitempty"`
	LogIndex    uint64  `json:"logIndex"`
	TxHash      string  `json:"txHash,omitempty"`
}

func uint64Ptr(v uint64) *uint64 {
	vv := v
	return &vv
}

func progressFilePath() string {
	return filepath.Join(persistDir, progressFileName)
}

func loadProgress() (*progressState, error) {
	path := progressFilePath()
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var state progressState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, err
	}
	return &state, nil
}

func saveProgress(state *progressState) error {
	if state == nil {
		return errors.New("nil progress state")
	}
	if err := os.MkdirAll(persistDir, 0o755); err != nil {
		return err
	}
	data, err := json.Marshal(state)
	if err != nil {
		return err
	}
	tmp := progressFilePath() + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, progressFilePath())
}

func (w *NodeWorker) getProgress() *progressState {
	w.progressMu.RLock()
	defer w.progressMu.RUnlock()
	if w.progress == nil {
		return nil
	}
	cp := *w.progress
	return &cp
}

func (w *NodeWorker) setProgress(state *progressState) {
	w.progressMu.Lock()
	defer w.progressMu.Unlock()
	if state == nil {
		w.progress = nil
		return
	}
	cp := *state
	w.progress = &cp
}

func (w *NodeWorker) shouldSkipLog(lg types.Log) bool {
	state := w.getProgress()
	if state == nil {
		return false
	}

	if lg.BlockNumber < state.BlockNumber {
		return true
	}
	if lg.BlockNumber > state.BlockNumber {
		return false
	}

	if state.BlockHash != "" && state.BlockHash != lg.BlockHash.Hex() {
		return false
	}

	if state.TxIndex != nil {
		txIdx := uint64(lg.TxIndex)
		if txIdx < *state.TxIndex {
			return true
		}
		if txIdx > *state.TxIndex {
			return false
		}
	}

	idx := uint64(lg.Index)
	if idx < state.LogIndex {
		return true
	}
	if idx == state.LogIndex {
		if state.TxHash == "" || state.TxHash == lg.TxHash.Hex() {
			return true
		}
	}
	if idx > state.LogIndex {
		return false
	}
	return false
}

func (w *NodeWorker) recordProgress(ctx context.Context, lg types.Log) error {
	if err := w.waitForConfirmations(ctx, lg.BlockNumber); err != nil {
		return fmt.Errorf("wait for confirmations: %w", err)
	}
	state := &progressState{
		BlockNumber: lg.BlockNumber,
		BlockHash:   lg.BlockHash.Hex(),
		TxIndex:     uint64Ptr(uint64(lg.TxIndex)),
		LogIndex:    uint64(lg.Index),
		TxHash:      lg.TxHash.Hex(),
	}
	w.setProgress(state)
	return saveProgress(state)
}
