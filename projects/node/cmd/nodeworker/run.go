package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/BlockdagNetworkLabs/bdag/common/util"
	cfgutil "github.com/BlockdagNetworkLabs/bdag/config"
	"github.com/BlockdagNetworkLabs/bdag/log"
	"github.com/BlockdagNetworkLabs/bdag/params"
	backoff "github.com/cenkalti/backoff/v4"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/google/shlex"
	"github.com/urfave/cli/v2"
)

const governanceContractAddressHex = "0x1000000000000000000000000000000000000005"

func run(appCtx *cli.Context) error {
	ctx, cancel := context.WithCancel(appCtx.Context)
	defer cancel()

	// Base binary comes from entrypoint preference: /opt/bdag/bdag (if promoted) else /usr/local/bin/bdag
	bin := appCtx.String("node-binary")
	configurePersistRoot(appCtx.String("persist-root"))

	dumpDir := strings.TrimSpace(appCtx.String("diagnostics.liveness-dump-dir"))
	if dumpDir == "" {
		dumpDir = filepath.Join(persistDir, "liveness-dumps")
	}
	livenessDumpEnabled := appCtx.Bool("diagnostics.liveness-dump")

	// Parse child args safely (keeps quoted segments)
	argStr := appCtx.String("node-args")
	args, err := shlex.Split(argStr)
	if err != nil {
		return err
	}
	snapshotCfg, err := snapshotConfigFromContext(appCtx, args)
	if err != nil {
		return err
	}

	pprofBaseURL := parsePprofBaseFromArgs(args)
	if v := strings.TrimSpace(appCtx.String("diagnostics.pprof-base")); v != "" {
		pprofBaseURL = v
	}
	if livenessDumpEnabled {
		log.Info("liveness diagnostics enabled", "dump_dir", dumpDir, "pprof_base", pprofBaseURL)
	}

	// Discover health endpoint (default 127.0.0.1:6061)
	healthAddr := "127.0.0.1:6061"
	for i, a := range args {
		if strings.HasPrefix(a, "--health=") {
			parts := strings.SplitN(a, "=", 2)
			if len(parts) == 2 && parts[1] != "" {
				healthAddr = parts[1]
			}
		}
		if a == "--health" && i+1 < len(args) {
			healthAddr = args[i+1]
		}
	}
	readyURL := fmt.Sprintf("http://%s/readyz", healthAddr)
	legacyReadyURL := fmt.Sprintf("http://%s/ready", healthAddr)
	liveURL := fmt.Sprintf("http://%s/healthz", healthAddr)
	legacyLiveURL := fmt.Sprintf("http://%s/live", healthAddr)
	log.Info("using health endpoints", "readiness", readyURL, "liveness", liveURL)

	startupTimeout := appCtx.Duration("health.startup-timeout")
	livenessTimeout := appCtx.Duration("health.liveness-timeout")
	healthProbeInterval := appCtx.Duration("health.probe-interval")
	miningReadinessTimeout := appCtx.Duration("health.mining-readiness-timeout")
	miningReadinessGrace := appCtx.Duration("health.mining-readiness-grace")
	statusLogIntervals := nodeStatusLogIntervals{
		Syncing: appCtx.Duration("status-log.syncing-interval"),
		Synced:  appCtx.Duration("status-log.synced-interval"),
	}
	healthMinPeers := appCtx.Uint64("health-min-peers")

	// RPC / WS
	wsURL := appCtx.String("rpc-url")
	wsAddr := strings.TrimPrefix(wsURL, "ws://")

	network := parseNetwork(args)
	applyNetworkParams(network)

	configFile := parseConfigFile(args)
	dagRPCURL := buildDagRPCURL(args, appCtx.String("dag-rpc-url"))
	evmRPCURL := buildEVMRPCURL(args, appCtx.String("evm-rpc-url"), configFile)
	rpcUser, rpcPass := parseRPCAuth(args, configFile)
	fmt.Println("DAG RPC URL:", dagRPCURL)
	fmt.Println("EVM RPC URL:", evmRPCURL)
	fmt.Println("network:", network)

	// Governance event ABI
	contract := common.HexToAddress(governanceContractAddressHex)
	parser, err := abi.JSON(strings.NewReader(execABI))
	if err != nil {
		return err
	}
	evtID := parser.Events["ProposalExecuted"].ID

	// Handle "pending" upgrade (e.g., set before previous shutdown)
	var startedFromStaged bool
	var stagedPath string
	if pending, err := readPending(); err != nil {
		log.Error("run: read pending failed", "err", err)
	} else if pending != nil {
		log.Info("run: found pending upgrade", "link", pending.Link, "sha", pending.Sha256)
		// Download to temp, then stage inside the volume so it persists
		tmpPath, err := downloadBinary(ctx, pending.Link, pending.Sha256)
		if err != nil {
			log.Error("run: pending download failed", "err", err)
		} else {
			_ = clearPending()
			stagedPath = stagePathFromSHA(pending.Sha256) // <persist-root>/staging/<sha>/bdag
			if err := copyFile(tmpPath, stagedPath, 0o755); err != nil {
				log.Error("run: stage copy failed", "err", err, "dst", stagedPath)
			} else {
				bin = stagedPath
				startedFromStaged = true
			}
		}
	}

	nodeID, nodeTrace, nodeSource := deriveNodeID(args)
	if nodeTrace == "" {
		nodeTrace = "unknown"
	}
	log.Info("run: derived node identifier", "source", nodeSource, "hashPrefix", nodeTrace)

	// Build worker
	worker := &NodeWorker{
		binary:                 bin,
		prevBinary:             bin,
		args:                   args,
		exitCh:                 make(chan error, 1),
		wsURL:                  wsURL,
		evmRPCURL:              evmRPCURL,
		contract:               contract,
		eventParser:            parser,
		eventID:                evtID,
		readyURL:               readyURL,
		legacyReadyURL:         legacyReadyURL,
		liveURL:                liveURL,
		legacyLiveURL:          legacyLiveURL,
		startupTimeout:         startupTimeout,
		livenessTimeout:        livenessTimeout,
		shutdownTimeout:        appCtx.Duration("shutdown-timeout"),
		healthProbeInterval:    healthProbeInterval,
		miningReadinessTimeout: miningReadinessTimeout,
		miningReadinessGrace:   miningReadinessGrace,
		statusLogIntervals:     statusLogIntervals,
		rolloutWindow:          appCtx.Duration("rollout-window"),
		healthMinPeers:         healthMinPeers,
		nodeID:                 nodeID,
		nodeIDTrace:            nodeTrace,
		dagRPCURL:              dagRPCURL,
		dagRPCClient: &http.Client{
			Timeout: dagRPCRequestTimeout,
		},
		dagRPCUser:          rpcUser,
		dagRPCPass:          rpcPass,
		pprofBaseURL:        pprofBaseURL,
		livenessDumpDir:     dumpDir,
		livenessDumpEnabled: livenessDumpEnabled,
		ipRefreshInterval:   appCtx.Duration("ip-refresh-interval"),
		configFile:          configFile,
		snapshotCfg:         snapshotCfg,
	}
	worker.bindTerminalCancel(cancel)

	if progress, err := loadProgress(); err != nil {
		log.Error("run: load progress failed", "err", err)
	} else if progress != nil {
		worker.setProgress(progress)
		log.Info("run: loaded progress watermark", "block", progress.BlockNumber, "txIndex", progressTxIndex(progress), "logIndex", progress.LogIndex)
	}

	// Start child
	if err := worker.Start(); err != nil {
		return fmt.Errorf("failed to start child: %w", err)
	}

	// Wait until healthy
	if err := worker.waitForReadiness(ctx); err != nil {
		_ = worker.Stop()
		if terminal := worker.terminalFailure(); terminal != nil {
			return terminal
		}
		return fmt.Errorf("node never became ready on %s: %w", wsAddr, err)
	}
	if err := worker.startLivenessMonitor(); err != nil {
		_ = worker.Stop()
		return err
	}

	// If we booted from a staged pending binary, soak, then promote or rollback
	if startedFromStaged {
		if err := worker.proveHealthy(ctx); err != nil {
			log.Error("run: health soak failed for staged binary", "err", err)
			_ = os.RemoveAll(filepath.Dir(stagedPath))
			// rollback to last good (active or legacy)
			if err := worker.Stop(); err != nil {
				log.Warn("run: stop after soak fail", "err", err)
			}
			if err := worker.RestartWith(fallbackBinary()); err != nil {
				return fmt.Errorf("run: rollback restart failed: %w", err)
			}
		} else {
			if err := promoteAndPrune(stagedPath); err != nil {
				log.Error("run: promote failed", "err", err)
				_ = os.RemoveAll(filepath.Dir(stagedPath))
				// Optional rollback: keep running staged (already healthy) or restart to fallback
			} else {
				log.Info("run: promoted staged to active", "active", activePath)
			}
		}
	}
	go worker.runSnapshotLoop(ctx, snapshotCfg)

	socketPath := controlSocketPath(appCtx.String("persist-root"), appCtx.String("control.socket"))
	if err := worker.startControlListener(ctx, socketPath); err != nil {
		log.Error("run: control listener failed to start; on-demand snapshot export/import will be unavailable", "err", err)
	}

	// Wire WS client and event watcher. The EVM gateway may come up a moment
	// after the outer node starts, so retry instead of exiting immediately on
	// the first refused connection.
	var client *ethclient.Client
	wsBackoff := backoff.NewExponentialBackOff()
	wsBackoff.InitialInterval = 200 * time.Millisecond
	wsBackoff.MaxInterval = 2 * time.Second
	wsBackoff.MaxElapsedTime = 0 // keep retrying until the context is done
	op := func() error {
		c, err := ethclient.DialContext(ctx, wsURL)
		if err != nil {
			return err
		}
		client = c
		return nil
	}
	retryCtx := backoff.WithContext(wsBackoff, ctx)
	if err := backoff.RetryNotify(op, retryCtx, func(err error, next time.Duration) {
		log.Warn("WS dial failed; retrying", "url", wsURL, "err", err, "next_in", next)
	}); err != nil {
		_ = worker.Stop()
		if terminal := worker.terminalFailure(); terminal != nil {
			return terminal
		}
		return fmt.Errorf("could not connect over WS %q: %w", wsURL, err)
	}
	// if contract != (common.Address{}) {
	// 	if err := ensureContractHasCode(ctx, client, contract); err != nil {
	// 		client.Close()
	// 		log.Error("run: contract validation failed", "err", err)
	// 		_ = worker.Stop()
	// 		return err
	// 	}
	// }

	worker.client = client
	go worker.watchExecuted(ctx)
	go worker.monitorExternalIP(ctx)
	log.Info("subscribed to ProposalExecuted over WS", "url", wsURL)

	// Block on app context; main.go already installs a signal-aware context
	<-ctx.Done()
	log.Info("context cancelled; shutting down")

	if err := worker.Stop(); err != nil {
		log.Error("error stopping worker", "err", err)
	}
	// No extra Wait() here; the active generation's watcher owns reaping.
	log.Info("shutdown complete, exiting")
	if terminal := worker.terminalFailure(); terminal != nil {
		return terminal
	}
	return nil
}

