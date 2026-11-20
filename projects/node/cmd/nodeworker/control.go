package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/BlockdagNetworkLabs/bdag/core/snapshot"
	"github.com/BlockdagNetworkLabs/bdag/log"
)

const defaultControlSocketName = "control.sock"

// controlSocketPath resolves the Unix socket nodeworker listens on for
// on-demand snapshot requests. An explicit --control.socket always wins;
// otherwise it's derived from --persist-root so it lives alongside staged
// binaries without needing its own flag in the common case.
func controlSocketPath(persistRoot, explicit string) string {
	if explicit != "" {
		return explicit
	}
	if persistRoot == "" {
		persistRoot = "/opt/bdag"
	}
	return filepath.Join(persistRoot, defaultControlSocketName)
}

// controlRequest/controlResponse are the on-demand snapshot wire protocol:
// one JSON object per line, in each direction, over a local Unix socket.
type controlRequest struct {
	Action    string `json:"action"` // "export", "import", or "stop"
	Path      string `json:"path"`
	ChunkSize int    `json:"chunk_size,omitempty"`
	SkipEVM   bool   `json:"skip_evm,omitempty"` // export only
}

type controlResponse struct {
	OK       bool   `json:"ok"`
	Error    string `json:"error,omitempty"`
	TipHash  string `json:"tip_hash,omitempty"`
	TipOrder uint64 `json:"tip_order,omitempty"`
	RootHash string `json:"root_hash,omitempty"`
}

// controlEnvelope is the wire framing sent server -> client during an
// export/import: zero or more {"log": "..."} lines, always followed by
// exactly one {"result": {...}} carrying a controlResponse. Framing them
// separately (rather than one controlResponse at the end) is what lets a
// multi-hour on-demand export stream its progress instead of leaving the
// caller blocked and silent until the very end.
type controlEnvelope struct {
	Log    string           `json:"log,omitempty"`
	Result *controlResponse `json:"result,omitempty"`
}

// controlLineStreamer relays a running snapshot subprocess's combined
// stdout/stderr to a control-socket client line by line as it's produced,
// then delivers the final controlResponse through the same framing. It
// implements io.Writer so it can be handed to exec.Cmd (via runSnapshotCommand)
// directly.
//
// Write never returns an error: if the client has gone away mid-export, the
// export itself must still run to completion and restart the node -- a
// disconnected UI is not a reason to abandon a multi-hour operation that's
// already stopped the node.
type controlLineStreamer struct {
	mu     sync.Mutex
	enc    *json.Encoder
	buf    []byte
	broken bool
}

func newControlLineStreamer(conn net.Conn) *controlLineStreamer {
	return &controlLineStreamer{enc: json.NewEncoder(conn)}
}

func (s *controlLineStreamer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.broken {
		return len(p), nil
	}
	s.buf = append(s.buf, p...)
	for {
		i := bytes.IndexByte(s.buf, '\n')
		if i < 0 {
			break
		}
		line := string(bytes.TrimRight(s.buf[:i], "\r"))
		s.buf = s.buf[i+1:]
		if line == "" {
			continue
		}
		if err := s.enc.Encode(controlEnvelope{Log: line}); err != nil {
			s.broken = true
			break
		}
	}
	return len(p), nil
}

// writeResult flushes any trailing partial line (the child's last write
// doesn't always end in '\n') and sends the final result envelope. Sharing
// the mutex/encoder with Write guarantees the result can't be interleaved
// with, or arrive ahead of, a straggling log line.
func (s *controlLineStreamer) writeResult(resp controlResponse) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.buf) > 0 {
		line := string(bytes.TrimRight(s.buf, "\r\n"))
		s.buf = nil
		if !s.broken && line != "" {
			if err := s.enc.Encode(controlEnvelope{Log: line}); err != nil {
				s.broken = true
			}
		}
	}
	if s.broken {
		return
	}
	_ = s.enc.Encode(controlEnvelope{Result: &resp})
}

var errControlBusy = errors.New("busy: lifecycle operation already in progress")

