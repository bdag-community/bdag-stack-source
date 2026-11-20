package main

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/BlockdagNetworkLabs/bdag/log"
)

const (
	defaultPprofBaseURL    = "http://127.0.0.1:6060"
	livenessDumpMaxBody    = 64 << 20 // 64 MiB cap for goroutine text
	livenessDumpHTTPTimeout = 20 * time.Second
)

// parsePprofBaseFromArgs returns the HTTP base URL for the child's pprof server.
// It scans --profile in node args (same forms as blockdag-node). If unset, uses
// defaultPprofBaseURL (matches typical pool-stack NODE_METRICS / profile port).
func parsePprofBaseFromArgs(args []string) string {
	raw := ""
	for i, a := range args {
		if strings.HasPrefix(a, "--profile=") {
			raw = strings.TrimPrefix(a, "--profile=")
			break
		}
		if a == "--profile" && i+1 < len(args) {
			next := args[i+1]
			if next != "" && !strings.HasPrefix(next, "-") {
				raw = next
			}
			break
		}
	}
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return defaultPprofBaseURL
	}
	if strings.HasPrefix(raw, ":") {
		return "http://127.0.0.1" + raw
	}
	host, port, err := net.SplitHostPort(raw)
	if err != nil {
		log.Warn("diagnostics: could not parse --profile value, using default pprof URL",
			"profile", raw, "default", defaultPprofBaseURL, "err", err)
		return defaultPprofBaseURL
	}
	if host == "" || host == "0.0.0.0" || host == "[::]" || host == "::" {
		host = "127.0.0.1"
	}
	return "http://" + net.JoinHostPort(host, port)
}

// captureLivenessDiagnostics fetches goroutine stacks from the child's pprof HTTP
// server before nodeworker stops it. Safe to call if the process is wedged — the
// request may time out; any partial path is still useful for operators.
func (w *NodeWorker) captureLivenessDiagnostics(ctx context.Context, lastProbeErr error) {
	if !w.livenessDumpEnabled || w.livenessDumpDir == "" || w.pprofBaseURL == "" {
		return
	}
	dir := w.livenessDumpDir
	if err := os.MkdirAll(dir, 0o755); err != nil {
		log.Warn("liveness dump: could not create directory", "dir", dir, "err", err)
		return
	}

	now := time.Now().UTC()
	ts := fmt.Sprintf("%s-%09d", now.Format("20060102T150405"), now.Nanosecond())
	outPath := filepath.Join(dir, fmt.Sprintf("liveness-goroutine-%s.txt", ts))
	url := strings.TrimRight(w.pprofBaseURL, "/") + "/debug/pprof/goroutine?debug=2"

	client := &http.Client{Timeout: livenessDumpHTTPTimeout}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		log.Warn("liveness dump: request build failed", "err", err)
		return
	}

	resp, err := client.Do(req)
	if err != nil {
		log.Warn("liveness dump: GET failed (child may be wedged)", "url", url, "err", err,
			"last_probe_err", lastProbeErr)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		log.Warn("liveness dump: non-OK response from pprof", "url", url, "code", resp.StatusCode,
			"last_probe_err", lastProbeErr)
		return
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, livenessDumpMaxBody))
	if err != nil {
		log.Warn("liveness dump: read body failed", "err", err)
		return
	}
	if err := os.WriteFile(outPath, body, 0o644); err != nil {
		log.Warn("liveness dump: write file failed", "path", outPath, "err", err)
		return
	}
	log.Info("liveness dump: saved goroutine profile before child shutdown",
		"path", outPath, "bytes", len(body), "last_probe_err", lastProbeErr)
}
