package gateway

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

type fakeTransport struct {
	t         *testing.T
	responses map[string]map[string]string
}

func (f *fakeTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	body, err := io.ReadAll(req.Body)
	if err != nil {
		return nil, err
	}
	_ = req.Body.Close()
	var rpcReq struct {
		Method string `json:"method"`
	}
	_ = json.Unmarshal(body, &rpcReq)
	method := rpcReq.Method
	if method == "" {
		method = "__default__"
	}
	url := req.URL.String()
	if byMethod, ok := f.responses[url]; ok {
		if respBody, ok := byMethod[method]; ok {
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(respBody)),
				Header:     make(http.Header),
			}, nil
		}
	}
	f.t.Fatalf("unexpected request to %s method %s", url, method)
	return nil, nil
}