// waitForReadiness blocks until url responds with HTTP 200 or ctx is done.
// When fallback is provided it is retried if the primary endpoint responds 404.
func waitForReadiness(ctx context.Context, primary, fallback string, maxWait time.Duration) error {
	exp := backoff.NewExponentialBackOff()
	exp.InitialInterval = 200 * time.Millisecond
	exp.MaxInterval = 2 * time.Second
	exp.RandomizationFactor = 0.2
	if maxWait > 0 {
		exp.MaxElapsedTime = maxWait
	} else {
		exp.MaxElapsedTime = 0 // retry indefinitely
	}

	boCtx := backoff.WithContext(exp, ctx)
	client := &http.Client{Timeout: 500 * time.Millisecond}
	return backoff.Retry(func() error {
		return probeReadiness(client, primary, fallback)
	}, boCtx)
}

func probeReadiness(client *http.Client, primary, fallback string) error {
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
		return fmt.Errorf("not ready: %d", resp.StatusCode)
	}
	return nil
}

type contractCodeReader interface {
	CodeAt(ctx context.Context, contract common.Address, blockNumber *big.Int) ([]byte, error)
}

// func ensureContractHasCode(ctx context.Context, reader contractCodeReader, addr common.Address) error {
// 	codeCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
// 	defer cancel()

