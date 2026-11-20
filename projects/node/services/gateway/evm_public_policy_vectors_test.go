package gateway

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	l "github.com/BlockdagNetworkLabs/bdag/log"
)

const evmPolicyLocalResponse = `{"jsonrpc":"2.0","id":1,"result":"local"}`

func TestEVMGatewayPublicRPCPolicyVectors(t *testing.T) {
	tests := []struct {
		method      string
		localOnly   bool
		localMining bool
		critical    bool
		certified   bool
		discardable bool
		rejected    bool
		description string
	}{
		{method: "eth_chainId", localOnly: true, description: "public local metadata"},
		{method: "eth_accounts", localOnly: true, description: "public local account metadata"},
		{method: "net_version", localOnly: true, description: "public network metadata"},
		{method: "eth_blockNumber", certified: true, discardable: true, description: "public certified head read"},
		{method: "eth_getBalance", certified: true, discardable: true, description: "public certified state read"},
		{method: "eth_getLogs", certified: true, discardable: true, description: "public certified log read"},
		{method: "eth_sendRawTransaction", certified: true, description: "public side-effecting transaction relay"},
		{method: "eth_sendTransaction", localOnly: true, certified: true, description: "local wallet transaction"},
		{method: "eth_getFilterChanges", localOnly: true, certified: true, description: "local filter lifecycle"},
		{method: "debug_traceTransaction", localOnly: true, certified: true, description: "local diagnostic debug"},
		{method: "trace_block", localOnly: true, certified: true, discardable: true, description: "local diagnostic trace"},
		{method: "txpool_status", localOnly: true, description: "local txpool state"},
		{method: "personal_sign", localOnly: true, description: "local personal signer"},
		{method: "personal_sendTransaction", localOnly: true, certified: true, description: "local personal transaction"},
		{method: "engine_newPayloadV1", localOnly: true, rejected: true, description: "consensus engine API rejected on public gateway"},
		{method: "getTemplateHealth", localMining: true, critical: true, description: "critical local mining health"},
		{method: "vendor_getCanonicalState", certified: true, description: "unknown vendor method treated as state dependent"},
	}

	for _, test := range tests {
		t.Run(test.description, func(t *testing.T) {
			policy := classifyGatewayRequest(test.method)
			assertGatewayPolicy(t, "localOnly", policy.localOnly, test.localOnly)
			assertGatewayPolicy(t, "localMining", policy.localMining, test.localMining)
			assertGatewayPolicy(t, "critical", policy.critical, test.critical)
			assertGatewayPolicy(t, "certified", requiresCertifiedEVMHead(test.method), test.certified)
			assertGatewayPolicy(t, "discardable", canDiscardCertifiedEVMResponse(test.method), test.discardable)
			assertGatewayPolicy(t, "rejected", isPublicGatewayRejectedMethod(test.method), test.rejected)
		})
	}
}

func TestEVMGatewayUnauthenticatedLocalVectorsReachHandler(t *testing.T) {
	for _, method := range []string{"eth_chainId", "eth_accounts", "net_version"} {
		t.Run(method, func(t *testing.T) {
			var localHits int
			g := newEVMPolicyTestGateway(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				localHits++
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprint(w, evmPolicyLocalResponse)
			}))

			rr := httptest.NewRecorder()
			g.handle(rr, newEVMPolicyRequest(method))

			if rr.Code != http.StatusOK || localHits != 1 || !strings.Contains(rr.Body.String(), `"result":"local"`) {
				t.Fatalf("status/local/body = %d/%d/%q, want unauthenticated local EVM response", rr.Code, localHits, rr.Body.String())
			}
		})
	}
}

