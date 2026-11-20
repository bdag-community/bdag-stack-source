package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/BlockdagNetworkLabs/bdag/core/snapshot"
	"github.com/BlockdagNetworkLabs/bdag/log"
	"github.com/urfave/cli/v2"
)

const (
	defaultSnapshotOutputDir = "/snapshot"
	defaultSnapshotDataRoot  = "/bdag/data"
	defaultSnapshotNetwork   = "mainnet"
	latestSnapshotMetadata   = "latest.json"
)

var errSnapshotNotEligible = errors.New("snapshot not eligible")

type snapshotConfig struct {
	Enabled    bool
	Interval   time.Duration
	OutputDir  string
	DataDir    string
	Network    string
	ChunkSize  int
	RunOnStart bool
	SkipEVM    bool // set per-request by on-demand exports only; the automatic loop always includes EVM state
}

type snapshotLatestMetadata struct {
	CreatedAt      time.Time `json:"created_at"`
	Network        string    `json:"network"`
	DataDir        string    `json:"datadir"`
	Binary         string    `json:"binary"`
	Archive        string    `json:"archive"`
	ArchiveSize    int64     `json:"archive_size"`
	ArchiveSHA256  string    `json:"archive_sha256"`
	Manifest       string    `json:"manifest"`
	ManifestSize   int64     `json:"manifest_size"`
	ManifestSHA256 string    `json:"manifest_sha256"`
	BDAGVersion    string    `json:"bdag_version"`
	DBEngine       string    `json:"db_engine"`
	TipHash        string    `json:"tip_hash"`
	TipOrder       uint64    `json:"tip_order"`
	TipID          uint64    `json:"tip_id"`
	BlockTotal     uint64    `json:"block_total"`
	StateRoot      string    `json:"state_root"`
	RootHash       string    `json:"root_hash"`
	TotalKeys      uint64    `json:"total_keys"`
}

func snapshotConfigFromContext(ctx *cli.Context, nodeArgs []string) (snapshotConfig, error) {
	enabled := ctx.Bool("snapshot.enable")
	network := strings.TrimSpace(ctx.String("snapshot.network"))
	if network == "" {
		network = defaultSnapshotNetwork
	}
	outputDir := strings.TrimSpace(ctx.String("snapshot.output-dir"))
	if outputDir == "" {
		outputDir = defaultSnapshotOutputDir
	}
	dataDir := strings.TrimSpace(ctx.String("snapshot.datadir"))
	if dataDir == "" {
		parent := parseDataDir(nodeArgs)
		if parent == "" {
			parent = defaultSnapshotDataRoot
		}
		dataDir = filepath.Join(parent, network)
	}
	interval := ctx.Duration("snapshot.interval")
	if interval <= 0 {
		if enabled {
			return snapshotConfig{}, fmt.Errorf("snapshot.interval must be positive")
		}
		interval = time.Hour
	}
	chunkSize := ctx.Int("snapshot.chunk-size")
	if chunkSize < 0 {
		return snapshotConfig{}, fmt.Errorf("snapshot.chunk-size must be non-negative")
	}
	return snapshotConfig{
		Enabled:    enabled,
		Interval:   interval,
		OutputDir:  outputDir,
		DataDir:    dataDir,
		Network:    network,
		ChunkSize:  chunkSize,
		RunOnStart: ctx.Bool("snapshot.run-on-start"),
	}, nil
}

func (w *NodeWorker) runSnapshotLoop(ctx context.Context, cfg snapshotConfig) {
	if !cfg.Enabled {
		return
	}
	log.Info("snapshot loop enabled",
		"interval", cfg.Interval,
		"output_dir", cfg.OutputDir,
		"datadir", cfg.DataDir,
		"network", cfg.Network,
		"run_on_start", cfg.RunOnStart,
	)

	if cfg.RunOnStart {
		if err := w.runSnapshotOnce(ctx, cfg); err != nil && !errors.Is(err, errSnapshotNotEligible) && !errors.Is(err, context.Canceled) {
			log.Error("snapshot: run-on-start failed", "err", err)
		}
	}

	timer := time.NewTimer(cfg.Interval)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			if err := w.runSnapshotOnce(ctx, cfg); err != nil && !errors.Is(err, errSnapshotNotEligible) && !errors.Is(err, context.Canceled) {
				log.Error("snapshot: run failed", "err", err)
			}
			timer.Reset(cfg.Interval)
		}
	}
}

