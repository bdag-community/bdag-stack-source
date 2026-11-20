package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/BlockdagNetworkLabs/bdag/params"
	"github.com/ethereum/go-ethereum/common"
	"github.com/urfave/cli/v2"
)

func newRunTestContext(t *testing.T, args []string) *cli.Context {
	t.Helper()

	fs := flag.NewFlagSet("nodeworker-test", flag.ContinueOnError)
	fs.String("node-binary", "/bin/true", "")
	fs.String("node-args", "", "")
	fs.String("rpc-url", "", "")
	fs.String("dag-rpc-url", "", "")
	fs.String("persist-root", "", "")
	fs.Uint64("health-min-peers", 0, "")
	fs.Duration("rollout-window", 0, "")
	fs.Duration("health.startup-timeout", 0, "")
	fs.Duration("health.liveness-timeout", 0, "")
	fs.Duration("health.probe-interval", 0, "")
	fs.Duration("health.mining-readiness-timeout", 0, "")
	fs.Duration("health.mining-readiness-grace", 0, "")
	fs.Duration("status-log.syncing-interval", 0, "")
	fs.Duration("status-log.synced-interval", 0, "")
	fs.Bool("snapshot.enable", false, "")
	fs.Duration("snapshot.interval", time.Hour, "")
	fs.String("snapshot.output-dir", defaultSnapshotOutputDir, "")
	fs.String("snapshot.datadir", "", "")
	fs.String("snapshot.network", defaultSnapshotNetwork, "")
	fs.Int("snapshot.chunk-size", 0, "")
	fs.Bool("snapshot.run-on-start", false, "")
	if err := fs.Parse(args); err != nil {
		t.Fatalf("parse flags: %v", err)
	}
	app := cli.NewApp()
	return cli.NewContext(app, fs, nil)
}

func TestGovernanceContractAddressDefault(t *testing.T) {
	const expected = "0x1000000000000000000000000000000000000005"
	if governanceContractAddressHex != expected {
		t.Fatalf("unexpected governance contract: got %q want %q", governanceContractAddressHex, expected)
	}
}

type fakeCodeReader struct {
	code []byte
	err  error
}

func (f fakeCodeReader) CodeAt(ctx context.Context, _ common.Address, _ *big.Int) ([]byte, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}
	if f.err != nil {
		return nil, f.err
	}
	return f.code, nil
}

// func TestEnsureContractHasCodeRejectsEOA(t *testing.T) {
// 	reader := fakeCodeReader{code: nil}
// 	err := ensureContractHasCode(context.Background(), reader, common.HexToAddress("0x0000000000000000000000000000000000000001"))
// 	if err == nil || !strings.Contains(err.Error(), "no deployed bytecode") {
// 		t.Fatalf("expected bytecode error, got %v", err)
// 	}
// }

// func TestEnsureContractHasCodeAllowsContracts(t *testing.T) {
// 	reader := fakeCodeReader{code: []byte{0x60, 0x01}}
// 	if err := ensureContractHasCode(context.Background(), reader, common.HexToAddress("0x0000000000000000000000000000000000000002")); err != nil {
// 		t.Fatalf("expected contract validation to pass, got %v", err)
// 	}
// }

func TestParseDataDir(t *testing.T) {
	args := []string{"--datadir=/tmp/data"}
	if got := parseDataDir(args); got != "/tmp/data" {
		t.Fatalf("unexpected datadir: %q", got)
	}
	args = []string{"-b", "/var/lib/bdag"}
	if got := parseDataDir(args); got != "/var/lib/bdag" {
		t.Fatalf("unexpected datadir from short flag: %q", got)
	}
	args = []string{"-b=/srv/bdag"}
	if got := parseDataDir(args); got != "/srv/bdag" {
		t.Fatalf("unexpected datadir from equals flag: %q", got)
	}
}

func TestParseDataDirFallsBackToConfigFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "node.conf")
	if err := os.WriteFile(path, []byte("# comment\nlisten=0.0.0.0\ndatadir=/var/lib/bdagStack/node\n"), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	if got := parseDataDir([]string{"--configfile", path}); got != "/var/lib/bdagStack/node" {
		t.Fatalf("unexpected datadir from config file: %q", got)
	}
}

func TestParseDataDirFlagOverridesConfigFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "node.conf")
	if err := os.WriteFile(path, []byte("datadir=/from/config\n"), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	if got := parseDataDir([]string{"--datadir=/from/flag", "--configfile", path}); got != "/from/flag" {
		t.Fatalf("unexpected datadir: %q", got)
	}
}

func TestParseNetwork(t *testing.T) {
	if got := parseNetwork([]string{"--privnet"}); got != "privnet" {
		t.Fatalf("expected privnet, got %q", got)
	}
	if got := parseNetwork([]string{"--privnet=false"}); got != "" {
		t.Fatalf("expected empty when flag disabled, got %q", got)
	}
	if got := parseNetwork([]string{"--testnet"}); got != "testnet" {
		t.Fatalf("expected testnet, got %q", got)
	}
	if got := parseNetwork([]string{"--devnet"}); got != "devnet" {
		t.Fatalf("expected devnet, got %q", got)
	}
}

func TestApplyNetworkParams(t *testing.T) {
	prev := params.ActiveNetParams
	t.Cleanup(func() {
		params.ActiveNetParams = prev
	})

	applyNetworkParams("testnet")
	if params.ActiveNetParams != &params.TestNetParam {
		t.Fatalf("expected testnet params active")
	}

	applyNetworkParams("privnet")
	if params.ActiveNetParams != &params.PrivNetParam {
		t.Fatalf("expected privnet params active")
	}

	applyNetworkParams("devnet")
	if params.ActiveNetParams != &params.DevNetParam {
		t.Fatalf("expected devnet params active")
	}

	applyNetworkParams("")
	if params.ActiveNetParams != &params.MainNetParam {
		t.Fatalf("expected mainnet params active")
	}
}

