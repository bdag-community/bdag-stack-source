package rpc

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/BlockdagNetworkLabs/bdag/config"
)

type limitedAuthoritySpy struct{}

func (limitedAuthoritySpy) GetBlockCount() (string, error)      { return "getBlockCount", nil }
func (limitedAuthoritySpy) GetBlock() (string, error)           { return "getBlock", nil }
func (limitedAuthoritySpy) GetBlockByOrder() (string, error)    { return "getBlockByOrder", nil }
func (limitedAuthoritySpy) Tips() (string, error)               { return "tips", nil }
func (limitedAuthoritySpy) GetCoinbaseAddress() (string, error) { return "getCoinbaseAddress", nil }
func (limitedAuthoritySpy) IsCurrent() (string, error)          { return "isCurrent", nil }
func (limitedAuthoritySpy) GetPeerInfo() (string, error)        { return "getPeerInfo", nil }
func (limitedAuthoritySpy) GetPeerForks() (string, error)       { return "getPeerForks", nil }
func (limitedAuthoritySpy) GetPeerList() (string, error)        { return "getPeerList", nil }
func (limitedAuthoritySpy) GetPeerDiscoveryStatus() (string, error) {
	return "getPeerDiscoveryStatus", nil
}
func (limitedAuthoritySpy) Stop() (string, error) { return "stop", nil }

func TestLimitedAuthorityActualHandlerDispatchAndHTTPMatrix(t *testing.T) {
	server, err := NewRPCServer(&config.Config{RPCUser: "admin", RPCPass: "primary", RPCLimitUser: "readonly", RPCLimitPass: "limited"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := server.RegisterService("Blockdag", limitedAuthoritySpy{}); err != nil {
		t.Fatal(err)
	}
	if err := server.Service.Start(); err != nil {
		t.Fatal(err)
	}
	defer server.Service.Stop()

	request := func(auth string, body string, duplicateAuth bool) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "http://rpc.local/", strings.NewReader(body))
		req.Header.Set("Content-Type", contentType)
		if auth != "" {
			req.Header.Set("Authorization", auth)
			if duplicateAuth {
				req.Header.Add("Authorization", auth)
			}
		}
		recorder := httptest.NewRecorder()
		server.Handler().ServeHTTP(recorder, req)
		return recorder
	}
	primary := "Basic " + base64.StdEncoding.EncodeToString([]byte("admin:primary"))
	limited := "Basic " + base64.StdEncoding.EncodeToString([]byte("readonly:limited"))

	for _, method := range []string{
		"getBlockCount", "getBlock", "getBlockByOrder", "tips", "getCoinbaseAddress",
		"isCurrent", "getPeerInfo", "getPeerForks", "getPeerList", "getPeerDiscoveryStatus",
	} {
		recorder := request(limited, `{"jsonrpc":"2.0","id":1,"method":"`+method+`","params":[]}`, false)
		if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"`+method+`"`) {
			t.Fatalf("limited method %q status=%d body=%s", method, recorder.Code, recorder.Body.String())
		}
	}
	if recorder := request(primary, `{"jsonrpc":"2.0","id":1,"method":"stop","params":[]}`, false); recorder.Code != http.StatusOK {
		t.Fatalf("primary registered complement status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if recorder := request(limited, `{"jsonrpc":"2.0","id":1,"method":"stop","params":[]}`, false); recorder.Code != http.StatusForbidden {
		t.Fatalf("limited registered complement status=%d body=%s", recorder.Code, recorder.Body.String())
	}

	for name, auth := range map[string]string{
		"missing":   "",
		"wrong":     "Basic YWRtaW46d3Jvbmc=",
		"malformed": "Basic not-base64",
	} {
		if recorder := request(auth, `{"jsonrpc":"2.0","id":1,"method":"tips"}`, false); recorder.Code != http.StatusUnauthorized {
			t.Errorf("%s auth status=%d body=%s", name, recorder.Code, recorder.Body.String())
		}
	}
	if recorder := request(primary, `{"jsonrpc":"2.0","id":1,"method":"tips"}`, true); recorder.Code != http.StatusUnauthorized {
		t.Fatalf("duplicated auth status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if recorder := request(limited, `{"jsonrpc":"2.0","id":1,"method":"stop"}`, false); recorder.Code != http.StatusForbidden {
		t.Fatalf("limited forbidden status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if recorder := request(limited, `[{"jsonrpc":"2.0","id":1,"method":"tips"}]`, false); recorder.Code != http.StatusForbidden {
		t.Fatalf("limited valid batch status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	for name, body := range map[string]string{
		"malformed": `{"jsonrpc":"2.0","id":1,"method":`,
		"duplicate": `{"jsonrpc":"2.0","id":1,"method":"tips","method":"stop"}`,
	} {
		if recorder := request(limited, body, false); recorder.Code != http.StatusBadRequest {
			t.Errorf("%s body status=%d body=%s", name, recorder.Code, recorder.Body.String())
		}
	}

	handler := server.Handler()
	concurrent := []struct {
		name string
		auth string
		body string
		want int
	}{
		{name: "primary", auth: primary, body: `{"jsonrpc":"2.0","id":1,"method":"stop"}`, want: http.StatusOK},
		{name: "limited-allowed", auth: limited, body: `{"jsonrpc":"2.0","id":1,"method":"tips"}`, want: http.StatusOK},
		{name: "limited-denied", auth: limited, body: `{"jsonrpc":"2.0","id":1,"method":"stop"}`, want: http.StatusForbidden},
		{name: "limited-malformed", auth: limited, body: `{"jsonrpc":"2.0","id":1,"method":`, want: http.StatusBadRequest},
	}
	var wg sync.WaitGroup
	for _, test := range concurrent {
		test := test
		for i := 0; i < 16; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				req := httptest.NewRequest(http.MethodPost, "http://rpc.local/", strings.NewReader(test.body))
				req.Header.Set("Content-Type", contentType)
				req.Header.Set("Authorization", test.auth)
				recorder := httptest.NewRecorder()
				handler.ServeHTTP(recorder, req)
				if recorder.Code != test.want {
					t.Errorf("concurrent %s status=%d body=%s", test.name, recorder.Code, recorder.Body.String())
				}
			}()
		}
	}
	wg.Wait()
}

func TestLimitedAuthorityDigestRoles(t *testing.T) {
	server, err := NewRPCServer(&config.Config{RPCUser: "admin", RPCPass: "primary", RPCLimitUser: "readonly", RPCLimitPass: "limited"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name   string
		user   string
		pass   string
		admin  bool
		failed bool
	}{
		{name: "primary", user: "admin", pass: "primary", admin: true},
		{name: "limited", user: "readonly", pass: "limited"},
		{name: "wrong", user: "readonly", pass: "wrong", failed: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			req, _ := http.NewRequest(http.MethodPost, "http://rpc.local", nil)
			req.SetBasicAuth(test.user, test.pass)
			admin, err := server.checkAuth(req, true)
			if admin != test.admin || (err != nil) != test.failed {
				t.Fatalf("admin=%t err=%v, want admin=%t failed=%t", admin, err, test.admin, test.failed)
			}
		})
	}
}

func TestAuthorizeLimitedRequestExactMethodsAndPreservesBody(t *testing.T) {
	server := &RpcServer{}
	for method := range limitedRPCMethods {
		body := []byte(`{"jsonrpc":"2.0","id":1,"method":"` + method + `","params":[]}`)
		req, _ := http.NewRequest(http.MethodPost, "http://rpc.local", bytes.NewReader(body))
		got, err := server.authorizeLimitedRequest(req)
		if err != nil || got != method {
			t.Fatalf("method %q authorization got %q err=%v", method, got, err)
		}
		restored, err := io.ReadAll(req.Body)
		if err != nil || !bytes.Equal(restored, body) {
			t.Fatalf("method %q body changed: %q err=%v", method, restored, err)
		}
	}
}

func TestExportedLimitedAuthorizerPreservesBodyAndNativeStatus(t *testing.T) {
	server, err := NewRPCServer(&config.Config{RPCUser: "admin", RPCPass: "primary", RPCLimitUser: "readonly", RPCLimitPass: "limited"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	body := []byte(`{"jsonrpc":"2.0","id":1,"method":"tips","params":[]}`)
	req := httptest.NewRequest(http.MethodPost, "http://rpc.local", bytes.NewReader(body))
	recorder := httptest.NewRecorder()
	if !server.AuthorizeLimitedRequest(recorder, req) {
		t.Fatal("exported authorizer rejected an allowlisted request")
	}
	restored, err := io.ReadAll(req.Body)
	if err != nil || !bytes.Equal(restored, body) {
		t.Fatalf("restored body = %q, err=%v", restored, err)
	}

	req = httptest.NewRequest(http.MethodPost, "http://rpc.local", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"stop"}`))
	recorder = httptest.NewRecorder()
	if server.AuthorizeLimitedRequest(recorder, req) {
		t.Fatal("exported authorizer accepted a forbidden request")
	}
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("forbidden status = %d, want %d", recorder.Code, http.StatusForbidden)
	}
}

