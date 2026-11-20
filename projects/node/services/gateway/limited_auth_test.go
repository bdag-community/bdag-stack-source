package gateway

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/BlockdagNetworkLabs/bdag/config"
	"github.com/BlockdagNetworkLabs/bdag/rpc"
	"golang.org/x/time/rate"
)

type gatewayLimitedService struct{}

func (gatewayLimitedService) GetBlockCount() (string, error)      { return "getBlockCount", nil }
func (gatewayLimitedService) GetBlock() (string, error)           { return "getBlock", nil }
func (gatewayLimitedService) GetBlockByOrder() (string, error)    { return "getBlockByOrder", nil }
func (gatewayLimitedService) Tips() (string, error)               { return "tips", nil }
func (gatewayLimitedService) GetCoinbaseAddress() (string, error) { return "getCoinbaseAddress", nil }
func (gatewayLimitedService) IsCurrent() (string, error)          { return "isCurrent", nil }
func (gatewayLimitedService) GetPeerInfo() (string, error)        { return "getPeerInfo", nil }
func (gatewayLimitedService) GetPeerForks() (string, error)       { return "getPeerForks", nil }
func (gatewayLimitedService) GetPeerList() (string, error)        { return "getPeerList", nil }
func (gatewayLimitedService) GetPeerDiscoveryStatus() (string, error) {
	return "getPeerDiscoveryStatus", nil
}
func (gatewayLimitedService) Stop() (string, error) { return "stop", nil }

