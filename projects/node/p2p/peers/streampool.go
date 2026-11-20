package peers

import (
	"github.com/libp2p/go-libp2p/core/network"
	libp2pprotocol "github.com/libp2p/go-libp2p/core/protocol"
)

// CheckoutStream removes and returns a pooled stream for topic, if one
// exists. This is a non-blocking take-and-clear, not a checkout that waits
// for the stream to be returned: at most one caller can ever hold a given
// stream at a time. Callers that don't get one (nil) must open a fresh
// stream themselves.
func (p *Peer) CheckoutStream(topic libp2pprotocol.ID) network.Stream {
	p.streamPoolLock.Lock()
	defer p.streamPoolLock.Unlock()

	s, ok := p.streamPool[topic]
	if !ok {
		return nil
	}
	delete(p.streamPool, topic)
	return s
}

// ReturnStream deposits a still-usable stream back into the pool for reuse.
// If another concurrent caller already deposited one for the same topic
// (lost race), the incoming stream is reset rather than clobbering the
// existing pooled entry or leaking it silently.
func (p *Peer) ReturnStream(topic libp2pprotocol.ID, s network.Stream) {
	if s == nil {
		return
	}
	p.streamPoolLock.Lock()
	if p.streamPool == nil {
		p.streamPool = make(map[libp2pprotocol.ID]network.Stream)
	}
	if _, exists := p.streamPool[topic]; exists {
		p.streamPoolLock.Unlock()
		_ = s.Reset()
		return
	}
	p.streamPool[topic] = s
	p.streamPoolLock.Unlock()
}

// EvictStream drops a specific pooled stream, e.g. after a write/read on it
// failed, and resets it. Only removes it from the pool if it's still the
// exact stream instance that failed, guarding against a benign race where
// it was already checked out or replaced by another goroutine.
func (p *Peer) EvictStream(topic libp2pprotocol.ID, s network.Stream) {
	p.streamPoolLock.Lock()
	if cur, ok := p.streamPool[topic]; ok && cur == s {
		delete(p.streamPool, topic)
	}
	p.streamPoolLock.Unlock()

	if s != nil {
		_ = s.Reset()
	}
}

// CloseAllStreams tears down every pooled stream for this peer. Called on
// peer disconnect.
func (p *Peer) CloseAllStreams() {
	p.streamPoolLock.Lock()
	pool := p.streamPool
	p.streamPool = nil
	p.streamPoolLock.Unlock()

	for _, s := range pool {
		_ = s.Reset()
	}
}

// PooledStreamCountForTest reports how many streams are currently pooled
// for this peer. Read-only test introspection, safe to call in production
// code paths but not otherwise used there.
func (p *Peer) PooledStreamCountForTest() int {
	p.streamPoolLock.Lock()
	defer p.streamPoolLock.Unlock()
	return len(p.streamPool)
}
