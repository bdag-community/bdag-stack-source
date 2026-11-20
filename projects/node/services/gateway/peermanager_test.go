package gateway

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	l "github.com/BlockdagNetworkLabs/bdag/log"
)

func TestPeerManager(t *testing.T) {
	// 1. Mock Local DAG and Peers
	peer1 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Mock peer 1: Synced, Block 100
		handlePeerRequest(w, r, true, "0x64") // 100
	}))
	defer peer1.Close()

	peer2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Mock peer 2: Synced, Block 200 (Better)
		time.Sleep(10 * time.Millisecond)     // Slightly slower
		handlePeerRequest(w, r, true, "0xc8") // 200
	}))
	defer peer2.Close()

	localDAG := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Return peer list
		resp := struct {
			Result []struct {
				Address string `json:"address"`
			} `json:"result"`
			Error interface{} `json:"error"`
		}{
			Result: []struct {
				Address string `json:"address"`
			}{
				{Address: peer1.URL[7:]}, // strip http://
				{Address: peer2.URL[7:]},
			},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer localDAG.Close()

	// 2. Setup Gateway
	cfg := Config{
		ListenAddr:  ":0",
		PeerDagPort: 1234, // Ignored by mock? No, buildPeerURL uses it.
		// Wait, our mock peers are full URLs with random ports.
		// peermanager fetches hosts. probePeer calls peerDagIsCurrent which builds URL using cfg.PeerDagPort.
		// This makes testing tricky if we can't control the port usage.
		// Gateway assumes fixed ports for peers.
		// We need to trick it or use the mock's port if possible.
		// But Gateway enforces port from config.
		// Workaround: We can't easily test the *probe* network call unless we start servers on specific ports or mock forwardCtx.
		// However, PeerManager uses g.forwardCtx.
		// Does Gateway allow overriding transport? Yes.
		// But it builds URL using `fmt.Sprintf("http://%s:%d", host, port)`.
	}

	// Real integration test is hard due to port constraint.
	// But we can test the sorting logic if we manually inject peers.

	g := &Gateway{
		cfg:    cfg,
		logger: l.New(l.Ctx{"module": "test"}),
	}
	pm := NewPeerManager(g)

	// Inject peers manually
	pm.mu.Lock()
	pm.peers["host1"] = &Peer{
		Host:        "host1",
		Latency:     100 * time.Millisecond,
		BlockHeight: 100,
		IsActive:    true,
		IsSynced:    true,
	}
	pm.peers["host2"] = &Peer{
		Host:        "host2",
		Latency:     50 * time.Millisecond,
		BlockHeight: 200, // Higher height, should win despite slightly better latency?
		IsActive:    true,
		IsSynced:    true,
	}
	pm.peers["host3"] = &Peer{
		Host:        "host3",
		Latency:     10 * time.Millisecond,
		BlockHeight: 100, // Same height as host1, but faster.
		IsActive:    true,
		IsSynced:    true,
	}
	pm.mu.Unlock()

	// 3. Test GetBestPeer
	best, ok := pm.GetBestPeer("")
	if !ok {
		t.Fatal("Expected best peer")
	}
	if best != "host2" {
		t.Errorf("Expected host2 (highest block), got %s", best)
	}

	// 4. Test ReportFailure
	pm.ReportFailure("host2")

	best, ok = pm.GetBestPeer("")
	if !ok {
		t.Fatal("Expected best peer")
	}
	// host2 is still active after one failure, but degraded by FailureCount.
	// host1 (100ms, #100) vs host3 (10ms, #100). host3 should win.
	if best != "host3" {
		t.Errorf("Expected host3 (fastest with height 100), got %s", best)
	}
}

func TestPeerManagerDelaysInactivePeerProbe(t *testing.T) {
	pm := &PeerManager{
		peers: make(map[string]*Peer),
		g: &Gateway{
			logger: l.New(l.Ctx{"module": "test"}),
		},
	}
	pm.peers["host1"] = &Peer{
		Host:         "host1",
		IsActive:     false,
		FailureCount: 3,
		NextProbe:    time.Now().Add(time.Minute),
	}

	if pm.shouldProbePeer("host1", time.Now()) {
		t.Fatal("expected peer probe to stay delayed during cooldown")
	}
}

func TestPeerProbeBackoffCaps(t *testing.T) {
	if got := peerProbeBackoff(3); got != peerProbeBackoffBase {
		t.Fatalf("backoff for 3 failures = %s, want %s", got, peerProbeBackoffBase)
	}
	if got := peerProbeBackoff(20); got != peerProbeBackoffMax {
		t.Fatalf("backoff should cap at %s, got %s", peerProbeBackoffMax, got)
	}
}

