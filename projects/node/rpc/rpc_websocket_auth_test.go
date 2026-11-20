package rpc

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/BlockdagNetworkLabs/bdag/config"
)

func TestNativeWebSocketRequiresPrimaryAuthentication(t *testing.T) {
	server, err := NewRPCServer(&config.Config{RPCUser: "admin", RPCPass: "primary", RPCLimitUser: "readonly", RPCLimitPass: "limited"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name string
		user string
		pass string
		want int
	}{
		{name: "missing", want: http.StatusUnauthorized},
		{name: "wrong", user: "bad", pass: "bad", want: http.StatusUnauthorized},
		{name: "limited", user: "readonly", pass: "limited", want: http.StatusForbidden},
		{name: "primary", user: "admin", pass: "primary", want: http.StatusBadRequest},
	} {
		t.Run(test.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "http://rpc.local/ws", nil)
			if test.user != "" {
				req.SetBasicAuth(test.user, test.pass)
			}
			recorder := httptest.NewRecorder()
			server.Handler().ServeHTTP(recorder, req)
			if recorder.Code != test.want {
				t.Fatalf("status=%d, want %d", recorder.Code, test.want)
			}
		})
	}
}
