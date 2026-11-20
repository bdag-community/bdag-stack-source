package miner

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"math"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	bdagevm "github.com/BlockdagNetworkLabs/bdag/bevm/bdag"
	"github.com/BlockdagNetworkLabs/bdag/common/hash"
	"github.com/BlockdagNetworkLabs/bdag/config"
	"github.com/BlockdagNetworkLabs/bdag/consensus/model"
	"github.com/BlockdagNetworkLabs/bdag/core/blockchain"
	corejson "github.com/BlockdagNetworkLabs/bdag/core/json"
	coretypes "github.com/BlockdagNetworkLabs/bdag/core/types"
	"github.com/BlockdagNetworkLabs/bdag/core/types/pow"
	"github.com/BlockdagNetworkLabs/bdag/rpc"
	"github.com/BlockdagNetworkLabs/bdag/rpc/websocket"
	"github.com/BlockdagNetworkLabs/bdag/services/mining"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/txpool/legacypool"
)

type RpcClient struct {
	Server string
	User   string
	Pwd    string
}

type testWorker struct {
	running bool
}

func (w testWorker) GetType() string { return GBTWorkerType }
func (w testWorker) Start() error    { return nil }
func (w testWorker) Stop()           {}
func (w testWorker) IsRunning() bool { return w.running }
func (w testWorker) Update()         {}

type testPendingTemplateResetter struct {
	err   error
	calls int
}

func (r *testPendingTemplateResetter) ResetTemplate() error {
	r.calls++
	return r.err
}

type testSyncingPendingTemplateResetter struct {
	testPendingTemplateResetter
	syncing bool
}

func (r *testSyncingPendingTemplateResetter) IsSyncing() bool {
	return r.syncing
}

type testMiningPendingTemplateResetter struct {
	testPendingTemplateResetter
	miningErr   error
	miningCalls int
}

func (r *testMiningPendingTemplateResetter) ResetTemplateForMining() error {
	r.miningCalls++
	return r.miningErr
}

type testCanonicalPendingTemplateResetter struct {
	testMiningPendingTemplateResetter
	syncing        bool
	canonicalReady bool
	canonicalErr   error
	canonicalCalls int
}

func (r *testCanonicalPendingTemplateResetter) IsSyncing() bool {
	return r.syncing
}

func (r *testCanonicalPendingTemplateResetter) CanonicalMiningTemplateReady() bool {
	return r.canonicalReady
}

func (r *testCanonicalPendingTemplateResetter) ResetTemplateForCanonicalMining() error {
	r.canonicalCalls++
	return r.canonicalErr
}

type testP2PService struct {
	current      bool
	nearlySynced bool
	freshness    model.P2PMiningFreshnessSnapshot
}

func (s testP2PService) IsCurrent() bool { return s.current }

func (s testP2PService) IsNearlySynced() bool { return s.nearlySynced }

func (s testP2PService) MiningPeerFreshnessSnapshot(time.Time) model.P2PMiningFreshnessSnapshot {
	return s.freshness
}

type fixedMedianTimeSource struct{ now time.Time }

func (f fixedMedianTimeSource) AdjustedTime() time.Time         { return f.now }
func (f fixedMedianTimeSource) AddTimeSample(string, time.Time) {}
func (f fixedMedianTimeSource) Offset() time.Duration           { return 0 }

func TestAppendRollingInt64MaintainsBoundedSum(t *testing.T) {
	var values []int64
	var sum int64
	var initialized bool
	for i := int64(1); i <= 125; i++ {
		values, sum, initialized = appendRollingInt64(values, sum, initialized, i, 100)
	}
	if len(values) != 100 {
		t.Fatalf("window length = %d, want 100", len(values))
	}
	if values[0] != 26 || values[len(values)-1] != 125 {
		t.Fatalf("window bounds = %d..%d, want 26..125", values[0], values[len(values)-1])
	}
	var want int64
	for _, v := range values {
		want += v
	}
	if sum != want {
		t.Fatalf("rolling sum = %d, want %d", sum, want)
	}
}

func TestAppendRollingFloat64MaintainsBoundedSum(t *testing.T) {
	values := []float64{1, 2, 3}
	values, sum, _ := appendRollingFloat64(values, 0, false, 4, 3)
	if len(values) != 3 {
		t.Fatalf("window length = %d, want 3", len(values))
	}
	if values[0] != 2 || values[1] != 3 || values[2] != 4 {
		t.Fatalf("window values = %v, want [2 3 4]", values)
	}
	if sum != 9 {
		t.Fatalf("rolling sum = %f, want 9", sum)
	}
}

func TestRollingStatsMatchIndependentReferenceAcrossWraps(t *testing.T) {
	var ints []int64
	var intSum int64
	var intReady bool
	var floats []float64
	var floatSum float64
	var floatReady bool
	for i := 0; i < 537; i++ {
		iv := int64((i*37)%101) - 50
		ints, intSum, intReady = appendRollingInt64(ints, intSum, intReady, iv, 100)
		wantInts := make([]int64, 0, 100)
		start := i - 99
		if start < 0 {
			start = 0
		}
		for j := start; j <= i; j++ {
			wantInts = append(wantInts, int64((j*37)%101)-50)
		}
		var wantIntSum int64
		for _, v := range wantInts {
			wantIntSum += v
		}
		if len(ints) != len(wantInts) || intSum != wantIntSum {
			t.Fatalf("int rolling step %d = len %d sum %d, want len %d sum %d", i, len(ints), intSum, len(wantInts), wantIntSum)
		}

		fv := []float64{0, 1e12, -1e12, float64(i%7) / 3, -float64(i%11) / 5}[i%5]
		floats, floatSum, floatReady = appendRollingFloat64(floats, floatSum, floatReady, fv, 100)
		start = i - 99
		if start < 0 {
			start = 0
		}
		var wantFloatSum float64
		var wantFloatMagnitude float64
		for j := start; j <= i; j++ {
			want := []float64{0, 1e12, -1e12, float64(j%7) / 3, -float64(j%11) / 5}[j%5]
			wantFloatSum += want
			wantFloatMagnitude += math.Abs(want)
		}
		bound := 1e-9 * math.Max(1, wantFloatMagnitude)
		if len(floats) != i-start+1 || math.Abs(floatSum-wantFloatSum) > bound {
			t.Fatalf("float rolling step %d = len %d sum %.17g, want len %d sum %.17g (bound %.3g)", i, len(floats), floatSum, i-start+1, wantFloatSum, bound)
		}
	}
}

func TestGBTWorkerReusesAndInvalidatesSerializedTemplateCache(t *testing.T) {
	m := newRemoteGBTResultTestMiner()
	m.policy = &mining.Policy{PeerID: "test-peer"}
	m.timeSource = fixedMedianTimeSource{now: time.Unix(1_770_000_010, 0)}
	m.minTimestamp = time.Unix(1_770_000_000, 0)
	m.lastTemplate = time.Unix(1_770_000_001, 0)
	parent := common.HexToHash("0x11")
	m.template.Block.Parents = []*common.Hash{&parent}
	w := NewGBTWorker(m)

	first, err := w.getResult(nil)
	if err != nil {
		t.Fatalf("first getResult: %v", err)
	}
	cache := w.cache
	if cache == nil {
		t.Fatal("expected serialized template cache")
	}
	first.Parents[0].Hash = "mutated"
	second, err := w.getResult(nil)
	if err != nil {
		t.Fatalf("second getResult: %v", err)
	}
	if w.cache != cache {
		t.Fatal("expected unchanged template to reuse cache")
	}
	if second.Parents[0].Hash == "mutated" {
		t.Fatal("caller mutation leaked into serialized template cache")
	}

	m.template.Block.Header.TxRoot = common.HexToHash("0x99")
	if _, err := w.getResult(nil); err != nil {
		t.Fatalf("changed template getResult: %v", err)
	}
	if w.cache == cache {
		t.Fatal("expected changed template content to invalidate cache")
	}
	w.Update()
	if w.cache != nil {
		t.Fatal("Update did not invalidate serialized template cache")
	}
}

// newHTTPClient returns a new HTTP client that is configured according to the
// proxy and TLS settings in the associated connection configuration.
func (rpc *RpcClient) newHTTPClient() (*http.Client, error) {
	// Configure proxy if needed.
	var dial func(network, addr string) (net.Conn, error)

	// Configure TLS if needed.
	var tlsConfig *tls.Config

	// Create and return the new HTTP client potentially configured with a
	// proxy and TLS.
	client := http.Client{
		Transport: &http.Transport{
			Dial:            dial,
			TLSClientConfig: tlsConfig,
			DialContext: (&net.Dialer{
				Timeout:   5 * time.Second,
				KeepAlive: 5 * time.Second,
				DualStack: true,
			}).DialContext,
		},
	}
	return &client, nil
}

func (rpc *RpcClient) RpcResult(method string, params []any) []byte {
	paramStr, err := json.Marshal(params)
	if err != nil {
		fmt.Println("rpc params error:", err)
		return nil
	}
	jsonStr := []byte(`{"jsonrpc": "2.0", "method": "` + method + `", "params": ` + string(paramStr) + `, "id": 1}`)
	bodyBuff := bytes.NewBuffer(jsonStr)
	httpRequest, err := http.NewRequest("POST", rpc.Server, bodyBuff)
	if err != nil {
		fmt.Println("rpc connect failed", err)
		return nil
	}
	httpRequest.Close = true
	httpRequest.Header.Set("Content-Type", "application/json")
	// Configure basic access authorization.
	httpRequest.SetBasicAuth(rpc.User, rpc.Pwd)

	// Create the new HTTP client that is configured according to the user-
	// specified options and submit the request.
	httpClient, err := rpc.newHTTPClient()
	if err != nil {
		fmt.Println("rpc auth faild", err)
		return nil
	}
	httpClient.Timeout = 10 * time.Second
	httpResponse, err := httpClient.Do(httpRequest)
	if err != nil {
		fmt.Println("rpc request faild", err)
		return nil
	}
	body, err := ioutil.ReadAll(httpResponse.Body)
	defer func() {
		_ = httpResponse.Body.Close()
	}()
	if err != nil {
		fmt.Println("error reading json reply:", err)
		return nil
	}

	if httpResponse.Status != "200 OK" {
		fmt.Println("error http response :", httpResponse.Status, body)
		return nil
	}
	return body
}

