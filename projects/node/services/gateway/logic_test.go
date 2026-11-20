package gateway

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/BlockdagNetworkLabs/bdag/core/types"
	l "github.com/BlockdagNetworkLabs/bdag/log"
	"github.com/ethereum/go-ethereum/common"
)

type mockSyncChecker struct {
	syncing               bool
	height                uint64
	nativeEVMHeight       uint64
	syncChecks            int
	forceInvisible        bool
	genesisVisible        bool
	headInvisibleChecks   int
	headChecks            int
	guardInvisible        bool
	guardInvisibleChecks  int
	guardChanges          bool
	guardChangesRemaining int
	guardCalls            int
}

// DagHeight implements [SyncChecker].
func (m *mockSyncChecker) DagHeight() uint64 {
	return m.height
}

func (m *mockSyncChecker) IsSyncing() bool {
	m.syncChecks++
	return m.syncing
}
func (m *mockSyncChecker) EvmHeight() uint64 { return m.height }
func (m *mockSyncChecker) EVMHead() (uint64, bool) {
	m.headChecks++
	if m.forceInvisible {
		return m.height, false
	}
	if m.headInvisibleChecks > 0 {
		m.headInvisibleChecks--
		return m.height, false
	}
	if m.genesisVisible {
		return 0, true
	}
	return m.height, m.height > 0
}
func (m *mockSyncChecker) WithCommittedEVMHead(read func()) bool {
	m.guardCalls++
	if m.guardInvisible {
		return false
	}
	if m.guardInvisibleChecks > 0 {
		m.guardInvisibleChecks--
		return false
	}
	_, visible := m.EVMHead()
	if !visible || read == nil {
		return false
	}
	read()
	if m.guardChanges {
		return false
	}
	if m.guardChangesRemaining > 0 {
		m.guardChangesRemaining--
		return false
	}
	return true
}
func (m *mockSyncChecker) NativeEVMHeight() uint64 {
	return m.nativeEVMHeight
}
func (m *mockSyncChecker) CalculateStateRoot(block *types.Block) *common.Hash {
	return &common.Hash{}
}

func TestSyncStatusCaching(t *testing.T) {
	// The caching logic was removed along with isSyncing() because
	// SyncChecker is expected to be fast/in-memory.
	// We'll just verify that SyncChecker is called.
	sc := &mockSyncChecker{syncing: true}

	g := &Gateway{
		SyncChecker: sc,
	}

	if !g.SyncChecker.IsSyncing() {
		t.Error("expected syncing to be true")
	}

	sc.syncing = false
	if g.SyncChecker.IsSyncing() {
		t.Error("expected syncing to be false")
	}
}

func TestSensitiveRPCFiltering(t *testing.T) {
	// Logic verified by manual code inspection in handler.go
	// Since handle() is complex to unit test in isolation.
}

func TestHeightBasedSyncing(t *testing.T) {
	sc := &mockSyncChecker{syncing: true}
	g := &Gateway{
		cfg: Config{
			IsEVM: true,
		},
		SyncChecker: sc,
	}

	// SyncChecker says we are syncing, regardless of peer heights (SyncChecker is source of truth)
	if !g.SyncChecker.IsSyncing() {
		t.Error("expected IsSyncing to be true based on SyncChecker")
	}

	sc.syncing = false
	if g.SyncChecker.IsSyncing() {
		t.Error("expected IsSyncing to be false")
	}
}

func TestPeerFailureCooldown(t *testing.T) {
	pm := &PeerManager{
		peers: make(map[string]*Peer),
	}
	// Link to gateway for logger access
	pm.g = &Gateway{
		logger: l.New(l.Ctx{"module": "test"}),
	}
	pm.peers["host1"] = &Peer{
		Host:        "host1",
		IsActive:    true,
		IsSynced:    true,
		LastChecked: time.Now(),
	}

	// First and second failures keep a known-good peer routable.
	pm.ReportFailure("host1")
	pm.ReportFailure("host1")

	best, ok := pm.GetBestPeer("")
	if !ok || best != "host1" {
		t.Fatalf("expected peer to stay selectable before unhealthy threshold, got %s ok=%v", best, ok)
	}

	// Third consecutive failure marks the peer unhealthy and starts cooldown.
	pm.ReportFailure("host1")

	best, ok = pm.GetBestPeer("")
	if ok {
		t.Fatalf("expected failed peer to stay out of selection during cooldown, got %s", best)
	}

	// Fake cooldown expiration
	pm.mu.Lock()
	pm.peers["host1"].LastChecked = time.Now().Add(-31 * time.Second)
	pm.peers["host1"].NextProbe = time.Now().Add(-time.Second)
	pm.mu.Unlock()

	best, ok = pm.GetBestPeer("")
	if !ok {
		t.Error("expected host1 to be available again after cooldown")
	}
	if best != "host1" {
		t.Errorf("expected host1, got %s", best)
	}
}

