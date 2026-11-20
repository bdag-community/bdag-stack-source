package gateway

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	l "github.com/BlockdagNetworkLabs/bdag/log"
)

func TestCertifiedEVMMethodsFailClosedWithoutRelayRC21(t *testing.T) {
	tests := []struct {
		name      string
		method    string
		forwarded string
	}{
		{name: "state read", method: "eth_getBalance"},
		{name: "history read", method: "eth_getBlockByNumber"},
		{name: "transaction submission", method: "eth_sendRawTransaction"},
		{name: "filter lifecycle", method: "eth_getFilterChanges"},
		{name: "debug diagnostic", method: "debug_traceTransaction"},
		{name: "trace diagnostic", method: "trace_block"},
		{name: "wallet transaction", method: "personal_sendTransaction"},
		{name: "spoofed forwarding marker", method: "eth_getBalance", forwarded: "bdag-gateway"},
		{name: "untrusted forwarding marker", method: "eth_getBalance", forwarded: "attacker"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var localHits atomic.Int32
			g, peerHits := newEVMGatewayWithPeer(t,
				&mockSyncChecker{syncing: true, height: 20, nativeEVMHeight: 20, forceInvisible: true},
				http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					localHits.Add(1)
					fmt.Fprint(w, `{"jsonrpc":"2.0","id":1,"result":"local"}`)
				}),
			)

			body := fmt.Sprintf(`{"jsonrpc":"2.0","id":1,"method":%q,"params":[]}`, test.method)
			req := newEVMRoutingRequest(body)
			if test.forwarded != "" {
				req.Header.Set("X-Forwarded-By", test.forwarded)
			}
			rr := httptest.NewRecorder()
			g.handle(rr, req)

			if rr.Code != http.StatusOK || localHits.Load() != 0 || peerHits.Load() != 0 {
				t.Fatalf("status/local/peer/body = %d/%d/%d/%q, want fail-closed JSON-RPC error", rr.Code, localHits.Load(), peerHits.Load(), rr.Body.String())
			}
			if !strings.Contains(rr.Body.String(), `"code":-32000`) || !strings.Contains(rr.Body.String(), "EVM head unavailable") {
				t.Fatalf("response body = %q, want certified-head error", rr.Body.String())
			}
		})
	}
}

func TestHeadIndependentAndNodeSpecificEVMMethodsStayLocal(t *testing.T) {
	methods := []string{
		"eth_chainId",
		"eth_accounts",
		"eth_sign",
		"eth_newPendingTransactionFilter",
		"eth_uninstallFilter",
		"eth_unsubscribe",
		"txpool_status",
		"personal_sign",
		"admin_exportChain",
		"miner_setGasPrice",
		"net_version",
		"web3_clientVersion",
		"rpc_modules",
		"bdag_getBdagChainInfo",
	}

	for _, method := range methods {
		t.Run(method, func(t *testing.T) {
			var localHits atomic.Int32
			g, peerHits := newEVMGatewayWithPeer(t,
				&mockSyncChecker{syncing: true, height: 20, nativeEVMHeight: 30, forceInvisible: true},
				http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					localHits.Add(1)
					fmt.Fprint(w, `{"jsonrpc":"2.0","id":1,"result":"local"}`)
				}),
			)

			body := fmt.Sprintf(`{"jsonrpc":"2.0","id":1,"method":%q,"params":[]}`, method)
			rr := httptest.NewRecorder()
			g.handle(rr, newEVMRoutingRequest(body))

			if rr.Code != http.StatusOK || localHits.Load() != 1 || peerHits.Load() != 0 || !strings.Contains(rr.Body.String(), `"result":"local"`) {
				t.Fatalf("status/local/peer/body = %d/%d/%d/%q, want local head-independent response", rr.Code, localHits.Load(), peerHits.Load(), rr.Body.String())
			}
		})
	}
}

