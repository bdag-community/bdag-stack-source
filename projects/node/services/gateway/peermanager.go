package gateway

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"hash/crc32"
	"net"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"time"

	multiaddr "github.com/multiformats/go-multiaddr"
)

const (
	peerFailureUnhealthyThreshold = 3
	peerProbeConcurrency          = 4
	peerProbeUpdateFreq           = 30 * time.Second
	peerProbeBackoffBase          = 30 * time.Second
	peerProbeBackoffMax           = 5 * time.Minute
)

// Peer represents a remote node and its current status.
type Peer struct {
	Host         string
	Latency      time.Duration
	BlockHeight  uint64
	IsActive     bool
	IsSynced     bool
	LastChecked  time.Time
	DAGPort      int
	EVMPort      int
	FailureCount int
	NextProbe    time.Time
}

// PeerManager handles background monitoring of peers.
type PeerManager struct {
	g           *Gateway
	peers       map[string]*Peer
	mu          sync.RWMutex
	stopCh      chan struct{}
	updateFreq  time.Duration
	maxHeight   uint64               // Highest block number ever seen from any peer
	affinity    map[string]string    // key -> host
	affinityTS  map[string]time.Time // key -> lastUsed
	affinityTTL time.Duration
}

// NewPeerManager creates a new PeerManager.
func NewPeerManager(g *Gateway) *PeerManager {
	return &PeerManager{
		g:           g,
		peers:       make(map[string]*Peer),
		stopCh:      make(chan struct{}),
		updateFreq:  peerProbeUpdateFreq,
		affinity:    make(map[string]string),
		affinityTS:  make(map[string]time.Time),
		affinityTTL: 10 * time.Minute,
	}
}

// Start begins the background peer update loop.
func (pm *PeerManager) Start() {
	go pm.loop()
}

// Stop halts the background loop.
func (pm *PeerManager) Stop() {
	close(pm.stopCh)
}

// GetPeerCount returns the number of currently tracked peers.
func (pm *PeerManager) GetPeerCount() int {
	pm.mu.RLock()
	defer pm.mu.RUnlock()
	return len(pm.peers)
}

func (pm *PeerManager) loop() {
	// Initial update
	pm.UpdatePeers()

	ticker := time.NewTicker(pm.updateFreq)
	defer ticker.Stop()

	// Cleanup expired affinities every 5 minutes
	cleanupTicker := time.NewTicker(5 * time.Minute)
	defer cleanupTicker.Stop()

	for {
		select {
		case <-pm.stopCh:
			return
		case <-ticker.C:
			pm.UpdatePeers()
		case <-cleanupTicker.C:
			pm.cleanExpiredAffinities()
		}
	}
}

// UpdatePeers fetches peers from the local DAG and probes them.
func (pm *PeerManager) UpdatePeers() {
	hostList, err := pm.fetchPeerHosts()
	if err != nil {
		pm.g.logger.Warn("Failed to fetch peer list", "error", err)
		return
	}

	sem := make(chan struct{}, peerProbeConcurrency)
	now := time.Now()

	var wg sync.WaitGroup
	for _, host := range hostList {
		if !pm.shouldProbePeer(host.Host, now) {
			continue
		}
		wg.Add(1)
		go func(p Peer) {
			defer wg.Done()
			sem <- struct{}{}        // Acquire
			defer func() { <-sem }() // Release
			pm.probePeer(p)
		}(host)
	}
	wg.Wait()

	// Prune peers that are no longer in the host list (disconnected from node)
	current := make(map[string]bool)
	for _, h := range hostList {
		current[h.Host] = true
	}
	pm.mu.Lock()
	for h := range pm.peers {
		if !current[h] {
			delete(pm.peers, h)
		}
	}
	// Reset maxHeight to be recalculated from current active peers in GetMaxHeight calls?
	// Actually, keeping pm.maxHeight as a record-high is still useful for some logging,
	// but the functional benchmark should be dynamic.
	pm.mu.Unlock()

	pm.logPeerSummary("update")
}

