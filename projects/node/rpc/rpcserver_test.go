package rpc

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/BlockdagNetworkLabs/bdag/config"
	rpcwebsocket "github.com/BlockdagNetworkLabs/bdag/rpc/websocket"
)

type throttlingService struct{}

type gatedRPCService struct {
	started     chan struct{}
	release     chan struct{}
	blockSubmit bool
	once        sync.Once
}

func (s *gatedRPCService) Wait(_ int) (string, error) {
	s.once.Do(func() { close(s.started) })
	<-s.release
	return "ok", nil
}

func (s *gatedRPCService) SubmitBlockHeader(ctx context.Context, _ int) (string, error) {
	s.once.Do(func() { close(s.started) })
	if !s.blockSubmit {
		return "ok", nil
	}
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case <-s.release:
		return "ok", nil
	}
}

func (s *throttlingService) Sleep(ms int) (string, error) {
	time.Sleep(time.Duration(ms) * time.Millisecond)
	return "ok", nil
}

func (s *throttlingService) SleepWithContext(ctx context.Context, ms int) (string, error) {
	select {
	case <-time.After(time.Duration(ms) * time.Millisecond):
		return "ok", nil
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

func newThrottlingServerWithConfig(t *testing.T, cfg *config.Config) (*RpcServer, string) {
	return newThrottlingServerWithExtraService(t, cfg, "", nil)
}

func newThrottlingServerWithExtraService(t *testing.T, cfg *config.Config, namespace string, extra any) (*RpcServer, string) {
	if cfg.RPCListeners == nil {
		cfg.RPCListeners = []string{"127.0.0.1:0"}
	}
	if cfg.RPCUser == "" {
		cfg.RPCUser = "user"
	}
	if cfg.RPCPass == "" {
		cfg.RPCPass = "pass"
	}
	server, err := NewRPCServer(cfg, nil)
	if err != nil {
		t.Fatalf("NewRPCServer: %v", err)
	}
	if err := server.RegisterService("test", &throttlingService{}); err != nil {
		t.Fatalf("register service: %v", err)
	}
	if extra != nil {
		if err := server.RegisterService(namespace, extra); err != nil {
			t.Fatalf("register extra service: %v", err)
		}
	}
	if err := server.Start(); err != nil {
		t.Fatalf("server start: %v", err)
	}
	return server, server.listeners[0].Addr().String()
}

func newThrottlingServer(t *testing.T) (*RpcServer, string) {
	return newThrottlingServerWithConfig(t, &config.Config{
		RPCListeners:     []string{"127.0.0.1:0"},
		DisableTLS:       true,
		RPCUser:          "user",
		RPCPass:          "pass",
		RPCMaxClients:    10,
		RPCMaxConcurrent: 1,
	})
}

func doRPCMethod(addr string, method string, ms int) (*http.Response, error) {
	return doRPCMethodWithContext(context.Background(), addr, method, ms)
}

func doRPCMethodWithContext(ctx context.Context, addr string, method string, ms int) (*http.Response, error) {
	body := fmt.Sprintf(`{"jsonrpc":"2.0","id":1,"method":"%s","params":[%d]}`, method, ms)
	req, _ := http.NewRequestWithContext(ctx, "POST", "http://"+addr, bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", basicAuthHeader("user", "pass"))
	return http.DefaultClient.Do(req)
}

func doRPC(addr string, ms int) (*http.Response, error) {
	return doRPCMethod(addr, "test_sleep", ms)
}

func basicAuthHeader(user string, pass string) string {
	return "Basic " + base64.StdEncoding.EncodeToString([]byte(user+":"+pass))
}

func newRPCAuthRequest(method string, authHeader string) *http.Request {
	body := fmt.Sprintf(`{"jsonrpc":"2.0","id":1,"method":"%s","params":[0]}`, method)
	req := httptest.NewRequest(http.MethodPost, "http://rpc.local/", bytes.NewBufferString(body))
	req.RemoteAddr = "127.0.0.1:12345"
	req.Header.Set("Content-Type", "application/json")
	if authHeader != "" {
		req.Header.Set("Authorization", authHeader)
	}
	return req
}

func readTestResponseBody(t *testing.T, resp *http.Response) string {
	t.Helper()
	if resp == nil {
		return ""
	}
	body, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		t.Fatalf("read response body: %v", err)
	}
	return string(body)
}

func assertNoAuthMaterial(t *testing.T, body string) {
	t.Helper()
	for _, secret := range []string{"pass", "wrong-pass", "user:pass", "wrong-user:wrong-pass"} {
		if strings.Contains(body, secret) {
			t.Fatalf("auth failure response exposed credential material %q in %q", secret, body)
		}
	}
}

type rpcCheckAuthCase struct {
	name        string
	authHeader  string
	require     bool
	wantAdmin   bool
	wantFailure bool
}

func assertRPCCheckAuthCase(t *testing.T, server *RpcServer, test rpcCheckAuthCase) {
	t.Helper()
	req := newRPCAuthRequest("test_sleep", test.authHeader)
	gotAdmin, err := server.checkAuth(req, test.require)
	if test.wantFailure {
		assertRPCCheckAuthFailure(t, gotAdmin, err)
		return
	}
	if err != nil {
		t.Fatalf("checkAuth: %v", err)
	}
	if gotAdmin != test.wantAdmin {
		t.Fatalf("checkAuth admin = %v, want %v", gotAdmin, test.wantAdmin)
	}
}

func assertRPCCheckAuthFailure(t *testing.T, gotAdmin bool, err error) {
	t.Helper()
	if err == nil {
		t.Fatal("expected auth failure")
	}
	if gotAdmin {
		t.Fatal("failed auth must not grant admin access")
	}
}

func TestRPCCheckAuthBehavior(t *testing.T) {
	server, err := NewRPCServer(&config.Config{
		RPCUser: "user",
		RPCPass: "pass",
	}, nil)
	if err != nil {
		t.Fatalf("NewRPCServer: %v", err)
	}

	tests := []rpcCheckAuthCase{
		{
			name:        "missing required credentials fail",
			require:     true,
			wantFailure: true,
		},
		{
			name:      "missing optional credentials are anonymous",
			require:   false,
			wantAdmin: false,
		},
		{
			name:        "wrong credentials fail",
			authHeader:  basicAuthHeader("wrong-user", "wrong-pass"),
			require:     true,
			wantFailure: true,
		},
		{
			name:       "correct credentials are admin",
			authHeader: basicAuthHeader("user", "pass"),
			require:    true,
			wantAdmin:  true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assertRPCCheckAuthCase(t, server, test)
		})
	}
}

func TestRPCHTTPAuthBehavior(t *testing.T) {
	server, _ := newThrottlingServer(t)
	defer server.Stop()

	tests := []struct {
		name       string
		authHeader string
		wantStatus int
	}{
		{
			name:       "missing credentials",
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:       "wrong credentials",
			authHeader: basicAuthHeader("wrong-user", "wrong-pass"),
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:       "correct credentials",
			authHeader: basicAuthHeader("user", "pass"),
			wantStatus: http.StatusOK,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			server.Handler().ServeHTTP(recorder, newRPCAuthRequest("test_sleep", test.authHeader))
			body := recorder.Body.String()
			if recorder.Code != test.wantStatus {
				t.Fatalf("HTTP status = %d body=%q, want %d", recorder.Code, body, test.wantStatus)
			}
			assertNoAuthMaterial(t, body)
			if test.wantStatus == http.StatusUnauthorized {
				if got := recorder.Header().Get("WWW-Authenticate"); got != `Basic realm="Blockdag RPC"` {
					t.Fatalf("WWW-Authenticate = %q, want Blockdag RPC basic challenge", got)
				}
			}
		})
	}
}