func TestPublicEngineMethodsAreRejected(t *testing.T) {
	tests := []struct {
		name       string
		body       string
		wantStatus int
		wantLocal  int32
		wantText   string
	}{
		{
			name:       "scalar call",
			body:       `{"jsonrpc":"2.0","id":1,"method":"engine_newPayloadV1","params":[]}`,
			wantStatus: http.StatusOK,
			wantText:   `"code":-32601`,
		},
		{
			name:       "notification",
			body:       `{"jsonrpc":"2.0","method":"engine_forkchoiceUpdatedV1","params":[]}`,
			wantStatus: http.StatusNoContent,
		},
		{
			name:       "mixed batch",
			body:       `[{"jsonrpc":"2.0","id":1,"method":"engine_newPayloadV1","params":[]},{"jsonrpc":"2.0","id":2,"method":"eth_chainId","params":[]}]`,
			wantStatus: http.StatusOK,
			wantLocal:  1,
			wantText:   `"code":-32601`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var localHits atomic.Int32
			g := &Gateway{
				cfg: Config{IsEVM: true},
				EvmHandler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					localHits.Add(1)
					fmt.Fprint(w, `{"jsonrpc":"2.0","id":2,"result":"local"}`)
				}),
				SyncChecker: &mockSyncChecker{forceInvisible: true},
				peerManager: &PeerManager{peers: make(map[string]*Peer)},
				logger:      l.New(l.Ctx{"module": "test"}),
			}
			rr := httptest.NewRecorder()
			g.handle(rr, newEVMRoutingRequest(test.body))

			if rr.Code != test.wantStatus || localHits.Load() != test.wantLocal {
				t.Fatalf("status/local/body = %d/%d/%q, want %d/%d", rr.Code, localHits.Load(), rr.Body.String(), test.wantStatus, test.wantLocal)
			}
			if test.wantText == "" {
				if rr.Body.Len() != 0 {
					t.Fatalf("notification body = %q, want empty", rr.Body.String())
				}
			} else if !strings.Contains(rr.Body.String(), test.wantText) {
				t.Fatalf("response body = %q, want %q", rr.Body.String(), test.wantText)
			}
		})
	}
}

func TestEVMWebSocketHandshakeIsDisabled(t *testing.T) {
	var wsHits atomic.Int32
	g := &Gateway{
		cfg: Config{IsEVM: true},
		EvmWSHandler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			wsHits.Add(1)
		}),
		SyncChecker: &mockSyncChecker{height: 20},
		logger:      l.New(l.Ctx{"module": "test"}),
	}

	req := httptest.NewRequest(http.MethodGet, "http://gateway.local", nil)
	req.Header.Set("Connection", "Upgrade")
	req.Header.Set("Upgrade", "websocket")
	rr := httptest.NewRecorder()
	g.handle(rr, req)

	if rr.Code != http.StatusServiceUnavailable || wsHits.Load() != 0 || !strings.Contains(rr.Body.String(), "EVM websocket is disabled") {
		t.Fatalf("status/ws/body = %d/%d/%q, want disabled websocket handshake", rr.Code, wsHits.Load(), rr.Body.String())
	}
}

func TestDAGBatchCannotBypassMiningRouting(t *testing.T) {
	var localHits atomic.Int32
	g := &Gateway{
		cfg: Config{},
		DagHandler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			localHits.Add(1)
		}),
		SyncChecker: &mockSyncChecker{syncing: true, height: 10},
		peerManager: &PeerManager{
			peers:     make(map[string]*Peer),
			maxHeight: 20,
		},
		logger: l.New(l.Ctx{"module": "test"}),
	}
	req := httptest.NewRequest(http.MethodPost, "http://gateway.local", strings.NewReader(
		`[{"jsonrpc":"2.0","id":1,"method":"getBlockTemplate","params":[]}]`,
	))
	req.RemoteAddr = "127.0.0.1:12345"
	rr := httptest.NewRecorder()
	g.handle(rr, req)

	if rr.Code != http.StatusOK || localHits.Load() != 0 || !strings.Contains(rr.Body.String(), "DAG JSON-RPC batches are disabled") {
		t.Fatalf("status/local/body = %d/%d/%q, want fail-closed DAG batch", rr.Code, localHits.Load(), rr.Body.String())
	}
}

func TestCertifiedEVMMethodRechecksHeadInsideDispatchGuard(t *testing.T) {
	var localHits atomic.Int32
	checker := &mockSyncChecker{height: 20, guardInvisible: true}
	g := &Gateway{
		cfg: Config{IsEVM: true},
		EvmHandler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			localHits.Add(1)
		}),
		SyncChecker: checker,
		peerManager: &PeerManager{peers: make(map[string]*Peer)},
		logger:      l.New(l.Ctx{"module": "test"}),
	}
	rr := httptest.NewRecorder()
	g.handle(rr, newEVMRoutingRequest(`{"jsonrpc":"2.0","id":1,"method":"eth_getBalance","params":["0x0000000000000000000000000000000000000000","latest"]}`))

	if rr.Code != http.StatusOK || checker.guardCalls != 1 || localHits.Load() != 0 ||
		!strings.Contains(rr.Body.String(), "EVM head unavailable at dispatch") {
		t.Fatalf("status/guards/local/body = %d/%d/%d/%q, want guarded fail-closed dispatch", rr.Code, checker.guardCalls, localHits.Load(), rr.Body.String())
	}
}