func (w *NodeWorker) runSnapshotOnce(ctx context.Context, cfg snapshotConfig) error {
	eligible, reason, peers, err := w.snapshotEligible(ctx, cfg)
	if err != nil {
		return err
	}
	if !eligible {
		log.Info("snapshot: skipping", "reason", reason, "peers", peers)
		return fmt.Errorf("%w: %s", errSnapshotNotEligible, reason)
	}
	if !atomic.CompareAndSwapUint32(&w.upgrading, 0, 1) {
		log.Info("snapshot: skipping because restart/upgrade is already in progress")
		return fmt.Errorf("%w: lifecycle busy", errSnapshotNotEligible)
	}
	defer atomic.StoreUint32(&w.upgrading, 0)

	w.lifecycleMu.Lock()
	defer w.lifecycleMu.Unlock()

	if err := os.MkdirAll(cfg.OutputDir, 0o755); err != nil {
		return fmt.Errorf("snapshot: create output dir: %w", err)
	}
	tmpRoot := filepath.Join(cfg.OutputDir, ".tmp")
	if err := os.MkdirAll(tmpRoot, 0o755); err != nil {
		return fmt.Errorf("snapshot: create temp dir: %w", err)
	}
	runDir, err := os.MkdirTemp(tmpRoot, "snapshot-*")
	if err != nil {
		return fmt.Errorf("snapshot: create run temp dir: %w", err)
	}
	defer func() {
		if err := os.RemoveAll(runDir); err != nil {
			log.Warn("snapshot: failed to clean temp dir", "dir", runDir, "err", err)
		}
	}()

	binary := w.snapshotBinary()
	tmpArchive := filepath.Join(runDir, "snapshot"+snapshot.ArchiveFileSuffix)

	log.Info("snapshot: stopping node for cold export", "datadir", cfg.DataDir, "archive", tmpArchive)
	stopErr := w.stopLocked()
	w.closeClient()
	runErr := stopErr
	if stopErr == nil && ctx.Err() == nil {
		if err := w.exportSnapshot(ctx, cfg, binary, tmpArchive, nil); err != nil {
			runErr = errors.Join(runErr, err)
		} else if err := w.verifySnapshot(ctx, binary, tmpArchive, nil); err != nil {
			runErr = errors.Join(runErr, err)
		} else if err := publishSnapshotArtifacts(tmpArchive, cfg.OutputDir, cfg, binary); err != nil {
			runErr = errors.Join(runErr, err)
		}
	}

	if ctx.Err() != nil {
		return errors.Join(runErr, ctx.Err())
	}
	var restartErr error
	if stopErr != nil {
		restartErr = fmt.Errorf("snapshot: refusing restart after stop cleanup failure: %w", stopErr)
	} else {
		restartErr = w.startLocked()
	}
	if restartErr == nil {
		restartErr = w.waitForReadinessGeneration(w.ctx, w.generation)
		if restartErr == nil {
			restartErr = w.startLivenessMonitor()
		}
		if restartErr == nil {
			log.Info("snapshot: node restarted after export")
		} else {
			_ = w.stopLocked()
		}
	}
	if restartErr != nil {
		runErr = errors.Join(runErr, fmt.Errorf("snapshot: restart node: %w", restartErr))
	}
	return runErr
}

func (w *NodeWorker) snapshotEligible(ctx context.Context, cfg snapshotConfig) (bool, string, uint64, error) {
	if w.readyURL != "" {
		client := &http.Client{Timeout: 500 * time.Millisecond}
		if err := probeReadiness(client, w.readyURL, w.legacyReadyURL); err != nil {
			return false, "not_ready", 0, nil
		}
	}
	current, err := w.dagIsCurrent(ctx)
	if err != nil {
		return false, "", 0, fmt.Errorf("snapshot: isCurrent check failed: %w", err)
	}
	if !current {
		return false, "not_current", 0, nil
	}
	peerCount, err := w.snapshotPeerCount(ctx)
	if err != nil {
		return false, "", 0, fmt.Errorf("snapshot: peer count check failed: %w", err)
	}
	if peerCount < w.healthMinPeers {
		return false, "insufficient_peers", peerCount, nil
	}
	return true, "ok", peerCount, nil
}