func handlePeerRequest(w http.ResponseWriter, r *http.Request, isCurrent bool, blockHex string) {
	// ... helper
}

func TestPeerAffinity(t *testing.T) {
	pm := &PeerManager{
		peers:      make(map[string]*Peer),
		affinity:   make(map[string]string),
		affinityTS: make(map[string]time.Time),
		g: &Gateway{
			logger: l.New(l.Ctx{"module": "test"}),
		},
	}
	// Link to gateway for logger access
	pm.g = &Gateway{
		logger: l.New(l.Ctx{"module": "test"}),
	}

	// Setup peers
	pm.peers["peerA"] = &Peer{
		Host:        "peerA",
		IsActive:    true,
		IsSynced:    true,
		BlockHeight: 100,
		LastChecked: time.Now(),
	}
	// Peer B is "better" (higher block)
	pm.peers["peerB"] = &Peer{
		Host:        "peerB",
		IsActive:    true,
		IsSynced:    true,
		BlockHeight: 101, // Higher height
		LastChecked: time.Now(),
	}

	key := "user123"

	// 1. First call should pick Peer B (Best)
	best, ok := pm.GetBestPeer(key)
	if !ok || best != "peerB" {
		t.Fatalf("expected peerB as initial best, got %s", best)
	}

	// 2. Degrade Peer B (still healthy but lower height than A - simulating latency/jitter logic if sort changed)
	// Actually, let's just make Peer A significantly better now to prove affinity holds B.
	pm.mu.Lock()
	pm.peers["peerA"].BlockHeight = 200 // A is now MUCH better
	pm.mu.Unlock()

	// 3. Second call with SAME KEY should now return Peer A because B is lagging by >10 blocks (101 vs 200)
	// Lag tolerance is 10. (200 - 101 = 99 > 10).
	best, ok = pm.GetBestPeer(key)
	if !ok || best != "peerA" {
		t.Errorf("expected peerA due to lag tolerance (B is too far behind), got %s", best)
	}

	// 4. Kill Peer B
	pm.mu.Lock()
	pm.peers["peerB"].IsActive = false
	pm.mu.Unlock()

	// 5. Third call should failover to Peer A
	best, ok = pm.GetBestPeer(key)
	if !ok || best != "peerA" {
		t.Errorf("expected failover to peerA, got %s", best)
	}
}

func TestLoadDistribution(t *testing.T) {
	pm := &PeerManager{
		peers:      make(map[string]*Peer),
		affinity:   make(map[string]string),
		affinityTS: make(map[string]time.Time),
		g: &Gateway{
			logger: l.New(l.Ctx{"module": "test"}),
		},
	}
	// Link to gateway for logger access
	pm.g = &Gateway{
		logger: l.New(l.Ctx{"module": "test"}),
	}

	// Create 5 identical top-tier peers
	peers := []string{"p1", "p2", "p3", "p4", "p5"}
	for _, p := range peers {
		pm.peers[p] = &Peer{
			Host:        p,
			IsActive:    true,
			IsSynced:    true,
			BlockHeight: 1000,
			Latency:     10 * time.Millisecond,
			LastChecked: time.Now(),
		}
	}

	// Simulate 1000 distinct clients
	counts := make(map[string]int)
	for i := 0; i < 1000; i++ {
		clientIP := fmt.Sprintf("192.168.1.%d", i)
		best, ok := pm.GetBestPeer(clientIP)
		if !ok {
			t.Fatal("expected a peer")
		}
		counts[best]++
	}

	// Verify distribution
	t.Logf("Distribution across %d peers for 1000 clients:", len(peers))
	for p, count := range counts {
		t.Logf("  %s: %d (%.1f%%)", p, count, float64(count)/10.0)
		if count < 100 || count > 300 {
			// Ideally 200 each. Allow reasonable variance (randomness of CRC32)
			t.Errorf("peer %s has unexpected load count: %d (expected ~200)", p, count)
		}
	}
}