func TestMiningSyncAllowed(t *testing.T) {
	tests := []struct {
		name              string
		submitNoSynced    bool
		allowNearlySynced bool
		current           bool
		nearlySynced      bool
		chainCurrent      bool
		miningFreshSeen   bool
		miningFresh       bool
		miningFreshReason string
		want              bool
	}{
		{
			name:         "current node is allowed",
			current:      true,
			nearlySynced: false,
			want:         true,
		},
		{
			name:              "current node is rejected by weak mining freshness",
			current:           true,
			chainCurrent:      true,
			miningFreshSeen:   true,
			miningFresh:       false,
			miningFreshReason: model.P2PMiningFreshNotEnoughFreshPeers,
			want:              false,
		},
		{
			name:           "global submit override is allowed",
			submitNoSynced: true,
			current:        false,
			nearlySynced:   false,
			want:           true,
		},
		{
			name:              "global submit override does not bypass observed stale mining freshness",
			submitNoSynced:    true,
			current:           false,
			nearlySynced:      false,
			chainCurrent:      true,
			miningFreshSeen:   true,
			miningFresh:       false,
			miningFreshReason: model.P2PMiningFreshNoConsensusPeers,
			want:              false,
		},
		{
			name:              "nearly synced requires feature flag",
			allowNearlySynced: true,
			current:           false,
			nearlySynced:      true,
			want:              true,
		},
		{
			name:         "nearly synced remains rejected by default",
			current:      false,
			nearlySynced: true,
			want:         false,
		},
		{
			name:              "feature flag does not allow unsynced node",
			allowNearlySynced: true,
			current:           false,
			nearlySynced:      false,
			want:              false,
		},
		{
			name:              "mining fresh peer view is allowed",
			current:           false,
			nearlySynced:      false,
			chainCurrent:      true,
			miningFreshSeen:   true,
			miningFresh:       true,
			miningFreshReason: model.P2PMiningFreshOK,
			want:              true,
		},
		{
			name:              "observed stale mining peer view is rejected",
			current:           false,
			nearlySynced:      false,
			chainCurrent:      true,
			miningFreshSeen:   true,
			miningFresh:       false,
			miningFreshReason: model.P2PMiningFreshPeerLeadTooHigh,
			want:              false,
		},
		{
			name:              "mining fresh peer view still requires chain current",
			current:           false,
			nearlySynced:      false,
			chainCurrent:      false,
			miningFreshSeen:   true,
			miningFresh:       true,
			miningFreshReason: model.P2PMiningFreshOK,
			want:              false,
		},
		{
			name:            "partial mining freshness snapshot is rejected",
			current:         false,
			nearlySynced:    false,
			chainCurrent:    true,
			miningFreshSeen: false,
			miningFresh:     true,
			want:            false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := miningSyncAllowed(tt.submitNoSynced, tt.allowNearlySynced, tt.current, tt.nearlySynced, tt.chainCurrent, tt.miningFreshSeen, tt.miningFresh, tt.miningFreshReason)
			if got != tt.want {
				t.Fatalf("miningSyncAllowed() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestMiningTemplateSyncAllowed(t *testing.T) {
	tests := []struct {
		name              string
		current           bool
		chainCurrent      bool
		miningFreshSeen   bool
		miningFresh       bool
		miningFreshReason string
		want              bool
	}{
		{
			name:    "current node is allowed",
			current: true,
			want:    true,
		},
		{
			name:              "current node is rejected by weak mining freshness",
			current:           true,
			chainCurrent:      true,
			miningFreshSeen:   true,
			miningFresh:       false,
			miningFreshReason: model.P2PMiningFreshNotEnoughFreshPeers,
			want:              false,
		},
		{
			name: "syncing node without mining freshness is rejected",
			want: false,
		},
		{
			name:              "mining-fresh peer view is allowed",
			chainCurrent:      true,
			miningFreshSeen:   true,
			miningFresh:       true,
			miningFreshReason: model.P2PMiningFreshOK,
			want:              true,
		},
		{
			name:              "stale mining freshness is rejected",
			chainCurrent:      true,
			miningFreshSeen:   true,
			miningFresh:       false,
			miningFreshReason: model.P2PMiningFreshPeerLeadTooHigh,
			want:              false,
		},
		{
			name:              "mining freshness requires chain current",
			chainCurrent:      false,
			miningFreshSeen:   true,
			miningFresh:       true,
			miningFreshReason: model.P2PMiningFreshOK,
			want:              false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := miningTemplateSyncAllowed(tt.current, tt.chainCurrent, tt.miningFreshSeen, tt.miningFresh, tt.miningFreshReason)
			if got != tt.want {
				t.Fatalf("miningTemplateSyncAllowed() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestEvaluateMiningRPCReadiness(t *testing.T) {
	boolPtr := func(v bool) *bool { return &v }
	tests := []struct {
		name              string
		submitNoSynced    bool
		allowNearlySynced bool
		mainOrder         uint64
		chainCurrent      bool
		p2pAvailable      bool
		p2pCurrent        bool
		p2pNearlySynced   bool
		p2pFreshSeen      bool
		p2pMiningFresh    bool
		p2pFreshReason    string
		wantAllowed       bool
		wantCode          string
		wantTemplateReady *bool
		wantTemplateCode  string
	}{
		{
			name:        "bootstrap height does not need peers",
			wantAllowed: true,
			wantCode:    templateHealthOK,
		},
		{
			name:            "p2p unavailable blocks non-bootstrap mining",
			mainOrder:       10,
			wantAllowed:     false,
			wantCode:        templateHealthP2PUnavailable,
			p2pAvailable:    false,
			p2pCurrent:      false,
			p2pNearlySynced: false,
		},
		{
			name:         "syncing node blocks mining",
			mainOrder:    10,
			p2pAvailable: true,
			wantAllowed:  false,
			wantCode:     templateHealthNodeSyncing,
		},
		{
			name:              "nearly synced node is allowed only with feature flag",
			allowNearlySynced: true,
			mainOrder:         10,
			p2pAvailable:      true,
			p2pNearlySynced:   true,
			wantAllowed:       true,
			wantCode:          templateHealthOK,
			wantTemplateReady: boolPtr(false),
			wantTemplateCode:  templateHealthNodeSyncing,
		},
		{
			name:         "current node is allowed",
			mainOrder:    10,
			p2pAvailable: true,
			p2pCurrent:   true,
			wantAllowed:  true,
			wantCode:     templateHealthOK,
		},
		{
			name:           "current node is rejected by weak mining freshness",
			mainOrder:      10,
			chainCurrent:   true,
			p2pAvailable:   true,
			p2pCurrent:     true,
			p2pFreshSeen:   true,
			p2pMiningFresh: false,
			p2pFreshReason: model.P2PMiningFreshNotEnoughFreshPeers,
			wantAllowed:    false,
			wantCode:       templateHealthNodeSyncing,
		},
		{
			name:           "mining fresh node is allowed without strict current flag",
			mainOrder:      10,
			chainCurrent:   true,
			p2pAvailable:   true,
			p2pFreshSeen:   true,
			p2pMiningFresh: true,
			p2pFreshReason: model.P2PMiningFreshOK,
			wantAllowed:    true,
			wantCode:       templateHealthOK,
		},
		{
			name:           "observed stale mining freshness still blocks mining",
			mainOrder:      10,
			chainCurrent:   true,
			p2pAvailable:   true,
			p2pFreshSeen:   true,
			p2pMiningFresh: false,
			p2pFreshReason: model.P2PMiningFreshPeerLeadTooHigh,
			wantAllowed:    false,
			wantCode:       templateHealthNodeSyncing,
		},
		{
			name:           "mining fresh node still requires chain current",
			mainOrder:      10,
			chainCurrent:   false,
			p2pAvailable:   true,
			p2pFreshSeen:   true,
			p2pMiningFresh: true,
			p2pFreshReason: model.P2PMiningFreshOK,
			wantAllowed:    false,
			wantCode:       templateHealthNodeSyncing,
		},
		{
			name:           "partial mining freshness snapshot is not enough",
			mainOrder:      10,
			chainCurrent:   true,
			p2pAvailable:   true,
			p2pFreshSeen:   false,
			p2pMiningFresh: true,
			wantAllowed:    false,
			wantCode:       templateHealthNodeSyncing,
		},
		{
			name:              "submit override is allowed",
			submitNoSynced:    true,
			mainOrder:         10,
			p2pAvailable:      true,
			wantAllowed:       true,
			wantCode:          templateHealthOK,
			wantTemplateReady: boolPtr(false),
			wantTemplateCode:  templateHealthNodeSyncing,
		},
		{
			name:              "bootstrap p2p syncing blocks template creation despite submit override",
			submitNoSynced:    true,
			mainOrder:         0,
			p2pAvailable:      true,
			wantAllowed:       true,
			wantCode:          templateHealthOK,
			wantTemplateReady: boolPtr(false),
			wantTemplateCode:  templateHealthNodeSyncing,
		},
		{
			name:           "submit override does not bypass observed stale mining freshness",
			submitNoSynced: true,
			mainOrder:      10,
			chainCurrent:   true,
			p2pAvailable:   true,
			p2pFreshSeen:   true,
			p2pMiningFresh: false,
			p2pFreshReason: model.P2PMiningFreshNoConsensusPeers,
			wantAllowed:    false,
			wantCode:       templateHealthNodeSyncing,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := evaluateMiningRPCReadiness(
				tt.submitNoSynced,
				tt.allowNearlySynced,
				tt.mainOrder,
				tt.chainCurrent,
				tt.p2pAvailable,
				tt.p2pCurrent,
				tt.p2pNearlySynced,
				tt.p2pFreshSeen,
				tt.p2pMiningFresh,
				tt.p2pFreshReason,
			)
			if got.allowed != tt.wantAllowed || got.reasonCode != tt.wantCode {
				t.Fatalf("readiness = allowed:%v code:%q, want allowed:%v code:%q", got.allowed, got.reasonCode, tt.wantAllowed, tt.wantCode)
			}
			wantTemplateReady := tt.wantAllowed
			if tt.wantTemplateReady != nil {
				wantTemplateReady = *tt.wantTemplateReady
			}
			wantTemplateCode := tt.wantCode
			if tt.wantTemplateCode != "" {
				wantTemplateCode = tt.wantTemplateCode
			}
			if got.getTemplateReady != wantTemplateReady || got.getTemplateCode != wantTemplateCode {
				t.Fatalf("getTemplate readiness = ready:%v code:%q, want ready:%v code:%q", got.getTemplateReady, got.getTemplateCode, wantTemplateReady, wantTemplateCode)
			}
		})
	}
}

func TestTemplateHealthReportsMissingTemplate(t *testing.T) {
	m := &Miner{
		cfg: &config.Config{Miner: true},
	}
	if err := m.Service.Start(); err != nil {
		t.Fatalf("start service: %v", err)
	}
	defer func() {
		if err := m.Service.Stop(); err != nil {
			t.Fatalf("stop service: %v", err)
		}
	}()
	m.templateSeq.Store(42)

	health := m.buildTemplateHealthResult(time.Unix(100, 0))
	if health.Enabled != true {
		t.Fatal("expected enabled miner in health result")
	}
	if health.Started != true {
		t.Fatal("expected started service in health result")
	}
	if health.TemplateAvailable {
		t.Fatal("expected no template to be available")
	}
	if health.ReasonCode != templateHealthTemplateUnavailable {
		t.Fatalf("ReasonCode = %q, want %q", health.ReasonCode, templateHealthTemplateUnavailable)
	}
	if health.LatestTemplateSeq != 42 {
		t.Fatalf("LatestTemplateSeq = %d, want 42", health.LatestTemplateSeq)
	}
	if health.MineableNow || health.SubmitReady {
		t.Fatal("expected miner to be neither mineable nor submit-ready without a template")
	}
}

func TestTemplateHealthSeparatesGBTReadinessFromCachedTemplateUsability(t *testing.T) {
	now := time.Unix(260, 0)
	m := &Miner{
		cfg:      &config.Config{Miner: true, SubmitNoSynced: true},
		worker:   testWorker{running: true},
		template: testBlockTemplate(now.Add(-2 * time.Minute)),
	}
	if err := m.Service.Start(); err != nil {
		t.Fatalf("start service: %v", err)
	}
	defer func() {
		if err := m.Service.Stop(); err != nil {
			t.Fatalf("stop service: %v", err)
		}
	}()
	m.lastTemplate = now.Add(-2 * time.Minute)

	health := m.buildTemplateHealthResult(now)
	if health.GetBlockTemplateReady != true || health.GetBlockTemplateReasonCode != templateHealthOK {
		t.Fatalf("getBlockTemplate preflight should remain ready for on-demand refresh: %#v", health)
	}
	if health.MineableNow || health.SubmitReady || health.ReasonCode != templateHealthTemplateExpired {
		t.Fatalf("expired cached template should not be immediately mineable: %#v", health)
	}
}

func TestTemplateHealthKeepsMineableWithNonBlockingBuildError(t *testing.T) {
	now := time.Unix(200, 0)
	m := &Miner{
		cfg:      &config.Config{Miner: true, SubmitNoSynced: true},
		worker:   testWorker{running: true},
		template: testBlockTemplate(now.Add(-time.Second)),
	}
	if err := m.Service.Start(); err != nil {
		t.Fatalf("start service: %v", err)
	}
	defer func() {
		if err := m.Service.Stop(); err != nil {
			t.Fatalf("stop service: %v", err)
		}
	}()
	m.lastTemplate = now.Add(-time.Second)
	m.recordTemplateBuildError("txpool_changed", now.Add(-500*time.Millisecond), fmt.Errorf("Blockdag is downloading tx"))

	health := m.buildTemplateHealthResult(now)
	if !health.TemplateAvailable || !health.TemplateUsable {
		t.Fatalf("template should be available and usable: %#v", health)
	}
	if !health.MineableNow || !health.SubmitReady {
		t.Fatalf("non-blocking build error should not clear mineability: %#v", health)
	}
	if health.ReasonCode != templateHealthOK {
		t.Fatalf("ReasonCode = %q, want %q", health.ReasonCode, templateHealthOK)
	}
	if health.LastTemplateBuildErrorCode != "mempool_not_ready" {
		t.Fatalf("LastTemplateBuildErrorCode = %q, want mempool_not_ready", health.LastTemplateBuildErrorCode)
	}
	if health.LastTemplateBuildBlocking {
		t.Fatal("fresh last-good template should make the build error non-blocking")
	}
	if health.LastGoodTemplateAgeMs != int64(time.Second/time.Millisecond) {
		t.Fatalf("LastGoodTemplateAgeMs = %d, want 1000", health.LastGoodTemplateAgeMs)
	}
}

func TestTemplateHealthPendingResetErrorNonBlockingWithFreshLastGood(t *testing.T) {
	now := time.Unix(220, 0)
	m := &Miner{
		cfg:      &config.Config{Miner: true, SubmitNoSynced: true},
		worker:   testWorker{running: true},
		template: testBlockTemplate(now.Add(-time.Second)),
	}
	if err := m.Service.Start(); err != nil {
		t.Fatalf("start service: %v", err)
	}
	defer func() {
		if err := m.Service.Stop(); err != nil {
			t.Fatalf("stop service: %v", err)
		}
	}()
	m.lastTemplate = now.Add(-time.Second)
	m.recordTemplateBuildError("parents_changed", now.Add(-500*time.Millisecond), fmt.Errorf("Failed to reset BdagPool template: evm pending unavailable"))

	health := m.buildTemplateHealthResult(now)
	if !health.MineableNow || !health.SubmitReady {
		t.Fatalf("fresh last-good template should remain mineable after pending reset error: %#v", health)
	}
	if health.LastTemplateBuildErrorCode != templateHealthEVMPendingUnavailable {
		t.Fatalf("LastTemplateBuildErrorCode = %q, want %q", health.LastTemplateBuildErrorCode, templateHealthEVMPendingUnavailable)
	}
	if health.LastTemplateBuildBlocking {
		t.Fatal("fresh last-good template should make pending reset error non-blocking")
	}
}

func TestTemplateHealthAllowsCachedTemplateSubmitsWhenBdagPoolIsSyncing(t *testing.T) {
	now := time.Unix(225, 0)
	m := &Miner{
		cfg:      &config.Config{Miner: true, SubmitNoSynced: true, AllowMiningWhenNearlySynced: true},
		worker:   testWorker{running: true},
		template: testBlockTemplate(now.Add(-time.Second)),
		bdagPool: &testSyncingPendingTemplateResetter{syncing: true},
	}
	if err := m.Service.Start(); err != nil {
		t.Fatalf("start service: %v", err)
	}
	defer func() {
		if err := m.Service.Stop(); err != nil {
			t.Fatalf("stop service: %v", err)
		}
	}()
	m.lastTemplate = now.Add(-time.Second)

	health := m.buildTemplateHealthResult(now)
	if !health.SyncAllowed || health.GetBlockTemplateReady || !health.MineableNow || !health.SubmitReady {
		t.Fatalf("syncing BdagPool should only block fresh template creation when the cached template is usable: %#v", health)
	}
	if health.ReasonCode != templateHealthOK ||
		health.SyncReasonCode != templateHealthOK ||
		health.GetBlockTemplateReasonCode != templateHealthNodeSyncing {
		t.Fatalf("unexpected syncing reason codes: %#v", health)
	}
}

func TestTemplateHealthIncludesP2PFreshness(t *testing.T) {
	now := time.Unix(240, 0)
	m := &Miner{
		cfg:      &config.Config{Miner: true, SubmitNoSynced: true},
		worker:   testWorker{running: true},
		template: testBlockTemplate(now.Add(-time.Second)),
		p2pSer: testP2PService{
			current: true,
			freshness: model.P2PMiningFreshnessSnapshot{
				FreshnessMaxAge:         3 * time.Second,
				PeerLeadToleranceBlocks: 10,
				ConsensusPeerCount:      3,
				FreshConsensusPeerCount: 2,
				StaleConsensusPeerCount: 1,
				BestPeerMainOrder:       120,
				BestPeerLeadBlocks:      4,
				BestPeerGraphStateAge:   900 * time.Millisecond,
				AvgGraphStateAge:        2 * time.Second,
				MaxGraphStateAge:        5 * time.Second,
				SyncPeerPresent:         true,
				SyncPeerFresh:           false,
				SyncPeerGraphStateAge:   5 * time.Second,
				MiningFresh:             false,
				MiningFreshReasonCode:   model.P2PMiningFreshSyncPeerStale,
			},
		},
	}
	if err := m.Service.Start(); err != nil {
		t.Fatalf("start service: %v", err)
	}
	defer func() {
		if err := m.Service.Stop(); err != nil {
			t.Fatalf("stop service: %v", err)
		}
	}()
	m.lastTemplate = now.Add(-time.Second)

	health := m.buildTemplateHealthResult(now)
	if health.ReasonCode != templateHealthOK || !health.MineableNow || !health.SubmitReady {
		t.Fatalf("P2P freshness should not change otherwise healthy mining state: %#v", health)
	}
	if health.P2PFreshnessMaxAgeMs != 3000 ||
		health.P2PPeerLeadToleranceBlocks != 10 ||
		health.P2PConsensusPeerCount != 3 ||
		health.P2PFreshConsensusPeerCount != 2 ||
		health.P2PStaleConsensusPeerCount != 1 ||
		health.P2PBestPeerMainOrder != 120 ||
		health.P2PBestPeerLeadBlocks != 4 ||
		health.P2PBestPeerGraphStateAgeMs != 900 ||
		health.P2PAvgGraphStateAgeMs != 2000 ||
		health.P2PMaxGraphStateAgeMs != 5000 ||
		!health.P2PSyncPeerPresent ||
		health.P2PSyncPeerFresh ||
		health.P2PSyncPeerGraphStateAgeMs != 5000 ||
		health.P2PMiningFresh ||
		health.P2PMiningFreshReasonCode != model.P2PMiningFreshSyncPeerStale {
		t.Fatalf("unexpected P2P freshness fields: %#v", health)
	}
}

func TestTemplateHealthIncludesPendingTemplateBuildContext(t *testing.T) {
	now := time.Unix(245, 0)
	m := &Miner{
		cfg:      &config.Config{Miner: true, SubmitNoSynced: true},
		worker:   testWorker{running: true},
		template: testBlockTemplate(now.Add(-time.Second)),
	}
	if err := m.Service.Start(); err != nil {
		t.Fatalf("start service: %v", err)
	}
	defer func() {
		if err := m.Service.Stop(); err != nil {
			t.Fatalf("stop service: %v", err)
		}
	}()
	m.lastTemplate = now.Add(-time.Second)
	m.recordTemplateBuildSuccess("initial", now.Add(-2*time.Second))
	m.enqueueTemplateBuildRequest(templateBuildRequest{kind: templateBuildMempool, reason: "mempool_change"})

	health := m.buildTemplateHealthResult(now)
	if health.ReasonCode != templateHealthOK || !health.MineableNow || !health.SubmitReady {
		t.Fatalf("pending template build context should not change mining readiness: %#v", health)
	}
	if health.LastTemplateBuildAgeMs != int64((2*time.Second)/time.Millisecond) {
		t.Fatalf("LastTemplateBuildAgeMs = %d, want 2000", health.LastTemplateBuildAgeMs)
	}
	if !health.PendingTemplateBuild ||
		health.PendingTemplateBuildReason != "mempool_change" ||
		health.PendingTemplateBuildPriority != (templateBuildRequest{kind: templateBuildMempool}).priority() {
		t.Fatalf("unexpected pending build context: %#v", health)
	}

	<-m.templateReqChan
	req, ok := m.takeTemplateBuildRequest()
	if !ok || req.reason != "mempool_change" {
		t.Fatalf("health check consumed or changed pending template build request: %#v ok=%v", req, ok)
	}
}

func TestTemplateHealthPendingResetErrorBlockingWithoutUsableTemplate(t *testing.T) {
	now := time.Unix(230, 0)
	m := &Miner{
		cfg:    &config.Config{Miner: true, SubmitNoSynced: true},
		worker: testWorker{running: true},
	}
	if err := m.Service.Start(); err != nil {
		t.Fatalf("start service: %v", err)
	}
	defer func() {
		if err := m.Service.Stop(); err != nil {
			t.Fatalf("stop service: %v", err)
		}
	}()
	m.recordTemplateBuildError("parents_changed", now.Add(-500*time.Millisecond), fmt.Errorf("Failed to reset BdagPool template: evm pending unavailable"))

	health := m.buildTemplateHealthResult(now)
	if health.MineableNow || health.SubmitReady {
		t.Fatalf("missing template should not be mineable after pending reset error: %#v", health)
	}
	if health.ReasonCode != templateHealthEVMPendingUnavailable {
		t.Fatalf("ReasonCode = %q, want %q", health.ReasonCode, templateHealthEVMPendingUnavailable)
	}
	if !health.LastTemplateBuildBlocking {
		t.Fatal("missing last-good template should make pending reset error blocking")
	}
}

func TestTemplateHealthMarksExpiredTemplateNotMineable(t *testing.T) {
	now := time.Unix(200, 0)
	maxAge := 3 * time.Second
	state := assessTemplateUsability(testBlockTemplate(now.Add(-4*time.Second)), now.Add(-4*time.Second), now, maxAge, nil)

	if !state.available {
		t.Fatal("expected template to be available")
	}
	if state.usable {
		t.Fatal("expected expired template to be unusable")
	}
	if state.reasonCode != templateHealthTemplateExpired {
		t.Fatalf("reasonCode = %q, want %q", state.reasonCode, templateHealthTemplateExpired)
	}
}

func TestTemplateHealthMarksParentMismatchNotMineable(t *testing.T) {
	now := time.Unix(200, 0)
	templateParent := common.HexToHash("0x1234")
	currentParent := common.HexToHash("0xabcd")
	tpl := testBlockTemplate(now.Add(-time.Second))
	tpl.Block.Parents = []*common.Hash{&templateParent}

	state := assessTemplateUsability(tpl, now.Add(-time.Second), now, time.Minute, []*common.Hash{&currentParent})

	if !state.available {
		t.Fatal("expected template to be available")
	}
	if state.usable {
		t.Fatal("expected parent-mismatched template to be unusable")
	}
	if state.reasonCode != templateHealthTemplateStaleParent {
		t.Fatalf("reasonCode = %q, want %q", state.reasonCode, templateHealthTemplateStaleParent)
	}
}

func TestTemplateParentsMatchSameOrderAndReordered(t *testing.T) {
	first := common.HexToHash("0x1234")
	second := common.HexToHash("0xabcd")
	third := common.HexToHash("0xbeef")
	tpl := testBlockTemplate(time.Unix(200, 0))
	tpl.Block.Parents = []*common.Hash{&first, &second}

	if !templateParentsMatch(tpl, []*common.Hash{&first, &second}) {
		t.Fatal("expected same-order parents to match")
	}
	if !templateParentsMatch(tpl, []*common.Hash{&second, &first}) {
		t.Fatal("expected reordered parents to match through set fallback")
	}
	if templateParentsMatch(tpl, []*common.Hash{&first, &third}) {
		t.Fatal("expected unequal parent sets not to match")
	}

	tpl.Block.Parents = []*common.Hash{&first, &first}
	if !templateParentsMatch(tpl, []*common.Hash{&first}) {
		t.Fatal("expected duplicate parent counts to retain set semantics")
	}
	if templateParentsMatch(tpl, []*common.Hash{&first, nil}) {
		t.Fatal("expected nil current parent to fail closed")
	}
	tpl.Block.Parents = []*common.Hash{nil}
	if templateParentsMatch(tpl, []*common.Hash{&first}) {
		t.Fatal("expected nil template parent to fail closed")
	}
	empty := testBlockTemplate(time.Unix(200, 0))
	if !templateParentsMatch(empty, nil) {
		t.Fatal("expected two empty parent sets to match")
	}
}

func TestSubmitBlockHeaderUsesRetainedTemplateTxRoot(t *testing.T) {
	m := newRemoteGBTResultTestMiner()
	m.cfg = &config.Config{Miner: true}
	if err := m.Service.Start(); err != nil {
		t.Fatalf("start miner service: %v", err)
	}
	defer m.Service.Stop()

	current := testBlockTemplate(time.Unix(200, 0))
	current.Block.Header.Pow = pow.GetInstance(pow.BLAKE2BD, 0, 0, nil)
	currentParent := common.HexToHash("0xbb")
	current.Block.Parents = []*common.Hash{&currentParent}
	current.Block.Header.ParentRoot = currentParent
	current.Block.Header.TxRoot = common.HexToHash("0xb0")
	m.template = current

	retained := testBlockTemplate(time.Unix(200, 0))
	retained.Block.Header.Pow = pow.GetInstance(pow.BLAKE2BD, 0, 0, nil)
	retainedParent := common.HexToHash("0xaa")
	retained.Block.Parents = []*common.Hash{&retainedParent}
	retained.Block.Header.ParentRoot = retainedParent
	retained.Block.Header.TxRoot = common.HexToHash("0xa0")
	m.recentTemplates = []recentTemplate{{template: retained, addedAt: time.Now()}}

	header := retained.Block.Header
	header.TxRoot = common.HexToHash("0xdead")
	result, err := m.submitBlockHeaderContext(context.Background(), &header)
	if err == nil || result.Code != submitOutcomeTxRootMismatch {
		t.Fatalf("mismatched retained tx root result = %#v, err = %v; want txroot_mismatch before clone", result, err)
	}

	header.TxRoot = retained.Block.Header.TxRoot
	result, err = m.submitBlockHeaderContext(context.Background(), &header)
	if err == nil || result.Code == submitOutcomeTxRootMismatch {
		t.Fatalf("matching retained tx root was rejected by early guard: result=%#v err=%v", result, err)
	}
	if result.Code != submitOutcomeMinerDisabled {
		t.Fatalf("matching retained tx root reached unexpected path: result=%#v err=%v", result, err)
	}
}

func TestMinerHandlerRefusesRetainedGBTGenerationAfterBuildFailure(t *testing.T) {
	m := newRemoteGBTResultTestMiner()
	m.policy = &mining.Policy{PeerID: "test-peer"}
	m.timeSource = fixedMedianTimeSource{now: time.Unix(1_770_000_010, 0)}
	m.minTimestamp = time.Unix(1_770_000_000, 0)
	w := NewGBTWorker(m)
	if _, err := w.getResult(nil); err != nil {
		t.Fatalf("seed getResult: %v", err)
	}
	if w.cache == nil {
		t.Fatal("expected serialized cache before handler failure")
	}
	oldTemplate := m.template
	// Keep the old template installed: the production failure path retains it
	// for submit classification while GBT admission must reject its generation.
	m.bdagPool = nil
	m.msgChan = make(chan any)
	m.templateReqChan = make(chan struct{}, 1)
	m.quit = make(chan struct{})
	m.worker = w
	m.enqueueTemplateBuildRequest(templateBuildRequest{
		kind:              templateBuildBlockChain,
		force:             true,
		reason:            "required parent rebuild",
		invalidationCause: templateInvalidationParentChanged,
	})
	// The request remains pending for the GBT handler to consume, exercising
	// the request-local required-build path rather than the signal case.
	<-m.templateReqChan
	m.wg.Add(1)
	done := make(chan struct{})
	go func() {
		m.handler()
		close(done)
	}()
	failedReply := make(chan *gbtResponse, 1)
	m.msgChan <- &GBTMiningMsg{request: &corejson.TemplateRequest{}, reply: failedReply}
	failed := <-failedReply
	if failed == nil || failed.err == nil || failed.result != nil {
		t.Fatalf("required rebuild response = %#v, want error without old generation", failed)
	}
	if m.template != oldTemplate {
		t.Fatal("failed required rebuild replaced retained template")
	}
	if w.cache != nil {
		t.Fatal("failed required rebuild retained serialized cache")
	}

	repeatedReply := make(chan *gbtResponse, 1)
	m.msgChan <- &GBTMiningMsg{request: &corejson.TemplateRequest{}, reply: repeatedReply}
	repeated := <-repeatedReply
	if repeated == nil || repeated.err == nil || repeated.result != nil {
		t.Fatalf("repeated invalidated-generation response = %#v, want error without old generation", repeated)
	}

	// Model the successful updateBlockTemplate commit through the handler's
	// production success plumbing. The retained template remains available for
	// submit classification, while the new pointer clears the admission error.
	replacement := *oldTemplate
	replacement.Block = oldTemplate.Block
	m.template = &replacement
	m.updateGBTWorkerAfterTemplateBuild(oldTemplate)
	replacementReply := make(chan *gbtResponse, 1)
	m.msgChan <- &GBTMiningMsg{request: &corejson.TemplateRequest{}, reply: replacementReply}
	replaced := <-replacementReply
	if replaced == nil || replaced.err != nil || replaced.result == nil {
		t.Fatalf("replacement generation response = %#v, want success", replaced)
	}
	close(m.quit)
	<-done
}

func TestMinerHandlerPreservesGenerationFailureFromAsynchronousBuild(t *testing.T) {
	m := newRemoteGBTResultTestMiner()
	m.policy = &mining.Policy{PeerID: "test-peer"}
	m.timeSource = fixedMedianTimeSource{now: time.Unix(1_770_000_010, 0)}
	m.minTimestamp = time.Unix(1_770_000_000, 0)
	m.bdagPool = nil
	w := NewGBTWorker(m)
	if _, err := w.getResult(nil); err != nil {
		t.Fatalf("seed getResult: %v", err)
	}
	m.msgChan = make(chan any)
	m.templateReqChan = make(chan struct{}, 1)
	m.quit = make(chan struct{})
	m.worker = w
	m.wg.Add(1)
	done := make(chan struct{})
	go func() {
		m.handler()
		close(done)
	}()
	m.msgChan <- &BlockChainChangeMsg{}
	failedReply := make(chan *gbtResponse, 1)
	m.msgChan <- &GBTMiningMsg{request: &corejson.TemplateRequest{}, reply: failedReply}
	failed := <-failedReply
	if failed == nil || failed.err == nil || failed.result != nil {
		t.Fatalf("asynchronous required failure response = %#v, want error without old generation", failed)
	}
	close(m.quit)
	<-done
}

func TestMinerHandlerReplacesFailedGenerationThroughProductionInstallAndNotify(t *testing.T) {
	m := newRemoteGBTResultTestMiner()
	m.cfg = &config.Config{Miner: true}
	m.policy = &mining.Policy{PeerID: "test-peer"}
	m.timeSource = fixedMedianTimeSource{now: time.Unix(1_770_000_010, 0)}
	m.minTimestamp = time.Unix(1_770_000_000, 0)
	m.minimumMedianTimeFn = func() time.Time { return time.Unix(1_770_000_000, 0) }
	m.txpool = &legacypool.LegacyPool{}
	m.msgChan = make(chan any)
	m.templateReqChan = make(chan struct{}, 1)
	m.quit = make(chan struct{})
	worker := NewGBTWorker(m)
	m.worker = worker
	if _, err := worker.getResult(nil); err != nil {
		t.Fatalf("seed getResult: %v", err)
	}
	oldTemplate := m.template

	// A required rebuild fails through the handler's request path, retaining
	// the old template while closing GBT publication admission.
	m.bdagPool = nil
	m.enqueueTemplateBuildRequest(templateBuildRequest{
		kind:              templateBuildBlockChain,
		force:             true,
		reason:            "required parent rebuild",
		invalidationCause: templateInvalidationParentChanged,
	})
	<-m.templateReqChan
	m.wg.Add(1)
	done := make(chan struct{})
	go func() {
		m.handler()
		close(done)
	}()
	failedReply := make(chan *gbtResponse, 1)
	m.msgChan <- &GBTMiningMsg{request: &corejson.TemplateRequest{}, reply: failedReply}
	failed := <-failedReply
	if failed == nil || failed.err == nil || failed.result != nil {
		t.Fatalf("required rebuild response = %#v, want retained generation refusal", failed)
	}
	if m.template != oldTemplate || m.gbtGenerationError() == nil {
		t.Fatal("failed production rebuild did not retain old template and generation hold")
	}
	readiness := m.miningRPCReadiness()
	if readiness.getTemplateReady || readiness.buildTemplateReady == false {
		t.Fatalf("generation hold readiness = publication:%v build:%v, want false/true", readiness.getTemplateReady, readiness.buildTemplateReady)
	}

	parent := common.HexToHash("0xfeed")
	replacement := *oldTemplate
	replacement.Block = &coretypes.Block{Header: oldTemplate.Block.Header}
	replacement.Block.Header.ParentRoot = parent
	replacement.Block.Header.CoinbaseAddress = m.coinbaseAddress
	replacement.Fees = nil
	resetter := &testPendingTemplateResetter{}
	m.bdagPool = resetter
	m.buildTemplate = func(common.Address, bool) (*coretypes.BlockTemplate, error) {
		return &replacement, nil
	}

	feed := make(chan *corejson.RemoteGBTResult, 1)
	feedSub := m.TemplateEvent().Subscribe(feed)
	defer feedSub.Unsubscribe()
	m.msgChan <- &BlockChainChangeMsg{}
	var notified *corejson.RemoteGBTResult
	select {
	case notified = <-feed:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for replacement notification")
	}
	if notified == nil || notified.PreviousHash != parent.String() {
		t.Fatalf("replacement notification = %#v, want parent %s", notified, parent)
	}
	if m.template != &replacement || m.gbtGenerationError() != nil {
		t.Fatal("production install did not clear the generation hold")
	}
	readiness = m.miningRPCReadiness()
	if !readiness.getTemplateReady || !readiness.buildTemplateReady {
		t.Fatalf("replacement readiness = publication:%v build:%v, want true/true", readiness.getTemplateReady, readiness.buildTemplateReady)
	}
	if resetter.calls != 1 {
		t.Fatalf("replacement reset calls = %d, want 1", resetter.calls)
	}

	replacementReply := make(chan *gbtResponse, 1)
	m.msgChan <- &GBTMiningMsg{request: &corejson.TemplateRequest{}, reply: replacementReply}
	replaced := <-replacementReply
	if replaced == nil || replaced.err != nil || replaced.result == nil {
		t.Fatalf("replacement generation response = %#v, want success", replaced)
	}
	close(m.quit)
	<-done
}

func TestMinerHandlerAllowsGBTWithoutPendingBuild(t *testing.T) {
	m := newRemoteGBTResultTestMiner()
	m.policy = &mining.Policy{PeerID: "test-peer"}
	m.timeSource = fixedMedianTimeSource{now: time.Unix(1_770_000_010, 0)}
	m.minTimestamp = time.Unix(1_770_000_000, 0)
	w := NewGBTWorker(m)
	if _, err := w.getResult(nil); err != nil {
		t.Fatalf("seed getResult: %v", err)
	}
	m.msgChan = make(chan any)
	m.templateReqChan = make(chan struct{}, 1)
	m.quit = make(chan struct{})
	m.worker = w
	m.wg.Add(1)
	done := make(chan struct{})
	go func() {
		m.handler()
		close(done)
	}()
	reply := make(chan *gbtResponse, 1)
	m.msgChan <- &GBTMiningMsg{request: &corejson.TemplateRequest{}, reply: reply}
	response := <-reply
	if response == nil || response.err != nil || response.result == nil {
		t.Fatalf("no-pending GBT response = %#v, want success", response)
	}
	close(m.quit)
	<-done
}

func TestQueueLatestBlockTemplateDropsOldest(t *testing.T) {
	ch := make(chan *corejson.RemoteGBTResult, 1)
	queueLatestBlockTemplate(ch, &corejson.RemoteGBTResult{Seq: 1})
	queueLatestBlockTemplate(ch, &corejson.RemoteGBTResult{Seq: 2})

	got := <-ch
	if got.Seq != 2 {
		t.Fatalf("queued template seq = %d, want newest seq 2", got.Seq)
	}
	select {
	case extra := <-ch:
		t.Fatalf("expected only one latest template, got extra seq %d", extra.Seq)
	default:
	}
}

func TestTemplateInvalidationNormalizesRequiredCauses(t *testing.T) {
	tests := []struct {
		name string
		req  templateBuildRequest
		want templateInvalidationCause
	}{
		{name: "parent changed", req: templateBuildRequest{kind: templateBuildBlockChain}, want: templateInvalidationParentChanged},
		{name: "state changed", req: templateBuildRequest{force: true}, want: templateInvalidationStateChanged},
		{name: "mempool root changed", req: templateBuildRequest{kind: templateBuildMempool}, want: templateInvalidationMempoolRootChanged},
		{name: "template sequence obsolete", req: templateBuildRequest{kind: templateBuildStall}, want: templateInvalidationSequenceObsolete},
		{name: "submit obsolete depth", req: templateBuildRequest{kind: templateBuildSubmit}, want: templateInvalidationSubmitObsoleteDepth},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := normalizeTemplateBuildRequest(test.req)
			if got.invalidationCause != test.want {
				t.Fatalf("invalidationCause = %q, want %q", got.invalidationCause, test.want)
			}
			if got.reason != test.want.String() {
				t.Fatalf("reason = %q, want %q", got.reason, test.want)
			}
		})
	}
}

func TestTemplateBuildQueueCoalescesMempoolBurst(t *testing.T) {
	m := &Miner{templateReqChan: make(chan struct{}, 1)}

	m.enqueueTemplateBuildRequest(templateBuildRequest{kind: templateBuildMempool, reason: "mempool-1"})
	m.enqueueTemplateBuildRequest(templateBuildRequest{kind: templateBuildMempool, reason: "mempool-2"})
	m.enqueueTemplateBuildRequest(templateBuildRequest{kind: templateBuildMempool, reason: "mempool-3"})

	if got := len(m.templateReqChan); got != 1 {
		t.Fatalf("template request signal count = %d, want 1", got)
	}
	<-m.templateReqChan
	req, ok := m.takeTemplateBuildRequest()
	if !ok {
		t.Fatal("expected pending template request")
	}
	if req.kind != templateBuildMempool || req.reason != "mempool-3" {
		t.Fatalf("request = %#v, want latest mempool request", req)
	}
}

func TestTemplateInvalidationCoalescesMempoolBurstOnce(t *testing.T) {
	m := &Miner{templateReqChan: make(chan struct{}, 1)}

	m.enqueueTemplateBuildRequest(templateBuildRequest{kind: templateBuildMempool, reason: "mempool-1"})
	m.enqueueTemplateBuildRequest(templateBuildRequest{kind: templateBuildMempool, reason: "mempool-2"})
	m.enqueueTemplateBuildRequest(templateBuildRequest{kind: templateBuildMempool, reason: "mempool-3"})

	if got := len(m.templateReqChan); got != 1 {
		t.Fatalf("template request signal count = %d, want 1", got)
	}
	if m.lastTemplateInvalidationSeq != 1 {
		t.Fatalf("lastTemplateInvalidationSeq = %d, want 1", m.lastTemplateInvalidationSeq)
	}
	if m.lastTemplateInvalidationCause != templateInvalidationMempoolRootChanged {
		t.Fatalf("lastTemplateInvalidationCause = %q, want %q", m.lastTemplateInvalidationCause, templateInvalidationMempoolRootChanged)
	}

	<-m.templateReqChan
	req, ok := m.takeTemplateBuildRequest()
	if !ok {
		t.Fatal("expected pending template request")
	}
	if req.reason != "mempool-3" || req.invalidationCause != templateInvalidationMempoolRootChanged {
		t.Fatalf("request = %#v, want latest mempool reason with mempool_root_changed cause", req)
	}
	if !req.invalidationRecorded {
		t.Fatal("pending request should carry recorded invalidation marker")
	}
}

func TestTemplateBuildQueueParentOutranksMempool(t *testing.T) {
	m := &Miner{templateReqChan: make(chan struct{}, 1)}

	m.enqueueTemplateBuildRequest(templateBuildRequest{kind: templateBuildMempool, reason: "mempool"})
	m.enqueueTemplateBuildRequest(templateBuildRequest{kind: templateBuildBlockChain, reason: "parent"})

	<-m.templateReqChan
	req, ok := m.takeTemplateBuildRequest()
	if !ok {
		t.Fatal("expected pending template request")
	}
	if req.kind != templateBuildBlockChain || req.reason != "parent" {
		t.Fatalf("request = %#v, want parent change to outrank mempool", req)
	}
}

func TestTemplateInvalidationParentCauseOutranksMempool(t *testing.T) {
	m := &Miner{templateReqChan: make(chan struct{}, 1)}

	m.enqueueTemplateBuildRequest(templateBuildRequest{kind: templateBuildMempool, reason: "mempool"})
	m.enqueueTemplateBuildRequest(templateBuildRequest{kind: templateBuildBlockChain, reason: "parent"})

	if m.lastTemplateInvalidationSeq != 2 {
		t.Fatalf("lastTemplateInvalidationSeq = %d, want 2", m.lastTemplateInvalidationSeq)
	}
	if m.lastTemplateInvalidationCause != templateInvalidationParentChanged {
		t.Fatalf("lastTemplateInvalidationCause = %q, want %q", m.lastTemplateInvalidationCause, templateInvalidationParentChanged)
	}

	<-m.templateReqChan
	req, ok := m.takeTemplateBuildRequest()
	if !ok {
		t.Fatal("expected pending template request")
	}
	if req.kind != templateBuildBlockChain || req.invalidationCause != templateInvalidationParentChanged {
		t.Fatalf("request = %#v, want parent_changed to outrank mempool", req)
	}
}

func TestTemplateBuildQueueParentIsNotDowngradedByLaterMempool(t *testing.T) {
	m := &Miner{templateReqChan: make(chan struct{}, 1)}

	m.enqueueTemplateBuildRequest(templateBuildRequest{kind: templateBuildMempool, reason: "mempool-1"})
	m.enqueueTemplateBuildRequest(templateBuildRequest{kind: templateBuildBlockChain, reason: "parent"})
	m.enqueueTemplateBuildRequest(templateBuildRequest{kind: templateBuildMempool, reason: "mempool-2"})

	<-m.templateReqChan
	req, ok := m.takeTemplateBuildRequest()
	if !ok {
		t.Fatal("expected pending template request")
	}
	if req.kind != templateBuildBlockChain || req.reason != "parent" {
		t.Fatalf("request = %#v, want parent change to survive later mempool churn", req)
	}
}

func TestTemplateInvalidationSubmitObsoleteDepthOutranksParent(t *testing.T) {
	m := &Miner{templateReqChan: make(chan struct{}, 1)}

	m.enqueueTemplateBuildRequest(templateBuildRequest{kind: templateBuildBlockChain, reason: "parent"})
	m.invalidateForSubmitObsoleteDepth(3)

	if m.lastTemplateInvalidationSeq != 2 {
		t.Fatalf("lastTemplateInvalidationSeq = %d, want 2", m.lastTemplateInvalidationSeq)
	}
	if m.lastTemplateInvalidationCause != templateInvalidationSubmitObsoleteDepth {
		t.Fatalf("lastTemplateInvalidationCause = %q, want %q", m.lastTemplateInvalidationCause, templateInvalidationSubmitObsoleteDepth)
	}
	if m.lastTemplateInvalidationSubmitObsoleteDepth != 3 {
		t.Fatalf("lastTemplateInvalidationSubmitObsoleteDepth = %d, want 3", m.lastTemplateInvalidationSubmitObsoleteDepth)
	}

	<-m.templateReqChan
	req, ok := m.takeTemplateBuildRequest()
	if !ok {
		t.Fatal("expected pending template request")
	}
	if req.kind != templateBuildSubmit || req.invalidationCause != templateInvalidationSubmitObsoleteDepth || req.submitObsoleteDepth != 3 {
		t.Fatalf("request = %#v, want submit_obsolete_depth to outrank parent", req)
	}
}

func TestTemplateBuildQueueDrainsOnlyParentBeforeGBT(t *testing.T) {
	minPriority := templateBuildRequest{kind: templateBuildBlockChain}.priority()
	m := &Miner{templateReqChan: make(chan struct{}, 1)}

	m.enqueueTemplateBuildRequest(templateBuildRequest{kind: templateBuildMempool, reason: "mempool"})
	if _, ok := m.takePendingTemplateBuildRequestAtLeast(minPriority); ok {
		t.Fatal("mempool-only request should not be drained before GBT")
	}
	if got := len(m.templateReqChan); got != 1 {
		t.Fatalf("mempool signal count = %d, want still queued", got)
	}
	<-m.templateReqChan
	if _, ok := m.takeTemplateBuildRequest(); !ok {
		t.Fatal("mempool request should remain pending")
	}

	m.enqueueTemplateBuildRequest(templateBuildRequest{kind: templateBuildBlockChain, reason: "parent"})
	req, ok := m.takePendingTemplateBuildRequestAtLeast(minPriority)
	if !ok {
		t.Fatal("parent request should drain before GBT")
	}
	if req.kind != templateBuildBlockChain {
		t.Fatalf("drained request kind = %v, want parent change", req.kind)
	}
	if got := len(m.templateReqChan); got != 0 {
		t.Fatalf("parent signal count after drain = %d, want 0", got)
	}
}

func TestTemplateHealthReportsInvalidationCause(t *testing.T) {
	now := time.Unix(123, 0)
	m := &Miner{templateReqChan: make(chan struct{}, 1)}

	m.enqueueTemplateBuildRequest(templateBuildRequest{kind: templateBuildStall})
	health := m.buildTemplateHealthResult(now)

	if health.LastTemplateInvalidationSeq != 1 {
		t.Fatalf("LastTemplateInvalidationSeq = %d, want 1", health.LastTemplateInvalidationSeq)
	}
	if health.LastTemplateInvalidationCause != templateInvalidationSequenceObsolete.String() {
		t.Fatalf("LastTemplateInvalidationCause = %q, want %q", health.LastTemplateInvalidationCause, templateInvalidationSequenceObsolete)
	}
	if health.PendingTemplateInvalidationCause != templateInvalidationSequenceObsolete.String() {
		t.Fatalf("PendingTemplateInvalidationCause = %q, want %q", health.PendingTemplateInvalidationCause, templateInvalidationSequenceObsolete)
	}
	if !health.PendingTemplateBuild {
		t.Fatal("expected pending template build")
	}
}

func TestTemplateBuildQueueSignalsAgainDuringActiveBuild(t *testing.T) {
	m := &Miner{templateReqChan: make(chan struct{}, 1)}

	m.enqueueTemplateBuildRequest(templateBuildRequest{kind: templateBuildMempool, reason: "mempool"})
	<-m.templateReqChan
	if _, ok := m.takeTemplateBuildRequest(); !ok {
		t.Fatal("expected initial pending template request")
	}

	m.enqueueTemplateBuildRequest(templateBuildRequest{kind: templateBuildBlockChain, reason: "parent"})
	if got := len(m.templateReqChan); got != 1 {
		t.Fatalf("template request signal count after active build enqueue = %d, want 1", got)
	}
	<-m.templateReqChan
	req, ok := m.takeTemplateBuildRequest()
	if !ok {
		t.Fatal("expected second pending template request")
	}
	if req.kind != templateBuildBlockChain {
		t.Fatalf("second request kind = %v, want parent change", req.kind)
	}
}

func TestUpdateBlockTemplateReturnsPendingResetErrorBeforeTemplateBuild(t *testing.T) {
	resetter := &testPendingTemplateResetter{err: fmt.Errorf("evm pending unavailable")}
	m := &Miner{
		bdagPool: resetter,
	}

	err := m.updateBlockTemplate(true, nil)
	if err == nil {
		t.Fatal("expected pending reset error")
	}
	if resetter.calls != 1 {
		t.Fatalf("ResetTemplate calls = %d, want 1", resetter.calls)
	}
	if m.lastTemplateBuildErrorCode != templateHealthEVMPendingUnavailable {
		t.Fatalf("lastTemplateBuildErrorCode = %q, want %q", m.lastTemplateBuildErrorCode, templateHealthEVMPendingUnavailable)
	}
	if m.template != nil {
		t.Fatal("pending reset failure should not install a mining template")
	}
}

func TestUpdateBlockTemplatePreservesTypedResetError(t *testing.T) {
	sentinel := errors.New("template reset failed")
	resetErr := &bdagevm.PendingTemplateResetError{
		Code: bdagevm.PendingTemplateResetFailed,
		Err:  sentinel,
	}
	m := &Miner{bdagPool: &testPendingTemplateResetter{err: resetErr}}

	err := m.updateBlockTemplate(true, nil)
	if !errors.Is(err, sentinel) {
		t.Fatalf("wrapped reset error does not preserve sentinel: %v", err)
	}
	var got *bdagevm.PendingTemplateResetError
	if !errors.As(err, &got) || got != resetErr {
		t.Fatalf("wrapped reset error = %T %v, want original typed error", err, err)
	}
}

func TestTemplateBuildErrorClassification(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want string
	}{
		{name: "nil", err: nil, want: ""},
		{name: "initial download", err: fmt.Errorf("Client in initial download: Blockdag is downloading blocks"), want: templateHealthNodeSyncing},
		{name: "mempool", err: fmt.Errorf("Blockdag is downloading tx"), want: "mempool_not_ready"},
		{name: "evm pending", err: fmt.Errorf("Failed to reset BdagPool template: evm pending unavailable"), want: templateHealthEVMPendingUnavailable},
		{name: "evm pending nonce drift", err: fmt.Errorf("nonce too low: address 0xabc, tx: 25372 state: 25374"), want: templateHealthEVMPendingNonceDrift},
		{name: "bdag pool stopped", err: fmt.Errorf("Failed to reset BdagPool template: bdag pool stopped"), want: templateHealthTemplateBuildFailed},
		{name: "bdag pool syncing", err: fmt.Errorf("Failed to reset BdagPool template: bdag pool syncing"), want: templateHealthEVMPendingUnavailable},
		{name: "typed bdag pool stopped", err: &bdagevm.PendingTemplateResetError{Code: bdagevm.PendingTemplateResetStopped, Err: fmt.Errorf("template stopped")}, want: templateHealthTemplateBuildFailed},
		{name: "typed bdag pool syncing", err: &bdagevm.PendingTemplateResetError{Code: bdagevm.PendingTemplateResetSyncing, Err: fmt.Errorf("node busy syncing")}, want: templateHealthEVMPendingUnavailable},
		{name: "wrapped typed bdag pool syncing", err: fmt.Errorf("Failed to reset BdagPool template: %w", &bdagevm.PendingTemplateResetError{Code: bdagevm.PendingTemplateResetSyncing, Err: fmt.Errorf("node busy syncing")}), want: templateHealthEVMPendingUnavailable},
		{name: "time", err: fmt.Errorf("unable to update block template time"), want: "template_time_update_failed"},
		{name: "fallback", err: fmt.Errorf("boom"), want: templateHealthTemplateBuildFailed},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := classifyTemplateBuildError(tt.err); got != tt.want {
				t.Fatalf("classifyTemplateBuildError() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestTemplateBuildRecoversNonceDriftBySkippingPendingTransactions(t *testing.T) {
	var attempts []bool
	m := &Miner{
		buildTemplate: func(_ common.Address, skipPendingTransactions bool) (*coretypes.BlockTemplate, error) {
			attempts = append(attempts, skipPendingTransactions)
			if !skipPendingTransactions {
				return nil, fmt.Errorf("nonce too low: address 0xabc, tx: 25372 state: 25374")
			}
			return &coretypes.BlockTemplate{}, nil
		},
	}

	template, err, recovered := m.buildTemplateWithNonceDriftFallback(common.Address{})
	if err != nil {
		t.Fatalf("buildTemplateWithNonceDriftFallback returned error: %v", err)
	}
	if template == nil {
		t.Fatal("expected fallback template")
	}
	if !recovered {
		t.Fatal("expected nonce drift recovery")
	}
	if len(attempts) != 2 || attempts[0] || !attempts[1] {
		t.Fatalf("attempts = %v, want [false true]", attempts)
	}
}

func TestTemplateBuildDoesNotRetryNonNonceDrift(t *testing.T) {
	var attempts int
	m := &Miner{
		buildTemplate: func(_ common.Address, _ bool) (*coretypes.BlockTemplate, error) {
			attempts++
			return nil, fmt.Errorf("template unavailable")
		},
	}

	_, err, recovered := m.buildTemplateWithNonceDriftFallback(common.Address{})
	if err == nil {
		t.Fatal("expected template build error")
	}
	if recovered {
		t.Fatal("did not expect nonce drift recovery")
	}
	if attempts != 1 {
		t.Fatalf("attempts = %d, want 1", attempts)
	}
}

func TestTemplateBuildReportsNonceDriftFallbackFailure(t *testing.T) {
	var attempts []bool
	m := &Miner{
		buildTemplate: func(_ common.Address, skipPendingTransactions bool) (*coretypes.BlockTemplate, error) {
			attempts = append(attempts, skipPendingTransactions)
			if !skipPendingTransactions {
				return nil, fmt.Errorf("nonce too low: address 0xabc, tx: 25372 state: 25374")
			}
			return nil, fmt.Errorf("no block template available")
		},
	}

	_, err, recovered := m.buildTemplateWithNonceDriftFallback(common.Address{})
	if err == nil {
		t.Fatal("expected fallback failure")
	}
	if recovered {
		t.Fatal("did not expect recovery when fallback fails")
	}
	if classifyTemplateBuildError(err) != templateHealthEVMPendingNonceDrift {
		t.Fatalf("classifyTemplateBuildError() = %q, want %q", classifyTemplateBuildError(err), templateHealthEVMPendingNonceDrift)
	}
	if len(attempts) != 2 || attempts[0] || !attempts[1] {
		t.Fatalf("attempts = %v, want [false true]", attempts)
	}
}

func TestUpdateBlockTemplateStopsOnSyncingResetError(t *testing.T) {
	resetter := &testMiningPendingTemplateResetter{miningErr: fmt.Errorf("node busy syncing")}
	buildCalls := 0
	m := &Miner{
		cfg:      &config.Config{Miner: true},
		bdagPool: resetter,
		buildTemplate: func(common.Address, bool) (*coretypes.BlockTemplate, error) {
			buildCalls++
			return &coretypes.BlockTemplate{}, nil
		},
	}

	err := m.updateBlockTemplateForRequest(templateBuildRequest{force: true})
	if err == nil {
		t.Fatal("expected syncing reset error")
	}
	if buildCalls != 0 {
		t.Fatalf("buildTemplate calls = %d, want 0 while BdagPool is syncing", buildCalls)
	}
	if resetter.miningCalls != 1 {
		t.Fatalf("ResetTemplateForMining calls = %d, want 1", resetter.miningCalls)
	}
}

func TestUpdateBlockTemplateSkipsBuilderWhileP2PSyncingWithSubmitOverride(t *testing.T) {
	resetter := &testMiningPendingTemplateResetter{}
	buildCalls := 0
	m := &Miner{
		cfg:      &config.Config{Miner: true, SubmitNoSynced: true},
		p2pSer:   testP2PService{current: false},
		bdagPool: resetter,
		buildTemplate: func(common.Address, bool) (*coretypes.BlockTemplate, error) {
			buildCalls++
			return &coretypes.BlockTemplate{}, nil
		},
	}

	err := m.updateBlockTemplateForRequest(templateBuildRequest{force: true})
	if err == nil {
		t.Fatal("expected p2p syncing to block template creation")
	}
	if buildCalls != 0 {
		t.Fatalf("buildTemplate calls = %d, want 0 while p2p is syncing", buildCalls)
	}
	if resetter.miningCalls != 0 {
		t.Fatalf("ResetTemplateForMining calls = %d, want 0 while p2p is syncing", resetter.miningCalls)
	}
}

func TestResetTemplateForMiningPrefersMiningResetter(t *testing.T) {
	resetter := &testMiningPendingTemplateResetter{miningErr: fmt.Errorf("mining reset failed")}

	err := resetTemplateForMining(resetter, false)

	if err == nil || err.Error() != "mining reset failed" {
		t.Fatalf("resetTemplateForMining() error = %v, want mining reset failed", err)
	}
	if resetter.miningCalls != 1 {
		t.Fatalf("ResetTemplateForMining calls = %d, want 1", resetter.miningCalls)
	}
	if resetter.calls != 0 {
		t.Fatalf("fallback ResetTemplate calls = %d, want 0", resetter.calls)
	}
}

func TestResetTemplateForMiningUsesCanonicalResetOnlyWhenAuthorized(t *testing.T) {
	resetter := &testCanonicalPendingTemplateResetter{
		testMiningPendingTemplateResetter: testMiningPendingTemplateResetter{miningErr: fmt.Errorf("ordinary reset must not run")},
		canonicalReady:                    true,
		canonicalErr:                      fmt.Errorf("canonical reset failed"),
	}

	err := resetTemplateForMining(resetter, true)

	if err == nil || err.Error() != "canonical reset failed" {
		t.Fatalf("resetTemplateForMining() error = %v, want canonical reset failed", err)
	}
	if resetter.canonicalCalls != 1 {
		t.Fatalf("ResetTemplateForCanonicalMining calls = %d, want 1", resetter.canonicalCalls)
	}
	if resetter.miningCalls != 0 || resetter.calls != 0 {
		t.Fatalf("fallback reset calls = mining:%d ordinary:%d, want 0", resetter.miningCalls, resetter.calls)
	}
}

func TestCanonicalMiningBypassRequiresFreshCurrentPeersAndBackend(t *testing.T) {
	fresh := model.P2PMiningFreshnessSnapshot{
		ConsensusPeerCount:      3,
		FreshConsensusPeerCount: 3,
		MiningFresh:             true,
		MiningFreshReasonCode:   model.P2PMiningFreshOK,
	}
	stale := fresh
	stale.MiningFresh = false
	stale.MiningFreshReasonCode = model.P2PMiningFreshAllConsensusPeersStale

	tests := []struct {
		name           string
		backendSyncing bool
		chainCurrent   bool
		freshness      model.P2PMiningFreshnessSnapshot
		backendReady   bool
		want           bool
	}{
		{name: "stale latch with exact backend and fresh peers", backendSyncing: true, chainCurrent: true, freshness: fresh, backendReady: true, want: true},
		{name: "ordinary healthy path does not bypass", chainCurrent: true, freshness: fresh, backendReady: true},
		{name: "backend not exact", backendSyncing: true, chainCurrent: true, freshness: fresh},
		{name: "chain not current", backendSyncing: true, freshness: fresh, backendReady: true},
		{name: "stale peers", backendSyncing: true, chainCurrent: true, freshness: stale, backendReady: true},
		{name: "unobserved peers", backendSyncing: true, chainCurrent: true, backendReady: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := canonicalMiningBypassAllowed(test.backendSyncing, test.chainCurrent, test.freshness, test.backendReady); got != test.want {
				t.Fatalf("canonicalMiningBypassAllowed() = %v, want %v", got, test.want)
			}
		})
	}
}

func TestGetBlockTemplateRejectsWhenBdagPoolIsSyncing(t *testing.T) {
	api := NewPublicMinerAPI(&Miner{
		cfg:      &config.Config{Miner: true, SubmitNoSynced: true, AllowMiningWhenNearlySynced: true},
		bdagPool: &testSyncingPendingTemplateResetter{syncing: true},
	})

	_, err := api.GetBlockTemplate(context.Background(), nil, 0, nil)
	if err == nil {
		t.Fatal("expected getBlockTemplate to reject while BdagPool is syncing")
	}
	if got := err.Error(); got != "Block Template Request Rejected : Node is Syncing" {
		t.Fatalf("getBlockTemplate error = %q, want node syncing rejection", got)
	}
}

func TestGetBlockTemplateRejectsSubmitOverrideWhileP2PSyncing(t *testing.T) {
	api := NewPublicMinerAPI(&Miner{
		cfg:    &config.Config{Miner: true, SubmitNoSynced: true},
		p2pSer: testP2PService{current: false},
	})

	_, err := api.GetBlockTemplate(context.Background(), nil, 0, nil)
	if err == nil {
		t.Fatal("expected getBlockTemplate to reject while p2p is syncing")
	}
	if got := err.Error(); got != "Block Template Request Rejected : Node is Syncing" {
		t.Fatalf("getBlockTemplate error = %q, want node syncing rejection", got)
	}
}

func TestGBTMiningReturnsWhenHandlerDoesNotAcceptRequest(t *testing.T) {
	m := &Miner{
		cfg:     &config.Config{Miner: true},
		msgChan: make(chan any),
		quit:    make(chan struct{}),
		p2pSer:  testP2PService{current: true},
	}
	if err := m.Service.Start(); err != nil {
		t.Fatalf("Service.Start() error = %v", err)
	}
	defer m.Service.Stop()

	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()
	start := time.Now()
	err := m.GBTMining(ctx, &corejson.TemplateRequest{}, make(chan *gbtResponse, 1))
	if err == nil {
		t.Fatal("GBTMining() unexpectedly succeeded while miner handler was not accepting requests")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("GBTMining() error = %v, want context deadline exceeded", err)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("GBTMining() took %s, want bounded by request context", elapsed)
	}
}

func TestSubmitBlockHeaderV2ReturnsStructuredOutcome(t *testing.T) {
	api := NewPublicMinerAPI(&Miner{
		cfg: &config.Config{Miner: true},
	})
	headerHex := testHeaderHex(t)

	res, err := api.SubmitBlockHeaderV2(context.Background(), headerHex, nil)
	if err != nil {
		t.Fatalf("SubmitBlockHeaderV2 returned RPC error for structured outcome: %v", err)
	}
	outcome, ok := res.(corejson.SubmitBlockResult)
	if !ok {
		t.Fatalf("SubmitBlockHeaderV2 result type = %T, want SubmitBlockResult", res)
	}
	if outcome.Accepted {
		t.Fatal("expected rejected outcome")
	}
	if outcome.Code != submitOutcomeMinerDisabled {
		t.Fatalf("Code = %q, want %q", outcome.Code, submitOutcomeMinerDisabled)
	}
	if outcome.Final {
		t.Fatal("miner-disabled outcome must remain nonfinal")
	}
	if !outcome.Retryable {
		t.Fatal("miner-disabled outcome must be retryable")
	}
}

func TestSubmitBlockHeaderLegacyPreservesError(t *testing.T) {
	api := NewPublicMinerAPI(&Miner{
		cfg: &config.Config{Miner: true},
	})
	headerHex := testHeaderHex(t)

	res, err := api.SubmitBlockHeader(context.Background(), headerHex, nil)
	if err == nil {
		t.Fatal("expected legacy SubmitBlockHeader to preserve error response")
	}
	if res != nil {
		t.Fatalf("legacy SubmitBlockHeader result = %#v, want nil on error", res)
	}
}

func TestSubmitRuleErrorClassification(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want string
	}{
		{name: "duplicate", err: fmt.Errorf("duplicate block already known"), want: submitOutcomeAlreadyKnown},
		{name: "overdue", err: fmt.Errorf("main chain tip is overdue"), want: submitOutcomeTipOverdue},
		{name: "expired", err: fmt.Errorf("submitted block template expired"), want: submitOutcomeExpired},
		{name: "p2p not current", err: fmt.Errorf("p2p peer state is not current"), want: submitOutcomeP2PNotCurrent},
		{name: "generic not current", err: fmt.Errorf("chain is not current"), want: submitOutcomeNodeSyncing},
		{name: "parent", err: fmt.Errorf("Parent(abc) is overdue"), want: submitOutcomeTipOverdue},
		{name: "pow", err: fmt.Errorf("high hash pow rejected"), want: submitOutcomeInvalidPOW},
		{name: "txroot", err: fmt.Errorf("tx root mismatch"), want: submitOutcomeTxRootMismatch},
		{name: "missing trie", err: fmt.Errorf("missing trie node 0xabc"), want: submitOutcomeMissingTrie},
		{name: "nonce too low", err: fmt.Errorf("nonce too low: address 0xabc"), want: submitOutcomeNonceTooLow},
		{name: "processing queue", err: fmt.Errorf("Already exists in the queue:0xabc"), want: submitOutcomeQueued},
		{name: "already queued", err: fmt.Errorf("block is already queued"), want: submitOutcomeQueued},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := classifySubmitError(tt.err); got != tt.want {
				t.Fatalf("classifySubmitError() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestSubmitOutcomeProperties(t *testing.T) {
	tests := []struct {
		code      string
		accepted  bool
		final     bool
		retryable bool
	}{
		{code: submitOutcomeAccepted, accepted: true, final: true},
		{code: submitOutcomeAlreadyAccepted, accepted: true, final: true},
		{code: submitOutcomeAlreadyRejected, final: true},
		{code: submitOutcomeQueued, retryable: true},
		{code: submitOutcomeRetryable, retryable: true},
		{code: submitOutcomeDuplicate, retryable: true},
		{code: submitOutcomeAlreadyKnown, retryable: true},
		{code: submitOutcomeStaleParent, final: true},
		{code: submitOutcomeTipOverdue, final: true},
		{code: submitOutcomeExpired, final: true},
		{code: submitOutcomeTemplateSeqUnknown, retryable: true},
		{code: submitOutcomeInvalidPOW, final: true},
		{code: submitOutcomeTxRootMismatch, final: true},
		{code: submitOutcomeMissingTrie, retryable: true},
		{code: submitOutcomeNonceTooLow, retryable: true},
		{code: submitOutcomeGovernanceRejected, final: true},
		{code: submitOutcomeNodeSyncing, retryable: true},
		{code: submitOutcomeP2PNotCurrent, retryable: true},
		{code: submitOutcomeMinerDisabled, retryable: true},
		{code: submitOutcomeInternalError, retryable: true},
	}

	for _, test := range tests {
		t.Run(test.code, func(t *testing.T) {
			accepted, final, retryable := submitOutcomeProperties(test.code)
			if accepted != test.accepted || final != test.final || retryable != test.retryable {
				t.Fatalf("submitOutcomeProperties(%q) = (%t, %t, %t), want (%t, %t, %t)",
					test.code, accepted, final, retryable, test.accepted, test.final, test.retryable)
			}
			if final && retryable {
				t.Fatal("an outcome cannot be both final and retryable")
			}
			if accepted && !final {
				t.Fatal("an accepted outcome must be final")
			}
		})
	}
}

func TestSubmitOutcomeForBlockStatus(t *testing.T) {
	m := &Miner{}
	cause := errors.New("process result")
	const blockHash = "0xabc"
	tests := []struct {
		name      string
		status    blockchain.BlockSubmissionStatus
		code      string
		accepted  bool
		final     bool
		retryable bool
	}{
		{name: "accepted", status: blockchain.BlockSubmissionAccepted, code: submitOutcomeAlreadyAccepted, accepted: true, final: true},
		{name: "rejected", status: blockchain.BlockSubmissionRejected, code: submitOutcomeAlreadyRejected, final: true},
		{name: "queued", status: blockchain.BlockSubmissionQueued, code: submitOutcomeQueued, retryable: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result, err, handled := m.submitOutcomeForBlockStatus(test.status, blockHash, cause)
			if !handled {
				t.Fatal("known block status was not handled")
			}
			if err == nil || !isSubmitOutcomeError(err) || !errors.Is(err, cause) {
				t.Fatalf("error = %v, want wrapped submit outcome error", err)
			}
			if result.Code != test.code || result.Accepted != test.accepted ||
				result.Final != test.final || result.Retryable != test.retryable {
				t.Fatalf("result = %#v, want code=%q accepted=%t final=%t retryable=%t",
					result, test.code, test.accepted, test.final, test.retryable)
			}
			if result.BlockHash != blockHash {
				t.Fatalf("BlockHash = %q, want %q", result.BlockHash, blockHash)
			}
		})
	}

	result, err, handled := m.submitOutcomeForBlockStatus(blockchain.BlockSubmissionUnknown, blockHash, cause)
	if handled || err != nil || result != (corejson.SubmitBlockResult{}) {
		t.Fatalf("unknown status = (%#v, %v, %t), want zero result, nil error, false", result, err, handled)
	}
}

func TestDecodeSubmitBlockHeaderHex(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want []byte
	}{
		{name: "empty", src: "", want: nil},
		{name: "single nibble", src: "f", want: []byte{0x0f}},
		{name: "odd length", src: "abc", want: []byte{0x0a, 0xbc}},
		{name: "even length", src: "0abc", want: []byte{0x0a, 0xbc}},
		{name: "upper case", src: "ABCDEF", want: []byte{0xab, 0xcd, 0xef}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dst := make([]byte, decodedSubmitBlockHeaderLen(tt.src))
			n, err := decodeSubmitBlockHeaderHex(dst, tt.src)
			if err != nil {
				t.Fatalf("decodeSubmitBlockHeaderHex() error = %v", err)
			}
			if n != len(tt.want) {
				t.Fatalf("decoded length = %d, want %d", n, len(tt.want))
			}
			if !bytes.Equal(dst, tt.want) {
				t.Fatalf("decoded bytes = %x, want %x", dst, tt.want)
			}
		})
	}
}

func TestDecodeSubmitBlockHeaderHexRejectsInvalid(t *testing.T) {
	for _, src := range []string{"x", "0x", "1z"} {
		t.Run(src, func(t *testing.T) {
			dst := make([]byte, decodedSubmitBlockHeaderLen(src))
			if _, err := decodeSubmitBlockHeaderHex(dst, src); err == nil {
				t.Fatal("expected invalid hex error")
			}
		})
	}
}

func TestSubmitBlockHeaderPreservesLegacyDecodeAndSizeErrorPrecedence(t *testing.T) {
	api := NewPublicMinerAPI(&Miner{cfg: &config.Config{Miner: true}})
	ctx := context.Background()
	_, oddErr := api.SubmitBlockHeader(ctx, "x", nil)
	_, normalizedErr := api.SubmitBlockHeader(ctx, "0x", nil)
	if oddErr == nil || normalizedErr == nil || oddErr.Error() != normalizedErr.Error() {
		t.Fatalf("odd invalid errors = (%v, %v), want identical normalized RpcDecodeHexError", oddErr, normalizedErr)
	}

	wellFormedOversized := strings.Repeat("00", coretypes.BaseBlockHeaderPayload+1)
	_, sizeErr := api.SubmitBlockHeader(ctx, wellFormedOversized, nil)
	if sizeErr == nil || !strings.Contains(sizeErr.Error(), "block header size") || !strings.Contains(sizeErr.Error(), "exceeds") {
		t.Fatalf("well-formed oversized error = %v, want bounded size error", sizeErr)
	}
	malformedOversized := strings.Repeat("00", coretypes.BaseBlockHeaderPayload+1) + "x"
	_, malformedErr := api.SubmitBlockHeader(ctx, malformedOversized, nil)
	if malformedErr == nil || !strings.Contains(malformedErr.Error(), "Argument must be hexadecimal string") || strings.Contains(malformedErr.Error(), "block header size") {
		t.Fatalf("malformed oversized error = %v, want legacy hex error precedence", malformedErr)
	}
	oddOversized := strings.Repeat("a", 2*coretypes.BaseBlockHeaderPayload+1)
	_, oddSizeErr := api.SubmitBlockHeader(ctx, oddOversized, nil)
	if oddSizeErr == nil || !strings.Contains(oddSizeErr.Error(), "block header size") {
		t.Fatalf("odd well-formed oversized error = %v, want bounded size error", oddSizeErr)
	}
	oddMalformed := oddOversized[:len(oddOversized)-1] + "x"
	_, oddMalformedErr := api.SubmitBlockHeader(ctx, oddMalformed, nil)
	_, evenMalformedErr := api.SubmitBlockHeader(ctx, "0"+oddMalformed, nil)
	if oddMalformedErr == nil || evenMalformedErr == nil || oddMalformedErr.Error() != evenMalformedErr.Error() {
		t.Fatalf("odd malformed oversized errors = (%v, %v), want identical normalized legacy errors", oddMalformedErr, evenMalformedErr)
	}
}

func BenchmarkSubmitBlockHeaderOddOversizedSizeError(b *testing.B) {
	api := NewPublicMinerAPI(&Miner{cfg: &config.Config{Miner: true}})
	for _, multiplier := range []int{1, 8, 64} {
		input := strings.Repeat("a", multiplier*(2*coretypes.BaseBlockHeaderPayload)+1)
		b.Run(fmt.Sprintf("x%d", multiplier), func(b *testing.B) {
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, err := api.SubmitBlockHeader(context.Background(), input, nil); err == nil {
					b.Fatal("odd oversized input unexpectedly accepted")
				}
			}
		})
	}
}

func testHeaderHex(t *testing.T) string {
	t.Helper()
	header := coretypes.BlockHeader{
		Pow: pow.GetInstance(pow.BDAGXKECCAKV1, 0, 0, nil),
	}
	var buf bytes.Buffer
	if err := header.Serialize(&buf); err != nil {
		t.Fatalf("serialize header: %v", err)
	}
	return hex.EncodeToString(buf.Bytes())
}

func testBlockTemplate(timestamp time.Time) *coretypes.BlockTemplate {
	parent := common.HexToHash("0x1234")
	return &coretypes.BlockTemplate{
		Block: &coretypes.Block{
			Header: coretypes.BlockHeader{
				ParentRoot:      parent,
				Timestamp:       timestamp,
				CoinbaseAddress: common.HexToAddress("0x1"),
			},
		},
		Height:          12,
		ValidPayAddress: true,
	}
}

func TestMining(t *testing.T) {
	rpcC := &RpcClient{
		Server: "http://47.244.17.119:2234",
		User:   "test",
		Pwd:    "test",
	}
	for {
		b := rpcC.RpcResult("getBlockTemplate", []any{
			[]string{"coinbasetxn", "coinbasevalue"}, 8,
		})
		type GbtTemplate struct {
			Result struct {
				WorkData         string `json:"workdata"`
				Height           int    `json:"height"`
				PowDiffReference struct {
					Target string `json:"target"`
				} `json:"pow_diff_reference"`
			} `json:"result"`
		}
		type Submit struct {
			Result string `json:"result"`
		}
		var gbtR GbtTemplate
		err := json.Unmarshal(b, &gbtR)
		if err != nil {
			fmt.Println(err)
			return
		}
		fmt.Println("New work height and target ", gbtR.Result.Height, gbtR.Result.PowDiffReference.Target)
		workData, err := hex.DecodeString(gbtR.Result.WorkData)
		if err != nil {
			fmt.Println(err)
			return
		}
		b1, _ := hex.DecodeString(gbtR.Result.PowDiffReference.Target)
		var r [32]byte
		copy(r[:], Reverse(b1)[:])
		r1 := common.Hash(r)
		targetDiff := pow.HashToBig(&r1)
		start := time.Now().UnixNano()
		i := uint64(0)
		for ; i < ^uint64(0); i++ {
			nonce := make([]byte, 8)
			binary.LittleEndian.PutUint64(nonce, i)
			copy(workData[109:117], nonce)
			h := hash.HashbdagXKeccakV1(workData[:117])
			if pow.HashToBig(&h).Cmp(targetDiff) <= 0 {
				fmt.Println(i, hex.EncodeToString(nonce), h)
				// find hash
				b = rpcC.RpcResult("submitBlock", []any{
					hex.EncodeToString(workData),
				})
				var subR Submit
				err = json.Unmarshal(b, &subR)
				if err != nil {
					fmt.Println(err)
					continue
				}
				break
			}
		}
		end := time.Now().UnixNano()
		gap := end - start
		hashrate := float64(i) / float64(gap)
		fmt.Printf("Hashrate: %.6f hash/s\n", 1/hashrate/1000)
	}
}

// Reverse reverses a byte array.
func Reverse(src []byte) []byte {
	dst := make([]byte, len(src))
	for i := len(src); i > 0; i-- {
		dst[len(src)-i] = src[i-1]
	}
	return dst
}

func TestBuildRemoteGBTResultDoesNotAdvanceTemplateSeq(t *testing.T) {
	m := newRemoteGBTResultTestMiner()
	m.templateSeq.Store(42)

	first := m.buildRemoteGBTResult()
	second := m.buildRemoteGBTResult()
	if first == nil || second == nil {
		t.Fatal("expected remote template result")
	}
	if first.Seq != 42 || second.Seq != 42 {
		t.Fatalf("snapshot sequence advanced: first=%d second=%d", first.Seq, second.Seq)
	}
	if got := m.templateSeq.Load(); got != 42 {
		t.Fatalf("templateSeq = %d, want 42", got)
	}
}

func TestNotifyBlockTemplateAdvancesTemplateSeqOnce(t *testing.T) {
	m := newRemoteGBTResultTestMiner()
	m.templateSeq.Store(7)

	ch := make(chan *corejson.RemoteGBTResult, 2)
	sub := m.TemplateEvent().Subscribe(ch)
	defer sub.Unsubscribe()

	m.notifyBlockTemplate()
	first := receiveRemoteGBTResult(t, ch)
	if first.Seq != 8 {
		t.Fatalf("first notify seq = %d, want 8", first.Seq)
	}
	if snapshot := m.buildRemoteGBTResult(); snapshot.Seq != 8 {
		t.Fatalf("snapshot seq after notify = %d, want 8", snapshot.Seq)
	}

	m.notifyBlockTemplate()
	second := receiveRemoteGBTResult(t, ch)
	if second.Seq != 9 {
		t.Fatalf("second notify seq = %d, want 9", second.Seq)
	}
	if got := m.templateSeq.Load(); got != 9 {
		t.Fatalf("templateSeq = %d, want 9", got)
	}
}

func TestQueuedTemplatePayloadFromInvalidatedGenerationIsRefusedAfterReplacement(t *testing.T) {
	m := newRemoteGBTResultTestMiner()
	queuedA := m.buildRemoteGBTResult()
	if queuedA == nil || queuedA.Seq != 0 {
		t.Fatalf("queued generation A = %#v, want initial sequence zero", queuedA)
	}

	m.invalidateGBTWorkerGeneration(errors.New("required generation A build failed"))
	if m.templatePayloadAdmitted(queuedA.Seq) {
		t.Fatal("invalidated generation A remained admitted while failure was active")
	}

	// A valid replacement clears the current failure, but the monotonic
	// invalidation watermark must continue refusing the already queued A.
	m.clearGBTGenerationFailure(m.template, &coretypes.BlockTemplate{Block: m.template.Block})
	if m.gbtGenerationError() != nil {
		t.Fatal("replacement did not clear the current generation failure")
	}
	if m.templatePayloadAdmitted(queuedA.Seq) {
		t.Fatal("queued generation A was admitted after replacement cleared failure")
	}

	m.templateSeq.Store(1)
	if !m.templatePayloadAdmitted(1) {
		t.Fatal("replacement generation B was incorrectly rejected")
	}
}

func TestBlockTemplateSubscriptionRejectsQueuedInvalidatedGeneration(t *testing.T) {
	fixtureDeadline := time.Now().Add(10 * time.Second)
	if deadline, ok := t.Deadline(); ok && deadline.Before(fixtureDeadline) {
		fixtureDeadline = deadline.Add(-250 * time.Millisecond)
	}
	waitCtx, cancelWait := context.WithDeadline(context.Background(), fixtureDeadline)
	defer cancelWait()
	waitSignal := func(ch <-chan struct{}, what string) {
		t.Helper()
		select {
		case <-ch:
		case <-waitCtx.Done():
			t.Fatalf("waiting for %s: %v", what, waitCtx.Err())
		}
	}
	waitReply := func(ch <-chan *gbtResponse, what string) *gbtResponse {
		t.Helper()
		select {
		case reply := <-ch:
			return reply
		case <-waitCtx.Done():
			t.Fatalf("waiting for %s: %v", what, waitCtx.Err())
			return nil
		}
	}
	m := newRemoteGBTResultTestMiner()
	m.cfg = &config.Config{Miner: true}
	m.policy = &mining.Policy{PeerID: "test-peer"}
	m.timeSource = fixedMedianTimeSource{now: time.Unix(1_770_000_010, 0)}
	m.minTimestamp = time.Unix(1_770_000_000, 0)
	m.minimumMedianTimeFn = func() time.Time { return time.Unix(1_770_000_000, 0) }
	m.txpool = &legacypool.LegacyPool{}
	m.msgChan = make(chan any)
	m.templateReqChan = make(chan struct{}, 1)
	m.quit = make(chan struct{})
	sendMsg := func(msg any) {
		t.Helper()
		select {
		case m.msgChan <- msg:
		case <-waitCtx.Done():
			t.Fatalf("sending miner message %T: %v", msg, waitCtx.Err())
		}
	}
	worker := NewGBTWorker(m)
	m.worker = worker
	if _, err := worker.getResult(nil); err != nil {
		t.Fatalf("seed template: %v", err)
	}
	oldTemplate := m.template
	oldParent := common.HexToHash("0xa1")
	oldTemplate.Block.Header.ParentRoot = oldParent
	m.templateSeq.Store(0)

	admissionEntered := make(chan *corejson.RemoteGBTResult, 1)
	releaseA := make(chan struct{})
	var releaseOnce sync.Once
	releaseHeldA := func() { releaseOnce.Do(func() { close(releaseA) }) }
	var quitOnce sync.Once
	handlerStarted := false
	done := make(chan struct{})
	var cleanupOnce sync.Once
	cleanup := func() {
		cleanupOnce.Do(func() {
			releaseHeldA()
			if !handlerStarted {
				return
			}
			quitOnce.Do(func() { close(m.quit) })
			select {
			case <-done:
			case <-time.After(2 * time.Second):
				t.Error("miner handler did not stop during cleanup")
			}
		})
	}
	t.Cleanup(cleanup) // fallback for construction failures before the defer below
	m.templateNotifyAdmissionHook = func(bt *corejson.RemoteGBTResult) {
		if bt == nil || bt.Seq != 0 {
			return
		}
		select {
		case admissionEntered <- bt:
		default:
		}
		<-releaseA
	}

	server, err := rpc.NewRPCServer(&config.Config{
		RPCUser:              "user",
		RPCPass:              "pass",
		DisableTLS:           true,
		RPCListeners:         []string{"127.0.0.1:0"},
		RPCMaxClients:        4,
		RPCMaxWebsockets:     4,
		RPCMaxConcurrent:     4,
		RPCMaxConcurrentReqs: 4,
	}, nil)
	if err != nil {
		t.Fatalf("NewRPCServer: %v", err)
	}
	if err := server.RegisterService("Blockdag", NewPublicMinerAPI(m)); err != nil {
		t.Fatalf("RegisterService: %v", err)
	}
	if err := server.Start(); err != nil {
		t.Fatalf("RPC server start: %v", err)
	}
	defer server.Stop()
	httpServer := httptest.NewServer(server.Handler())
	defer httpServer.Close()
	requestHeader := make(http.Header)
	requestHeader.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte("user:pass")))
	conn, _, err := websocket.DefaultDialer.Dial(strings.Replace(httpServer.URL, "http://", "ws://", 1)+"/ws", requestHeader)
	if err != nil {
		t.Fatalf("websocket dial: %v", err)
	}
	defer conn.Close()
	defer cleanup() // unblock A and join the handler before RPC/socket teardown
	if err := conn.SetWriteDeadline(fixtureDeadline); err != nil {
		t.Fatalf("set subscription write deadline: %v", err)
	}
	if err := conn.WriteJSON(map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "Blockdag_subscribe", "params": []any{"blockTemplate", nil},
	}); err != nil {
		t.Fatalf("subscribe write: %v", err)
	}
	if err := conn.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatalf("set subscription read deadline: %v", err)
	}
	var subscriptionResponse struct {
		Result string          `json:"result"`
		Error  json.RawMessage `json:"error"`
	}
	if err := conn.ReadJSON(&subscriptionResponse); err != nil {
		t.Fatalf("subscribe response: %v", err)
	}
	if subscriptionResponse.Result == "" || len(subscriptionResponse.Error) != 0 {
		t.Fatalf("subscribe response = %+v, want active subscription", subscriptionResponse)
	}
	var queuedA *corejson.RemoteGBTResult
	select {
	case queuedA = <-admissionEntered:
	case <-waitCtx.Done():
		t.Fatalf("waiting for held generation A: %v", waitCtx.Err())
	}
	if queuedA.PreviousHash != oldParent.String() || queuedA.Seq != 0 {
		t.Fatalf("held generation A = %#v, want seq 0 parent %s", queuedA, oldParent)
	}

	// Drive the required failure through the real handler path while A is held
	// immediately before final RPC admission.
	m.bdagPool = nil
	m.enqueueTemplateBuildRequest(templateBuildRequest{
		kind:              templateBuildBlockChain,
		force:             true,
		reason:            "required parent rebuild",
		invalidationCause: templateInvalidationParentChanged,
	})
	waitSignal(m.templateReqChan, "queued rebuild request")
	m.wg.Add(1)
	handlerStarted = true
	go func() {
		m.handler()
		close(done)
	}()
	failedReply := make(chan *gbtResponse, 1)
	sendMsg(&GBTMiningMsg{request: &corejson.TemplateRequest{}, reply: failedReply})
	failed := waitReply(failedReply, "failed rebuild reply")
	if failed == nil || failed.err == nil || failed.result != nil {
		t.Fatalf("required rebuild response = %#v, want failure without old generation", failed)
	}
	if m.template != oldTemplate || m.gbtGenerationError() == nil {
		t.Fatal("failed production rebuild did not retain old template and generation hold")
	}
	if _, err := NewPublicMinerAPI(m).GetBlockTemplate(waitCtx, nil, 0, nil); err == nil {
		t.Fatal("GetBlockTemplate admitted retained generation during required failure")
	}

	parent := common.HexToHash("0xb2")
	replacement := *oldTemplate
	replacement.Block = &coretypes.Block{Header: oldTemplate.Block.Header}
	replacement.Block.Header.ParentRoot = parent
	replacement.Block.Header.CoinbaseAddress = m.coinbaseAddress
	replacement.Fees = nil
	resetter := &testPendingTemplateResetter{}
	replacementBuilt := make(chan struct{}, 1)
	m.bdagPool = resetter
	m.buildTemplate = func(common.Address, bool) (*coretypes.BlockTemplate, error) {
		select {
		case replacementBuilt <- struct{}{}:
		case <-waitCtx.Done():
			return nil, waitCtx.Err()
		}
		return &replacement, nil
	}
	sendMsg(&BlockChainChangeMsg{})
	waitSignal(replacementBuilt, "replacement builder")
	replacementReply := make(chan *gbtResponse, 1)
	sendMsg(&GBTMiningMsg{request: &corejson.TemplateRequest{}, reply: replacementReply})
	replacementResult := waitReply(replacementReply, "replacement installation reply")
	if replacementResult == nil || replacementResult.err != nil || replacementResult.result == nil {
		t.Fatalf("replacement GBT response = %#v, want successful installed generation", replacementResult)
	}
	if m.template != &replacement || m.gbtGenerationError() != nil {
		t.Fatalf("replacement was not installed before releasing A: template=%p want=%p generationErr=%v", m.template, &replacement, m.gbtGenerationError())
	}
	releaseHeldA()

	if err := conn.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatalf("set replacement read deadline: %v", err)
	}
	var notification struct {
		Method string `json:"method"`
		Params struct {
			Result corejson.RemoteGBTResult `json:"result"`
		} `json:"params"`
	}
	if err := conn.ReadJSON(&notification); err != nil {
		t.Fatalf("read replacement notification: %v", err)
	}
	if notification.Method != "Blockdag_subscription" {
		t.Fatalf("notification method = %q, want Blockdag_subscription", notification.Method)
	}
	result := notification.Params.Result
	if result.Seq != 1 || result.PreviousHash != parent.String() {
		t.Fatalf("first eligible notification = %+v, want replacement seq 1 parent %s", result, parent)
	}
	if m.template != &replacement || m.gbtGenerationError() != nil {
		t.Fatal("production replacement did not install and clear generation hold")
	}
	if m.templateSeq.Load() != 1 || resetter.calls != 1 {
		t.Fatalf("replacement lifecycle = seq %d reset calls %d, want 1/1", m.templateSeq.Load(), resetter.calls)
	}

	quitOnce.Do(func() { close(m.quit) })
	waitSignal(done, "miner handler completion")
}