func (w *NodeWorker) dagIsCurrent(ctx context.Context) (bool, error) {
	if w.isCurrentFn != nil {
		return w.isCurrentFn(ctx)
	}
	if w.dagRPCClient == nil {
		return false, errors.New("dag rpc client unavailable")
	}
	if w.dagRPCURL == "" {
		return false, errors.New("dag rpc url not configured")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, w.dagRPCURL, strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"isCurrent","params":[]}`))
	if err != nil {
		return false, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-Forwarded-By", "nodeworker")
	if w.dagRPCUser != "" || w.dagRPCPass != "" {
		req.SetBasicAuth(w.dagRPCUser, w.dagRPCPass)
	}
	resp, err := w.dagRPCClient.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return false, fmt.Errorf("dag rpc status %s: %s", resp.Status, string(body))
	}
	var env struct {
		Result *bool `json:"result"`
		Error  *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
		return false, fmt.Errorf("decode isCurrent response: %w", err)
	}
	if env.Error != nil {
		msg := env.Error.Message
		if msg == "" {
			msg = "dag rpc error"
		}
		return false, errors.New(msg)
	}
	if env.Result == nil {
		return false, errors.New("missing isCurrent result")
	}
	return *env.Result, nil
}

func (w *NodeWorker) snapshotPeerCount(ctx context.Context) (uint64, error) {
	if w.peerCountFn != nil {
		return w.peerCountFn(ctx)
	}
	peerCtx, cancel := context.WithTimeout(ctx, dagRPCRequestTimeout)
	defer cancel()
	return w.dagPeerCount(peerCtx)
}

// out, when non-nil, additionally receives a live copy of the child's
// combined stdout/stderr -- the control-socket path (see control.go) passes
// its controlLineStreamer here so an operator watching e.g. bdagrig's UI
// sees the same "Snapshot export in progress ..." lines this process logs
// to its own stdout, instead of silence followed by a single opaque error
// at the end. The automatic hourly loop (runSnapshotOnce) has no client to
// stream to and passes nil.
func (w *NodeWorker) exportSnapshot(ctx context.Context, cfg snapshotConfig, binary, outputPath string, out io.Writer) error {
	args := []string{"snap", "export", "--datadir", cfg.DataDir, "--path", outputPath}
	if cfg.ChunkSize > 0 {
		args = append(args, "--chunk-size", strconv.Itoa(cfg.ChunkSize))
	}
	if cfg.SkipEVM {
		args = append(args, "--skip-evm")
	}
	args = append(args, snapshotNetworkArgs(cfg.Network)...)
	if err := w.runSnapshotCommand(ctx, binary, out, args...); err != nil {
		return fmt.Errorf("snapshot export failed: %w", err)
	}
	return nil
}

func (w *NodeWorker) verifySnapshot(ctx context.Context, binary, archivePath string, out io.Writer) error {
	if err := w.runSnapshotCommand(ctx, binary, out, "snap", "verify", "--path", archivePath); err != nil {
		return fmt.Errorf("snapshot verify failed: %w", err)
	}
	return nil
}

// importSnapshot loads an archive into cfg.DataDir via `bdag snap import`.
// The destination chain DB must be empty; bdag snap import enforces that.
// Network flags mirror exportSnapshot so the child validates the archive's
// manifest network against the same active network as the supervised node.
// See exportSnapshot's comment on out.
func (w *NodeWorker) importSnapshot(ctx context.Context, cfg snapshotConfig, binary, archivePath string, out io.Writer) error {
	args := []string{"snap", "import", "--datadir", cfg.DataDir, "--path", archivePath}
	args = append(args, snapshotNetworkArgs(cfg.Network)...)
	if err := w.runSnapshotCommand(ctx, binary, out, args...); err != nil {
		return fmt.Errorf("snapshot import failed: %w", err)
	}
	return nil
}

func (w *NodeWorker) runSnapshotCommand(ctx context.Context, binary string, out io.Writer, args ...string) error {
	if w.snapshotCommandFn != nil {
		return w.snapshotCommandFn(ctx, binary, args...)
	}
	cmd := exec.CommandContext(ctx, binary, args...)
	if out != nil {
		cmd.Stdout = io.MultiWriter(os.Stdout, out)
		cmd.Stderr = io.MultiWriter(os.Stderr, out)
	} else {
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
	}
	cmd.Env = os.Environ()
	return cmd.Run()
}

func (w *NodeWorker) snapshotBinary() string {
	if w.binary != "" {
		if _, err := os.Stat(w.binary); err == nil {
			return w.binary
		}
	}
	return fallbackBinary()
}

func snapshotNetworkArgs(network string) []string {
	switch strings.ToLower(strings.TrimSpace(network)) {
	case "", "mainnet":
		return nil
	case "testnet", "devnet", "privnet", "mixnet":
		return []string{"--" + strings.ToLower(strings.TrimSpace(network))}
	default:
		return nil
	}
}

func publishSnapshotArtifacts(tmpArchive, outputDir string, cfg snapshotConfig, binary string) error {
	tmpManifest := tmpArchive + snapshot.ManifestFileSuffix
	manifest, err := snapshot.OpenManifest(tmpArchive)
	if err != nil {
		return fmt.Errorf("snapshot: open manifest: %w", err)
	}
	archiveHash, archiveSize, err := fileSHA256(tmpArchive)
	if err != nil {
		return fmt.Errorf("snapshot: hash archive: %w", err)
	}
	manifestHash, manifestSize, err := fileSHA256(tmpManifest)
	if err != nil {
		return fmt.Errorf("snapshot: hash manifest: %w", err)
	}
	meta := snapshotLatestMetadata{
		CreatedAt:      time.Now().UTC(),
		Network:        cfg.Network,
		DataDir:        cfg.DataDir,
		Binary:         binary,
		Archive:        "snapshot" + snapshot.ArchiveFileSuffix,
		ArchiveSize:    archiveSize,
		ArchiveSHA256:  archiveHash,
		Manifest:       "snapshot" + snapshot.ArchiveFileSuffix + snapshot.ManifestFileSuffix,
		ManifestSize:   manifestSize,
		ManifestSHA256: manifestHash,
		BDAGVersion:    manifest.BDAGVersion,
		DBEngine:       manifest.DBEngine,
		TipHash:        manifest.TipHash,
		TipOrder:       manifest.TipOrder,
		TipID:          manifest.TipID,
		BlockTotal:     manifest.BlockTotal,
		StateRoot:      manifest.StateRoot,
		RootHash:       manifest.RootHash,
		TotalKeys:      manifest.TotalKeys,
	}
	metaPath := filepath.Join(filepath.Dir(tmpArchive), latestSnapshotMetadata)
	raw, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return fmt.Errorf("snapshot: encode latest metadata: %w", err)
	}
	raw = append(raw, '\n')
	if err := os.WriteFile(metaPath, raw, 0o644); err != nil {
		return fmt.Errorf("snapshot: write latest metadata: %w", err)
	}

	finalArchive := filepath.Join(outputDir, "snapshot"+snapshot.ArchiveFileSuffix)
	finalManifest := finalArchive + snapshot.ManifestFileSuffix
	finalMeta := filepath.Join(outputDir, latestSnapshotMetadata)
	if err := os.Rename(tmpArchive, finalArchive); err != nil {
		return fmt.Errorf("snapshot: publish archive: %w", err)
	}
	if err := os.Rename(tmpManifest, finalManifest); err != nil {
		return fmt.Errorf("snapshot: publish manifest: %w", err)
	}
	if err := os.Rename(metaPath, finalMeta); err != nil {
		return fmt.Errorf("snapshot: publish latest metadata: %w", err)
	}
	log.Info("snapshot: published",
		"archive", finalArchive,
		"manifest", finalManifest,
		"latest", finalMeta,
		"tip", manifest.TipHash,
		"order", manifest.TipOrder,
		"root", manifest.RootHash,
	)
	return nil
}

func fileSHA256(path string) (string, int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(h.Sum(nil)), n, nil
}