func TestCertifiedEVMMethodDiscardsResponseAcrossMutation(t *testing.T) {
	var localHits atomic.Int32
	checker := &mockSyncChecker{height: 20, guardChanges: true}
	g := &Gateway{
		cfg: Config{IsEVM: true},
		EvmHandler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			localHits.Add(1)
			w.Header().Set("X-Transient-Head", "must-not-escape")
			fmt.Fprint(w, `{"jsonrpc":"2.0","id":1,"result":"transient"}`)
		}),
		SyncChecker: checker,
		peerManager: &PeerManager{peers: make(map[string]*Peer)},
		logger:      l.New(l.Ctx{"module": "test"}),
	}
	rr := httptest.NewRecorder()
	g.handle(rr, newEVMRoutingRequest(`{"jsonrpc":"2.0","id":1,"method":"eth_getBalance","params":["0x0000000000000000000000000000000000000000","latest"]}`))

	if rr.Code != http.StatusOK || checker.guardCalls != 1 || localHits.Load() != 1 ||
		strings.Contains(rr.Body.String(), "transient") || rr.Header().Get("X-Transient-Head") != "" ||
		!strings.Contains(rr.Body.String(), "EVM head unavailable at dispatch") {
		t.Fatalf("status/guards/local/header/body = %d/%d/%d/%q/%q, want discarded transient response", rr.Code, checker.guardCalls, localHits.Load(), rr.Header().Get("X-Transient-Head"), rr.Body.String())
	}
}

func TestCertifiedEVMReadWaitsForVisibleHead(t *testing.T) {
	var localHits atomic.Int32
	checker := &mockSyncChecker{height: 20, headInvisibleChecks: 2}
	g := &Gateway{
		cfg: Config{
			IsEVM:                true,
			CertifiedEVMHeadWait: 350 * time.Millisecond,
		},
		EvmHandler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			localHits.Add(1)
			fmt.Fprint(w, `{"jsonrpc":"2.0","id":1,"result":"stable"}`)
		}),
		SyncChecker: checker,
		peerManager: &PeerManager{peers: make(map[string]*Peer)},
		logger:      l.New(l.Ctx{"module": "test"}),
	}
	rr := httptest.NewRecorder()
	g.handle(rr, newEVMRoutingRequest(`{"jsonrpc":"2.0","id":1,"method":"eth_getBalance","params":["0x0000000000000000000000000000000000000000","latest"]}`))

	if rr.Code != http.StatusOK || localHits.Load() != 1 || checker.headChecks < 3 ||
		!strings.Contains(rr.Body.String(), `"result":"stable"`) {
		t.Fatalf("status/local/head checks/body = %d/%d/%d/%q, want bounded wait then stable response", rr.Code, localHits.Load(), checker.headChecks, rr.Body.String())
	}
}

func TestCertifiedEVMReadRetriesDiscardedMutationResponse(t *testing.T) {
	var localHits atomic.Int32
	checker := &mockSyncChecker{height: 20, guardChangesRemaining: 1}
	g := &Gateway{
		cfg: Config{
			IsEVM:                true,
			CertifiedEVMHeadWait: 350 * time.Millisecond,
		},
		EvmHandler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			attempt := localHits.Add(1)
			w.Header().Set("X-Read-Attempt", fmt.Sprint(attempt))
			fmt.Fprintf(w, `{"jsonrpc":"2.0","id":1,"result":"attempt-%d"}`, attempt)
		}),
		SyncChecker: checker,
		peerManager: &PeerManager{peers: make(map[string]*Peer)},
		logger:      l.New(l.Ctx{"module": "test"}),
	}
	rr := httptest.NewRecorder()
	g.handle(rr, newEVMRoutingRequest(`{"jsonrpc":"2.0","id":1,"method":"eth_getBalance","params":["0x0000000000000000000000000000000000000000","latest"]}`))

	if rr.Code != http.StatusOK || checker.guardCalls != 2 || localHits.Load() != 2 ||
		rr.Header().Get("X-Read-Attempt") != "2" ||
		strings.Contains(rr.Body.String(), "attempt-1") ||
		!strings.Contains(rr.Body.String(), `"result":"attempt-2"`) {
		t.Fatalf("status/guards/local/header/body = %d/%d/%d/%q/%q, want only retried stable response", rr.Code, checker.guardCalls, localHits.Load(), rr.Header().Get("X-Read-Attempt"), rr.Body.String())
	}
}