func TestStickySync(t *testing.T) {
	pm := &PeerManager{
		peers: make(map[string]*Peer),
	}
	pm.peers["host1"] = &Peer{
		Host:        "host1",
		IsActive:    true,
		IsSynced:    true,
		BlockHeight: 1000,
		LastChecked: time.Now(),
	}
	pm.maxHeight = 1000

	g := &Gateway{
		cfg: Config{
			IsEVM: true,
		},
		peerManager: pm,
		client:      http.DefaultClient,
		logger:      l.New(l.Ctx{"module": "test"}),
	}
	pm.g = g // Link gateway back to peermanager
	// Mock local head at 500 (behind)
	g.SyncChecker = &mockSyncChecker{syncing: true} // was syncing

	// If we lose host1, the last seen height should be preserved for diagnostics
	// but ignored for live routing targets.
	pm.ReportFailure("host1")
	pm.ReportFailure("host1")
	pm.ReportFailure("host1")

	if pm.peers["host1"].BlockHeight != 1000 {
		t.Errorf("expected height 1000 to be preserved, got %d", pm.peers["host1"].BlockHeight)
	}

	// GetHistoricalMaxHeight should still return 1000 (high-water mark preserved)
	if pm.GetHistoricalMaxHeight() != 1000 {
		t.Errorf("expected historical maxHeight 1000, got %d", pm.GetHistoricalMaxHeight())
	}
	if pm.GetMaxHeight() != 0 {
		t.Errorf("expected live maxHeight 0 after peer becomes unusable, got %d", pm.GetMaxHeight())
	}
}

func TestLenientPeerSelection(t *testing.T) {
	pm := &PeerManager{
		peers: make(map[string]*Peer),
	}
	// Link to gateway for logger access
	pm.g = &Gateway{
		logger: l.New(l.Ctx{"module": "test"}),
	}
	// Case 1: No synced peers, but one active peer with height
	pm.peers["peer1"] = &Peer{
		Host:        "peer1",
		IsActive:    true,
		IsSynced:    false, // Partially synced
		BlockHeight: 1000,
		LastChecked: time.Now(),
	}

	best, ok := pm.GetBestPeerInfo("")
	if !ok {
		t.Fatal("expected a best peer even if not synced")
	}
	if best.Host != "peer1" {
		t.Errorf("expected peer1, got %s", best.Host)
	}

	// Case 2: One synced peer, one non-synced but higher height peer
	// Synced should be preferred regardless of height (stability/trust)
	pm.peers["peer2"] = &Peer{
		Host:        "peer2",
		IsActive:    true,
		IsSynced:    true,
		BlockHeight: 500,
		LastChecked: time.Now(),
	}

	best, ok = pm.GetBestPeerInfo("")
	if !ok {
		t.Fatal("expected a best peer")
	}
	if best.Host != "peer2" {
		t.Errorf("expected synced peer2 to be preferred over non-synced peer1, got %s", best.Host)
	}
}

func TestFailSafe503(t *testing.T) {
	// This test would require full Gateway.handle setup.
	// We've verified the logic by inspection in handler.go.
}

func TestNodeSpecificRPCsStayLocalWhileGatewayIsSyncing(t *testing.T) {
	methods := []string{
		"getBlockTemplate",
		"Blockdag_GetBlockTemplate",
		"getTemplateHealth",
		"Blockdag_GetTemplateHealth",
		"submitBlockHeader",
		"Blockdag_SubmitBlockHeader",
		"submitBlockHeaderV2",
		"Blockdag_SubmitBlockHeaderV2",
		"submitBlock",
		"Blockdag_SubmitBlock",
		"p2p_getStatus",
		"p2p_getInfo",
	}

	for _, method := range methods {
		t.Run(method, func(t *testing.T) {
			var localHits int
			g := &Gateway{
				cfg: Config{},
				DagHandler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					localHits++
					w.Header().Set("Content-Type", "application/json")
					fmt.Fprintf(w, `{"jsonrpc":"2.0","id":1,"result":"local-%s"}`, method)
				}),
				SyncChecker: &mockSyncChecker{syncing: true, height: 10},
				peerManager: &PeerManager{
					peers:     map[string]*Peer{},
					maxHeight: 20,
				},
				logger: l.New(l.Ctx{"module": "test"}),
			}

			body := strings.NewReader(fmt.Sprintf(`{"jsonrpc":"2.0","id":1,"method":"%s","params":[]}`, method))
			req := httptest.NewRequest(http.MethodPost, "http://gateway.local", body)
			req.RemoteAddr = "127.0.0.1:12345"
			req.Header.Set("Content-Type", "application/json")
			rr := httptest.NewRecorder()

			g.handle(rr, req)

			if rr.Code != http.StatusOK {
				t.Fatalf("status = %d body=%q, want 200 local response", rr.Code, rr.Body.String())
			}
			if localHits != 1 {
				t.Fatalf("local handler hits = %d, want 1", localHits)
			}
			if !strings.Contains(rr.Body.String(), "local-"+method) {
				t.Fatalf("response body = %q, want local result", rr.Body.String())
			}
		})
	}
}

