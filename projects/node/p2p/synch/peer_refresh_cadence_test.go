package synch

import (
	"context"
	"testing"
	"time"

	"github.com/BlockdagNetworkLabs/bdag/params"
)

func TestPeerRefreshCadenceHasMiningFreshnessMargin(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	h := newTestHost(t)
	defer h.Close()
	sy := NewSync(newCleanupTestP2P(ctx, h))
	want := params.ActiveNetParams.TargetTimePerBlock
	if sy.PeerInterval != want {
		t.Fatalf("peer refresh interval = %s, want %s inside the unchanged %s freshness budget", sy.PeerInterval, want, 3*want)
	}
}

func TestLatePeerResponseRefreshesBeforeFreshnessExpires(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	h := newTestHost(t)
	defer h.Close()
	sy := NewSync(newCleanupTestP2P(ctx, h))
	target := params.ActiveNetParams.TargetTimePerBlock
	base := time.Unix(1000, 0)
	latency := target / 10
	lastUpdated := base.Add(latency) // response arrives just after a tick
	nextTick := base.Add(sy.PeerInterval)
	for !nextTick.After(lastUpdated.Add(sy.PeerInterval)) {
		nextTick = nextTick.Add(sy.PeerInterval)
	}
	ageAtNextResponse := nextTick.Add(latency).Sub(lastUpdated)
	if ageAtNextResponse >= 3*target {
		t.Fatalf("late-response maintenance phase leaves peer age %s, freshness expires at %s", ageAtNextResponse, 3*target)
	}
}