func (pm *PeerManager) shouldProbePeer(host string, now time.Time) bool {
	pm.mu.RLock()
	defer pm.mu.RUnlock()

	peer, exists := pm.peers[host]
	if !exists {
		return true
	}
	return peer.NextProbe.IsZero() || !now.Before(peer.NextProbe)
}

// fetchPeerHosts retrieves the list of peer addresses from the local DAG.
func (pm *PeerManager) fetchPeerHosts() ([]Peer, error) {
	reqJSON := []byte(`{"jsonrpc":"2.0","id":1,"method":"getPeerInfo","params":[]}`)

	if pm.g.DagHandler == nil {
		return nil, errors.New("DagHandler not initialized")
	}

	req, err := http.NewRequest("POST", "/", bytes.NewReader(reqJSON))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if user, pass, err := pm.g.outboundCredentials(roleInternal); err == nil && user != "" {
		req.SetBasicAuth(user, pass)
	}

	rr := httptest.NewRecorder()
	pm.g.DagHandler.ServeHTTP(rr, req)

	resp := rr.Body.Bytes()

	var env struct {
		Result []struct {
			Address string `json:"address"`
			DAGPort int    `json:"dagport"`
			EVMPort int    `json:"evmport"`
		} `json:"result"`
		Error *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}

	if json.Unmarshal(resp, &env) != nil {
		return nil, errors.New("invalid peerinfo response")
	}
	if env.Error != nil {
		return nil, errors.New(env.Error.Message)
	}

	var hosts []Peer
	for _, p := range env.Result {
		host := p.Address
		// Logic adapted from sync.go to parse multiaddr or host:port
		if strings.HasPrefix(host, "/") {
			if maddr, err := multiaddr.NewMultiaddr(host); err == nil {
				if ip, err := maddr.ValueForProtocol(multiaddr.P_IP4); err == nil {
					host = ip
				} else if ip, err := maddr.ValueForProtocol(multiaddr.P_IP6); err == nil {
					host = ip
				} else {
					host = ""
				}
			} else {
				host = ""
			}
		} else {
			if h, _, err := net.SplitHostPort(host); err == nil {
				host = h
			}
		}
		if host != "" {
			hosts = append(hosts, Peer{
				Host:    host,
				DAGPort: p.DAGPort,
				EVMPort: p.EVMPort,
			})
		}
	}
	return hosts, nil
}

// probePeer checks the status and latency of a single peer.
func (pm *PeerManager) probePeer(p Peer) {
	host := p.Host
	start := time.Now()

	// Check DAG status
	isCurrent := pm.g.peerDagIsCurrent(host, p.DAGPort)

	// Check EVM Block Number if applicable
	var blockHeight uint64
	var isActive bool

	if pm.g.cfg.IsEVM {
		// Fetch EVM height even if DAG layer is not yet "current"
		// This keeps us from 503 errors during synchronization.
		if height, ok := pm.g.peerEvmBlockNumberWithRetry(host, p.EVMPort, 2*time.Second); ok {
			blockHeight = height
			isActive = true
		}
	} else {
		// For DAG gateway, also fetch height even if not current
		if height, ok := pm.g.peerDagBlockCount(host, p.DAGPort); ok {
			blockHeight = height
			isActive = true
		}
	}

	latency := time.Since(start)

	pm.mu.Lock()
	defer pm.mu.Unlock()

	peer, exists := pm.peers[host]
	if !exists {
		peer = &Peer{
			Host:    host,
			DAGPort: p.DAGPort,
			EVMPort: p.EVMPort,
		}
		pm.peers[host] = peer
	}

	peer.Latency = latency
	peer.LastChecked = time.Now()
	peer.DAGPort = p.DAGPort
	peer.EVMPort = p.EVMPort

	if isActive {
		peer.FailureCount = 0
		peer.IsActive = true
		peer.IsSynced = isCurrent
		peer.BlockHeight = blockHeight
		peer.NextProbe = time.Time{}
	} else {
		recordPeerFailure(peer, peer.LastChecked)
		// Preserve last known height on failure
		blockHeight = peer.BlockHeight
	}

	if blockHeight > pm.maxHeight {
		pm.maxHeight = blockHeight
	}

	if !isActive {
		pm.g.logger.Debug("Peer probe failed", "host", host, "dagPort", p.DAGPort, "evmPort", p.EVMPort, "isEVM", pm.g.cfg.IsEVM, "failures", peer.FailureCount, "nextProbe", peer.NextProbe)
	}
}