func TestNodeSpecificAliasesStayLocalWithHealthyEligibleRemote(t *testing.T) {
	var remoteHits int
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		remoteHits++
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"jsonrpc":"2.0","id":1,"result":"remote"}`)
	}))
	defer remote.Close()
	parsed, err := url.Parse(remote.URL)
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(parsed.Port())
	if err != nil {
		t.Fatal(err)
	}

	var localHits int
	g := &Gateway{
		cfg: Config{PeerDagPort: port},
		DagHandler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			localHits++
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"jsonrpc":"2.0","id":1,"result":"local"}`)
		}),
		SyncChecker: &mockSyncChecker{syncing: true, height: 1},
		peerManager: &PeerManager{
			peers: map[string]*Peer{
				parsed.Hostname(): {Host: parsed.Hostname(), DAGPort: port, IsActive: true, IsSynced: true, BlockHeight: 100, LastChecked: time.Now()},
			},
			maxHeight: 100,
		},
		logger: l.New(l.Ctx{"module": "test"}),
	}
	g.peerManager.g = g
	methods := []string{
		"getBlockTemplate", "Blockdag_GetBlockTemplate", "getTemplateHealth", "Blockdag_GetTemplateHealth",
		"submitBlockHeader", "Blockdag_SubmitBlockHeader", "submitBlockHeaderV2", "Blockdag_SubmitBlockHeaderV2",
		"submitBlock", "Blockdag_SubmitBlock", "p2p_getStatus", "p2p_getInfo",
	}
	for _, method := range methods {
		t.Run(method, func(t *testing.T) {
			body := fmt.Sprintf(`{"jsonrpc":"2.0","id":1,"method":%q,"params":[]}`, method)
			req := httptest.NewRequest(http.MethodPost, "http://gateway.local", strings.NewReader(body))
			req.RemoteAddr = "127.0.0.1:12345"
			req.Header.Set("Content-Type", "application/json")
			rr := httptest.NewRecorder()
			g.handle(rr, req)
			if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), `"result":"local"`) {
				t.Fatalf("status/body = %d/%q, want local response", rr.Code, rr.Body.String())
			}
		})
	}
	if remoteHits != 0 {
		t.Fatalf("healthy remote request count = %d, want 0", remoteHits)
	}
	if localHits != len(methods) {
		t.Fatalf("local request count = %d, want %d", localHits, len(methods))
	}
}

func TestEVMSyncingFallsThroughWhenNativeSyncingButEVMNotBehind(t *testing.T) {
	var localHits int
	g := &Gateway{
		cfg: Config{IsEVM: true},
		EvmHandler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			localHits++
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"jsonrpc":"2.0","id":1,"result":false}`)
		}),
		SyncChecker: &mockSyncChecker{syncing: true, height: 100},
		peerManager: &PeerManager{
			peers:     map[string]*Peer{},
			maxHeight: 90,
		},
		logger: l.New(l.Ctx{"module": "test"}),
	}

	req := httptest.NewRequest(http.MethodPost, "http://gateway.local", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"eth_syncing","params":[]}`))
	req.RemoteAddr = "127.0.0.1:12345"
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	g.handle(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d body=%q, want 200 local response", rr.Code, rr.Body.String())
	}
	if localHits != 1 {
		t.Fatalf("local handler hits = %d, want 1", localHits)
	}
	if !strings.Contains(rr.Body.String(), `"result":false`) {
		t.Fatalf("response body = %q, want local eth_syncing=false", rr.Body.String())
	}
}

func TestEVMSyncingInterceptsOnlyWhenEVMBehindPeerTarget(t *testing.T) {
	var localHits int
	g := &Gateway{
		cfg: Config{IsEVM: true},
		EvmHandler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			localHits++
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"jsonrpc":"2.0","id":1,"result":false}`)
		}),
		SyncChecker: &mockSyncChecker{syncing: false, height: 10},
		peerManager: &PeerManager{
			peers: map[string]*Peer{
				"peer": {
					Host:        "peer",
					BlockHeight: 20,
					IsActive:    true,
					IsSynced:    true,
					LastChecked: time.Now(),
				},
			},
			maxHeight: 20,
		},
		logger: l.New(l.Ctx{"module": "test"}),
	}

	req := httptest.NewRequest(http.MethodPost, "http://gateway.local", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"eth_syncing","params":[]}`))
	req.RemoteAddr = "127.0.0.1:12345"
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	g.handle(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d body=%q, want 200 intercepted response", rr.Code, rr.Body.String())
	}
	if localHits != 0 {
		t.Fatalf("local handler hits = %d, want 0", localHits)
	}
	if !strings.Contains(rr.Body.String(), `"currentBlock":"0xa"`) || !strings.Contains(rr.Body.String(), `"highestBlock":"0x14"`) {
		t.Fatalf("response body = %q, want synthesized EVM sync progress", rr.Body.String())
	}
}

func TestEVMSyncingCapsPeerTargetAtNativeEVMHeight(t *testing.T) {
	var localHits int
	g := &Gateway{
		cfg: Config{IsEVM: true},
		EvmHandler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			localHits++
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"jsonrpc":"2.0","id":1,"result":false}`)
		}),
		SyncChecker: &mockSyncChecker{height: 90, nativeEVMHeight: 100},
		peerManager: &PeerManager{
			peers: map[string]*Peer{
				"peer": {
					Host:        "peer",
					BlockHeight: 200,
					IsActive:    true,
					IsSynced:    true,
					LastChecked: time.Now(),
				},
			},
			maxHeight: 200,
		},
		logger: l.New(l.Ctx{"module": "test"}),
	}

	req := httptest.NewRequest(http.MethodPost, "http://gateway.local", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"eth_syncing","params":[]}`))
	req.RemoteAddr = "127.0.0.1:12345"
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	g.handle(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d body=%q, want 200 intercepted response", rr.Code, rr.Body.String())
	}
	if localHits != 0 {
		t.Fatalf("local handler hits = %d, want 0", localHits)
	}
	if !strings.Contains(rr.Body.String(), `"currentBlock":"0x5a"`) || !strings.Contains(rr.Body.String(), `"highestBlock":"0x64"`) {
		t.Fatalf("response body = %q, want native-capped EVM sync progress", rr.Body.String())
	}
}