func TestBuildDagRPCURLUsesNetworkDefaults(t *testing.T) {
	prev := params.ActiveNetParams
	t.Cleanup(func() {
		params.ActiveNetParams = prev
	})

	applyNetworkParams("testnet")
	if got := buildDagRPCURL(nil, ""); got != "http://127.0.0.1:38131" {
		t.Fatalf("unexpected dag rpc url: %q", got)
	}
}

func TestBuildDagRPCURLRespectsRPCListen(t *testing.T) {
	prev := params.ActiveNetParams
	t.Cleanup(func() {
		params.ActiveNetParams = prev
	})

	applyNetworkParams("")
	if got := buildDagRPCURL([]string{"--rpclisten=0.0.0.0:9999"}, ""); got != "http://0.0.0.0:9999" {
		t.Fatalf("unexpected dag rpc url: %q", got)
	}

	applyNetworkParams("")
	if got := buildDagRPCURL([]string{"--rpclisten=10.0.0.2"}, ""); got != "http://10.0.0.2:8131" {
		t.Fatalf("unexpected dag rpc url with host-only: %q", got)
	}
}

func TestBuildEVMRPCURLResolvesConfigGatewayAddress(t *testing.T) {
	path := filepath.Join(t.TempDir(), "node.conf")
	if err := os.WriteFile(path, []byte("evm.http.addr=0.0.0.0\nevm.http.port=18545\n"), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	got := buildEVMRPCURL([]string{"--configfile", path}, "", path)
	if got != "http://127.0.0.1:18545" {
		t.Fatalf("unexpected EVM RPC URL: %q", got)
	}
}

func TestBuildEVMRPCURLFlagsOverrideConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "node.conf")
	if err := os.WriteFile(path, []byte("evm.http.addr=0.0.0.0\nevm.http.port=18545\n"), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	got := buildEVMRPCURL([]string{"--evm.http.addr=10.0.0.2", "--evm.http.port", "28545"}, "", path)
	if got != "http://10.0.0.2:28545" {
		t.Fatalf("unexpected EVM RPC URL: %q", got)
	}
}

func TestParseStringFlag(t *testing.T) {
	if val, ok := parseStringFlag([]string{"--rpcuser=tester"}, "--rpcuser"); !ok || val != "tester" {
		t.Fatalf("unexpected rpcuser parse: %v %q", ok, val)
	}

	if val, ok := parseStringFlag([]string{"--rpcpass", "secret"}, "--rpcpass"); !ok || val != "secret" {
		t.Fatalf("unexpected rpcpass parse: %v %q", ok, val)
	}
}

func TestParseRPCAuthFallsBackToConfigFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "node.conf")
	if err := os.WriteFile(path, []byte("# comment\nrpcuser=from_config\nrpcpass=\"config secret\"\n"), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	user, pass := parseRPCAuth([]string{"--configfile", path}, path)
	if user != "from_config" || pass != "config secret" {
		t.Fatalf("unexpected rpc auth from config: user=%q pass=%q", user, pass)
	}
}

func TestParseRPCAuthFlagsOverrideConfigFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "node.conf")
	if err := os.WriteFile(path, []byte("rpcuser=from_config\nrpcpass=config_pass\n"), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	user, pass := parseRPCAuth([]string{"--rpcuser=flag_user", "--rpcpass", "flag_pass", "--configfile", path}, path)
	if user != "flag_user" || pass != "flag_pass" {
		t.Fatalf("expected flag auth to win: user=%q pass=%q", user, pass)
	}
}

func TestDeriveNodeIDFromNetworkKey(t *testing.T) {
	dir := t.TempDir()
	privnetDir := filepath.Join(dir, "privnet")
	if err := os.MkdirAll(privnetDir, 0o755); err != nil {
		t.Fatalf("mkdir failed: %v", err)
	}
	keyPath := filepath.Join(privnetDir, "network.key")
	keyData := []byte("super-secret-key")
	if err := os.WriteFile(keyPath, keyData, 0o600); err != nil {
		t.Fatalf("write network.key failed: %v", err)
	}

	id, trace, source := deriveNodeID([]string{"--datadir=" + dir, "--privnet"})
	sum := sha256.Sum256(keyData)
	if !strings.Contains(source, "privnet") {
		t.Fatalf("expected source to mention privnet, got %q", source)
	}
	if trace != hex.EncodeToString(sum[:6]) {
		t.Fatalf("unexpected trace: got %q want %q", trace, hex.EncodeToString(sum[:6]))
	}
	if !bytes.Equal(id, sum[:]) {
		t.Fatalf("unexpected node id hash")
	}
}

func TestDeriveNodeIDFallbackHostname(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "missing")
	id, trace, source := deriveNodeID([]string{"--datadir=" + dir})
	host, err := os.Hostname()
	if err != nil {
		t.Fatalf("hostname: %v", err)
	}
	sum := sha256.Sum256([]byte(host))
	expectedTrace := hex.EncodeToString(sum[:6])
	if trace != expectedTrace {
		t.Fatalf("unexpected trace for hostname fallback: got %q want %q", trace, expectedTrace)
	}
	if source != "hostname" {
		t.Fatalf("expected hostname source, got %q", source)
	}
	if !bytes.Equal(id, sum[:]) {
		t.Fatalf("unexpected id for hostname fallback")
	}
}
