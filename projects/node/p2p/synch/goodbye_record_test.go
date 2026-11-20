package synch

import (
	"fmt"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/BlockdagNetworkLabs/bdag/p2p/common"
	"github.com/libp2p/go-libp2p/core/peer"
)

func TestGoodbyeCodeCatalogIncludesDAGConsensus(t *testing.T) {
	found := false
	for _, row := range GoodbyeCodeCatalog() {
		if row["name"] == common.ErrDAGConsensus.String() {
			found = true
			if row["code"] != int(common.ErrDAGConsensus) {
				t.Fatalf("catalog code = %v, want %d", row["code"], int(common.ErrDAGConsensus))
			}
		}
	}
	if !found {
		t.Fatal("catalog missing ErrDAGConsensus")
	}
}

func TestGoodbyeRecorderTracksOutboundAndSuppressesSilent(t *testing.T) {
	r := newGoodbyeRecorder()
	pid, err := peer.Decode("16Uiu2HAmBjXSaLE5NFw53W4MiG6zu4NMQmnWppu2ADVTihkku3gK")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	r.record(GoodbyeRecord{
		At:        now,
		Direction: goodbyeDirectionOutbound,
		Code:      common.ErrDAGConsensus.String(),
		CodeInt:   int(common.ErrDAGConsensus),
		PeerID:    pid.String(),
		Detail:    "evm checkpoint mismatch",
	}, pid)

	if !r.consumeRecentGoodbye(pid, now.Add(time.Second)) {
		t.Fatal("expected recent goodbye to suppress silent disconnect")
	}
	if r.consumeRecentGoodbye(pid, now.Add(time.Second)) {
		t.Fatal("recent goodbye should be consumed exactly once")
	}

	snap := r.snapshot(10)
	if len(snap) != 1 || snap[0].Code != common.ErrDAGConsensus.String() || snap[0].Detail == "" {
		t.Fatalf("snapshot = %#v", snap)
	}
	counts := r.counts()
	if counts[goodbyeDirectionOutbound+"/"+common.ErrDAGConsensus.String()] != 1 {
		t.Fatalf("counts = %#v", counts)
	}
}

func TestGoodbyeRecorderRingCapacity(t *testing.T) {
	r := newGoodbyeRecorder()
	for i := 0; i < goodbyeRecordCapacity+10; i++ {
		r.record(GoodbyeRecord{
			At:        time.Now().UTC(),
			Direction: goodbyeDirectionInbound,
			Code:      common.ErrBadPeer.String(),
			CodeInt:   int(common.ErrBadPeer),
			PeerID:    strconv.Itoa(i),
		}, "")
	}
	snap := r.snapshot(0)
	if len(snap) != goodbyeRecordCapacity {
		t.Fatalf("len=%d want %d", len(snap), goodbyeRecordCapacity)
	}
}

func TestGoodbyeUnknownCodesNormalizeAndKeepFixedMeters(t *testing.T) {
	r := newGoodbyeRecorder()
	s := &Sync{goodbyes: r}
	maxInt := uint64(^uint(0) >> 1)
	for i := uint64(0); i < 5000; i++ {
		var raw uint64
		switch i % 3 {
		case 0:
			raw = 1000 + i/3
		case 1:
			raw = maxInt + 1 + i/3
		default:
			raw = ^uint64(0) - i/3
		}
		s.recordGoodbyeWire(goodbyeDirectionInbound, raw, nil, "")
		recs := r.snapshot(1)
		if len(recs) != 1 || recs[0].Code != goodbyeCodeUnknown || recs[0].CodeInt != -2 {
			t.Fatalf("wire code %d normalized to %#v", raw, recs)
		}
	}
	if got := len(r.meters); got != 45 {
		t.Fatalf("meter cardinality = %d, want 45", got)
	}
	allowed := make(map[string]struct{}, 45)
	for _, direction := range []string{goodbyeDirectionOutbound, goodbyeDirectionInbound, goodbyeDirectionUnknown} {
		for _, code := range goodbyeKnownCodes {
			allowed[fmt.Sprintf("p2p/goodbye/%s/%s", direction, code.String())] = struct{}{}
		}
		allowed[fmt.Sprintf("p2p/goodbye/%s/%s", direction, goodbyeCodeUnknown)] = struct{}{}
		allowed[fmt.Sprintf("p2p/goodbye/%s/%s", direction, goodbyeCodeNone)] = struct{}{}
	}
	for key := range r.meters {
		if _, ok := allowed[key]; !ok {
			t.Fatalf("raw code entered meter key: %q", key)
		}
	}
}