func TestEVMGatewayUnauthenticatedResourceControls(t *testing.T) {
	t.Run("request size", func(t *testing.T) {
		var localHits int
		g := newEVMPolicyTestGateway(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			localHits++
			fmt.Fprint(w, evmPolicyLocalResponse)
		}))
		body := evmPolicyBody("eth_chainId")
		g.cfg.MaxRequestBytes = int64(len(body) - 1)

		rr := httptest.NewRecorder()
		g.handle(rr, newEVMPolicyRequest("eth_chainId"))

		if rr.Code != http.StatusRequestEntityTooLarge || localHits != 0 {
			t.Fatalf("status/local/body = %d/%d/%q, want size rejection before local handler", rr.Code, localHits, rr.Body.String())
		}
	})

	t.Run("rate limit", func(t *testing.T) {
		var localHits int
		g := newInitializedEVMPolicyGateway(t, Config{
			RateLimit: 1,
			RateBurst: 1,
		}, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			localHits++
			fmt.Fprint(w, evmPolicyLocalResponse)
		}))

		first := httptest.NewRecorder()
		g.handle(first, newEVMPolicyRequestFrom("eth_chainId", "203.0.113.10:51000"))
		second := httptest.NewRecorder()
		g.handle(second, newEVMPolicyRequestFrom("eth_chainId", "203.0.113.10:51000"))

		if first.Code != http.StatusOK || second.Code != http.StatusTooManyRequests || localHits != 1 {
			t.Fatalf("first/second/local = %d/%d/%d, want 200/429/1", first.Code, second.Code, localHits)
		}
	})

	t.Run("normal concurrency", func(t *testing.T) {
		var localHits int
		g := newEVMPolicyTestGateway(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			localHits++
			fmt.Fprint(w, evmPolicyLocalResponse)
		}))
		g.sem <- struct{}{}

		rr := httptest.NewRecorder()
		g.handle(rr, newEVMPolicyRequest("eth_getLogs"))

		if rr.Code != http.StatusServiceUnavailable || localHits != 0 || !strings.Contains(rr.Body.String(), "server busy") {
			t.Fatalf("status/local/body = %d/%d/%q, want concurrency rejection before local handler", rr.Code, localHits, rr.Body.String())
		}
	})

	t.Run("request context cancellation", func(t *testing.T) {
		var localHits int
		g := newEVMPolicyTestGateway(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			localHits++
			fmt.Fprint(w, evmPolicyLocalResponse)
		}))
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		rr := httptest.NewRecorder()
		g.handle(rr, newEVMPolicyRequest("eth_chainId").WithContext(ctx))

		if rr.Code != http.StatusGatewayTimeout || localHits != 0 {
			t.Fatalf("status/local/body = %d/%d/%q, want cancelled request before local handler", rr.Code, localHits, rr.Body.String())
		}
	})
}

func assertGatewayPolicy(t *testing.T, field string, got bool, want bool) {
	t.Helper()
	if got != want {
		t.Fatalf("%s = %v, want %v", field, got, want)
	}
}

func newEVMPolicyTestGateway(local http.Handler) *Gateway {
	return &Gateway{
		cfg: Config{
			IsEVM:          true,
			RequestTimeout: time.Second,
		},
		EvmHandler:  local,
		SyncChecker: &mockSyncChecker{height: 20, nativeEVMHeight: 20},
		peerManager: &PeerManager{peers: map[string]*Peer{}},
		logger:      l.New(l.Ctx{"module": "test"}),
		sem:         make(chan struct{}, 1),
		criticalSem: make(chan struct{}, 1),
	}
}

func newInitializedEVMPolicyGateway(t *testing.T, cfg Config, local http.Handler) *Gateway {
	t.Helper()
	cfg.IsEVM = true
	cfg.ListenAddr = "127.0.0.1:0"
	cfg.PeerDagPort = 18110
	cfg.PeerEvmPort = 18111
	cfg.Logger = l.New(l.Ctx{"module": "test"})
	g, err := New(cfg)
	if err != nil {
		t.Fatalf("New gateway: %v", err)
	}
	g.EvmHandler = local
	g.SyncChecker = &mockSyncChecker{height: 20, nativeEVMHeight: 20}
	return g
}

func newEVMPolicyRequest(method string) *http.Request {
	return newEVMPolicyRequestFrom(method, "127.0.0.1:12345")
}

func newEVMPolicyRequestFrom(method string, remoteAddr string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "http://gateway.local", strings.NewReader(evmPolicyBody(method)))
	req.RemoteAddr = remoteAddr
	req.Header.Set("Content-Type", "application/json")
	return req
}

func evmPolicyBody(method string) string {
	return fmt.Sprintf(`{"jsonrpc":"2.0","id":1,"method":%q,"params":[]}`, method)
}