func recordPeerFailure(p *Peer, now time.Time) {
	p.FailureCount++
	p.LastChecked = now
	if p.FailureCount >= peerFailureUnhealthyThreshold {
		p.IsActive = false
		p.IsSynced = false
		p.NextProbe = now.Add(peerProbeBackoff(p.FailureCount))
		return
	}
	p.NextProbe = time.Time{}
}

func peerProbeBackoff(failureCount int) time.Duration {
	if failureCount <= 0 {
		return 0
	}
	if failureCount <= peerFailureUnhealthyThreshold {
		return peerProbeBackoffBase
	}
	backoff := peerProbeBackoffBase
	for i := peerFailureUnhealthyThreshold; i < failureCount; i++ {
		if backoff >= peerProbeBackoffMax/2 {
			return peerProbeBackoffMax
		}
		backoff *= 2
	}
	if backoff > peerProbeBackoffMax {
		return peerProbeBackoffMax
	}
	return backoff
}

// GetBestPeer returns the best available peer's host string.
func (pm *PeerManager) GetBestPeer(key string) (string, bool) {
	if p, ok := pm.GetBestPeerInfo(key); ok {
		return p.Host, true
	}
	return "", false
}

// GetBestPeerInfo returns a copy of the best available peer's information.
func (pm *PeerManager) GetBestPeerInfo(key string) (*Peer, bool) {
	return pm.getBestPeerInfo(key, 0, false)
}

// GetBestPeerInfoWithTTL allows specifying a custom affinity TTL and deterministic selection.
func (pm *PeerManager) GetBestPeerInfoWithTTL(key string, ttl time.Duration, deterministic bool) (*Peer, bool) {
	return pm.getBestPeerInfo(key, ttl, deterministic)
}