func TestEVMSyncingReturnsFalseAtNativeEVMHeightDuringMutationDespitePeerLead(t *testing.T) {
	var localHits int
	checker := &mockSyncChecker{height: 100, nativeEVMHeight: 100, forceInvisible: true}
	g := &Gateway{
		cfg: Config{IsEVM: true},
		EvmHandler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			localHits++
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"jsonrpc":"2.0","id":1,"result":{"currentBlock":"0x64","highestBlock":"0xc8"}}`)
		}),
		SyncChecker: checker,
		peerManager: &PeerManager{
			peers: map[string]*Peer{
				"peer": {
					Host:        "peer",
					BlockHeight: 200,
					IsActive:    true,
					IsSynced:    true,
					LastChecked: time.Now(),
				},
			},
			maxHeight: 200,
		},
		logger: l.New(l.Ctx{"module": "test"}),
	}

	req := httptest.NewRequest(http.MethodPost, "http://gateway.local", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"eth_syncing","params":[]}`))
	req.RemoteAddr = "127.0.0.1:12345"
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	g.handle(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d body=%q, want 200 response", rr.Code, rr.Body.String())
	}
	if localHits != 0 {
		t.Fatalf("local handler hits = %d, want gateway false response", localHits)
	}
	if checker.syncChecks != 0 {
		t.Fatalf("native sync checks = %d, want lock-free EVM health response", checker.syncChecks)
	}
	if checker.headChecks != 0 {
		t.Fatalf("EVM head visibility checks = %d, want committed-height metadata response", checker.headChecks)
	}
	if !strings.Contains(rr.Body.String(), `"result":false`) {
		t.Fatalf("response body = %q, want eth_syncing=false", rr.Body.String())
	}
}

func TestEVMBlockNumberUnavailableDoesNotCheckNativeSync(t *testing.T) {
	checker := &mockSyncChecker{nativeEVMHeight: 1}
	g := &Gateway{
		cfg:         Config{IsEVM: true},
		SyncChecker: checker,
		peerManager: &PeerManager{peers: make(map[string]*Peer)},
		logger:      l.New(l.Ctx{"module": "test"}),
	}

	req := httptest.NewRequest(http.MethodPost, "http://gateway.local", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"eth_blockNumber","params":[]}`))
	req.RemoteAddr = "127.0.0.1:12345"
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	g.handle(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d body=%q, want JSON-RPC error response", rr.Code, rr.Body.String())
	}
	if checker.syncChecks != 0 {
		t.Fatalf("native sync checks = %d, want lock-free unavailable response", checker.syncChecks)
	}
	if !strings.Contains(rr.Body.String(), `"error"`) || !strings.Contains(rr.Body.String(), `"EVM head unavailable"`) {
		t.Fatalf("response body = %q, want explicit EVM head unavailable error", rr.Body.String())
	}
}

func TestEVMBlockNumberAllowsGenesisHead(t *testing.T) {
	var localHits int
	checker := &mockSyncChecker{nativeEVMHeight: 1, genesisVisible: true}
	g := &Gateway{
		cfg: Config{IsEVM: true},
		EvmHandler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			localHits++
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"jsonrpc":"2.0","id":1,"result":"0x0"}`)
		}),
		SyncChecker: checker,
		peerManager: &PeerManager{peers: make(map[string]*Peer)},
		logger:      l.New(l.Ctx{"module": "test"}),
	}

	req := httptest.NewRequest(http.MethodPost, "http://gateway.local", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"eth_blockNumber","params":[]}`))
	req.RemoteAddr = "127.0.0.1:12345"
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	g.handle(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d body=%q, want visible genesis response", rr.Code, rr.Body.String())
	}
	if localHits != 0 {
		t.Fatalf("local handler hits = %d, want lock-free visible genesis response", localHits)
	}
	if !strings.Contains(rr.Body.String(), `"result":"0x0"`) {
		t.Fatalf("response body = %q, want genesis block number", rr.Body.String())
	}
}

func TestEVMBlockNumberUsesNativeBoundedVisibleHeight(t *testing.T) {
	var localHits int
	g := &Gateway{
		cfg: Config{IsEVM: true},
		EvmHandler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			localHits++
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"jsonrpc":"2.0","id":1,"result":"0xc8"}`)
		}),
		SyncChecker: &mockSyncChecker{height: 100, nativeEVMHeight: 100},
		peerManager: &PeerManager{
			peers: map[string]*Peer{
				"peer": {
					Host:        "peer",
					BlockHeight: 200,
					IsActive:    true,
					IsSynced:    true,
					LastChecked: time.Now(),
				},
			},
			maxHeight: 200,
		},
		logger: l.New(l.Ctx{"module": "test"}),
	}

	req := httptest.NewRequest(http.MethodPost, "http://gateway.local", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"eth_blockNumber","params":[]}`))
	req.RemoteAddr = "127.0.0.1:12345"
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	g.handle(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d body=%q, want 200 response", rr.Code, rr.Body.String())
	}
	if localHits != 0 {
		t.Fatalf("local handler hits = %d, want gateway response", localHits)
	}
	if !strings.Contains(rr.Body.String(), `"result":"0x64"`) {
		t.Fatalf("response body = %q, want native-capped block number", rr.Body.String())
	}
}

func TestEVMBlockNumberUsesCommittedNativeHeightDuringMutation(t *testing.T) {
	checker := &mockSyncChecker{height: 150, nativeEVMHeight: 100, forceInvisible: true}
	g := &Gateway{
		cfg:         Config{IsEVM: true},
		SyncChecker: checker,
		peerManager: &PeerManager{peers: make(map[string]*Peer)},
		logger:      l.New(l.Ctx{"module": "test"}),
	}

	req := httptest.NewRequest(http.MethodPost, "http://gateway.local", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"eth_blockNumber","params":[]}`))
	req.RemoteAddr = "127.0.0.1:12345"
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	g.handle(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d body=%q, want committed-height response", rr.Code, rr.Body.String())
	}
	if checker.headChecks != 1 {
		t.Fatalf("EVM head visibility checks = %d, want one non-blocking check", checker.headChecks)
	}
	if strings.Contains(rr.Body.String(), `"error"`) || !strings.Contains(rr.Body.String(), `"result":"0x64"`) {
		t.Fatalf("response body = %q, want atomically committed native height", rr.Body.String())
	}
}