type rpcWebsocketAuthCase struct {
	name       string
	authHeader string
	wantStatus int
	wantOK     bool
}

func dialRPCWebsocket(addr string, authHeader string) (*rpcwebsocket.Conn, *http.Response, error) {
	header := http.Header{}
	if authHeader != "" {
		header.Set("Authorization", authHeader)
	}
	return rpcwebsocket.DefaultDialer.Dial("ws://"+addr+"/ws", header)
}

func assertRPCWebsocketDialOK(t *testing.T, conn *rpcwebsocket.Conn, resp *http.Response, err error) {
	t.Helper()
	if err != nil {
		status, body := websocketFailureStatusAndBody(t, resp)
		t.Fatalf("websocket dial failed status=%d body=%q: %v", status, body, err)
	}
	if conn == nil {
		t.Fatal("websocket dial succeeded without a connection")
	}
	conn.Close()
}

func websocketFailureStatusAndBody(t *testing.T, resp *http.Response) (int, string) {
	t.Helper()
	if resp == nil {
		return 0, ""
	}
	return resp.StatusCode, readTestResponseBody(t, resp)
}

func assertRPCWebsocketAuthRejected(t *testing.T, test rpcWebsocketAuthCase, conn *rpcwebsocket.Conn, resp *http.Response, err error) {
	t.Helper()
	if !errors.Is(err, rpcwebsocket.ErrBadHandshake) {
		t.Fatalf("websocket dial error = %v, want bad handshake", err)
	}
	if conn != nil {
		conn.Close()
		t.Fatal("failed websocket auth returned a connection")
	}
	if resp == nil {
		t.Fatal("failed websocket auth did not return an HTTP response")
	}
	body := readTestResponseBody(t, resp)
	if resp.StatusCode != test.wantStatus {
		t.Fatalf("websocket auth status = %d body=%q, want %d", resp.StatusCode, body, test.wantStatus)
	}
	assertNoAuthMaterial(t, body)
}