// 	code, err := reader.CodeAt(codeCtx, addr, nil)
// 	if err != nil {
// 		return fmt.Errorf("failed reading contract bytecode at %s: %w", addr.Hex(), err)
// 	}
// 	if len(code) == 0 {
// 		return fmt.Errorf("contract address %s has no deployed bytecode", addr.Hex())
// 	}
// 	return nil
// }

func deriveNodeID(args []string) ([]byte, string, string) {
	datadir := parseDataDir(args)
	if datadir == "" {
		home := util.AppDataDir("", false)
		if home != "" {
			datadir = filepath.Join(home, "data")
		}
	}

	network := parseNetwork(args)
	candidates := make([]string, 0, 4)
	if datadir != "" {
		seen := make(map[string]struct{})
		ordered := []string{}
		if network != "" {
			ordered = append(ordered, network)
		}
		ordered = append(ordered, "privnet", "testnet", "mixnet", "mainnet")
		for _, n := range ordered {
			if n == "" {
				continue
			}
			if _, ok := seen[n]; ok {
				continue
			}
			seen[n] = struct{}{}
			candidates = append(candidates, filepath.Join(datadir, n, "network.key"))
		}
	}

	for _, candidate := range candidates {
		data, err := os.ReadFile(candidate)
		if err != nil {
			continue
		}
		sum := sha256.Sum256(data)
		trace := hex.EncodeToString(sum[:6])
		networkDir := filepath.Base(filepath.Dir(candidate))
		source := "network.key"
		if networkDir != "." && networkDir != "" {
			source = fmt.Sprintf("network.key:%s", networkDir)
		}
		return sum[:], trace, source
	}

	host, err := os.Hostname()
	if err != nil || host == "" {
		host = "unknown"
	}
	sum := sha256.Sum256([]byte(host))
	trace := hex.EncodeToString(sum[:6])
	return sum[:], trace, "hostname"
}

