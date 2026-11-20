package bdag

import (
	"context"
	"net"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/rpc"
)

func TestPoolRewardIPCRecoversOnlyStaleSocket(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "reward.sock")
	live, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	live.SetUnlinkOnClose(false)
	if _, err = startPoolRewardIPC(nil, path); err == nil {
		live.Close()
		t.Fatal("live socket was replaced")
	}
	if err = live.Close(); err != nil {
		t.Fatal(err)
	}
	stop, err := startPoolRewardIPC(nil, path)
	if err != nil {
		t.Fatalf("stale socket was not recovered: %v", err)
	}
	defer stop()
	if _, err = startPoolRewardIPC(nil, path); err == nil {
		t.Fatal("second live listener was accepted")
	}
}

func TestPoolRewardEvidenceRequiresIPC(t *testing.T) {
	server := rpc.NewServer()
	defer server.Stop()
	if err := server.RegisterName("bdag", &poolRewardIPCService{}); err != nil {
		t.Fatal(err)
	}
	httpServer := httptest.NewServer(server)
	defer httpServer.Close()
	httpClient, err := rpc.DialHTTP(httpServer.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer httpClient.Close()
	var result PoolRewardEvidence
	hash, wallet := common.HexToHash("0x12"), common.HexToAddress("0x1234")
	if err = httpClient.CallContext(context.Background(), &result, "bdag_getPoolRewardEvidence", hash, wallet); err == nil || !strings.Contains(err.Error(), "requires local IPC") {
		t.Fatalf("HTTP replay not refused: %v", err)
	}
	dir := t.TempDir()
	if err = os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "rpc.sock")
	occupied := filepath.Join(dir, "existing")
	if err = os.WriteFile(occupied, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = startPoolRewardIPC(nil, occupied); err == nil {
		t.Fatal("existing path was accepted")
	}
	stop, err := startPoolRewardIPC(nil, path)
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("socket permissions invalid: %v %v", info, err)
	}
	ipcClient, err := rpc.DialIPC(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer ipcClient.Close()
	if err = ipcClient.CallContext(context.Background(), &result, "bdag_getPoolRewardEvidence", hash, wallet); err == nil || !strings.Contains(err.Error(), "chain unavailable") {
		t.Fatalf("IPC did not reach chain validation: %v", err)
	}
}
