package synch

import (
	"fmt"
	"sync"
	"time"

	"github.com/BlockdagNetworkLabs/bdag/p2p/common"
	"github.com/ethereum/go-ethereum/metrics"
	"github.com/libp2p/go-libp2p/core/peer"
)

const (
	goodbyeRecordCapacity = 256

	goodbyeDirectionOutbound = "outbound"
	goodbyeDirectionInbound  = "inbound"
	goodbyeDirectionUnknown  = "disconnect"

	// goodbyeCodeNone marks a transport disconnect that had no goodbye RPC
	// in either direction within goodbyeSilentGrace.
	goodbyeCodeNone = "no_goodbye"

	goodbyeSilentGrace = 3 * time.Second
)

var goodbyeKnownCodes = []common.ErrorCode{
	common.ErrNone,
	common.ErrStreamWrite,
	common.ErrStreamRead,
	common.ErrStreamBase,
	common.ErrPeerUnknown,
	common.ErrBadPeer,
	common.ErrDAGConsensus,
	common.ErrMessage,
	common.ErrGeneric,
	common.ErrConnectFrequent,
	common.ErrSequence,
	common.ErrRevalidate,
	common.ErrLibp2pConnect,
}

const goodbyeCodeUnknown = "unknown"

func normalizeGoodbyeWireCode(raw uint64) (string, int) {
	if raw <= uint64(common.ErrLibp2pConnect) {
		code := common.ErrorCode(raw)
		if _, ok := goodbyeKnownCodeSet[code]; ok {
			return code.String(), int(code)
		}
	}
	return goodbyeCodeUnknown, -2
}

var goodbyeKnownCodeSet = func() map[common.ErrorCode]struct{} {
	set := make(map[common.ErrorCode]struct{}, len(goodbyeKnownCodes))
	for _, code := range goodbyeKnownCodes {
		set[code] = struct{}{}
	}
	return set
}()

func normalizeGoodbyeRecordCode(code string, codeInt int) (string, int) {
	if code == goodbyeCodeNone {
		return goodbyeCodeNone, -1
	}
	for _, known := range goodbyeKnownCodes {
		if code == known.String() {
			return code, int(known)
		}
	}
	return goodbyeCodeUnknown, -2
}

// GoodbyeRecord is one observed peer-session teardown signal.
type GoodbyeRecord struct {
	At         time.Time `json:"at"`
	Direction  string    `json:"direction"`
	Code       string    `json:"code"`
	CodeInt    int       `json:"code_int"`
	PeerID     string    `json:"peer_id"`
	PeerAddr   string    `json:"peer_addr,omitempty"`
	Detail     string    `json:"detail,omitempty"`
	Consensus  bool      `json:"consensus"`
	Reconnects uint64    `json:"reconnects,omitempty"`
}

// GoodbyeCodeCatalog documents the ErrorCode values the goodbye RPC carries.
// The wire payload is a uint64 ErrorCode; these are the defined meanings.
func GoodbyeCodeCatalog() []map[string]any {
	out := make([]map[string]any, 0, len(goodbyeKnownCodes)+2)
	for _, code := range goodbyeKnownCodes {
		out = append(out, map[string]any{
			"code":    int(code),
			"name":    code.String(),
			"goodbye": code == common.ErrBadPeer || code == common.ErrDAGConsensus || code == common.ErrGeneric || code == common.ErrPeerUnknown,
		})
	}
	out = append(out, map[string]any{
		"code":    -2,
		"name":    goodbyeCodeUnknown,
		"goodbye": false,
	})
	out = append(out, map[string]any{
		"code":    -1,
		"name":    goodbyeCodeNone,
		"goodbye": false,
		"note":    "transport disconnect observed without a goodbye RPC in either direction",
	})
	return out
}

type goodbyeRecorder struct {
	mu      sync.Mutex
	records []GoodbyeRecord
	// recent marks peer IDs that exchanged a goodbye recently so a following
	// libp2p disconnect is not double-counted as no_goodbye.
	recent map[peer.ID]time.Time
	meters map[string]metrics.Meter
}

