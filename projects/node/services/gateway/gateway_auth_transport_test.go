package gateway

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	l "github.com/BlockdagNetworkLabs/bdag/log"
)

const (
	gatewayTestUser = "gateway-user"
	gatewayTestPass = "gateway-pass"
)

type gatewayAuthCase struct {
	name       string
	authUser   string
	authPass   string
	wantStatus int
	wantHits   int
}

func newGatewayAuthTestGateway(t *testing.T) (*Gateway, *int) {
	t.Helper()
	localHits := 0
	gateway, err := NewDAG(Config{
		ListenAddr:  "127.0.0.1:0",
		PeerDagPort: 18110,
		PeerEvmPort: 18111,
		InboundAuth: true,
		InboundUser: gatewayTestUser,
		InboundPass: gatewayTestPass,
		Logger:      l.New(l.Ctx{"module": "test"}),
	})
	if err != nil {
		t.Fatalf("NewDAG: %v", err)
	}
	gateway.DagHandler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		localHits++
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"jsonrpc":"2.0","id":1,"result":"local-dag"}`)
	})
	gateway.SyncChecker = &mockSyncChecker{}
	return gateway.Gateway, &localHits
}

func newGatewayAuthRequest(user string, pass string) *http.Request {
	body := strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"getBlockCount","params":[]}`)
	req := httptest.NewRequest(http.MethodPost, "http://gateway.local", body)
	req.RemoteAddr = "127.0.0.1:12345"
	req.Header.Set("Content-Type", "application/json")
	if user != "" || pass != "" {
		req.SetBasicAuth(user, pass)
	}
	return req
}

func assertNoGatewayAuthMaterial(t *testing.T, body string) {
	t.Helper()
	for _, credential := range []string{
		gatewayTestPass,
		"wrong-pass",
		gatewayTestUser + ":" + gatewayTestPass,
		"wrong-user:wrong-pass",
	} {
		if strings.Contains(body, credential) {
			t.Fatalf("gateway response exposed credential material %q in %q", credential, body)
		}
	}
}

func assertGatewayAuthCase(t *testing.T, test gatewayAuthCase) {
	t.Helper()
	gateway, localHits := newGatewayAuthTestGateway(t)
	recorder := httptest.NewRecorder()

	gateway.handle(recorder, newGatewayAuthRequest(test.authUser, test.authPass))

	body := recorder.Body.String()
	if recorder.Code != test.wantStatus {
		t.Fatalf("status = %d body=%q, want %d", recorder.Code, body, test.wantStatus)
	}
	if *localHits != test.wantHits {
		t.Fatalf("local DAG handler hits = %d, want %d", *localHits, test.wantHits)
	}
	assertNoGatewayAuthMaterial(t, body)
	if test.wantStatus == http.StatusUnauthorized {
		if got := recorder.Header().Get("WWW-Authenticate"); got != `Basic realm="gateway"` {
			t.Fatalf("WWW-Authenticate = %q, want gateway basic challenge", got)
		}
	}
}

func TestDAGGatewayInboundAuthBehavior(t *testing.T) {
	tests := []gatewayAuthCase{
		{
			name:       "missing credentials",
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:       "wrong credentials",
			authUser:   "wrong-user",
			authPass:   "wrong-pass",
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:       "correct credentials",
			authUser:   gatewayTestUser,
			authPass:   gatewayTestPass,
			wantStatus: http.StatusOK,
			wantHits:   1,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assertGatewayAuthCase(t, test)
		})
	}
}

func readGatewaySource(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}

func assertGatewaySourceContains(t *testing.T, source string, token string) {
	t.Helper()
	if !strings.Contains(source, token) {
		t.Fatalf("gateway source is missing %q", token)
	}
}

func assertGatewaySourceOmits(t *testing.T, source string, token string) {
	t.Helper()
	if strings.Contains(source, token) {
		t.Fatalf("gateway source unexpectedly contains %q", token)
	}
}

func TestGatewayStartTransportBoundaryUsesPlainNetListen(t *testing.T) {
	source := readGatewaySource(t, "gateway.go")

	assertGatewaySourceContains(t, source, `net.Listen("tcp", g.cfg.ListenAddr)`)
	assertGatewaySourceContains(t, source, `net.Listen("tcp", g.cfg.WSListenAddr)`)
	for _, token := range []string{"ListenAndServeTLS", ".ServeTLS(", "tls.Listen"} {
		assertGatewaySourceOmits(t, source, token)
	}
}