func parseConfigFile(args []string) string {
	v, _ := parseStringFlag(args, "--configfile")
	return v
}

func parseRPCAuth(args []string, configFile string) (string, string) {
	rpcUser, _ := parseStringFlag(args, "--rpcuser")
	rpcPass, _ := parseStringFlag(args, "--rpcpass")
	if (rpcUser == "" || rpcPass == "") && configFile != "" {
		configUser, configPass, err := parseRPCCredentialsFromConfig(configFile)
		if err != nil {
			log.Warn("run: could not read rpc credentials from config file", "path", configFile, "err", err)
		}
		if rpcUser == "" {
			rpcUser = configUser
		}
		if rpcPass == "" {
			rpcPass = configPass
		}
	}
	return rpcUser, rpcPass
}

func parseRPCCredentialsFromConfig(path string) (string, string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", "", err
	}
	var user, pass string
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if unquoted, err := strconv.Unquote(value); err == nil {
			value = unquoted
		} else {
			value = strings.Trim(value, `"'`)
		}
		switch key {
		case "rpcuser":
			user = value
		case "rpcpass":
			pass = value
		}
	}
	return user, pass, nil
}

func parseDataDir(args []string) string {
	for i, arg := range args {
		switch {
		case strings.HasPrefix(arg, "--datadir="):
			val := strings.TrimPrefix(arg, "--datadir=")
			if val != "" {
				return val
			}
		case arg == "--datadir":
			if i+1 < len(args) && args[i+1] != "" {
				return args[i+1]
			}
		case strings.HasPrefix(arg, "-b="):
			val := strings.TrimPrefix(arg, "-b=")
			if val != "" {
				return val
			}
		case arg == "-b":
			if i+1 < len(args) && args[i+1] != "" {
				return args[i+1]
			}
		}
	}
	// No literal --datadir/-b flag on the command line -- deployments that
	// drive the node entirely via --configfile (e.g. bdagrig-console's
	// docker-compose.yml, which sets datadir= inside node.conf and never
	// passes --datadir) would otherwise fall through to the caller's
	// hardcoded default, which doesn't exist in that layout.
	if configFile := parseConfigFile(args); configFile != "" {
		if datadir, err := parseDataDirFromConfig(configFile); err != nil {
			log.Warn("run: could not read datadir from config file", "path", configFile, "err", err)
		} else if datadir != "" {
			return datadir
		}
	}
	return ""
}

func parseDataDirFromConfig(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok || strings.TrimSpace(key) != "datadir" {
			continue
		}
		value = strings.TrimSpace(value)
		if unquoted, err := strconv.Unquote(value); err == nil {
			value = unquoted
		} else {
			value = strings.Trim(value, `"'`)
		}
		return value, nil
	}
	return "", nil
}

func parseNetwork(args []string) string {
	for _, arg := range args {
		if net, ok := parseBoolFlag(arg, "--privnet"); ok {
			if net {
				return "privnet"
			}
			continue
		}
		if net, ok := parseBoolFlag(arg, "--testnet"); ok {
			if net {
				return "testnet"
			}
			continue
		}
		if net, ok := parseBoolFlag(arg, "--mixnet"); ok {
			if net {
				return "mixnet"
			}
			continue
		}
		if net, ok := parseBoolFlag(arg, "--devnet"); ok {
			if net {
				return "devnet"
			}
			continue
		}
	}
	return ""
}