func TestSideEffectingEVMMethodWaitsButExecutesOnce(t *testing.T) {
	var localHits atomic.Int32
	checker := &mockSyncChecker{height: 20, guardInvisibleChecks: 2}
	g := &Gateway{
		cfg: Config{
			IsEVM:                true,
			CertifiedEVMHeadWait: 350 * time.Millisecond,
		},
		EvmHandler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			localHits.Add(1)
			fmt.Fprint(w, `{"jsonrpc":"2.0","id":1,"result":"submitted"}`)
		}),
		SyncChecker: checker,
		peerManager: &PeerManager{peers: make(map[string]*Peer)},
		logger:      l.New(l.Ctx{"module": "test"}),
	}
	rr := httptest.NewRecorder()
	g.handle(rr, newEVMRoutingRequest(`{"jsonrpc":"2.0","id":1,"method":"eth_sendRawTransaction","params":["0x01"]}`))

	if rr.Code != http.StatusOK || checker.guardCalls != 3 || localHits.Load() != 1 ||
		!strings.Contains(rr.Body.String(), `"result":"submitted"`) {
		t.Fatalf("status/guards/local/body = %d/%d/%d/%q, want bounded preflight and one execution", rr.Code, checker.guardCalls, localHits.Load(), rr.Body.String())
	}
}

func TestCertifiedEVMHeadWaitHonorsRequestCancellation(t *testing.T) {
	var localHits atomic.Int32
	g := &Gateway{
		cfg: Config{
			IsEVM:                true,
			CertifiedEVMHeadWait: time.Second,
		},
		EvmHandler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			localHits.Add(1)
		}),
		SyncChecker: &mockSyncChecker{height: 20, forceInvisible: true},
		peerManager: &PeerManager{peers: make(map[string]*Peer)},
		logger:      l.New(l.Ctx{"module": "test"}),
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	req := newEVMRoutingRequest(`{"jsonrpc":"2.0","id":1,"method":"eth_getBalance","params":["0x0000000000000000000000000000000000000000","latest"]}`).WithContext(ctx)
	rr := httptest.NewRecorder()
	start := time.Now()
	g.handle(rr, req)

	if elapsed := time.Since(start); elapsed > 250*time.Millisecond {
		t.Fatalf("cancelled request returned after %s, want prompt cancellation", elapsed)
	}
	if rr.Code != http.StatusOK || localHits.Load() != 0 ||
		!strings.Contains(rr.Body.String(), "EVM head unavailable") {
		t.Fatalf("status/local/body = %d/%d/%q, want cancelled fail-closed response", rr.Code, localHits.Load(), rr.Body.String())
	}
}

func TestSideEffectingEVMMethodIsPreflightedBeforeSingleExecution(t *testing.T) {
	tests := []struct {
		name          string
		guardChanges  bool
		wantLocalHits int32
		wantText      string
	}{
		{name: "stable head", wantLocalHits: 1, wantText: `"result":"submitted"`},
		{name: "changing head", guardChanges: true, wantText: "EVM head unavailable at dispatch"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var localHits atomic.Int32
			checker := &mockSyncChecker{height: 20, guardChanges: test.guardChanges}
			g := &Gateway{
				cfg: Config{IsEVM: true},
				EvmHandler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					localHits.Add(1)
					fmt.Fprint(w, `{"jsonrpc":"2.0","id":1,"result":"submitted"}`)
				}),
				SyncChecker: checker,
				peerManager: &PeerManager{peers: make(map[string]*Peer)},
				logger:      l.New(l.Ctx{"module": "test"}),
			}
			rr := httptest.NewRecorder()
			g.handle(rr, newEVMRoutingRequest(`{"jsonrpc":"2.0","id":1,"method":"eth_sendRawTransaction","params":["0x01"]}`))

			if rr.Code != http.StatusOK || checker.guardCalls != 1 || localHits.Load() != test.wantLocalHits ||
				!strings.Contains(rr.Body.String(), test.wantText) {
				t.Fatalf("status/guards/local/body = %d/%d/%d/%q, want 200/1/%d containing %q", rr.Code, checker.guardCalls, localHits.Load(), rr.Body.String(), test.wantLocalHits, test.wantText)
			}
		})
	}
}

