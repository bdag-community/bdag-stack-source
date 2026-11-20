package miner

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	corejson "github.com/BlockdagNetworkLabs/bdag/core/json"
	coretypes "github.com/BlockdagNetworkLabs/bdag/core/types"
	"github.com/BlockdagNetworkLabs/bdag/rpc"
	"github.com/BlockdagNetworkLabs/bdag/rpc/client/cmds"
	"github.com/BlockdagNetworkLabs/bdag/rpc/websocket"
	"github.com/BlockdagNetworkLabs/bdag/services/mining"
	ethcommon "github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/txpool/legacypool"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestGBTInvalidatedGenerationCannotReturnThroughActualWebsocket(t *testing.T) {
	m := newRemoteGBTResultTestMiner()
	m.consensus = newGBTGenerationRuntimeConsensus(t)
	m.cfg = m.consensus.Config()
	m.timeSource = m.consensus.MedianTimeSource()
	m.policy = &mining.Policy{PeerID: "fixture-peer"}
	m.txpool = &legacypool.LegacyPool{}
	m.bdagPool = &testPendingTemplateResetter{}
	m.buildTemplate = func(address ethcommon.Address, _ bool) (*coretypes.BlockTemplate, error) {
		return newGBTGenerationRuntimeTemplate(t, m.consensus, address), nil
	}
	if err := m.updateBlockTemplateForRequest(templateBuildRequest{force: true}); err != nil {
		t.Fatal(err)
	}
	cfg := *m.cfg
	cfg.RPCUser = "fixture"
	cfg.RPCPass = "fixture"
	cfg.RPCListeners = nil
	cfg.DisableTLS = true
	cfg.RPCMaxWebsockets = 2
	cfg.RPCMaxConcurrentReqs = 8
	server, err := rpc.NewRPCServer(&cfg, m.consensus)
	if err != nil {
		t.Fatal(err)
	}
	if err := server.RegisterService(cmds.DefaultServiceNameSpace, NewPublicMinerAPI(m)); err != nil {
		t.Fatal(err)
	}
	if err := server.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := server.Stop(); err != nil {
			t.Error(err)
		}
	})
	host := httptest.NewServer(server.Handler())
	t.Cleanup(host.Close)
	header := http.Header{}
	header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte("fixture:fixture")))
	dialer := websocket.Dialer{HandshakeTimeout: 2 * time.Second}
	peer, response, err := dialer.Dial("ws"+strings.TrimPrefix(host.URL, "http")+"/ws", header)
	if err != nil {
		if response != nil {
			response.Body.Close()
		}
		t.Fatal(err)
	}
	t.Cleanup(func() { peer.Close() })
	if err := peer.WriteJSON(map[string]any{"jsonrpc": "2.0", "id": 1, "method": cmds.DefaultServiceNameSpace + "_subscribe", "params": []any{"blockTemplate"}}); err != nil {
		t.Fatal(err)
	}
	read := func() map[string]json.RawMessage {
		t.Helper()
		peer.SetReadDeadline(time.Now().Add(2 * time.Second))
		_, raw, err := peer.ReadMessage()
		if err != nil {
			t.Fatal(err)
		}
		var msg map[string]json.RawMessage
		if err := json.Unmarshal(raw, &msg); err != nil {
			t.Fatal(err)
		}
		if e := msg["error"]; len(e) > 0 && string(e) != "null" {
			t.Fatalf("RPC failure: %s", e)
		}
		return msg
	}
	var old *corejson.RemoteGBTResult
	for i := 0; i < 2; i++ {
		msg := read()
		if raw := msg["params"]; raw != nil {
			var params map[string]json.RawMessage
			json.Unmarshal(raw, &params)
			old = new(corejson.RemoteGBTResult)
			if err := json.Unmarshal(params["result"], old); err != nil {
				t.Fatal(err)
			}
		}
	}
	if old == nil {
		t.Fatal("eager actual subscription payload absent")
	}
	m.invalidateGBTWorkerGeneration(errors.New("synthetic required generation failure"))
	if m.buildRemoteGBTResult() != nil {
		t.Fatal("held direct publication emitted retained generation")
	}
	if err := m.updateBlockTemplateForRequest(templateBuildRequest{kind: templateBuildBlockChain, force: true}); err != nil {
		t.Fatal(err)
	}
	msg := read()
	var params map[string]json.RawMessage
	json.Unmarshal(msg["params"], &params)
	var fresh corejson.RemoteGBTResult
	if err := json.Unmarshal(params["result"], &fresh); err != nil {
		t.Fatal(err)
	}
	if fresh.Seq <= old.Seq {
		t.Fatal("replacement did not arrive through actual websocket")
	}
	// Replay only the old payload after repair. It must not be made valid again
	// by clearing the replacement's publication hold.
	m.TemplateEvent().Send(old)
	peer.SetReadDeadline(time.Now().Add(300 * time.Millisecond))
	_, _, err = peer.ReadMessage()
	var timeout net.Error
	if !errors.As(err, &timeout) || !timeout.Timeout() {
		t.Fatal("invalidated old generation appeared on wire or connection failed")
	}
}