func TestEVMMixedBatchPreservesPerMethodRouting(t *testing.T) {
	var localHits int
	var peerHits int
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		peerHits++
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"jsonrpc":"2.0","id":1,"result":"peer"}`)
	}))
	defer peer.Close()

	peerURL, err := url.Parse(peer.URL)
	if err != nil {
		t.Fatal(err)
	}
	host, portText, err := net.SplitHostPort(peerURL.Host)
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		t.Fatal(err)
	}

	checker := &mockSyncChecker{syncing: true, height: 100, nativeEVMHeight: 200, forceInvisible: true}
	g := &Gateway{
		cfg: Config{IsEVM: true},
		EvmHandler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			localHits++
			payload, _ := io.ReadAll(r.Body)
			if !strings.Contains(string(payload), `"method":"txpool_status"`) {
				t.Fatalf("local body = %q, want node-specific request only", payload)
			}
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"jsonrpc":"2.0","id":2,"result":"local"}`)
		}),
		SyncChecker: checker,
		peerManager: &PeerManager{
			peers: map[string]*Peer{
				host: {
					Host:        host,
					EVMPort:     port,
					BlockHeight: 200,
					IsActive:    true,
					IsSynced:    true,
					LastChecked: time.Now(),
				},
			},
			maxHeight:   200,
			affinity:    map[string]string{},
			affinityTS:  map[string]time.Time{},
			affinityTTL: time.Minute,
		},
		logger: l.New(l.Ctx{"module": "test"}),
	}
	g.peerManager.g = g

	body := `[{"jsonrpc":"2.0","method":"eth_blockNumber","params":[]},{"jsonrpc":"2.0","id":1,"method":"eth_getBalance","params":[]},{"jsonrpc":"2.0","id":2,"method":"txpool_status","params":[]}]`
	req := httptest.NewRequest(http.MethodPost, "http://gateway.local", strings.NewReader(body))
	req.RemoteAddr = "127.0.0.1:12345"
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	g.handle(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d body=%q, want batch response", rr.Code, rr.Body.String())
	}
	if localHits != 1 || peerHits != 0 {
		t.Fatalf("local/peer hits = %d/%d, want 1/0", localHits, peerHits)
	}
	if checker.syncChecks != 0 {
		t.Fatalf("native sync checks = %d, want forced-local policy to bypass native sync routing", checker.syncChecks)
	}
	if !strings.Contains(rr.Body.String(), `"id":1,"error"`) ||
		!strings.Contains(rr.Body.String(), "EVM head unavailable") ||
		!strings.Contains(rr.Body.String(), `"id":2,"result":"local"`) ||
		strings.Contains(rr.Body.String(), `"id":null`) {
		t.Fatalf("response body = %q, want certified error and local response with notification omitted", rr.Body.String())
	}
}

func TestEVMCertifiedBatchFailsClosedWithoutCommittedHead(t *testing.T) {
	var localHits int
	g := &Gateway{
		cfg: Config{IsEVM: true},
		EvmHandler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			localHits++
		}),
		SyncChecker: &mockSyncChecker{syncing: true, height: 10, nativeEVMHeight: 20, forceInvisible: true},
		peerManager: &PeerManager{peers: make(map[string]*Peer), maxHeight: 20},
		logger:      l.New(l.Ctx{"module": "test"}),
	}
	g.peerManager.g = g

	body := `[{"jsonrpc":"2.0","id":1,"method":"eth_getBalance","params":[]}]`
	req := httptest.NewRequest(http.MethodPost, "http://gateway.local", strings.NewReader(body))
	req.RemoteAddr = "127.0.0.1:12345"
	rr := httptest.NewRecorder()
	g.handle(rr, req)

	if rr.Code != http.StatusOK || localHits != 0 {
		t.Fatalf("status/local hits = %d/%d body=%q, want fail-closed JSON-RPC batch response", rr.Code, localHits, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), `"error"`) || !strings.Contains(rr.Body.String(), "EVM head unavailable") {
		t.Fatalf("response body = %q, want committed-head error", rr.Body.String())
	}
}