func TestGoodbyeKnownCodesRemainDistinct(t *testing.T) {
	r := newGoodbyeRecorder()
	for _, code := range goodbyeKnownCodes {
		r.record(GoodbyeRecord{Direction: goodbyeDirectionOutbound, Code: code.String(), CodeInt: int(code)}, "")
	}
	r.record(GoodbyeRecord{Direction: goodbyeDirectionUnknown, Code: goodbyeCodeNone, CodeInt: -1}, "")
	r.record(GoodbyeRecord{Direction: goodbyeDirectionUnknown, Code: goodbyeCodeUnknown, CodeInt: -2}, "")
	seen := map[string]int{}
	for _, rec := range r.snapshot(0) {
		seen[rec.Code] = rec.CodeInt
	}
	for _, code := range goodbyeKnownCodes {
		if got, ok := seen[code.String()]; !ok || got != int(code) {
			t.Fatalf("known code %s missing or changed: %#v", code, seen)
		}
	}
	if seen[goodbyeCodeNone] != -1 || seen[goodbyeCodeUnknown] != -2 {
		t.Fatalf("special codes changed: %#v", seen)
	}
}

func TestGoodbyeRecentPeersBoundAndEvictDeterministically(t *testing.T) {
	r := newGoodbyeRecorder()
	base := time.Unix(1000, 0).UTC()
	for i := 0; i < goodbyeRecordCapacity; i++ {
		r.record(GoodbyeRecord{At: base, Direction: goodbyeDirectionInbound, Code: common.ErrBadPeer.String()}, peer.ID(fmt.Sprintf("peer-%03d", i)))
	}
	r.record(GoodbyeRecord{At: base, Direction: goodbyeDirectionInbound, Code: common.ErrBadPeer.String()}, peer.ID("peer-999"))
	if got := len(r.recent); got != goodbyeRecordCapacity {
		t.Fatalf("recent peer count = %d, want %d", got, goodbyeRecordCapacity)
	}
	if _, ok := r.recent[peer.ID("peer-000")]; ok {
		t.Fatal("oldest peer was not deterministically evicted")
	}
	if _, ok := r.recent[peer.ID("peer-001")]; !ok {
		t.Fatal("unexpected peer eviction")
	}
}

func TestGoodbyeRecentSweepUsesFakeClockAndConsumeIsOneShot(t *testing.T) {
	r := newGoodbyeRecorder()
	base := time.Unix(2000, 0).UTC()
	first := peer.ID("expiry-peer")
	second := peer.ID("consume-peer")
	r.record(GoodbyeRecord{At: base, Direction: goodbyeDirectionInbound, Code: common.ErrBadPeer.String()}, first)
	r.record(GoodbyeRecord{At: base, Direction: goodbyeDirectionInbound, Code: common.ErrBadPeer.String()}, second)
	r.sweepRecent(base.Add(goodbyeSilentGrace + time.Nanosecond))
	if len(r.recent) != 0 {
		t.Fatalf("expired recent state retained: %d", len(r.recent))
	}
	r.record(GoodbyeRecord{At: base, Direction: goodbyeDirectionInbound, Code: common.ErrBadPeer.String()}, second)
	if !r.consumeRecentGoodbye(second, base.Add(time.Second)) {
		t.Fatal("expected recent goodbye to be consumed")
	}
	if r.consumeRecentGoodbye(second, base.Add(time.Second)) {
		t.Fatal("recent goodbye consumed more than once")
	}
	if len(r.recent) != 0 {
		t.Fatal("consumed recent state retained")
	}
}

func TestGoodbyeRecorderConcurrentRecordConsumeSweepSnapshot(t *testing.T) {
	r := newGoodbyeRecorder()
	base := time.Unix(3000, 0).UTC()
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for j := 0; j < 500; j++ {
				pid := peer.ID(fmt.Sprintf("race-%d-%d", worker, j))
				r.record(GoodbyeRecord{At: base.Add(time.Duration(j) * time.Millisecond), Direction: goodbyeDirectionInbound, Code: common.ErrGeneric.String()}, pid)
				if j%2 == 0 {
					r.consumeRecentGoodbye(pid, base.Add(time.Duration(j)*time.Millisecond+time.Nanosecond))
				}
				if j%5 == 0 {
					r.sweepRecent(base.Add(time.Duration(j) * time.Millisecond))
				}
				_ = r.snapshot(8)
			}
		}(i)
	}
	wg.Wait()
	if got := len(r.recent); got > goodbyeRecordCapacity {
		t.Fatalf("recent peer count after concurrent operations = %d", got)
	}
}