func TestNotifyBlockTemplateSuppressesWhileBdagPoolIsSyncing(t *testing.T) {
	m := newRemoteGBTResultTestMiner()
	m.bdagPool = &testSyncingPendingTemplateResetter{syncing: true}
	m.templateSeq.Store(7)

	ch := make(chan *corejson.RemoteGBTResult, 1)
	sub := m.TemplateEvent().Subscribe(ch)
	defer sub.Unsubscribe()

	m.notifyBlockTemplate()
	select {
	case bt := <-ch:
		t.Fatalf("unexpected template broadcast while syncing: %#v", bt)
	case <-time.After(50 * time.Millisecond):
	}
	if got := m.templateSeq.Load(); got != 7 {
		t.Fatalf("templateSeq = %d, want 7 after suppressed broadcast", got)
	}
}

func TestBuildRemoteGBTResultRejectsZeroCoinbase(t *testing.T) {
	m := newRemoteGBTResultTestMiner()
	m.template.Block.Header.CoinbaseAddress = common.Address{}

	if got := m.buildRemoteGBTResult(); got != nil {
		t.Fatalf("buildRemoteGBTResult() returned zero-coinbase payload: %#v", got)
	}
}

func TestNotifyBlockTemplateSuppressesZeroCoinbase(t *testing.T) {
	m := newRemoteGBTResultTestMiner()
	m.template.Block.Header.CoinbaseAddress = common.Address{}
	m.templateSeq.Store(7)

	ch := make(chan *corejson.RemoteGBTResult, 1)
	sub := m.TemplateEvent().Subscribe(ch)
	defer sub.Unsubscribe()

	m.notifyBlockTemplate()
	select {
	case bt := <-ch:
		t.Fatalf("unexpected zero-coinbase template broadcast: %#v", bt)
	case <-time.After(50 * time.Millisecond):
	}
	if got := m.templateSeq.Load(); got != 7 {
		t.Fatalf("templateSeq = %d, want 7 after suppressed broadcast", got)
	}
}

