package p2p

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/BlockdagNetworkLabs/bdag/p2p/peers"
	"github.com/libp2p/go-libp2p/core/peer"
	ma "github.com/multiformats/go-multiaddr"
)

const (
	recentPeerCacheFile        = "recent-peers.json"
	recentPeerCacheVersion     = 1
	recentPeerMaxEntries       = 256
	recentPeerMaxAge           = 14 * 24 * time.Hour
	recentPeerPersistInterval  = 5 * time.Minute
	recentPeerPersistFileMode  = 0600
	recentPeerPersistDirectory = 0700
	// coalesceWriteDelay is the debounce window for connection-burst writes.
	// Multiple rapid connection events are batched into a single write.
	coalesceWriteDelay = 2 * time.Second
)

type recentPeerCache struct {
	Version   int                `json:"version"`
	UpdatedAt string             `json:"updated_at"`
	Peers     []recentPeerRecord `json:"peers"`
}

type recentPeerRecord struct {
	Addr     string `json:"addr"`
	LastSeen string `json:"last_seen"`
	Source   string `json:"source,omitempty"`
}

func (s *Service) recentPeerCachePath() string {
	if s == nil || s.cfg == nil || s.staticPeersOnly() || strings.TrimSpace(s.cfg.DataDir) == "" {
		return ""
	}
	return filepath.Join(s.cfg.DataDir, recentPeerCacheFile)
}

func (s *Service) initRecentPeerCache() {
	// Hydrate the owned in-memory cache from persisted records at startup.
	// This ensures subsequent mergeRecentPeerAddrs calls have a complete base,
	// preventing the first write from discarding retained peers.
	path := s.recentPeerCachePath()
	if path == "" {
		return
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			log.Warn(fmt.Sprintf("Could not read recent peer cache %s: %v", path, err))
		}
		return
	}
	var cache recentPeerCache
	if err := json.Unmarshal(data, &cache); err != nil {
		log.Warn(fmt.Sprintf("Could not parse recent peer cache %s: %v", path, err))
		return
	}
	s.recentPeersMu.Lock()
	s.recentPeersCache = cache
	s.recentPeersMu.Unlock()
	if len(cache.Peers) > 0 {
		log.Info("Hydrated recent P2P peer cache from disk", "path", path, "count", len(cache.Peers))
	}
}

func (s *Service) loadRecentPeerAddrs() []string {
	path := s.recentPeerCachePath()
	if path == "" {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			log.Warn(fmt.Sprintf("Could not read recent peer cache %s: %v", path, err))
		}
		return nil
	}
	self := peer.ID("")
	if s.host != nil {
		self = s.host.ID()
	}
	addrs := recentPeerAddrsFromCache(data, self, time.Now())
	if len(addrs) > 0 {
		log.Info("Loaded recent P2P peers for startup", "path", path, "count", len(addrs))
	}
	return addrs
}

func (s *Service) recordRecentPeerInfo(info peer.AddrInfo, source string) {
	if s == nil || s.staticPeersOnly() || info.ID == "" {
		return
	}
	if s.host != nil && info.ID == s.host.ID() {
		return
	}
	addrs, err := peer.AddrInfoToP2pAddrs(&info)
	if err != nil || len(addrs) == 0 {
		return
	}
	raw := make([]string, 0, len(addrs))
	for _, addr := range addrs {
		if addr == nil {
			continue
		}
		raw = append(raw, addr.String())
	}
	s.mergeRecentPeerAddrs(raw, source)
}

func (s *Service) persistRecentPeersFromStatus(source string) {
	if s == nil || s.staticPeersOnly() || s.sy == nil || s.Peers() == nil {
		return
	}
	raw := make([]string, 0)
	for _, snapshot := range s.Peers().StatsSnapshots() {
		if snapshot == nil ||
			!snapshot.State ||
			snapshot.Address == "" ||
			!snapshot.IsTheSameNetwork() ||
			!peers.HasConsensusService(snapshot.Services) ||
			len(snapshot.Bads) > 0 {
			continue
		}
		raw = append(raw, snapshot.Address)
	}
	if len(raw) == 0 {
		return
	}
	s.mergeRecentPeerAddrs(raw, source)
}