func assertRPCWebsocketAuthCase(t *testing.T, addr string, test rpcWebsocketAuthCase) {
	t.Helper()
	conn, resp, err := dialRPCWebsocket(addr, test.authHeader)
	if test.wantOK {
		assertRPCWebsocketDialOK(t, conn, resp, err)
		return
	}
	assertRPCWebsocketAuthRejected(t, test, conn, resp, err)
}

func TestRPCWebsocketAuthBehavior(t *testing.T) {
	server, addr := newThrottlingServer(t)
	defer server.Stop()

	tests := []rpcWebsocketAuthCase{
		{
			name:       "missing credentials",
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:       "wrong credentials",
			authHeader: basicAuthHeader("wrong-user", "wrong-pass"),
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:       "correct credentials",
			authHeader: basicAuthHeader("user", "pass"),
			wantOK:     true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assertRPCWebsocketAuthCase(t, addr, test)
		})
	}
}

func TestRPCMaxConcurrentThrottling(t *testing.T) {
	server, addr := newThrottlingServer(t)
	defer server.Stop()

	var wg sync.WaitGroup
	var resp1, resp2 *http.Response
	var err1, err2 error
	wg.Add(2)
	go func() {
		defer wg.Done()
		resp1, err1 = doRPC(addr, 200)
	}()
	time.Sleep(50 * time.Millisecond)
	go func() {
		defer wg.Done()
		resp2, err2 = doRPC(addr, 0)
	}()
	wg.Wait()
	if err1 != nil || err2 != nil {
		t.Fatalf("request errors: %v %v", err1, err2)
	}
	io.ReadAll(resp1.Body)
	resp1.Body.Close()
	io.ReadAll(resp2.Body)
	resp2.Body.Close()
	if resp1.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 for first request, got %d", resp1.StatusCode)
	}
	if resp2.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 for throttled request, got %d", resp2.StatusCode)
	}
	if active := server.ActiveClients(); active != 0 {
		t.Fatalf("expected active RPC clients to drain, got %d", active)
	}
}

func TestRPCMaxClientsPerHostThrottling(t *testing.T) {
	server, addr := newThrottlingServerWithConfig(t, &config.Config{
		DisableTLS:           true,
		RPCMaxClients:        10,
		RPCMaxClientsPerHost: 1,
		RPCMaxConcurrent:     0,
		RPCMaxConcurrentReqs: 0,
		RPCRequestTimeout:    time.Second,
		RPCMaxWebsockets:     25,
	})
	defer server.Stop()

	var wg sync.WaitGroup
	var resp1 *http.Response
	var err1 error
	wg.Add(1)
	go func() {
		defer wg.Done()
		resp1, err1 = doRPC(addr, 200)
	}()
	deadline := time.Now().Add(time.Second)
	for server.ActiveClients() == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if active := server.ActiveClients(); active != 1 {
		t.Fatalf("expected one active client before second request, got %d", active)
	}

	resp2, err2 := doRPC(addr, 0)
	if err2 != nil {
		t.Fatalf("second request error: %v", err2)
	}
	io.ReadAll(resp2.Body)
	resp2.Body.Close()
	if resp2.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 for per-host throttled request, got %d", resp2.StatusCode)
	}

	wg.Wait()
	if err1 != nil {
		t.Fatalf("first request error: %v", err1)
	}
	io.ReadAll(resp1.Body)
	resp1.Body.Close()
	if resp1.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 for first request, got %d", resp1.StatusCode)
	}
	if active := server.ActiveClients(); active != 0 {
		t.Fatalf("expected active RPC clients to drain, got %d", active)
	}
}