func TestGBTWorkerGetResultRejectsZeroCoinbase(t *testing.T) {
	m := newRemoteGBTResultTestMiner()
	m.template.Block.Header.CoinbaseAddress = common.Address{}
	worker := &GBTWorker{miner: m}

	if result, err := worker.getResult(nil); err == nil {
		t.Fatalf("getResult() returned zero-coinbase template without error: %#v", result)
	}
}

func TestValidateBlockTemplateCoinbaseRejectsMismatch(t *testing.T) {
	m := newRemoteGBTResultTestMiner()
	expected := common.HexToAddress("0x1111111111111111111111111111111111111111")

	if err := validateBlockTemplateCoinbase(m.template, &expected, "test"); err == nil {
		t.Fatal("validateBlockTemplateCoinbase() accepted a mismatched coinbase")
	}
}

func receiveRemoteGBTResult(t *testing.T, ch <-chan *corejson.RemoteGBTResult) *corejson.RemoteGBTResult {
	t.Helper()
	select {
	case bt := <-ch:
		if bt == nil {
			t.Fatal("received nil remote template result")
		}
		return bt
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for remote template result")
		return nil
	}
}

func newRemoteGBTResultTestMiner() *Miner {
	coinbase := common.HexToAddress("0x05518E03e148C56e426ff9e1CBdB962B4FC5250A")
	return &Miner{
		coinbaseAddress: coinbase,
		template: &coretypes.BlockTemplate{
			Block: &coretypes.Block{
				Header: coretypes.BlockHeader{
					Version:         1,
					Pow:             pow.GetInstance(pow.BLAKE2BD, 0, 0, nil),
					Timestamp:       time.Unix(1_770_000_000, 0),
					Difficulty:      0x1d00ffff,
					BlockHeight:     12345,
					CoinbaseAddress: coinbase,
					Reward:          10,
				},
			},
			Height:    12345,
			BlockFees: 99,
			Fees:      []int64{0}, // newBlockTemplate's zero-transaction offset entry
		},
	}
}