func (s *Service) mergeRecentPeerAddrs(raw []string, source string) {
	path := s.recentPeerCachePath()
	if path == "" || len(raw) == 0 {
		return
	}
	s.recentPeersMu.Lock()

	// Use in-memory cache as base to avoid losing observations from concurrent calls.
	// Previously this re-read the disk file each time, which could lose entries
	// when two peers arrived before the deferred flush completed.
	existing := s.recentPeersCache

	// Mark dirty and schedule a deferred write to coalesce connection bursts.
	s.recentPeersDirty = true
	if s.recentPeersTimer == nil {
		s.recentPeersTimer = time.AfterFunc(coalesceWriteDelay, func() {
			s.flushRecentPeerCache(path)
		})
	} else {
		s.recentPeersTimer.Reset(coalesceWriteDelay)
	}

	now := time.Now().UTC()
	self := peer.ID("")
	if s.host != nil {
		self = s.host.ID()
	}
	records := mergeRecentPeerRecords(existing.Peers, raw, source, self, now)
	// Store updated records in-memory; file write is deferred.
	s.recentPeersCache = recentPeerCache{
		Version:   recentPeerCacheVersion,
		UpdatedAt: now.Format(time.RFC3339Nano),
		Peers:     records,
	}
	s.recentPeersMu.Unlock()
}

// flushRecentPeerCache writes the in-memory cache to disk atomically.
// Called by the coalescing timer, periodic persistence, and on clean shutdown.
func (s *Service) flushRecentPeerCache(path string) {
	s.recentPeersMu.Lock()
	defer s.recentPeersMu.Unlock()

	if !s.recentPeersDirty {
		return
	}

	// Check if file was externally replaced or deleted before overwriting.
	// Preserve the higher-updated cache when another process has made changes.
	if st, err := os.Stat(path); err == nil {
		fileMod := st.ModTime().UTC()
		var cacheMod time.Time
		if s.recentPeersCache.UpdatedAt != "" {
			cacheMod, _ = time.Parse(time.RFC3339Nano, s.recentPeersCache.UpdatedAt)
		}
		if !cacheMod.IsZero() && fileMod.After(cacheMod) {
			// External file is newer — reload it into memory to preserve evidence.
			if data, err := os.ReadFile(path); err == nil {
				var external recentPeerCache
				if json.Unmarshal(data, &external) == nil {
					external.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
					s.recentPeersCache = external
				}
			}
			s.recentPeersDirty = false
			return
		}
	}

	cache := s.recentPeersCache
	data, err := json.MarshalIndent(cache, "", "  ")
	if err != nil {
		log.Warn(fmt.Sprintf("Could not encode recent peer cache %s: %v", path, err))
		return
	}
	data = append(data, '\n')
	if err := os.MkdirAll(filepath.Dir(path), recentPeerPersistDirectory); err != nil {
		log.Warn(fmt.Sprintf("Could not create recent peer cache directory %s: %v", filepath.Dir(path), err))
		return
	}
	if err := writeRecentPeerCacheFile(path, data); err != nil {
		log.Warn(fmt.Sprintf("Could not write recent peer cache %s: %v", path, err))
		return
	}
	s.recentPeersDirty = false
}

func (s *Service) closeRecentPeerCache() {
	// Stop the deferred write timer and flush pending data immediately.
	// This ensures delayed peer observations are persisted before shutdown.
	s.recentPeersMu.Lock()
	if s.recentPeersTimer != nil {
		s.recentPeersTimer.Stop()
		s.recentPeersTimer = nil
	}
	s.recentPeersMu.Unlock()
	path := s.recentPeerCachePath()
	if path != "" {
		s.flushRecentPeerCache(path)
	}
}

func writeRecentPeerCacheFile(path string, data []byte) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".recent-peers-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(recentPeerPersistFileMode); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

func recentPeerAddrsFromCache(data []byte, self peer.ID, now time.Time) []string {
	var cache recentPeerCache
	if err := json.Unmarshal(data, &cache); err != nil {
		return nil
	}
	records := pruneRecentPeerRecords(cache.Peers, self, now)
	addrs := make([]string, 0, len(records))
	for _, record := range records {
		addrs = append(addrs, record.Addr)
	}
	return addrs
}