func TestRPCRequestTimeoutCancelsContextAwareHandler(t *testing.T) {
	server, addr := newThrottlingServerWithConfig(t, &config.Config{
		DisableTLS:        true,
		RPCMaxClients:     10,
		RPCMaxConcurrent:  0,
		RPCRequestTimeout: 50 * time.Millisecond,
	})
	defer server.Stop()

	resp, err := doRPCMethod(addr, "test_sleepWithContext", 500)
	if err != nil {
		t.Fatalf("request error: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected JSON-RPC callback error over HTTP 200, got %d: %s", resp.StatusCode, string(body))
	}
	if !bytes.Contains(body, []byte("context deadline exceeded")) {
		t.Fatalf("expected context deadline in response body, got %s", string(body))
	}
	if active := server.ActiveClients(); active != 0 {
		t.Fatalf("expected active RPC clients to drain, got %d", active)
	}
}

func waitForRPC(t *testing.T, timeout time.Duration, condition func() bool, message string) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for !condition() && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if !condition() {
		t.Fatal(message)
	}
}

func TestRPCClientLeaseReleasesOnDisconnectAndTimeout(t *testing.T) {
	server, addr := newThrottlingServerWithConfig(t, &config.Config{
		DisableTLS:           true,
		RPCMaxClients:        10,
		RPCMaxClientsPerHost: 1,
		RPCMaxConcurrent:     0,
		RPCRequestTimeout:    50 * time.Millisecond,
		RPCMaxWebsockets:     25,
	})
	defer server.Stop()

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		resp, err := doRPCMethodWithContext(ctx, addr, "test_sleep", 500)
		if resp != nil {
			io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
		}
		done <- err
	}()
	waitForRPC(t, time.Second, func() bool { return server.ActiveClients() == 1 }, "client lease was not acquired")
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("canceled request did not return")
	}
	waitForRPC(t, time.Second, func() bool { return server.ActiveClients() == 0 }, "disconnect did not release client lease")

	resp, err := doRPC(addr, 0)
	if err != nil {
		t.Fatalf("replacement request error: %v", err)
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("replacement request status = %d, want 200", resp.StatusCode)
	}

	resp, err = doRPCMethod(addr, "test_sleep", 300)
	if err != nil {
		t.Fatalf("timeout request error: %v", err)
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	waitForRPC(t, time.Second, func() bool { return server.ActiveClients() == 0 }, "server timeout did not release client lease")
}

func TestRPCRequestMethodClassifierPreservesBodyAndBounds(t *testing.T) {
	request := func(body string) *http.Request {
		req := httptest.NewRequest(http.MethodPost, "http://rpc.local", strings.NewReader(body))
		req.ContentLength = int64(len(body))
		return req
	}

	body := `{"jsonrpc":"2.0","id":1,"method":"submitBlockHeader","params":[]}`
	req := request(body)
	if got := rpcRequestMethod(req); got != "submitBlockHeader" {
		t.Fatalf("method = %q, want submitBlockHeader", got)
	}
	decoded, err := io.ReadAll(req.Body)
	if err != nil || string(decoded) != body {
		t.Fatalf("classifier changed body: %q (err %v)", decoded, err)
	}

	batch := request(`[{"method":"submitBlock"},{"method":"getBlockCount"}]`)
	if got := rpcRequestMethod(batch); got != "" {
		t.Fatalf("batch method = %q, want conservative normal classification", got)
	}
	if !isCriticalRPCMethod("Blockdag_SubmitBlockHeaderV2") {
		t.Fatal("namespaced submitBlockHeaderV2 should use critical reserve")
	}
	if got := rpcRequestMethod(request(`{"method":`)); got != "" {
		t.Fatalf("malformed method = %q, want fail-closed classification", got)
	}
	duplicate := request(`{"method":"submitBlock","Method":"getBlockCount"}`)
	if got := rpcRequestMethod(duplicate); got != "" {
		t.Fatalf("duplicate method = %q, want fail-closed classification", got)
	}
	if _, _, err := parseRequest([]byte(`{"method":"submitBlock","Method":"getBlockCount"}`)); err == nil {
		t.Fatal("duplicate method key was accepted by JSON-RPC parser")
	}

	large := request(`{"method":"submitBlock","params":["` + strings.Repeat("0", rpcMethodPeekBytes) + `"]}`)
	if got := rpcRequestMethod(large); got != "submitBlock" {
		t.Fatalf("large bounded method = %q, want submitBlock", got)
	}
}