func (pm *PeerManager) getBestPeerInfo(key string, ttl time.Duration, deterministic bool) (*Peer, bool) {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	var candidates []*Peer
	now := time.Now()
	var maxSyncedHeight uint64

	pm.g.logger.Trace("GetBestPeerInfo start", "totalPeers", len(pm.peers))
	for host, p := range pm.peers {
		inCooldown := peerInProbeCooldown(p, now)
		pm.g.logger.Trace("Checking peer", "host", host, "isActive", p.IsActive, "isSynced", p.IsSynced, "lastChecked", time.Since(p.LastChecked), "inCooldown", inCooldown)
		if !inCooldown && (p.IsActive || now.Sub(p.LastChecked) > 5*time.Second) {
			candidates = append(candidates, p)
			if p.IsActive && p.IsSynced && p.BlockHeight > maxSyncedHeight {
				maxSyncedHeight = p.BlockHeight
			}
		}
	}

	if len(candidates) == 0 {
		pm.g.logger.Trace("GetBestPeerInfo: no active candidates, trying last resort")
		for _, p := range pm.peers {
			if !peerInProbeCooldown(p, now) && p.IsSynced {
				candidates = append(candidates, p)
			}
		}
		if len(candidates) == 0 {
			for _, p := range pm.peers {
				if !peerInProbeCooldown(p, now) {
					candidates = append(candidates, p)
				}
			}
		}

		if len(candidates) > 0 {
			pm.g.logger.Warn("Using inactive peer as Last Resort", "count", len(candidates))
		} else {
			pm.g.logger.Trace("GetBestPeerInfo: absolutely no candidates")
			return nil, false
		}
	}
	pm.g.logger.Trace("GetBestPeerInfo: candidates found", "count", len(candidates))

	pm.g.logger.Trace("GetBestPeerInfo: candidates detail", "candidates", formatPeerList(candidates))

	affinityTTL := pm.affinityTTL
	if ttl > 0 {
		affinityTTL = ttl
	}

	// 1. Check for valid existing affinity
	if key != "" {
		if host, ok := pm.affinity[key]; ok {
			// Check if affinity has expired (TTL) to allow re-balancing of idle clients
			if time.Since(pm.affinityTS[key]) > affinityTTL {
				delete(pm.affinity, key)
				delete(pm.affinityTS, key)
			} else if p, exists := pm.peers[host]; exists {
				// Affinity peer must be Active and Synced to be reused.
				if p.IsActive && p.IsSynced {
					// Lag Tolerance Check:
					// While we prefer consistency, we must not stick to a peer that is falling significantly behind.
					// Tolerance threshold: 3 blocks.
					isLagging := false
					if maxSyncedHeight > p.BlockHeight {
						// Relaxing tolerance to 10 blocks to avoid mempool thrashing.
						// Switching peers breaks mempool consistency (pending txs), so we should only do it
						// if the peer is SERIOUSLY behind.
						if maxSyncedHeight-p.BlockHeight > 10 {
							isLagging = true
						}
					}

					if !isLagging {
						// Update usage TS
						pm.affinityTS[key] = time.Now()
						return p, true
					}
				}
				// Peer is dead/unsynced/lagging? Clear affinity and fall through to re-selection.
				delete(pm.affinity, key)
				delete(pm.affinityTS, key)
			}
		}
	}

	if deterministic {
		best, ok := pm.selectDeterministicPeer(key, candidates, maxSyncedHeight)
		if ok && key != "" {
			pm.affinity[key] = best.Host
			pm.affinityTS[key] = time.Now()
		}
		return best, ok
	}

	// Sort candidates:
	// 1. IsActive is prioritized (prefer live peers over those in retry cooldown).
	// 2. IsSynced is preferred (prioritize peers that have finished DAG sync).
	// 3. Higher BlockHeight is better (better EVM state).
	// 4. Lower Latency is better (faster response).
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].IsActive != candidates[j].IsActive {
			return candidates[i].IsActive
		}
		if candidates[i].FailureCount != candidates[j].FailureCount {
			return candidates[i].FailureCount < candidates[j].FailureCount
		}
		if candidates[i].IsSynced != candidates[j].IsSynced {
			return candidates[i].IsSynced
		}
		if candidates[i].BlockHeight != candidates[j].BlockHeight {
			return candidates[i].BlockHeight > candidates[j].BlockHeight
		}
		return candidates[i].Latency < candidates[j].Latency
	})

	pm.g.logger.Trace("GetBestPeerInfo: ranked candidates", "ranked", formatPeerList(candidates))

	// Sticky Session / Affinity Logic
	// Take top candidates to ensure quality.
	topN := 5
	if len(candidates) < topN {
		topN = len(candidates)
	}

	rawTargetPool := candidates[:topN]
	var targetPool []*Peer

	// Lag Tolerance Filter for New Selections:
	// Ensure that we only pick from peers that are reasonably close to the best available peer.
	// candidates[0] is the best because of the sort above.
	bestCandidate := candidates[0]
	if bestCandidate.IsSynced {
		for _, p := range rawTargetPool {
			// If best is synced, we demand peers be close to it
			if p.IsSynced {
				diff := uint64(0)
				if bestCandidate.BlockHeight > p.BlockHeight {
					diff = bestCandidate.BlockHeight - p.BlockHeight
				}
				if diff <= 10 {
					targetPool = append(targetPool, p)
				}
			} else {
				// If candidate is not synced but best is, we probably shouldn't pick it,
				// but top sorting already pushed unsynced to bottom.
				// Just keep it if it made the cut (unlikely).
				targetPool = append(targetPool, p)
			}
		}
	} else {
		// If even the best candidate is not synced, just use the raw pool
		targetPool = rawTargetPool
	}

	// Update topN based on filtered pool
	topN = len(targetPool)
	if topN == 0 {
		// Should not happen if rawTargetPool > 0 and we kept at least the best one
		// But just in case, fall back to best candidate
		targetPool = []*Peer{bestCandidate}
		topN = 1
	}

	pm.g.logger.Trace("GetBestPeerInfo: target pool", "targetPool", formatPeerList(targetPool), "topN", topN)

	idx := 0
	if key != "" && topN > 0 {
		// Valid session key: sort by host to ensure stable hashing
		sort.Slice(targetPool, func(i, j int) bool {
			return targetPool[i].Host < targetPool[j].Host
		})

		hash := crc32.ChecksumIEEE([]byte(key))
		idx = int(hash) % topN
	}

	best := targetPool[idx]

	pm.g.logger.Debug("GetBestPeerInfo: selected", "host", best.Host, "height", best.BlockHeight, "latency", best.Latency, "synced", best.IsSynced, "active", best.IsActive)

	// Save choice to affinity map
	if key != "" && best != nil {
		pm.affinity[key] = best.Host
		pm.affinityTS[key] = time.Now()
	}

	return best, true
}

