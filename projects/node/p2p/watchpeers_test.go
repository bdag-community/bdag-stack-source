package p2p

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"
)

func TestSchedulePeerReconnectSuppressesDuplicateDial(t *testing.T) {
	s := &Service{}
	s.InitContext()
	id := peer.ID("peer-one")
	info := peer.AddrInfo{ID: id}
	started := make(chan struct{}, 1)
	release := make(chan struct{})
	var calls int32

	dial := func(context.Context, peer.AddrInfo) error {
		atomic.AddInt32(&calls, 1)
		started <- struct{}{}
		<-release
		return nil
	}

	if !s.schedulePeerReconnectWithTimeout(info, time.Hour, dial) {
		t.Fatal("first reconnect was not scheduled")
	}
	<-started

	if s.schedulePeerReconnectWithTimeout(info, time.Hour, dial) {
		t.Fatal("duplicate reconnect was scheduled while first dial was in flight")
	}

	close(release)
	waitForReconnectTest(t, func() bool {
		return atomic.LoadInt32(&calls) == 1 && !peerReconnectInFlight(s, id)
	})
}

func TestSchedulePeerReconnectLimitsConcurrentDials(t *testing.T) {
	s := &Service{reconnectSlots: make(chan struct{}, 1)}
	s.InitContext()
	first := peer.AddrInfo{ID: peer.ID("peer-one")}
	second := peer.AddrInfo{ID: peer.ID("peer-two")}
	started := make(chan struct{}, 1)
	release := make(chan struct{})

	dial := func(context.Context, peer.AddrInfo) error {
		started <- struct{}{}
		<-release
		return nil
	}

	if !s.schedulePeerReconnectWithTimeout(first, time.Hour, dial) {
		t.Fatal("first reconnect was not scheduled")
	}
	<-started

	if s.schedulePeerReconnectWithTimeout(second, time.Hour, dial) {
		t.Fatal("second reconnect was scheduled despite full reconnect semaphore")
	}
	if peerReconnectInFlight(s, second.ID) {
		t.Fatal("second peer was left marked in flight after semaphore rejection")
	}

	close(release)
	waitForReconnectTest(t, func() bool {
		return !peerReconnectInFlight(s, first.ID)
	})
}

func TestPeerReconnectBackoffBlocksImmediateRetry(t *testing.T) {
	s := &Service{}
	id := peer.ID("peer-one")
	now := time.Unix(100, 0)

	if !s.beginPeerReconnect(id, now) {
		t.Fatal("initial reconnect did not start")
	}
	if inFlight, backoff := s.peerReconnectStateCounts(now); inFlight != 1 || backoff != 0 {
		t.Fatalf("state after begin = inFlight %d backoff %d, want 1/0", inFlight, backoff)
	}
	s.finishPeerReconnect(id, errors.New("dial failed"), now)
	if inFlight, backoff := s.peerReconnectStateCounts(now); inFlight != 0 || backoff != 1 {
		t.Fatalf("state after failure = inFlight %d backoff %d, want 0/1", inFlight, backoff)
	}

	if s.beginPeerReconnect(id, now.Add(time.Second)) {
		t.Fatal("reconnect started during failure backoff")
	}
	if !s.beginPeerReconnect(id, now.Add(peerReconnectFailureBackoff+time.Nanosecond)) {
		t.Fatal("reconnect did not start after failure backoff elapsed")
	}
	s.finishPeerReconnect(id, nil, now.Add(peerReconnectFailureBackoff+time.Second))
	if inFlight, backoff := s.peerReconnectStateCounts(now.Add(peerReconnectFailureBackoff + time.Second)); inFlight != 0 || backoff != 0 {
		t.Fatalf("state after success = inFlight %d backoff %d, want 0/0", inFlight, backoff)
	}
}

func TestSchedulePeerReconnectUsesTimeoutContext(t *testing.T) {
	s := &Service{}
	s.InitContext()
	info := peer.AddrInfo{ID: peer.ID("peer-one")}
	done := make(chan error, 1)

	dial := func(ctx context.Context, _ peer.AddrInfo) error {
		<-ctx.Done()
		done <- ctx.Err()
		return ctx.Err()
	}

	if !s.schedulePeerReconnectWithTimeout(info, 20*time.Millisecond, dial) {
		t.Fatal("reconnect was not scheduled")
	}

	select {
	case err := <-done:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("dial context error = %v, want deadline exceeded", err)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for reconnect dial context deadline")
	}
	waitForReconnectTest(t, func() bool {
		return !peerReconnectInFlight(s, info.ID)
	})
}

func peerReconnectInFlight(s *Service, id peer.ID) bool {
	s.reconnectMu.Lock()
	defer s.reconnectMu.Unlock()
	_, ok := s.reconnecting[id]
	return ok
}

func waitForReconnectTest(t *testing.T, f func() bool) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if f() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("condition was not met before deadline")
}