// startControlListener serves on-demand snapshot export/import requests
// over a local Unix socket for as long as ctx is alive. It is the operator
// counterpart to the automatic hourly loop in snapshot.go: the same
// Stop -> export/import -> Start cycle, triggered on demand instead of by
// timer.
func (w *NodeWorker) startControlListener(ctx context.Context, socketPath string) error {
	if err := os.RemoveAll(socketPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("control: remove stale socket: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(socketPath), 0o755); err != nil {
		return fmt.Errorf("control: create socket dir: %w", err)
	}
	ln, err := net.Listen("unix", socketPath)
	if err != nil {
		return fmt.Errorf("control: listen: %w", err)
	}
	if err := os.Chmod(socketPath, 0o600); err != nil {
		_ = ln.Close()
		return fmt.Errorf("control: chmod socket: %w", err)
	}
	log.Info("control: listening for on-demand snapshot requests", "socket", socketPath)

	go func() {
		<-ctx.Done()
		_ = ln.Close()
	}()

	go func() {
		defer os.RemoveAll(socketPath)
		for {
			conn, err := ln.Accept()
			if err != nil {
				if ctx.Err() != nil {
					return
				}
				log.Warn("control: accept failed", "err", err)
				continue
			}
			go w.handleControlConn(ctx, conn)
		}
	}()
	return nil
}

func (w *NodeWorker) handleControlConn(ctx context.Context, conn net.Conn) {
	defer conn.Close()

	var req controlRequest
	if err := json.NewDecoder(conn).Decode(&req); err != nil {
		w.writeControlResponse(conn, controlResponse{OK: false, Error: fmt.Sprintf("decode request: %v", err)})
		return
	}

	if req.Action == "stop" {
		w.writeControlResponse(conn, w.runOnDemandStop())
		return
	}

	// export/import stream the child's progress live -- see
	// controlLineStreamer's comment -- rather than going through the
	// single-shot writeControlResponse below.
	streamer := newControlLineStreamer(conn)
	streamer.writeResult(w.runOnDemandSnapshot(ctx, req, streamer))
}

func (w *NodeWorker) writeControlResponse(conn net.Conn, resp controlResponse) {
	if err := json.NewEncoder(conn).Encode(controlEnvelope{Result: &resp}); err != nil {
		log.Warn("control: write response failed", "err", err)
	}
}

// runOnDemandStop stops the supervised node cleanly and leaves it stopped
// -- unlike runOnDemandSnapshot, there is no restart afterward. This is the
// operator-facing counterpart to the internal w.Stop() call already used
// before snapshot export/import: it gives the child process nodeworker's
// own full shutdownTimeout budget (SIGTERM, wait, hard-kill only past that)
// to flush and close cleanly, so external tooling that needs to tear the
// whole container down (e.g. bdagrig's release cutover) can request a
// guaranteed-clean shutdown first, instead of racing Docker's own stop
// grace period against this same shutdown sequence.
func (w *NodeWorker) runOnDemandStop() controlResponse {
	if !atomic.CompareAndSwapUint32(&w.upgrading, 0, 1) {
		return controlResponse{OK: false, Error: errControlBusy.Error()}
	}
	defer atomic.StoreUint32(&w.upgrading, 0)

	w.lifecycleMu.Lock()
	defer w.lifecycleMu.Unlock()

	log.Info("control: stopping node on demand (staying stopped)")
	if err := w.stopLocked(); err != nil {
		w.closeClient()
		return controlResponse{OK: false, Error: err.Error()}
	}
	w.closeClient()
	return controlResponse{OK: true}
}

// runOnDemandSnapshot runs the same Stop -> export/import -> Start cycle
// runSnapshotOnce uses for the automatic loop, but skips the
// readiness/isCurrent/peer-count eligibility gate: an operator invoking
// this explicitly already knows what they're doing, and import in
// particular is normally run against a fresh/stalled node where those
// checks would never pass anyway. Mutual exclusion with the automatic loop
// and with governance-triggered binary upgrades (RestartWith) is provided
// by the same w.upgrading/w.lifecycleMu guard those paths already use.
func (w *NodeWorker) runOnDemandSnapshot(ctx context.Context, req controlRequest, out io.Writer) controlResponse {
	switch req.Action {
	case "export", "import":
	default:
		return controlResponse{OK: false, Error: fmt.Sprintf("unknown action %q (want export or import)", req.Action)}
	}
	if req.Path == "" {
		return controlResponse{OK: false, Error: "path is required"}
	}

	// A node that hasn't finished its own startup yet (still rebuilding
	// upgrade state from headers, still opening its databases, ...) won't
	// respond to Stop()'s SIGTERM promptly. nodeworker's own shutdown
	// timeout then elapses and it force-kills the process group instead --
	// an unclean kill that can lose enough freshly-imported, not-yet-synced
	// EVM state that the next startup's repair-rewind can't find any valid
	// checkpoint and refuses to proceed (checkBdagGenesisHeadWithAdvancedData
	// in deps/go-ethereum/core/blockchain.go), leaving the chain
	// unrecoverable short of restoring a full dataset. This reproduced in
	// practice: an operator-triggered export landed mid-startup and forced
	// exactly this. import is deliberately exempted -- it's normally run
	// against a fresh/stalled node where readiness would never pass anyway
	// (see importSnapshot's doc comment in snapshot.go).
	if req.Action == "export" && w.readyURL != "" {
		client := &http.Client{Timeout: 500 * time.Millisecond}
		if err := probeReadiness(client, w.readyURL, w.legacyReadyURL); err != nil {
			return controlResponse{OK: false, Error: fmt.Sprintf("node is not ready yet (still starting up) -- refusing to stop it for export, which risks an unclean kill: %v", err)}
		}
	}

	if !atomic.CompareAndSwapUint32(&w.upgrading, 0, 1) {
		return controlResponse{OK: false, Error: errControlBusy.Error()}
	}
	defer atomic.StoreUint32(&w.upgrading, 0)

	w.lifecycleMu.Lock()
	defer w.lifecycleMu.Unlock()

	cfg := w.snapshotCfg
	if req.ChunkSize > 0 {
		cfg.ChunkSize = req.ChunkSize
	}
	cfg.SkipEVM = req.SkipEVM

	binary := w.snapshotBinary()

	log.Info("control: stopping node for on-demand snapshot", "action", req.Action, "path", req.Path)
	stopErr := w.stopLocked()
	w.closeClient()
	runErr := stopErr
	if stopErr == nil && ctx.Err() == nil {
		switch req.Action {
		case "export":
			if err := w.exportSnapshot(ctx, cfg, binary, req.Path, out); err != nil {
				runErr = errors.Join(runErr, err)
			} else if err := w.verifySnapshot(ctx, binary, req.Path, out); err != nil {
				runErr = errors.Join(runErr, err)
			}
		case "import":
			if err := w.importSnapshot(ctx, cfg, binary, req.Path, out); err != nil {
				runErr = errors.Join(runErr, err)
			}
		}
	}

	if ctx.Err() != nil {
		runErr = errors.Join(runErr, ctx.Err())
	}

	var restartErr error
	if stopErr != nil {
		restartErr = fmt.Errorf("control: refusing restart after stop cleanup failure: %w", stopErr)
	} else {
		restartErr = w.startLocked()
	}
	if restartErr == nil {
		restartErr = w.waitForReadinessGeneration(w.ctx, w.generation)
		if restartErr == nil {
			restartErr = w.startLivenessMonitor()
		}
		if restartErr == nil {
			log.Info("control: node restarted after on-demand snapshot", "action", req.Action)
		} else {
			_ = w.stopLocked()
		}
	}
	if restartErr != nil {
		runErr = errors.Join(runErr, fmt.Errorf("control: restart node: %w", restartErr))
	}

	if runErr != nil {
		return controlResponse{OK: false, Error: runErr.Error()}
	}

	resp := controlResponse{OK: true}
	if m, err := snapshot.OpenManifest(req.Path); err == nil {
		resp.TipHash = m.TipHash
		resp.TipOrder = m.TipOrder
		resp.RootHash = m.RootHash
	}
	return resp
}