func peerInProbeCooldown(p *Peer, now time.Time) bool {
	return p != nil && !p.NextProbe.IsZero() && now.Before(p.NextProbe)
}

func (pm *PeerManager) selectDeterministicPeer(key string, candidates []*Peer, maxSyncedHeight uint64) (*Peer, bool) {
	if len(candidates) == 0 {
		return nil, false
	}

	activeSynced := make([]*Peer, 0, len(candidates))
	active := make([]*Peer, 0, len(candidates))
	for _, p := range candidates {
		if p.IsActive && p.IsSynced {
			activeSynced = append(activeSynced, p)
		}
		if p.IsActive {
			active = append(active, p)
		}
	}

	targetPool := activeSynced
	if len(targetPool) > 0 && maxSyncedHeight > 0 {
		filtered := make([]*Peer, 0, len(targetPool))
		for _, p := range targetPool {
			diff := uint64(0)
			if maxSyncedHeight > p.BlockHeight {
				diff = maxSyncedHeight - p.BlockHeight
			}
			if diff <= 10 {
				filtered = append(filtered, p)
			}
		}
		if len(filtered) > 0 {
			targetPool = filtered
		}
	}

	if len(targetPool) == 0 {
		if len(active) > 0 {
			targetPool = active
		} else {
			targetPool = candidates
		}
	}

	sort.Slice(targetPool, func(i, j int) bool {
		return targetPool[i].Host < targetPool[j].Host
	})

	if len(targetPool) == 0 {
		return nil, false
	}

	idx := 0
	if key != "" {
		hash := crc32.ChecksumIEEE([]byte(key))
		idx = int(hash) % len(targetPool)
	}
	return targetPool[idx], true
}

// GetHistoricalMaxHeight returns the highest block number ever seen from any peer.
// This is useful for logging/metrics but should NOT be used for routing decisions.
func (pm *PeerManager) GetHistoricalMaxHeight() uint64 {
	pm.mu.RLock()
	defer pm.mu.RUnlock()
	return pm.maxHeight
}

func (pm *PeerManager) logPeerSummary(reason string) {
	pm.mu.RLock()
	defer pm.mu.RUnlock()

	total := 0
	active := 0
	synced := 0
	var list []string
	for _, p := range pm.peers {
		total++
		if p.IsActive {
			active++
		}
		if p.IsSynced {
			synced++
		}
		list = append(list, formatPeer(p))
	}
	pm.g.logger.Debug("PeerManager snapshot", "reason", reason, "total", total, "active", active, "synced", synced, "maxHeight", pm.maxHeight, "peers", strings.Join(list, "; "))
}