func TestRequiresCertifiedEVMHead(t *testing.T) {
	tests := []struct {
		method string
		want   bool
	}{
		{method: "eth_getBalance", want: true},
		{method: "eth_getBlockByNumber", want: true},
		{method: "eth_getFilterLogs", want: true},
		{method: "eth_sendRawTransaction", want: true},
		{method: "eth_syncing", want: true},
		{method: "eth_getFilterChanges", want: true},
		{method: "debug_traceTransaction", want: true},
		{method: "trace_block", want: true},
		{method: "vendor_getCanonicalState", want: true},
		{method: "personal_sendTransaction", want: true},
		{method: "personal_signTransaction", want: true},
		{method: "eth_chainId", want: false},
		{method: "eth_accounts", want: false},
		{method: "eth_sign", want: false},
		{method: "eth_newPendingTransactionFilter", want: false},
		{method: "eth_uninstallFilter", want: false},
		{method: "eth_unsubscribe", want: false},
		{method: "personal_sign", want: false},
		{method: "txpool_status", want: false},
		{method: "admin_exportChain", want: false},
		{method: "miner_setGasPrice", want: false},
		{method: "net_peerCount", want: false},
		{method: "web3_clientVersion", want: false},
		{method: "bdag_getBdagChainInfo", want: false},
		{method: "engine_newPayloadV1", want: false},
		{method: "getBlockTemplate", want: false},
	}
	for _, test := range tests {
		if got := requiresCertifiedEVMHead(test.method); got != test.want {
			t.Fatalf("requiresCertifiedEVMHead(%q) = %v, want %v", test.method, got, test.want)
		}
	}
}

func TestCanDiscardCertifiedEVMResponse(t *testing.T) {
	tests := []struct {
		method string
		want   bool
	}{
		{method: "eth_getBalance", want: true},
		{method: "eth_getBlockByNumber", want: true},
		{method: "trace_block", want: true},
		{method: "eth_sendRawTransaction", want: false},
		{method: "eth_sendTransaction", want: false},
		{method: "eth_signTransaction", want: false},
		{method: "eth_resend", want: false},
		{method: "eth_newFilter", want: false},
		{method: "eth_getFilterChanges", want: false},
		{method: "eth_submitWork", want: false},
		{method: "personal_sendTransaction", want: false},
		{method: "debug_traceTransaction", want: false},
		{method: "eth_futureMutation", want: false},
		{method: "vendor_getCanonicalState", want: false},
		{method: "eth_chainId", want: false},
	}
	for _, test := range tests {
		if got := canDiscardCertifiedEVMResponse(test.method); got != test.want {
			t.Fatalf("canDiscardCertifiedEVMResponse(%q) = %v, want %v", test.method, got, test.want)
		}
	}
}

func TestPublicGatewayRejectedMethod(t *testing.T) {
	tests := []struct {
		method string
		want   bool
	}{
		{method: "engine_newPayloadV1", want: true},
		{method: "engine_forkchoiceUpdatedV1", want: true},
		{method: "eth_getBalance", want: false},
		{method: "admin_exportChain", want: false},
	}
	for _, test := range tests {
		if got := isPublicGatewayRejectedMethod(test.method); got != test.want {
			t.Fatalf("isPublicGatewayRejectedMethod(%q) = %v, want %v", test.method, got, test.want)
		}
	}
}

func newEVMGatewayWithPeer(t *testing.T, checker SyncChecker, local http.Handler) (*Gateway, *atomic.Int32) {
	t.Helper()
	var peerHits atomic.Int32
	peerServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		peerHits.Add(1)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"jsonrpc":"2.0","id":1,"result":"peer"}`)
	}))
	t.Cleanup(peerServer.Close)

	host, port := testPeerServerAddress(t, peerServer)
	g := &Gateway{
		cfg:         Config{IsEVM: true},
		EvmHandler:  local,
		SyncChecker: checker,
		peerManager: &PeerManager{
			peers: map[string]*Peer{
				host: {
					Host:        host,
					EVMPort:     port,
					BlockHeight: 30,
					IsActive:    true,
					IsSynced:    true,
					LastChecked: time.Now(),
				},
			},
			maxHeight:   30,
			affinity:    map[string]string{},
			affinityTS:  map[string]time.Time{},
			affinityTTL: time.Minute,
		},
		logger: l.New(l.Ctx{"module": "test"}),
	}
	g.peerManager.g = g
	return g, &peerHits
}

func newEVMRoutingRequest(body string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "http://gateway.local", strings.NewReader(body))
	req.RemoteAddr = "127.0.0.1:12345"
	req.Header.Set("Content-Type", "application/json")
	return req
}

func testPeerServerAddress(t *testing.T, server *httptest.Server) (string, int) {
	t.Helper()
	parsed, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	host, portText, err := net.SplitHostPort(parsed.Host)
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		t.Fatal(err)
	}
	return host, port
}