func newGatewayWithNativeLimitedHandler(t *testing.T) (*Gateway, *rpc.RpcServer) {
	t.Helper()
	native, err := rpc.NewRPCServer(&config.Config{
		RPCUser:      "admin",
		RPCPass:      "primary",
		RPCLimitUser: "readonly",
		RPCLimitPass: "limited",
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := native.RegisterService("Blockdag", gatewayLimitedService{}); err != nil {
		t.Fatal(err)
	}
	if err := native.Service.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = native.Service.Stop() })

	gateway, err := NewDAG(Config{
		ListenAddr:                 "127.0.0.1:0",
		PeerDagPort:                38131,
		PeerEvmPort:                18545,
		InboundAuth:                true,
		InboundUser:                "admin",
		InboundPass:                "primary",
		InboundLimitUser:           "readonly",
		InboundLimitPass:           "limited",
		UpUser:                     "admin",
		UpPass:                     "primary",
		UpLimitUser:                "readonly",
		UpLimitPass:                "limited",
		AuthorizeLimitedRequest:    native.AuthorizeLimitedRequest,
		MaxConcurrentRequests:      8,
		CriticalConcurrentRequests: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	gateway.DagHandler = native.Handler()
	gateway.SyncChecker = &mockSyncChecker{height: 1}
	return gateway.Gateway, native
}

func gatewayAuth(user, pass string) string {
	return "Basic " + base64.StdEncoding.EncodeToString([]byte(user+":"+pass))
}

func gatewayRequest(g *Gateway, auth, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "http://gateway.local/", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", auth)
	recorder := httptest.NewRecorder()
	g.handle(recorder, req)
	return recorder
}

func TestGatewayLimitedNativeLocalAndAuthFailures(t *testing.T) {
	g, _ := newGatewayWithNativeLimitedHandler(t)
	limited := gatewayAuth("readonly", "limited")
	if recorder := gatewayRequest(g, limited, `{"jsonrpc":"2.0","id":1,"method":"tips","params":[]}`); recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"tips"`) {
		t.Fatalf("limited local read status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if recorder := gatewayRequest(g, limited, `{"jsonrpc":"2.0","id":1,"method":"stop","params":[]}`); recorder.Code != http.StatusForbidden {
		t.Fatalf("limited local write status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if recorder := gatewayRequest(g, limited, `{"jsonrpc":"2.0","id":1,"method":"p2p_getStatus","params":[]}`); recorder.Code != http.StatusForbidden {
		t.Fatalf("limited p2p status=%d body=%s, want forbidden", recorder.Code, recorder.Body.String())
	}
	if recorder := gatewayRequest(g, limited, `{"jsonrpc":"2.0","id":1,"method":"p2p_getInfo","params":[]}`); recorder.Code != http.StatusForbidden {
		t.Fatalf("limited p2p info=%d body=%s, want forbidden", recorder.Code, recorder.Body.String())
	}
	if recorder := gatewayRequest(g, limited, `[{"jsonrpc":"2.0","id":1,"method":"tips"}]`); recorder.Code != http.StatusForbidden {
		t.Fatalf("limited batch status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if recorder := gatewayRequest(g, limited, `{"jsonrpc":"2.0","id":1,"method":`); recorder.Code != http.StatusBadRequest {
		t.Fatalf("limited malformed status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if recorder := gatewayRequest(g, limited, `{"jsonrpc":"2.0","id":1,"method":"tips","method":"stop"}`); recorder.Code != http.StatusBadRequest {
		t.Fatalf("limited duplicate JSON status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	primaryReq := httptest.NewRequest(http.MethodPost, "http://gateway.local", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tips","params":[]}`))
	primaryReq.Header.Set("Authorization", gatewayAuth("admin", "primary"))
	primaryReq.Header.Add("Authorization", gatewayAuth("admin", "primary"))
	primaryResp := httptest.NewRecorder()
	g.handle(primaryResp, primaryReq)
	if primaryResp.Code != http.StatusUnauthorized {
		t.Fatalf("duplicate authorization status=%d body=%s", primaryResp.Code, primaryResp.Body.String())
	}

	wsReq := httptest.NewRequest(http.MethodGet, "http://gateway.local", nil)
	wsReq.Header.Set("Authorization", limited)
	wsReq.Header.Set("Connection", "Upgrade")
	wsReq.Header.Set("Upgrade", "websocket")
	wsResp := httptest.NewRecorder()
	g.handle(wsResp, wsReq)
	if wsResp.Code != http.StatusForbidden {
		t.Fatalf("limited websocket status=%d body=%s", wsResp.Code, wsResp.Body.String())
	}
}

func TestGatewayRelayPreservesVerifiedRoleAndRejectsInvalidLimitedUpstream(t *testing.T) {
	var mu sync.Mutex
	var received []string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, pass, ok := r.BasicAuth()
		if !ok {
			http.Error(w, "missing auth", http.StatusUnauthorized)
			return
		}
		mu.Lock()
		received = append(received, user+":"+pass)
		mu.Unlock()
		fmt.Fprint(w, `{"jsonrpc":"2.0","id":1,"result":"relayed"}`)
	}))
	defer upstream.Close()
	parsed, err := url.Parse(upstream.URL)
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(parsed.Port())
	if err != nil {
		t.Fatal(err)
	}

	g, _ := newGatewayWithNativeLimitedHandler(t)
	g.cfg.PeerDagPort = port
	g.authPorts = map[string]bool{parsed.Port(): true}
	g.peerManager.peers[parsed.Hostname()] = &Peer{Host: parsed.Hostname(), DAGPort: port, EVMPort: port, IsActive: true, IsSynced: true, BlockHeight: 2}
	g.SyncChecker = &mockSyncChecker{syncing: true, height: 1}
	if recorder := gatewayRequest(g, gatewayAuth("readonly", "limited"), `{"jsonrpc":"2.0","id":1,"method":"getBlock","params":[]}`); recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), "relayed") {
		t.Fatalf("limited relay status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if recorder := gatewayRequest(g, gatewayAuth("admin", "primary"), `{"jsonrpc":"2.0","id":1,"method":"getBlock","params":[]}`); recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), "relayed") {
		t.Fatalf("primary relay status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	mu.Lock()
	got := append([]string(nil), received...)
	mu.Unlock()
	if len(got) != 2 || got[0] != "readonly:limited" || got[1] != "admin:primary" {
		t.Fatalf("forwarded credentials = %v, want limited then primary", got)
	}
	before := len(got)
	g.authPorts = map[string]bool{}
	if recorder := gatewayRequest(g, gatewayAuth("readonly", "limited"), `{"jsonrpc":"2.0","id":1,"method":"getBlock","params":[]}`); recorder.Code != http.StatusForbidden {
		t.Fatalf("limited unauthenticated target status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	mu.Lock()
	got = append([]string(nil), received...)
	mu.Unlock()
	if len(got) != before {
		t.Fatalf("limited unauthenticated target made outbound request: %v", got)
	}
	g.authPorts = map[string]bool{parsed.Port(): true}

	for name, pair := range map[string][2]string{
		"missing":   {"", ""},
		"colliding": {"admin", "primary"},
	} {
		g.cfg.UpLimitUser, g.cfg.UpLimitPass = pair[0], pair[1]
		before := len(got)
		if recorder := gatewayRequest(g, gatewayAuth("readonly", "limited"), `{"jsonrpc":"2.0","id":1,"method":"getBlock","params":[]}`); recorder.Code != http.StatusForbidden {
			t.Fatalf("%s limited relay status=%d body=%s", name, recorder.Code, recorder.Body.String())
		}
		mu.Lock()
		got = append([]string(nil), received...)
		mu.Unlock()
		if len(got) != before {
			t.Fatalf("%s invalid limited upstream made outbound request: %v", name, got)
		}
	}
}

func TestGatewayLimitedAuthorizationAdmissionAndByteCap(t *testing.T) {
	g, _ := newGatewayWithNativeLimitedHandler(t)
	var reads int
	g.cfg.AuthorizeLimitedRequest = func(w http.ResponseWriter, r *http.Request) bool {
		body, err := io.ReadAll(r.Body)
		reads = len(body)
		r.Body = io.NopCloser(bytes.NewReader(body))
		if err != nil {
			http.Error(w, "malformed RPC request.", http.StatusBadRequest)
			return false
		}
		return true
	}

	// Admission rejection must not invoke the body-consuming policy.
	g.globalLimiter = rate.NewLimiter(0, 0)
	if recorder := gatewayRequest(g, gatewayAuth("readonly", "limited"), `{"jsonrpc":"2.0","id":1,"method":"tips"}`); recorder.Code != http.StatusTooManyRequests {
		t.Fatalf("rate rejection status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if reads != 0 {
		t.Fatalf("rate rejection consumed %d body bytes", reads)
	}

	// An already-expired request context must also stop before authorization.
	g.globalLimiter = rate.NewLimiter(rate.Inf, 1)
	g.cfg.RequestTimeout = 0
	timeoutReq := httptest.NewRequest(http.MethodPost, "http://gateway.local/", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tips"}`))
	timeoutReq.Header.Set("Content-Type", "application/json")
	timeoutReq.Header.Set("Authorization", gatewayAuth("readonly", "limited"))
	timeoutCtx, cancel := context.WithCancel(timeoutReq.Context())
	cancel()
	timeoutReq = timeoutReq.WithContext(timeoutCtx)
	timeoutResp := httptest.NewRecorder()
	g.handle(timeoutResp, timeoutReq)
	if timeoutResp.Code != http.StatusGatewayTimeout {
		t.Fatalf("timeout rejection status=%d body=%s", timeoutResp.Code, timeoutResp.Body.String())
	}
	if reads != 0 {
		t.Fatalf("timeout rejection consumed %d body bytes", reads)
	}

	// MaxBytesReader must bound what the limited policy can consume.
	g.cfg.MaxRequestBytes = 8
	reads = 0
	if recorder := gatewayRequest(g, gatewayAuth("readonly", "limited"), `{"jsonrpc":"2.0","id":1,"method":"tips"}`); recorder.Code == http.StatusInternalServerError {
		t.Fatalf("byte-capped request failed unexpectedly: status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if reads > int(g.cfg.MaxRequestBytes) {
		t.Fatalf("limited policy consumed %d bytes beyond cap %d", reads, g.cfg.MaxRequestBytes)
	}
}

func TestLimitedForwardRejectsRedirectsToUnapprovedTargets(t *testing.T) {
	var redirectMu sync.Mutex
	var redirectedRequests int
	var leakedCredentials bool
	unapproved := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		redirectMu.Lock()
		defer redirectMu.Unlock()
		redirectedRequests++
		_, _, leakedCredentials = r.BasicAuth()
		w.WriteHeader(http.StatusOK)
	}))
	defer unapproved.Close()

	for _, status := range []int{http.StatusTemporaryRedirect, http.StatusPermanentRedirect} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			approved := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				http.Redirect(w, r, unapproved.URL, status)
			}))
			defer approved.Close()
			parsed, err := url.Parse(approved.URL)
			if err != nil {
				t.Fatal(err)
			}
			g := &Gateway{
				cfg: Config{
					UpUser:      "admin",
					UpPass:      "primary",
					UpLimitUser: "readonly",
					UpLimitPass: "limited",
				},
				client:    approved.Client(),
				authPorts: map[string]bool{parsed.Port(): true},
			}
			_, err = g.forwardCtx(context.Background(), approved.URL, []byte(`{"jsonrpc":"2.0","id":1,"method":"getBlock","params":[]}`), roleLimited)
			if err == nil || !strings.Contains(err.Error(), strconv.Itoa(status)) {
				t.Fatalf("limited redirect status=%d error=%v", status, err)
			}
		})
	}
	redirectMu.Lock()
	defer redirectMu.Unlock()
	if redirectedRequests != 0 || leakedCredentials {
		t.Fatalf("unapproved redirect target received %d requests; credentials leaked=%t", redirectedRequests, leakedCredentials)
	}
}