func TestLagTolerance(t *testing.T) {
	pm := &PeerManager{
		peers:      make(map[string]*Peer),
		affinity:   make(map[string]string),
		affinityTS: make(map[string]time.Time),
		g: &Gateway{
			logger: l.New(l.Ctx{"module": "test"}),
		},
	}
	// Link to gateway for logger access
	pm.g = &Gateway{
		logger: l.New(l.Ctx{"module": "test"}),
	}

	// 1. Setup Peer A (Height 100) and Peer B (Height 100)
	pm.peers["peerA"] = &Peer{Host: "peerA", IsActive: true, IsSynced: true, BlockHeight: 100, LastChecked: time.Now()}
	pm.peers["peerB"] = &Peer{Host: "peerB", IsActive: true, IsSynced: true, BlockHeight: 100, LastChecked: time.Now()}

	key := "userLag"

	// Force affinity to Peer A
	pm.affinity[key] = "peerA"
	pm.affinityTS[key] = time.Now()

	// 2. Peer B advances to 102 (Lag = 2). Should STAY on A.
	pm.peers["peerB"].BlockHeight = 102
	if best, _ := pm.GetBestPeer(key); best != "peerA" {
		t.Fatalf("expected to stay on peerA with lag=2 (acceptable consistency), got %s", best)
	}

	// 3. Peer B advances to 103 (Lag = 3). Should STAY on A.
	pm.peers["peerB"].BlockHeight = 103
	if best, _ := pm.GetBestPeer(key); best != "peerA" {
		t.Fatalf("expected to stay on peerA with lag=3 (acceptable limit), got %s", best)
	}

	// 4. Peer B advances to 104 (Lag = 4). MUST Switch to B.
	pm.peers["peerB"].BlockHeight = 104
	best, _ := pm.GetBestPeer(key)
	if best != "peerA" {
		t.Fatalf("expected to stay on peerA with lag=4 (new tolerance 10), got %s", best)
	}

	// 5. Peer B advances to 111 (Lag = 11). MUST Switch to B.
	pm.peers["peerB"].BlockHeight = 111
	best, _ = pm.GetBestPeer(key)
	if best != "peerB" {
		t.Fatalf("expected failover to peerB with lag=11 (too far behind), got %s", best)
	}

	// Verify affinity updated to B
	if pm.affinity[key] != "peerB" {
		t.Errorf("affinity should have updated to peerB")
	}
}

func TestPeerProbeBackoff(t *testing.T) {
	tests := []struct {
		failures int
		want     time.Duration
	}{
		{failures: 0, want: 0},
		{failures: 1, want: 30 * time.Second},
		{failures: 2, want: 30 * time.Second},
		{failures: 3, want: 30 * time.Second},
		{failures: 4, want: time.Minute},
		{failures: 5, want: 2 * time.Minute},
		{failures: 7, want: 5 * time.Minute},
		{failures: 20, want: 5 * time.Minute},
	}

	for _, tc := range tests {
		if got := peerProbeBackoff(tc.failures); got != tc.want {
			t.Fatalf("peerProbeBackoff(%d) = %s, want %s", tc.failures, got, tc.want)
		}
	}
}

func TestPeerManagerSkipsProbeDuringCooldown(t *testing.T) {
	pm := NewPeerManager(&Gateway{
		logger: l.New(l.Ctx{"module": "test"}),
	})
	now := time.Now()

	if !pm.shouldProbePeer("new-peer", now) {
		t.Fatalf("new peer should be probed")
	}

	pm.peers["cooling"] = &Peer{
		Host:      "cooling",
		NextProbe: now.Add(time.Minute),
	}
	if pm.shouldProbePeer("cooling", now) {
		t.Fatalf("peer in cooldown should not be probed")
	}
	if !pm.shouldProbePeer("cooling", now.Add(2*time.Minute)) {
		t.Fatalf("peer should be probed after cooldown expires")
	}
}

func TestGetBestPeerSkipsCooldownPeers(t *testing.T) {
	pm := NewPeerManager(&Gateway{
		logger: l.New(l.Ctx{"module": "test"}),
	})
	now := time.Now()

	pm.peers["cooling"] = &Peer{
		Host:        "cooling",
		IsActive:    true,
		IsSynced:    true,
		BlockHeight: 1000,
		LastChecked: now,
		NextProbe:   now.Add(time.Minute),
	}
	pm.peers["healthy"] = &Peer{
		Host:        "healthy",
		IsActive:    true,
		IsSynced:    true,
		BlockHeight: 900,
		LastChecked: now,
	}

	best, ok := pm.GetBestPeer("")
	if !ok {
		t.Fatalf("expected healthy peer")
	}
	if best != "healthy" {
		t.Fatalf("expected healthy peer, got %s", best)
	}

	delete(pm.peers, "healthy")
	if best, ok := pm.GetBestPeer(""); ok {
		t.Fatalf("expected no peer when all peers are in cooldown, got %s", best)
	}
}