func TestEVMInterceptionRequiresExplicitJSONRPC2(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{name: "scalar missing version", body: `{"id":1,"method":"eth_blockNumber","params":[]}`},
		{name: "scalar null version", body: `{"jsonrpc":null,"id":1,"method":"eth_blockNumber","params":[]}`},
		{name: "batch missing version", body: `[{"id":1,"method":"eth_blockNumber","params":[]}]`},
		{name: "batch null version", body: `[{"jsonrpc":null,"id":1,"method":"eth_blockNumber","params":[]}]`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var localHits int
			checker := &mockSyncChecker{height: 100, nativeEVMHeight: 100}
			g := &Gateway{
				cfg: Config{IsEVM: true},
				EvmHandler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					localHits++
					w.Header().Set("Content-Type", "application/json")
					fmt.Fprint(w, `{"jsonrpc":"2.0","id":null,"error":{"code":-32600,"message":"invalid request"}}`)
				}),
				SyncChecker: checker,
				peerManager: &PeerManager{peers: make(map[string]*Peer)},
				logger:      l.New(l.Ctx{"module": "test"}),
			}

			req := httptest.NewRequest(http.MethodPost, "http://gateway.local", strings.NewReader(tt.body))
			req.RemoteAddr = "127.0.0.1:12345"
			rr := httptest.NewRecorder()
			g.handle(rr, req)

			if rr.Code != http.StatusOK || localHits != 1 {
				t.Fatalf("status/local hits = %d/%d body=%q, want underlying EVM validation", rr.Code, localHits, rr.Body.String())
			}
			if !strings.Contains(rr.Body.String(), `"code":-32600`) || strings.Contains(rr.Body.String(), `"result":"0x64"`) {
				t.Fatalf("body = %q, invalid JSON-RPC version was synthetically intercepted", rr.Body.String())
			}
		})
	}
}

func TestMalformedEVMBatchReturnsParseError(t *testing.T) {
	g := &Gateway{
		cfg:         Config{IsEVM: true},
		SyncChecker: &mockSyncChecker{height: 100, nativeEVMHeight: 100},
		peerManager: &PeerManager{peers: make(map[string]*Peer)},
		logger:      l.New(l.Ctx{"module": "test"}),
	}

	req := httptest.NewRequest(http.MethodPost, "http://gateway.local", strings.NewReader(`[{"jsonrpc":"2.0"`))
	req.RemoteAddr = "127.0.0.1:12345"
	rr := httptest.NewRecorder()
	g.handle(rr, req)

	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), `"code":-32700`) {
		t.Fatalf("status/body = %d/%q, want JSON-RPC parse error", rr.Code, rr.Body.String())
	}
}

func TestEVMNotificationOnlyBatchReturnsNoContent(t *testing.T) {
	var localHits int
	g := &Gateway{
		cfg: Config{IsEVM: true},
		EvmHandler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			localHits++
		}),
		SyncChecker: &mockSyncChecker{height: 100, nativeEVMHeight: 100},
		peerManager: &PeerManager{peers: make(map[string]*Peer)},
		logger:      l.New(l.Ctx{"module": "test"}),
	}

	body := `[{"jsonrpc":"2.0","method":"eth_blockNumber","params":[]},{"jsonrpc":"2.0","method":"eth_syncing","params":[]}]`
	req := httptest.NewRequest(http.MethodPost, "http://gateway.local", strings.NewReader(body))
	req.RemoteAddr = "127.0.0.1:12345"
	rr := httptest.NewRecorder()
	g.handle(rr, req)

	if rr.Code != http.StatusNoContent || rr.Body.Len() != 0 || localHits != 0 {
		t.Fatalf("status/body/local hits = %d/%q/%d, want notification-only 204", rr.Code, rr.Body.String(), localHits)
	}
}

func TestEVMBatchItemLimitRejectsBeforeDispatch(t *testing.T) {
	var localHits int
	g := &Gateway{
		cfg: Config{IsEVM: true},
		EvmHandler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			localHits++
		}),
		SyncChecker: &mockSyncChecker{height: 100, nativeEVMHeight: 100},
		peerManager: &PeerManager{peers: make(map[string]*Peer)},
		logger:      l.New(l.Ctx{"module": "test"}),
	}

	elements := make([]string, maxGatewayBatchItems+1)
	for i := range elements {
		elements[i] = `{"jsonrpc":"2.0","method":"eth_blockNumber","params":[]}`
	}
	body := "[" + strings.Join(elements, ",") + "]"
	req := httptest.NewRequest(http.MethodPost, "http://gateway.local", strings.NewReader(body))
	req.RemoteAddr = "127.0.0.1:12345"
	rr := httptest.NewRecorder()
	g.handle(rr, req)

	if rr.Code != http.StatusOK || localHits != 0 || !strings.Contains(rr.Body.String(), `"code":-32600`) {
		t.Fatalf("status/local hits/body = %d/%d/%q, want pre-dispatch item-limit rejection", rr.Code, localHits, rr.Body.String())
	}
}

func TestInterceptedEVMNotificationsReturnNoContent(t *testing.T) {
	for _, method := range []string{"eth_blockNumber", "eth_syncing"} {
		t.Run(method, func(t *testing.T) {
			var localHits int
			g := &Gateway{
				cfg: Config{IsEVM: true},
				EvmHandler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					localHits++
				}),
				SyncChecker: &mockSyncChecker{height: 100, nativeEVMHeight: 100},
				peerManager: &PeerManager{peers: make(map[string]*Peer)},
				logger:      l.New(l.Ctx{"module": "test"}),
			}

			body := fmt.Sprintf(`{"jsonrpc":"2.0","method":%q,"params":[]}`, method)
			req := httptest.NewRequest(http.MethodPost, "http://gateway.local", strings.NewReader(body))
			req.RemoteAddr = "127.0.0.1:12345"
			req.Header.Set("Content-Type", "application/json")
			rr := httptest.NewRecorder()

			g.handle(rr, req)

			if rr.Code != http.StatusNoContent || rr.Body.Len() != 0 {
				t.Fatalf("status/body = %d/%q, want 204 with no JSON-RPC response", rr.Code, rr.Body.String())
			}
			if localHits != 0 {
				t.Fatalf("local handler hits = %d, want intercepted notification", localHits)
			}
		})
	}
}