func newGoodbyeRecorder() *goodbyeRecorder {
	r := &goodbyeRecorder{
		records: make([]GoodbyeRecord, 0, goodbyeRecordCapacity),
		recent:  make(map[peer.ID]time.Time),
		meters:  make(map[string]metrics.Meter),
	}
	for _, direction := range []string{goodbyeDirectionOutbound, goodbyeDirectionInbound, goodbyeDirectionUnknown} {
		for _, code := range goodbyeKnownCodes {
			r.registerMeterLocked(direction, code.String())
		}
		r.registerMeterLocked(direction, goodbyeCodeUnknown)
		r.registerMeterLocked(direction, goodbyeCodeNone)
	}
	return r
}

func (r *goodbyeRecorder) record(rec GoodbyeRecord, pid peer.ID) {
	if r == nil {
		return
	}
	if rec.At.IsZero() {
		rec.At = time.Now().UTC()
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	rec.Direction = normalizeGoodbyeDirection(rec.Direction)
	rec.Code, rec.CodeInt = normalizeGoodbyeRecordCode(rec.Code, rec.CodeInt)
	r.sweepRecentLocked(rec.At)
	if len(r.records) >= goodbyeRecordCapacity {
		copy(r.records, r.records[1:])
		r.records[len(r.records)-1] = rec
	} else {
		r.records = append(r.records, rec)
	}
	if pid != "" && rec.Code != goodbyeCodeNone {
		r.recent[pid] = rec.At
		r.evictRecentLocked()
	}
	r.bumpLocked(rec.Direction, rec.Code)
}

func (r *goodbyeRecorder) bumpLocked(direction, code string) {
	direction = normalizeGoodbyeDirection(direction)
	code, _ = normalizeGoodbyeRecordCode(code, 0)
	key := fmt.Sprintf("p2p/goodbye/%s/%s", direction, code)
	m := r.meters[key]
	m.Mark(1)
}

func (r *goodbyeRecorder) registerMeterLocked(direction, code string) {
	key := fmt.Sprintf("p2p/goodbye/%s/%s", direction, code)
	r.meters[key] = metrics.GetOrRegisterMeter(key, nil)
}

func normalizeGoodbyeDirection(direction string) string {
	switch direction {
	case goodbyeDirectionOutbound, goodbyeDirectionInbound, goodbyeDirectionUnknown:
		return direction
	default:
		return goodbyeDirectionUnknown
	}
}

func (r *goodbyeRecorder) sweepRecentLocked(now time.Time) {
	for pid, at := range r.recent {
		if now.Sub(at) > goodbyeSilentGrace {
			delete(r.recent, pid)
		}
	}
}

func (r *goodbyeRecorder) sweepRecent(now time.Time) {
	if r == nil {
		return
	}
	r.mu.Lock()
	r.sweepRecentLocked(now)
	r.mu.Unlock()
}

func (r *goodbyeRecorder) evictRecentLocked() {
	for len(r.recent) > goodbyeRecordCapacity {
		var oldest peer.ID
		var oldestAt time.Time
		for pid, at := range r.recent {
			if oldest == "" || at.Before(oldestAt) || (at.Equal(oldestAt) && pid.String() < oldest.String()) {
				oldest, oldestAt = pid, at
			}
		}
		delete(r.recent, oldest)
	}
}

func (r *goodbyeRecorder) consumeRecentGoodbye(pid peer.ID, now time.Time) bool {
	if r == nil || pid == "" {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sweepRecentLocked(now)
	_, ok := r.recent[pid]
	if !ok {
		return false
	}
	delete(r.recent, pid)
	return true
}

func (r *goodbyeRecorder) snapshot(limit int) []GoodbyeRecord {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if limit <= 0 || limit > len(r.records) {
		limit = len(r.records)
	}
	out := make([]GoodbyeRecord, limit)
	copy(out, r.records[len(r.records)-limit:])
	return out
}

func (r *goodbyeRecorder) counts() map[string]int {
	if r == nil {
		return map[string]int{}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	out := map[string]int{}
	for _, rec := range r.records {
		out[rec.Direction+"/"+rec.Code]++
	}
	return out
}