func formatPeerList(peers []*Peer) string {
	if len(peers) == 0 {
		return ""
	}
	list := make([]string, 0, len(peers))
	for _, p := range peers {
		list = append(list, formatPeer(p))
	}
	return strings.Join(list, "; ")
}

func formatPeer(p *Peer) string {
	if p == nil {
		return "<nil>"
	}
	cooldown := ""
	if !p.NextProbe.IsZero() {
		cooldown = fmt.Sprintf(" nextProbe=%s", time.Until(p.NextProbe).Round(time.Second))
	}
	return fmt.Sprintf("%s act=%t sync=%t h=%d lat=%s dag=%d evm=%d failures=%d%s", p.Host, p.IsActive, p.IsSynced, p.BlockHeight, p.Latency, p.DAGPort, p.EVMPort, p.FailureCount, cooldown)
}

// GetMaxHeight returns the highest block number from peers that are currently
// usable for routing decisions. Historical high-water marks are exposed through
// GetHistoricalMaxHeight and must not keep a lagging gateway in relay mode.
func (pm *PeerManager) GetMaxHeight() uint64 {
	pm.mu.RLock()
	defer pm.mu.RUnlock()

	now := time.Now()
	var maxHeight uint64
	for _, p := range pm.peers {
		if p == nil || !p.IsActive || peerInProbeCooldown(p, now) {
			continue
		}
		if p.BlockHeight > maxHeight {
			maxHeight = p.BlockHeight
		}
	}
	return maxHeight
}

// GetSyncedPeers returns a list of all currently active and synced peers.
func (pm *PeerManager) GetSyncedPeers() []Peer {
	pm.mu.RLock()
	defer pm.mu.RUnlock()

	var peers []Peer
	for _, p := range pm.peers {
		if p.IsActive && p.IsSynced {
			peers = append(peers, *p)
		}
	}
	return peers
}

// GetAllPeers returns a list of all tracked peers (for debugging).
func (pm *PeerManager) GetAllPeers() []Peer {
	pm.mu.RLock()
	defer pm.mu.RUnlock()

	var peers []Peer
	for _, p := range pm.peers {
		peers = append(peers, *p)
	}
	return peers
}

// ReportFailure allows reporting a failed request to a peer, potentially removing it or downgrading it.
func (pm *PeerManager) ReportFailure(host string) {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	if p, ok := pm.peers[host]; ok {
		recordPeerFailure(p, time.Now())
	}
}

// ReportSuccess signals that a peer successfully handled a request.
// This implements "Passive Health Checking", reviving the peer and refreshing its timestamp.
func (pm *PeerManager) ReportSuccess(host string) {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	if p, ok := pm.peers[host]; ok {
		// If it was dead, we resurrect it!
		if !p.IsActive {
			pm.g.logger.Info("Peer resurrected by passive check", "host", host)
		}
		p.FailureCount = 0
		p.IsActive = true
		p.FailureCount = 0
		p.NextProbe = time.Time{}
		p.LastChecked = time.Now()
	}
}

// cleanExpiredAffinities removes affinity entries that have exceeded their TTL.
// This prevents unbounded growth of the affinity map and ensures stale entries are cleaned up.
func (pm *PeerManager) cleanExpiredAffinities() {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	now := time.Now()
	removed := 0

	for key, lastUsed := range pm.affinityTS {
		// Check if the affinity has expired based on the default TTL
		if now.Sub(lastUsed) > pm.affinityTTL {
			delete(pm.affinity, key)
			delete(pm.affinityTS, key)
			removed++
		}
	}

	if removed > 0 {
		pm.g.logger.Debug("Cleaned expired affinities", "removed", removed, "remaining", len(pm.affinity))
	}
}