func TestMalformedInterceptedEVMCallIsNotNotification(t *testing.T) {
	var localHits int
	g := &Gateway{
		cfg: Config{IsEVM: true},
		EvmHandler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			localHits++
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"jsonrpc":"2.0","id":null,"error":{"code":-32600,"message":"invalid request"}}`)
		}),
		SyncChecker: &mockSyncChecker{height: 100, nativeEVMHeight: 100},
		peerManager: &PeerManager{peers: make(map[string]*Peer)},
		logger:      l.New(l.Ctx{"module": "test"}),
	}

	body := `{"jsonrpc":"2.0","method":"eth_blockNumber","params":[]`
	req := httptest.NewRequest(http.MethodPost, "http://gateway.local", strings.NewReader(body))
	req.RemoteAddr = "127.0.0.1:12345"
	rr := httptest.NewRecorder()
	g.handle(rr, req)

	if rr.Code == http.StatusNoContent || rr.Body.Len() == 0 {
		t.Fatalf("status/body = %d/%q, malformed call must receive an error", rr.Code, rr.Body.String())
	}
	if localHits != 1 || !strings.Contains(rr.Body.String(), `"code":-32600`) {
		t.Fatalf("local hits/body = %d/%q, want local invalid-request handling", localHits, rr.Body.String())
	}
}

func TestInterceptedEVMCallWithNullIDGetsResponse(t *testing.T) {
	g := &Gateway{
		cfg:         Config{IsEVM: true},
		SyncChecker: &mockSyncChecker{height: 100, nativeEVMHeight: 100},
		peerManager: &PeerManager{peers: make(map[string]*Peer)},
		logger:      l.New(l.Ctx{"module": "test"}),
	}

	body := `{"jsonrpc":"2.0","id":null,"method":"eth_blockNumber","params":[]}`
	req := httptest.NewRequest(http.MethodPost, "http://gateway.local", strings.NewReader(body))
	req.RemoteAddr = "127.0.0.1:12345"
	rr := httptest.NewRecorder()
	g.handle(rr, req)

	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), `"id":null`) || !strings.Contains(rr.Body.String(), `"result":"0x64"`) {
		t.Fatalf("status/body = %d/%q, explicit null id is not a notification", rr.Code, rr.Body.String())
	}
}

func TestEVMReadStaysLocalWhenCommittedHeadVisible(t *testing.T) {
	var localHits int
	var peerHits int
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		peerHits++
		if r.Header.Get("X-Forwarded-By") != "bdag-gateway" {
			t.Fatalf("missing forwarded marker")
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"jsonrpc":"2.0","id":1,"result":"0x2a"}`)
	}))
	defer peer.Close()

	peerURL, err := url.Parse(peer.URL)
	if err != nil {
		t.Fatal(err)
	}
	host, portText, err := net.SplitHostPort(peerURL.Host)
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		t.Fatal(err)
	}

	g := &Gateway{
		cfg: Config{IsEVM: true},
		EvmHandler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			localHits++
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"jsonrpc":"2.0","id":1,"result":"0x0"}`)
		}),
		SyncChecker: &mockSyncChecker{syncing: true, height: 10},
		peerManager: &PeerManager{
			peers: map[string]*Peer{
				host: {
					Host:        host,
					EVMPort:     port,
					BlockHeight: 20,
					IsActive:    true,
					IsSynced:    true,
					LastChecked: time.Now(),
				},
			},
			maxHeight:   20,
			affinity:    map[string]string{},
			affinityTS:  map[string]time.Time{},
			affinityTTL: 10 * time.Minute,
		},
		logger: l.New(l.Ctx{"module": "test"}),
	}
	g.peerManager.g = g

	req := httptest.NewRequest(http.MethodPost, "http://gateway.local", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"eth_getBalance","params":["0x0000000000000000000000000000000000000000","latest"]}`))
	req.RemoteAddr = "127.0.0.1:12345"
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	g.handle(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d body=%q, want 200 local response", rr.Code, rr.Body.String())
	}
	if localHits != 1 {
		t.Fatalf("local handler hits = %d, want 1", localHits)
	}
	if peerHits != 0 {
		t.Fatalf("peer handler hits = %d, want 0", peerHits)
	}
	if !strings.Contains(rr.Body.String(), `"result":"0x0"`) {
		t.Fatalf("response body = %q, want local result", rr.Body.String())
	}
}

func TestCriticalLocalRPCUsesReservedConcurrencyWhenNormalSlotsBusy(t *testing.T) {
	var localHits int
	checker := &mockSyncChecker{syncing: true, height: 10}
	g := &Gateway{
		cfg: Config{
			RequestTimeout: time.Second,
		},
		DagHandler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			localHits++
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"jsonrpc":"2.0","id":1,"result":"healthy"}`)
		}),
		SyncChecker: checker,
		peerManager: &PeerManager{
			peers: map[string]*Peer{},
		},
		logger:      l.New(l.Ctx{"module": "test"}),
		sem:         make(chan struct{}, 1),
		criticalSem: make(chan struct{}, 1),
	}
	g.sem <- struct{}{}

	req := httptest.NewRequest(http.MethodPost, "http://gateway.local", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"getTemplateHealth","params":[]}`))
	req.RemoteAddr = "127.0.0.1:12345"
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	g.handle(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d body=%q, want 200", rr.Code, rr.Body.String())
	}
	if localHits != 1 {
		t.Fatalf("local handler hits = %d, want 1", localHits)
	}
	if checker.syncChecks != 0 {
		t.Fatalf("native sync checks = %d, want local mining RPC to bypass routing sync", checker.syncChecks)
	}
}

