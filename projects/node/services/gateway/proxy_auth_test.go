package gateway

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	l "github.com/BlockdagNetworkLabs/bdag/log"
)

func TestForwardRejectsUpstreamAuthenticationFailure(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, password, ok := r.BasicAuth()
		if !ok || user != "peer-user" || password != "peer-secret" {
			http.Error(w, "authorization failed: peer-secret", http.StatusUnauthorized)
			return
		}
		fmt.Fprint(w, `{"jsonrpc":"2.0","id":1,"result":true}`)
	}))
	defer upstream.Close()

	parsed, err := url.Parse(upstream.URL)
	if err != nil {
		t.Fatal(err)
	}
	g := &Gateway{
		cfg:       Config{UpUser: "local-user", UpPass: "local-secret"},
		client:    upstream.Client(),
		authPorts: map[string]bool{parsed.Port(): true},
		logger:    l.New(l.Ctx{"module": "test"}),
	}
	pm := NewPeerManager(g)
	pm.peers[parsed.Hostname()] = &Peer{Host: parsed.Hostname(), IsActive: true, IsSynced: true}
	g.peerManager = pm

	body, err := g.forwardCtx(context.Background(), upstream.URL, []byte(`{"jsonrpc":"2.0","id":1,"method":"getBlockCount","params":[]}`), rolePrimary)
	if err == nil || !strings.Contains(err.Error(), "HTTP 401") {
		t.Fatalf("forward error = %v, want upstream 401", err)
	}
	if len(body) != 0 {
		t.Fatalf("forwarded unauthorized body = %q, want none", body)
	}
	if strings.Contains(err.Error(), "local-secret") || strings.Contains(err.Error(), "peer-secret") {
		t.Fatalf("authentication secret leaked in error: %v", err)
	}
	if pm.peers[parsed.Hostname()].FailureCount != 1 {
		t.Fatalf("peer failure count = %d, want 1", pm.peers[parsed.Hostname()].FailureCount)
	}
}
