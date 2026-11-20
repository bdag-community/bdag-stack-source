package peers

import (
	"sync"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p/core/network"
	libp2pprotocol "github.com/libp2p/go-libp2p/core/protocol"
)

// fakeStream is a minimal network.Stream stand-in for pool tests: only
// Reset() is meaningfully implemented (and observable), since pool logic
// never reads/writes through the stream directly.
type fakeStream struct {
	network.Stream
	mu         sync.Mutex
	resetCalls int
}

func (f *fakeStream) Reset() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.resetCalls++
	return nil
}

func (f *fakeStream) resetCallCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.resetCalls
}

func newFakeStream() *fakeStream {
	return &fakeStream{}
}

func testPeer() *Peer {
	return NewPeer("", nil)
}

const testTopic libp2pprotocol.ID = "/test/topic/1"

func TestCheckoutStreamOnEmptyPoolReturnsNil(t *testing.T) {
	p := testPeer()
	if s := p.CheckoutStream(testTopic); s != nil {
		t.Fatalf("expected nil from empty pool, got %v", s)
	}
}

func TestReturnThenCheckoutRoundTripsSameInstance(t *testing.T) {
	p := testPeer()
	s := newFakeStream()

	p.ReturnStream(testTopic, s)
	if got := p.PooledStreamCountForTest(); got != 1 {
		t.Fatalf("pooled count = %d, want 1", got)
	}

	got := p.CheckoutStream(testTopic)
	if got != network.Stream(s) {
		t.Fatalf("checkout returned a different instance than was returned")
	}
	if got := p.PooledStreamCountForTest(); got != 0 {
		t.Fatalf("pooled count after checkout = %d, want 0", got)
	}
}

func TestConcurrentReturnStreamOnlyOneSurvives(t *testing.T) {
	p := testPeer()
	a := newFakeStream()
	b := newFakeStream()

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		p.ReturnStream(testTopic, a)
	}()
	go func() {
		defer wg.Done()
		p.ReturnStream(testTopic, b)
	}()
	wg.Wait()

	if got := p.PooledStreamCountForTest(); got != 1 {
		t.Fatalf("pooled count = %d, want 1", got)
	}

	survivor := p.CheckoutStream(testTopic)
	// Exactly one of a/b should have been Reset (the loser), the other
	// should be the surviving pooled entry.
	aReset := a.resetCallCount()
	bReset := b.resetCallCount()
	if aReset+bReset != 1 {
		t.Fatalf("expected exactly one stream reset from the losing return, got a=%d b=%d", aReset, bReset)
	}
	if (aReset == 1 && survivor == network.Stream(a)) || (bReset == 1 && survivor == network.Stream(b)) {
		t.Fatalf("the reset (losing) stream should not be the surviving pooled entry")
	}
}

func TestEvictStreamOnlyRemovesExactInstance(t *testing.T) {
	p := testPeer()
	a := newFakeStream()
	b := newFakeStream()

	p.ReturnStream(testTopic, a)
	// Simulate a being checked out and failing, while meanwhile someone
	// else deposited b for the same topic.
	got := p.CheckoutStream(testTopic)
	if got != network.Stream(a) {
		t.Fatalf("expected to check out a")
	}
	p.ReturnStream(testTopic, b)

	p.EvictStream(testTopic, a)

	if a.resetCallCount() != 1 {
		t.Fatalf("expected a to be reset by evictStream, got %d resets", a.resetCallCount())
	}
	if b.resetCallCount() != 0 {
		t.Fatalf("b should not have been reset, since it was never the stream passed to evictStream")
	}
	if got := p.CheckoutStream(testTopic); got != network.Stream(b) {
		t.Fatalf("expected b to remain pooled after evicting a, got %v", got)
	}
}

func TestCloseAllStreamsEmptiesPoolAndResetsEverything(t *testing.T) {
	p := testPeer()
	const topicB libp2pprotocol.ID = "/test/topic/2"
	a := newFakeStream()
	b := newFakeStream()

	p.ReturnStream(testTopic, a)
	p.ReturnStream(topicB, b)

	p.CloseAllStreams()

	if got := p.PooledStreamCountForTest(); got != 0 {
		t.Fatalf("pooled count after closeAllStreams = %d, want 0", got)
	}
	if a.resetCallCount() != 1 || b.resetCallCount() != 1 {
		t.Fatalf("expected both streams reset, got a=%d b=%d", a.resetCallCount(), b.resetCallCount())
	}
}

func TestStreamPoolConcurrentUseUnderRace(t *testing.T) {
	p := testPeer()
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 100; i++ {
				s := newFakeStream()
				p.ReturnStream(testTopic, s)
				got := p.CheckoutStream(testTopic)
				if got != nil {
					// put it back so other goroutines have something to
					// contend over
					p.ReturnStream(testTopic, got)
				}
				p.EvictStream(testTopic, s)
			}
		}()
	}
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for concurrent pool stress test")
	}
}