func mergeRecentPeerRecords(existing []recentPeerRecord, raw []string, source string, self peer.ID, now time.Time) []recentPeerRecord {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	records := pruneRecentPeerRecords(existing, self, now)
	byAddr := make(map[string]int, len(records))
	for index, record := range records {
		byAddr[record.Addr] = index
	}
	for _, addr := range raw {
		normalized, ok := normalizeRecentPeerAddr(addr, self)
		if !ok {
			continue
		}
		record := recentPeerRecord{
			Addr:     normalized,
			LastSeen: now.Format(time.RFC3339Nano),
			Source:   strings.TrimSpace(source),
		}
		if index, exists := byAddr[normalized]; exists {
			records[index] = record
			continue
		}
		byAddr[normalized] = len(records)
		records = append(records, record)
	}
	sortRecentPeerRecords(records)
	if len(records) > recentPeerMaxEntries {
		records = records[:recentPeerMaxEntries]
	}
	return records
}

func pruneRecentPeerRecords(records []recentPeerRecord, self peer.ID, now time.Time) []recentPeerRecord {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	result := make([]recentPeerRecord, 0, len(records))
	seen := make(map[string]struct{}, len(records))
	for _, record := range records {
		normalized, ok := normalizeRecentPeerAddr(record.Addr, self)
		if !ok {
			continue
		}
		if _, exists := seen[normalized]; exists {
			continue
		}
		lastSeen, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(record.LastSeen))
		if err != nil {
			lastSeen, err = time.Parse(time.RFC3339, strings.TrimSpace(record.LastSeen))
		}
		if err != nil || lastSeen.IsZero() || now.Sub(lastSeen) > recentPeerMaxAge {
			continue
		}
		record.Addr = normalized
		record.LastSeen = lastSeen.UTC().Format(time.RFC3339Nano)
		record.Source = strings.TrimSpace(record.Source)
		seen[normalized] = struct{}{}
		result = append(result, record)
	}
	sortRecentPeerRecords(result)
	if len(result) > recentPeerMaxEntries {
		result = result[:recentPeerMaxEntries]
	}
	return result
}

func normalizeRecentPeerAddr(raw string, self peer.ID) (string, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", false
	}
	addr, err := ma.NewMultiaddr(raw)
	if err != nil {
		return "", false
	}
	info, err := peer.AddrInfoFromP2pAddr(addr)
	if err != nil || info == nil || info.ID == "" {
		return "", false
	}
	if self != "" && info.ID == self {
		return "", false
	}
	if !isPublicPeerMultiaddr(addr) {
		return "", false
	}
	return addr.String(), true
}

func sanitizeAutomaticPeerAddrInfo(info peer.AddrInfo, allowPrivate bool) (peer.AddrInfo, bool) {
	if info.ID == "" {
		return peer.AddrInfo{}, false
	}
	if allowPrivate {
		return info, true
	}
	filtered := peer.AddrInfo{ID: info.ID}
	for _, addr := range info.Addrs {
		if addr == nil {
			continue
		}
		if isPublicPeerMultiaddr(addr) {
			filtered.Addrs = append(filtered.Addrs, addr)
		}
	}
	return filtered, len(filtered.Addrs) > 0
}

func isPublicPeerMultiaddr(addr ma.Multiaddr) bool {
	ip, ok := multiaddrIP(addr)
	if !ok {
		return true
	}
	return ip.IsGlobalUnicast() &&
		!ip.IsPrivate() &&
		!ip.IsLoopback() &&
		!ip.IsLinkLocalUnicast() &&
		!ip.IsLinkLocalMulticast() &&
		!ip.IsUnspecified()
}

func multiaddrIP(addr ma.Multiaddr) (net.IP, bool) {
	for _, protocol := range []int{ma.P_IP4, ma.P_IP6} {
		value, err := addr.ValueForProtocol(protocol)
		if err != nil {
			continue
		}
		ip := net.ParseIP(value)
		if ip == nil {
			return nil, true
		}
		return ip, true
	}
	return nil, false
}

func sortRecentPeerRecords(records []recentPeerRecord) {
	sort.SliceStable(records, func(i, j int) bool {
		left, leftErr := time.Parse(time.RFC3339Nano, records[i].LastSeen)
		right, rightErr := time.Parse(time.RFC3339Nano, records[j].LastSeen)
		if leftErr == nil && rightErr == nil && !left.Equal(right) {
			return left.After(right)
		}
		return records[i].Addr < records[j].Addr
	})
}

func mergePeerAddrStrings(groups ...[]string) []string {
	seen := map[string]struct{}{}
	merged := make([]string, 0)
	for _, group := range groups {
		for _, addr := range group {
			addr = strings.TrimSpace(addr)
			if addr == "" {
				continue
			}
			if _, exists := seen[addr]; exists {
				continue
			}
			seen[addr] = struct{}{}
			merged = append(merged, addr)
		}
	}
	return merged
}