func applyNetworkParams(network string) {
	switch network {
	case "privnet":
		params.ActiveNetParams = &params.PrivNetParam
	case "testnet":
		params.ActiveNetParams = &params.TestNetParam
	case "mixnet":
		params.ActiveNetParams = &params.MixNetParam
	case "devnet":
		params.ActiveNetParams = &params.DevNetParam
	default:
		params.ActiveNetParams = &params.MainNetParam
	}
}

func buildDagRPCURL(args []string, explicit string) string {
	if explicit != "" {
		return explicit
	}

	host, port := parseRPCListen(args)
	if port == "" {
		port = params.ActiveNetParams.RpcPort
	}
	if host == "" {
		host = "127.0.0.1"
	}
	return "http://" + net.JoinHostPort(host, port)
}

func buildEVMRPCURL(args []string, explicit string, configFile string) string {
	if explicit != "" {
		return explicit
	}

	host, port := parseEVMHTTPListen(args)
	if configFile != "" {
		configHost, configPort, err := parseEVMHTTPListenFromConfig(configFile)
		if err != nil {
			log.Warn("run: could not read EVM HTTP endpoint from config file", "path", configFile, "err", err)
		}
		if host == "" {
			host = configHost
		}
		if port == "" {
			port = configPort
		}
	}
	if host == "" {
		host = cfgutil.DefaultEVMRPCAddr
	}
	if host == "0.0.0.0" || host == "::" || host == "[::]" {
		host = "127.0.0.1"
	}
	if port == "" {
		port = strconv.Itoa(cfgutil.DefaultEVMRPCPort)
	}
	return "http://" + net.JoinHostPort(host, port)
}

func parseEVMHTTPListen(args []string) (string, string) {
	host, _ := parseStringFlag(args, "--evm.http.addr")
	port, _ := parseStringFlag(args, "--evm.http.port")
	return host, port
}

func parseEVMHTTPListenFromConfig(path string) (string, string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", "", err
	}
	var host, port string
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		value = normalizeConfigValue(value)
		switch key {
		case "evm.http.addr":
			host = value
		case "evm.http.port":
			port = value
		}
	}
	return host, port, nil
}

func normalizeConfigValue(value string) string {
	value = strings.TrimSpace(value)
	if unquoted, err := strconv.Unquote(value); err == nil {
		return unquoted
	}
	return strings.Trim(value, `"'`)
}

func parseRPCListen(args []string) (string, string) {
	var host, port string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if strings.HasPrefix(arg, "--rpclisten=") {
			val := strings.TrimPrefix(arg, "--rpclisten=")
			if val == "" {
				host, port = "", ""
				continue
			}
			host, port = splitHostPort(val)
			continue
		}
		if arg == "--rpclisten" {
			var val string
			if i+1 < len(args) {
				next := args[i+1]
				if next != "" && !strings.HasPrefix(next, "-") {
					val = next
				}
			}
			if val == "" {
				host, port = "", ""
				continue
			}
			host, port = splitHostPort(val)
		}
	}
	return host, port
}

func splitHostPort(addr string) (string, string) {
	addr = strings.TrimSpace(addr)
	if addr == "" {
		return "", ""
	}
	if strings.HasPrefix(addr, "[") && strings.HasSuffix(addr, "]") {
		trimmed := strings.TrimPrefix(addr, "[")
		trimmed = strings.TrimSuffix(trimmed, "]")
		return trimmed, ""
	}
	if strings.Contains(addr, ":") {
		if host, port, err := net.SplitHostPort(addr); err == nil {
			return host, port
		}
	}
	return addr, ""
}

func parseStringFlag(args []string, name string) (string, bool) {
	found := false
	value := ""
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if strings.HasPrefix(arg, name+"=") {
			found = true
			value = strings.TrimPrefix(arg, name+"=")
			continue
		}
		if arg == name {
			found = true
			if i+1 < len(args) {
				next := args[i+1]
				if next != "" && !strings.HasPrefix(next, "-") {
					value = next
				} else {
					value = ""
				}
			} else {
				value = ""
			}
		}
	}
	return value, found
}

func parseBoolFlag(arg, name string) (bool, bool) {
	if arg == name {
		return true, true
	}
	if strings.HasPrefix(arg, name+"=") {
		raw := strings.TrimPrefix(arg, name+"=")
		if raw == "" {
			return false, true
		}
		val, err := strconv.ParseBool(raw)
		if err != nil {
			return false, true
		}
		return val, true
	}
	return false, false
}