func TestAuthorizeLimitedRequestRejectsAmbiguousAndNonAllowlistedEnvelopes(t *testing.T) {
	server := &RpcServer{}
	for _, body := range []string{
		`[{"jsonrpc":"2.0","id":1,"method":"getBlockCount"}]`,
		`{"jsonrpc":"2.0","id":1,"method":"stop"}`,
		`{"jsonrpc":"2.0","id":1,"method":"getblockcount"}`,
		`{"jsonrpc":"2.0","id":1,"method":"getBlockCount "}`,
		`{"jsonrpc":"2.0","id":1,"method":null}`,
		`{"jsonrpc":"2.0","id":1,"method":"getBlockCount","method":"tips"}`,
		`{"jsonrpc":"2.0","id":1,"m\u0065thod":"getBlockCount","method":"tips"}`,
		`{"jsonrpc":"2.0","id":1,"params":{"method":"getBlockCount"}}`,
	} {
		req, _ := http.NewRequest(http.MethodPost, "http://rpc.local", strings.NewReader(body))
		if _, err := server.authorizeLimitedRequest(req); err == nil {
			t.Fatalf("body unexpectedly authorized: %s", body)
		}
	}
}

func TestLimitedAuthDigestIsFixedSize(t *testing.T) {
	digest := sha256.Sum256([]byte("Basic " + base64.StdEncoding.EncodeToString([]byte("readonly:limited"))))
	if len(digest) != sha256.Size {
		t.Fatal("unexpected digest size")
	}
}