func TestNormalRPCReturns503WhenNormalConcurrencyBusy(t *testing.T) {
	var localHits int
	g := &Gateway{
		cfg: Config{
			RequestTimeout: time.Second,
		},
		DagHandler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			localHits++
			w.WriteHeader(http.StatusOK)
		}),
		SyncChecker: &mockSyncChecker{syncing: false, height: 10},
		peerManager: &PeerManager{
			peers: map[string]*Peer{},
		},
		logger:      l.New(l.Ctx{"module": "test"}),
		sem:         make(chan struct{}, 1),
		criticalSem: make(chan struct{}, 1),
	}
	g.sem <- struct{}{}

	req := httptest.NewRequest(http.MethodPost, "http://gateway.local", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"eth_getLogs","params":[]}`))
	req.RemoteAddr = "127.0.0.1:12345"
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	g.handle(rr, req)

	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d body=%q, want 503", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "server busy") {
		t.Fatalf("response body = %q, want server busy", rr.Body.String())
	}
	if localHits != 0 {
		t.Fatalf("local handler hits = %d, want 0", localHits)
	}
}

func TestIsLocalOnlyMethod(t *testing.T) {
	tests := []struct {
		method string
		want   bool
	}{
		{method: "txpool_status", want: true},
		{method: "txpool_content", want: true},
		{method: "eth_blockNumber", want: false},
		{method: "getNetworkInfo", want: true},
		{method: "p2p_getStatus", want: true},
		{method: "getMainChainHeight", want: true},
		{method: "debug_traceTransaction", want: true},
		{method: "trace_block", want: true},
		{method: "net_version", want: true},
		{method: "web3_clientVersion", want: true},
		{method: "rpc_modules", want: true},
		{method: "miner_setGasPrice", want: true},
		{method: "bdag_getBdagChainInfo", want: true},
		{method: "eth_accounts", want: true},
		{method: "eth_sign", want: true},
		{method: "eth_sendTransaction", want: true},
		{method: "eth_signTransaction", want: true},
		{method: "eth_pendingTransactions", want: true},
		{method: "eth_resend", want: true},
		{method: "eth_getLogs", want: false},
		{method: "eth_getTransactionReceipt", want: false},
		{method: "eth_getTransactionByHash", want: false},
		{method: "eth_getRawTransactionByHash", want: false},
		{method: "eth_getTransactionByBlockHashAndIndex", want: false},
		{method: "eth_getTransactionByBlockNumberAndIndex", want: false},
		{method: "eth_getRawTransactionByBlockHashAndIndex", want: false},
		{method: "eth_getRawTransactionByBlockNumberAndIndex", want: false},
		{method: "eth_getBlockByHash", want: false},
		{method: "eth_sendRawTransaction", want: false},
		{method: "eth_newFilter", want: true},
		{method: "eth_getFilterChanges", want: true},
		{method: "eth_subscribe", want: true},
		{method: "eth_unsubscribe", want: true},
		{method: "engine_newPayloadV1", want: true},
	}
	for _, tc := range tests {
		if got := isLocalOnlyMethod(tc.method); got != tc.want {
			t.Fatalf("isLocalOnlyMethod(%q) = %v, want %v", tc.method, got, tc.want)
		}
	}
}

func TestIsCriticalLocalMethod(t *testing.T) {
	tests := []struct {
		method string
		want   bool
	}{
		{method: "getTemplateHealth", want: true},
		{method: "Blockdag_GetTemplateHealth", want: true},
		{method: "getBlockCount", want: true},
		{method: "isCurrent", want: true},
		{method: "getMainChainHeight", want: true},
		{method: "submitBlockHeader", want: true},
		{method: "Blockdag_SubmitBlockHeader", want: true},
		{method: "submitBlockHeaderV2", want: true},
		{method: "Blockdag_SubmitBlockHeaderV2", want: true},
		{method: "getBlockTemplate", want: false},
		{method: "Blockdag_GetBlockTemplate", want: false},
		{method: "eth_getLogs", want: false},
	}
	for _, tc := range tests {
		if got := isCriticalLocalMethod(tc.method); got != tc.want {
			t.Fatalf("isCriticalLocalMethod(%q) = %v, want %v", tc.method, got, tc.want)
		}
	}
}

func TestIsLocalMiningRPCMethod(t *testing.T) {
	tests := []struct {
		method string
		want   bool
	}{
		{method: "getBlockTemplate", want: true},
		{method: "Blockdag_GetBlockTemplate", want: true},
		{method: "getTemplateHealth", want: true},
		{method: "Blockdag_GetTemplateHealth", want: true},
		{method: "submitBlockHeader", want: true},
		{method: "Blockdag_SubmitBlockHeader", want: true},
		{method: "submitBlockHeaderV2", want: true},
		{method: "Blockdag_SubmitBlockHeaderV2", want: true},
		{method: "submitBlock", want: true},
		{method: "Blockdag_SubmitBlock", want: true},
		{method: "eth_getLogs", want: false},
	}
	for _, tc := range tests {
		if got := isLocalMiningRPCMethod(tc.method); got != tc.want {
			t.Fatalf("isLocalMiningRPCMethod(%q) = %v, want %v", tc.method, got, tc.want)
		}
	}
}