func TestRPCNormalAndCriticalAdmissionSaturation(t *testing.T) {
	gate := &gatedRPCService{started: make(chan struct{}), release: make(chan struct{})}
	server, addr := newThrottlingServerWithExtraService(t, &config.Config{
		DisableTLS:            true,
		RPCMaxClients:         10,
		RPCMaxClientsPerHost:  1,
		RPCMaxConcurrent:      1,
		RPCCriticalConcurrent: 1,
		RPCRequestTimeout:     5 * time.Second,
	}, "gate", gate)
	defer server.Stop()
	var releaseGateOnce sync.Once
	releaseGate := func() { releaseGateOnce.Do(func() { close(gate.release) }) }
	defer releaseGate()

	firstDone := make(chan error, 1)
	go func() {
		resp, err := doRPCMethod(addr, "gate_wait", 0)
		if resp != nil {
			io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
		}
		firstDone <- err
	}()
	select {
	case <-gate.started:
	case <-time.After(time.Second):
		t.Fatal("normal request did not start")
	}
	if normal, critical := server.ActiveRequests(); normal != 1 || critical != 0 {
		t.Fatalf("active requests = (%d, %d), want (1, 0)", normal, critical)
	}

	criticalResp, err := doRPCMethod(addr, "gate_submitBlockHeader", 0)
	if err != nil {
		t.Fatalf("critical request error: %v", err)
	}
	io.Copy(io.Discard, criticalResp.Body)
	criticalResp.Body.Close()
	if criticalResp.StatusCode != http.StatusOK {
		t.Fatalf("critical request status = %d, want 200", criticalResp.StatusCode)
	}

	secondNormal, err := doRPCMethod(addr, "test_sleep", 0)
	if err != nil {
		t.Fatalf("second normal request error: %v", err)
	}
	io.Copy(io.Discard, secondNormal.Body)
	secondNormal.Body.Close()
	if secondNormal.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("normal saturation status = %d, want 503", secondNormal.StatusCode)
	}

	releaseGate()
	select {
	case <-firstDone:
	case <-time.After(time.Second):
		t.Fatal("normal request did not finish")
	}

	criticalGate := &gatedRPCService{started: make(chan struct{}), release: make(chan struct{}), blockSubmit: true}
	server2, addr2 := newThrottlingServerWithExtraService(t, &config.Config{
		DisableTLS: true, RPCMaxClients: 10, RPCMaxConcurrent: 1, RPCCriticalConcurrent: 1,
		RPCRequestTimeout: time.Second,
	}, "gate", criticalGate)
	defer server2.Stop()
	go func() {
		resp, _ := doRPCMethod(addr2, "gate_submitBlockHeader", 0)
		if resp != nil {
			io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
		}
	}()
	select {
	case <-criticalGate.started:
	case <-time.After(time.Second):
		t.Fatal("critical request did not start")
	}
	secondCritical, err := doRPCMethod(addr2, "gate_submitBlockHeader", 0)
	if err != nil {
		t.Fatalf("second critical request error: %v", err)
	}
	io.Copy(io.Discard, secondCritical.Body)
	secondCritical.Body.Close()
	if secondCritical.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("critical saturation status = %d, want 503", secondCritical.StatusCode)
	}
	close(criticalGate.release)
}