func TestReportFailureSetsCooldownAndSuccessClearsIt(t *testing.T) {
	pm := NewPeerManager(&Gateway{
		logger: l.New(l.Ctx{"module": "test"}),
	})
	pm.peers["peer"] = &Peer{
		Host:     "peer",
		IsActive: true,
		IsSynced: true,
	}

	pm.ReportFailure("peer")
	peer := pm.peers["peer"]
	if !peer.IsActive {
		t.Fatalf("peer should remain active until %d consecutive failures", peerFailureUnhealthyThreshold)
	}
	if peer.FailureCount != 1 {
		t.Fatalf("failure count = %d, want 1", peer.FailureCount)
	}
	if !peer.NextProbe.IsZero() {
		t.Fatalf("expected no cooldown before unhealthy threshold, got %s", peer.NextProbe)
	}

	pm.ReportFailure("peer")
	if !peer.IsActive {
		t.Fatalf("peer should remain active after second consecutive failure")
	}
	if peer.FailureCount != 2 {
		t.Fatalf("failure count = %d, want 2", peer.FailureCount)
	}
	if !peer.NextProbe.IsZero() {
		t.Fatalf("expected no cooldown before unhealthy threshold, got %s", peer.NextProbe)
	}

	pm.ReportFailure("peer")
	if peer.IsActive {
		t.Fatalf("expected peer to be inactive after third consecutive failure")
	}
	if peer.IsSynced {
		t.Fatalf("expected peer to be unsynced after third consecutive failure")
	}
	if peer.FailureCount != peerFailureUnhealthyThreshold {
		t.Fatalf("failure count = %d, want %d", peer.FailureCount, peerFailureUnhealthyThreshold)
	}
	if peer.NextProbe.IsZero() || !peer.NextProbe.After(time.Now()) {
		t.Fatalf("expected future next probe after unhealthy threshold, got %s", peer.NextProbe)
	}

	pm.ReportSuccess("peer")
	if !peer.IsActive {
		t.Fatalf("expected successful peer to be active")
	}
	if peer.FailureCount != 0 {
		t.Fatalf("failure count = %d, want 0", peer.FailureCount)
	}
	if !peer.NextProbe.IsZero() {
		t.Fatalf("expected next probe to be cleared, got %s", peer.NextProbe)
	}
}

func TestProbeFailureRequiresThreeConsecutiveFailures(t *testing.T) {
	pm := NewPeerManager(&Gateway{
		logger: l.New(l.Ctx{"module": "test"}),
		client: http.DefaultClient,
	})
	pm.peers["127.0.0.1"] = &Peer{
		Host:        "127.0.0.1",
		IsActive:    true,
		IsSynced:    true,
		BlockHeight: 100,
		LastChecked: time.Now(),
		DAGPort:     1,
	}
	failingProbe := Peer{Host: "127.0.0.1", DAGPort: 1}

	pm.probePeer(failingProbe)
	peer := pm.peers["127.0.0.1"]
	if peer.FailureCount != 1 {
		t.Fatalf("failure count after first probe = %d, want 1", peer.FailureCount)
	}
	if !peer.IsActive || !peer.IsSynced {
		t.Fatalf("peer should stay active/synced after first failed probe: %#v", peer)
	}
	if !peer.NextProbe.IsZero() {
		t.Fatalf("expected no cooldown after first failed probe, got %s", peer.NextProbe)
	}

	pm.probePeer(failingProbe)
	if peer.FailureCount != 2 {
		t.Fatalf("failure count after second probe = %d, want 2", peer.FailureCount)
	}
	if !peer.IsActive || !peer.IsSynced {
		t.Fatalf("peer should stay active/synced after second failed probe: %#v", peer)
	}
	if !peer.NextProbe.IsZero() {
		t.Fatalf("expected no cooldown after second failed probe, got %s", peer.NextProbe)
	}

	pm.probePeer(failingProbe)
	if peer.FailureCount != peerFailureUnhealthyThreshold {
		t.Fatalf("failure count after third probe = %d, want %d", peer.FailureCount, peerFailureUnhealthyThreshold)
	}
	if peer.IsActive || peer.IsSynced {
		t.Fatalf("peer should be inactive/unsynced after third failed probe: %#v", peer)
	}
	if peer.NextProbe.IsZero() || !peer.NextProbe.After(time.Now()) {
		t.Fatalf("expected cooldown after third failed probe, got %s", peer.NextProbe)
	}
}
