package synch

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	bdagprotocol "github.com/BlockdagNetworkLabs/bdag/core/protocol"
	"github.com/BlockdagNetworkLabs/bdag/p2p/common"
	p2pcommon "github.com/BlockdagNetworkLabs/bdag/p2p/common"
	"github.com/BlockdagNetworkLabs/bdag/p2p/encoder"
	"github.com/BlockdagNetworkLabs/bdag/p2p/peers"
	pb "github.com/BlockdagNetworkLabs/bdag/p2p/proto/v1"
	libp2pcore "github.com/libp2p/go-libp2p/core"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/protocol"
)

// echoGetBlocksHandler answers RPCGetBlocks by echoing the request's
// Locator back as a DagBlocks response (the real response type for this
// topic), counting how many times it's invoked.
func echoGetBlocksHandler(rpc peers.P2PRPC, callCount *int32) rpcHandler {
	return func(ctx context.Context, msg any, stream libp2pcore.Stream, pe *peers.Peer) *common.Error {
		atomic.AddInt32(callCount, 1)
		m, ok := msg.(*pb.GetBlocks)
		if !ok {
			return ErrMessage(fmt.Errorf("message is not *pb.GetBlocks"))
		}
		return EncodeResponseMsg(rpc, stream, &pb.DagBlocks{Blocks: m.Locator}, common.ErrNone)
	}
}

// sendGetBlocks drives the package-level Send/processReqError pair exactly
// as Sync.Send does, without needing a full Sync/PeerSync -- letting these
// tests exercise Send and RegisterRPC directly against real libp2p hosts.
func sendGetBlocks(t *testing.T, rpc peers.P2PRPC, pe *peers.Peer, reuse bool, req *pb.GetBlocks) (*pb.DagBlocks, network.Stream) {
	t.Helper()
	pid := protocol.ID(getProtocol(rpc, RPCGetBlocks))

	ctx, cancel := context.WithTimeout(context.Background(), ReqTimeout)
	defer cancel()

	stream, e := Send(ctx, rpc, req, RPCGetBlocks, pe, reuse)
	if e != nil && !e.Code.IsSuccess() {
		processReqError(e, stream, pe, pid, reuse)
		t.Fatalf("Send failed: %v", e.ToError())
	}

	e2 := ReadRspCode(stream, rpc)
	var resp *pb.DagBlocks
	if e2.Code.IsSuccess() {
		resp = &pb.DagBlocks{}
		if err := DecodeMessage(stream, rpc, resp); err != nil {
			e2 = common.NewError(common.ErrStreamRead, err)
		}
	}
	streamOK := processReqError(e2, stream, pe, pid, reuse)
	if !e2.Code.IsSuccess() {
		t.Fatalf("response error: %v", e2.ToError())
	}
	_ = streamOK
	return resp, stream
}

func newLocatorGetBlocks(marker byte, n int) *pb.GetBlocks {
	locator := make([]*pb.Hash, n)
	for i := range locator {
		h := make([]byte, 32)
		h[0] = marker
		h[1] = byte(i)
		locator[i] = &pb.Hash{Hash: h}
	}
	return &pb.GetBlocks{Locator: locator}
}

func assertLocatorEqual(t *testing.T, got []*pb.Hash, want []*pb.Hash) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("locator len = %d, want %d", len(got), len(want))
	}
	for i := range want {
		if !bytes.Equal(got[i].Hash, want[i].Hash) {
			t.Fatalf("locator[%d] = %x, want %x", i, got[i].Hash, want[i].Hash)
		}
	}
}

// twoHostSetup stands up a client and server libp2p host, each wrapped in
// testP2P, connected to each other, with RPCGetBlocks registered through the
// internal reuse-aware path. The server advertises v47 so the eligible path
// is selected at stream arrival time.
type twoHostSetup struct {
	cancel     context.CancelFunc
	clientHost host.Host
	serverHost host.Host
	clientP2P  *testP2P
	serverP2P  *testP2P
	clientSync *Sync
	serverSync *Sync
	pe         *peers.Peer // client's view of the server peer
	callCount  int32
}

type reuseQuotaSetup struct {
	ctx        context.Context
	cancel     context.CancelFunc
	serverHost host.Host
	serverP2P  *testP2P
	serverSync *Sync
}

type reuseQuotaClient struct {
	host host.Host
	p2p  *testP2P
}

func newReuseQuotaSetup(t *testing.T) *reuseQuotaSetup {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	serverHost := newTestHost(t)
	t.Cleanup(func() { serverHost.Close() })
	serverP2P := newTestP2P(ctx, serverHost, &p2pcommon.Config{MaxPeers: 64, MaxInbound: 64, EnableStreamReuse: true})
	serverSync := NewSync(serverP2P)
	for _, registration := range []struct {
		topic string
		base  any
	}{
		{RPCSyncDAG, &pb.SyncDAG{}},
		{RPCContinueSyncDAG, &pb.ContinueSyncDAG{}},
		{RPCGetBlockDatas, &pb.GetBlockDatas{}},
		{RPCGetBlocks, &pb.GetBlocks{}},
	} {
		serverSync.registerRPC(registration.topic, registration.base, func(ctx context.Context, msg any, stream libp2pcore.Stream, pe *peers.Peer) *common.Error {
			return EncodeResponseMsg(serverP2P, stream, nil, common.ErrNone)
		})
	}
	return &reuseQuotaSetup{ctx: ctx, cancel: cancel, serverHost: serverHost, serverP2P: serverP2P, serverSync: serverSync}
}

func (s *reuseQuotaSetup) addClient(t *testing.T) *reuseQuotaClient {
	t.Helper()
	clientHost := newTestHost(t)
	t.Cleanup(func() { clientHost.Close() })
	clientP2P := newTestP2P(s.ctx, clientHost, &p2pcommon.Config{MaxPeers: 64, MaxInbound: 64, EnableStreamReuse: true})
	connectHosts(t, clientHost, s.serverHost)
	state := &pb.ChainState{ProtocolVersion: uint32(bdagprotocol.StreamReuseProtocolVersion), GraphState: &pb.GraphState{Total: 1}}
	clientP2P.Peers().Fetch(s.serverHost.ID()).SetChainState(state)
	s.serverP2P.Peers().Fetch(clientHost.ID()).SetChainState(state)
	return &reuseQuotaClient{host: clientHost, p2p: clientP2P}
}

func reusableRequestForTopic(topic string, marker byte) any {
	hash := &pb.Hash{Hash: make([]byte, 32)}
	hash.Hash[0] = marker
	switch topic {
	case RPCSyncDAG:
		return &pb.SyncDAG{MainLocator: []*pb.Hash{hash}, GraphState: &pb.GraphState{Total: 1}}
	case RPCContinueSyncDAG:
		return &pb.ContinueSyncDAG{SyncPoint: hash, Start: hash}
	case RPCGetBlockDatas:
		return &pb.GetBlockDatas{Locator: []*pb.Hash{hash}}
	case RPCGetBlocks:
		return newLocatorGetBlocks(marker, 1)
	default:
		panic("non-poolable test topic")
	}
}

// holdReusableIdleSlot completes the first real RPC exchange, then sends the
// first byte of a second request and leaves the rest unread on the wire.
func holdReusableIdleSlot(t *testing.T, s *reuseQuotaSetup, client *reuseQuotaClient, topic string, marker byte) network.Stream {
	t.Helper()
	stream, err := client.host.NewStream(context.Background(), s.serverHost.ID(), protocol.ID(getProtocol(client.p2p, topic)))
	if err != nil {
		t.Fatalf("open %s stream: %v", topic, err)
	}
	SetRPCStreamDeadlines(stream)
	if _, err := EncodeMessage(stream, client.p2p, reusableRequestForTopic(topic, marker)); err != nil {
		stream.Close()
		t.Fatalf("encode first %s request: %v", topic, err)
	}
	if e := ReadRspCode(stream, client.p2p); !e.Code.IsSuccess() {
		stream.Close()
		t.Fatalf("first %s response: %v", topic, e.ToError())
	}
	if _, err := stream.Write([]byte{0x80}); err != nil {
		stream.Close()
		t.Fatalf("write stalled %s first byte: %v", topic, err)
	}
	return stream
}

func newTwoHostSetup(t *testing.T) *twoHostSetup {
	return newTwoHostSetupWithPeerState(t, true)
}

func newTwoHostSetupWithPeerState(t *testing.T, advertise bool) *twoHostSetup {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	clientHost := newTestHost(t)
	serverHost := newTestHost(t)
	t.Cleanup(func() { clientHost.Close() })
	t.Cleanup(func() { serverHost.Close() })

	clientP2P := newTestP2P(ctx, clientHost, &p2pcommon.Config{MaxPeers: 10, MaxInbound: 10, EnableStreamReuse: true})
	serverP2P := newTestP2P(ctx, serverHost, &p2pcommon.Config{MaxPeers: 10, MaxInbound: 10, EnableStreamReuse: true})

	s := &twoHostSetup{
		cancel:     cancel,
		clientHost: clientHost,
		serverHost: serverHost,
		clientP2P:  clientP2P,
		serverP2P:  serverP2P,
	}

	serverSync := NewSync(serverP2P)
	clientSync := NewSync(clientP2P)
	serverSync.registerRPC(RPCGetBlocks, &pb.GetBlocks{}, echoGetBlocksHandler(serverP2P, &s.callCount))
	atomic.StoreInt32(&clientSync.peerSync.started, 1)
	s.clientSync = clientSync
	s.serverSync = serverSync

	connectHosts(t, clientHost, serverHost)
	s.pe = clientP2P.Peers().Fetch(serverHost.ID())
	s.pe.SetConnectionState(peers.PeerConnected)
	if advertise {
		state := &pb.ChainState{
			ProtocolVersion: uint32(bdagprotocol.StreamReuseProtocolVersion),
			GraphState:      &pb.GraphState{Total: 1},
		}
		s.pe.SetChainState(state)
		serverP2P.Peers().Fetch(clientHost.ID()).SetChainState(state)
	}

	return s
}

func openReusableRequest(t *testing.T, s *twoHostSetup) network.Stream {
	t.Helper()
	stream, err := s.clientHost.NewStream(context.Background(), s.serverHost.ID(), protocol.ID(getProtocol(s.clientP2P, RPCGetBlocks)))
	if err != nil {
		t.Fatalf("open reusable stream: %v", err)
	}
	SetRPCStreamDeadlines(stream)
	if _, err := EncodeMessage(stream, s.clientP2P, newLocatorGetBlocks(0xFA, 1)); err != nil {
		stream.Close()
		t.Fatalf("encode reusable request: %v", err)
	}
	if e := ReadRspCode(stream, s.clientP2P); !e.Code.IsSuccess() {
		stream.Close()
		t.Fatalf("reusable response error: %v", e.ToError())
	}
	if err := DecodeMessage(stream, s.clientP2P, &pb.DagBlocks{}); err != nil {
		stream.Close()
		t.Fatalf("decode reusable response: %v", err)
	}
	return stream
}

func TestSyncSendUsesNegotiatedReuseEligibility(t *testing.T) {
	s := newTwoHostSetup(t)
	first, err := s.clientSync.Send(s.pe, RPCGetBlocks, newLocatorGetBlocks(0xD1, 1))
	if err != nil {
		t.Fatalf("first Sync.Send: %v", err)
	}
	second, err := s.clientSync.Send(s.pe, RPCGetBlocks, newLocatorGetBlocks(0xD2, 1))
	if err != nil {
		t.Fatalf("second Sync.Send: %v", err)
	}
	if _, ok := first.(*pb.DagBlocks); !ok {
		t.Fatalf("first response type = %T", first)
	}
	if _, ok := second.(*pb.DagBlocks); !ok {
		t.Fatalf("second response type = %T", second)
	}
	if got := s.pe.PooledStreamCountForTest(); got != 1 {
		t.Fatalf("pooled stream count = %d, want 1", got)
	}
}

func TestSyncSendRolloverAfterMaximumRequests(t *testing.T) {
	s := newTwoHostSetup(t)
	pid := protocol.ID(getProtocol(s.clientP2P, RPCGetBlocks))
	var firstID string
	for i := 0; i < streamReuseMaxRequests+1; i++ {
		resp, err := s.clientSync.Send(s.pe, RPCGetBlocks, newLocatorGetBlocks(byte(i), 1))
		if err != nil {
			t.Fatalf("Sync.Send request %d: %v", i+1, err)
		}
		if _, ok := resp.(*pb.DagBlocks); !ok {
			t.Fatalf("request %d response type = %T", i+1, resp)
		}
		if i == streamReuseMaxRequests-2 {
			stream := s.pe.CheckoutStream(pid)
			if stream == nil {
				t.Fatal("request 127 did not leave a reusable stream to roll over")
			}
			firstID = stream.ID()
			s.pe.ReturnStream(pid, stream)
		}
	}
	if got := atomic.LoadInt32(&s.callCount); got != streamReuseMaxRequests+1 {
		t.Fatalf("handler call count = %d, want %d", got, streamReuseMaxRequests+1)
	}
	if got := s.pe.PooledStreamCountForTest(); got != 1 {
		t.Fatalf("pooled stream count after rollover = %d, want 1", got)
	}
	stream := s.pe.CheckoutStream(pid)
	if stream == nil {
		t.Fatal("request 129 did not open a replacement reusable stream")
	}
	defer s.pe.ReturnStream(pid, stream)
	if stream.ID() == firstID {
		t.Fatalf("request 129 reused stream %q after request 128 rollover", stream.ID())
	}
}

func TestSyncStopClearsOutboundReuseLifecycle(t *testing.T) {
	s := newTwoHostSetup(t)
	if _, err := s.clientSync.Send(s.pe, RPCGetBlocks, newLocatorGetBlocks(0xD3, 1)); err != nil {
		t.Fatalf("Sync.Send: %v", err)
	}
	if got := s.clientSync.outboundReuse.activeEntries(); got != 1 {
		t.Fatalf("outbound entries before Stop = %d, want 1", got)
	}
	if err := s.clientSync.Stop(); err != nil {
		t.Fatalf("Sync.Stop: %v", err)
	}
	if got := s.clientSync.outboundReuse.activeEntries(); got != 0 {
		t.Fatalf("outbound entries after Stop = %d, want 0", got)
	}
	if got := s.pe.PooledStreamCountForTest(); got != 0 {
		t.Fatalf("pooled streams after Stop = %d, want 0", got)
	}
}

func TestOutboundReuseDisconnectClearsLifecycle(t *testing.T) {
	s := newTwoHostSetup(t)
	s.clientSync.addOutboundReuseDisconnectHandler()
	if _, err := s.clientSync.Send(s.pe, RPCGetBlocks, newLocatorGetBlocks(0xD4, 1)); err != nil {
		t.Fatalf("Sync.Send: %v", err)
	}
	if got := s.clientSync.outboundReuse.activeEntries(); got != 1 {
		t.Fatalf("outbound entries before disconnect = %d, want 1", got)
	}
	if err := s.clientHost.Network().ClosePeer(s.serverHost.ID()); err != nil {
		t.Fatalf("ClosePeer: %v", err)
	}
	waitFor(t, func() bool { return s.clientSync.outboundReuse.activeEntries() == 0 })
	if got := s.pe.PooledStreamCountForTest(); got != 0 {
		t.Fatalf("pooled streams after disconnect = %d, want 0", got)
	}
}

func TestOutboundReuseCollisionForgetsCounter(t *testing.T) {
	s := newTwoHostSetup(t)
	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	s.serverSync.registerRPC(RPCGetBlocks, &pb.GetBlocks{}, func(ctx context.Context, msg any, stream libp2pcore.Stream, pe *peers.Peer) *common.Error {
		entered <- struct{}{}
		<-release
		return EncodeResponseMsg(s.serverP2P, stream, &pb.DagBlocks{}, common.ErrNone)
	})
	result := make(chan error, 1)
	go func() {
		_, err := s.clientSync.Send(s.pe, RPCGetBlocks, newLocatorGetBlocks(0xD5, 1))
		result <- err
	}()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("handler did not receive reusable request")
	}
	pid := protocol.ID(getProtocol(s.clientP2P, RPCGetBlocks))
	collision, err := s.clientHost.NewStream(context.Background(), s.serverHost.ID(), pid)
	if err != nil {
		t.Fatalf("open collision stream: %v", err)
	}
	defer collision.Close()
	s.pe.ReturnStream(pid, collision)
	close(release)
	if err := <-result; err != nil {
		t.Fatalf("Sync.Send: %v", err)
	}
	if got := s.clientSync.outboundReuse.activeEntries(); got != 0 {
		t.Fatalf("outbound entries after pool collision = %d, want 0", got)
	}
}

func TestSyncReuseConcurrentDisconnectLeavesNoLifecycleState(t *testing.T) {
	s := newTwoHostSetup(t)
	s.clientSync.addOutboundReuseDisconnectHandler()
	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	s.serverSync.registerRPC(RPCGetBlocks, &pb.GetBlocks{}, func(ctx context.Context, msg any, stream libp2pcore.Stream, pe *peers.Peer) *common.Error {
		entered <- struct{}{}
		<-release
		return EncodeResponseMsg(s.serverP2P, stream, &pb.DagBlocks{}, common.ErrNone)
	})
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = s.clientSync.Send(s.pe, RPCGetBlocks, newLocatorGetBlocks(0xD6, 1))
	}()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("reusable request did not reach handler")
	}
	if err := s.clientHost.Network().ClosePeer(s.serverHost.ID()); err != nil {
		t.Fatalf("ClosePeer: %v", err)
	}
	close(release)
	<-done
	waitFor(t, func() bool { return s.clientSync.outboundReuse.activeEntries() == 0 })
}

func TestSendReusesStreamForPoolableTopic(t *testing.T) {
	s := newTwoHostSetup(t)

	req1 := newLocatorGetBlocks(0xAA, 3)
	resp1, stream1 := sendGetBlocks(t, s.clientP2P, s.pe, true, req1)
	assertLocatorEqual(t, resp1.Blocks, req1.Locator)

	if got := s.pe.PooledStreamCountForTest(); got != 1 {
		t.Fatalf("pooled stream count after first reused call = %d, want 1", got)
	}

	req2 := newLocatorGetBlocks(0xBB, 5)
	resp2, stream2 := sendGetBlocks(t, s.clientP2P, s.pe, true, req2)
	assertLocatorEqual(t, resp2.Blocks, req2.Locator)

	if stream1.ID() != stream2.ID() {
		t.Fatalf("expected the same pooled stream to be reused: first=%s second=%s", stream1.ID(), stream2.ID())
	}
	if got := s.pe.PooledStreamCountForTest(); got != 1 {
		t.Fatalf("pooled stream count after second reused call = %d, want 1", got)
	}
	if got := atomic.LoadInt32(&s.callCount); got != 2 {
		t.Fatalf("handler call count = %d, want 2", got)
	}
}

func TestSendWithoutReuseOpensFreshStreamEachTime(t *testing.T) {
	s := newTwoHostSetup(t)

	req1 := newLocatorGetBlocks(0xAA, 2)
	_, stream1 := sendGetBlocks(t, s.clientP2P, s.pe, false, req1)

	req2 := newLocatorGetBlocks(0xBB, 2)
	_, stream2 := sendGetBlocks(t, s.clientP2P, s.pe, false, req2)

	if stream1.ID() == stream2.ID() {
		t.Fatalf("expected distinct streams when reuse is disabled, got the same stream twice: %s", stream1.ID())
	}
	if got := s.pe.PooledStreamCountForTest(); got != 0 {
		t.Fatalf("pooled stream count with reuse disabled = %d, want 0", got)
	}
}

// oneShotGetBlocksHandler simulates an older-binary peer: decode one
// request, respond, then fully close -- never looping for a second request.
// Used to verify Send's fallback path when a pooled stream turns out to be
// stale.
func registerOneShotGetBlocks(t *testing.T, rpc peers.P2PRPC, callCount *int32) {
	t.Helper()
	topic := getProtocol(rpc, RPCGetBlocks)
	rpc.Host().SetStreamHandler(protocol.ID(topic), func(stream network.Stream) {
		defer stream.Close()
		SetRPCStreamDeadlines(stream)
		msg := &pb.GetBlocks{}
		if err := DecodeMessage(stream, rpc, msg); err != nil {
			return
		}
		atomic.AddInt32(callCount, 1)
		_ = EncodeResponseMsg(rpc, stream, &pb.DagBlocks{Blocks: msg.Locator}, common.ErrNone)
		_ = stream.CloseWrite()
	})
}

// TestSendEvictsStaleStreamOnReadFailureAndSubsequentCallSucceeds exercises
// the fallback path against a one-shot ("old-binary") peer: it always
// fully closes the stream right after answering, never looping for a
// second request. Per the documented design (see Send's doc comment), a
// write failure on a reused stream is safe to retry transparently (nothing
// was sent yet), but a failure discovered while reading the response is
// NOT retried by Send itself -- the request may have already landed, so
// blind retry isn't provably safe at this layer. It surfaces as a normal
// request error, with eviction restoring pool health so a subsequent
// attempt (mirroring the sync loop's own pre-existing retry-on-failure
// logic, e.g. ProcessResultActionTryAgain) succeeds.
func TestSendEvictsStaleStreamOnReadFailureAndSubsequentCallSucceeds(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	clientHost := newTestHost(t)
	defer clientHost.Close()
	serverHost := newTestHost(t)
	defer serverHost.Close()

	clientP2P := newTestP2P(ctx, clientHost, &p2pcommon.Config{MaxPeers: 10, MaxInbound: 10})
	serverP2P := newTestP2P(ctx, serverHost, &p2pcommon.Config{MaxPeers: 10, MaxInbound: 10})

	var callCount int32
	registerOneShotGetBlocks(t, serverP2P, &callCount)

	connectHosts(t, clientHost, serverHost)
	pe := clientP2P.Peers().Fetch(serverHost.ID())
	pe.SetConnectionState(peers.PeerConnected)

	req1 := newLocatorGetBlocks(0xAA, 2)
	resp1, _ := sendGetBlocks(t, clientP2P, pe, true, req1)
	assertLocatorEqual(t, resp1.Blocks, req1.Locator)

	// The server already fully closed after responding (one-shot,
	// old-binary-style), but Send's processReqError still pooled the
	// stream since, from the client's perspective, the round trip
	// succeeded transport-wise.
	if got := pe.PooledStreamCountForTest(); got != 1 {
		t.Fatalf("pooled stream count = %d, want 1", got)
	}

	// Second call: writing to the stale pooled stream may or may not
	// error immediately (mux-dependent), but reading a response from it
	// must fail, since the server already tore it down.
	pid := protocol.ID(getProtocol(clientP2P, RPCGetBlocks))
	sendCtx, sendCancel := context.WithTimeout(context.Background(), ReqTimeout)
	req2 := newLocatorGetBlocks(0xBB, 4)
	stream, e := Send(sendCtx, clientP2P, req2, RPCGetBlocks, pe, true)
	callFailed := false
	if e != nil && !e.Code.IsSuccess() {
		processReqError(e, stream, pe, pid, true)
		callFailed = true
	} else {
		e2 := ReadRspCode(stream, clientP2P)
		if e2.Code.IsSuccess() {
			resp := &pb.DagBlocks{}
			if err := DecodeMessage(stream, clientP2P, resp); err != nil {
				e2 = common.NewError(common.ErrStreamRead, err)
			}
		}
		processReqError(e2, stream, pe, pid, true)
		callFailed = !e2.Code.IsSuccess()
	}
	sendCancel()
	if !callFailed {
		t.Fatalf("expected the request against the stale pooled stream to fail")
	}

	// Pool health: the stale stream must have been evicted, not left
	// dangling in the pool.
	if got := pe.PooledStreamCountForTest(); got != 0 {
		t.Fatalf("pooled stream count after stale-stream failure = %d, want 0 (evicted)", got)
	}

	// A subsequent attempt -- mirroring the outer sync loop's own retry --
	// succeeds normally, since Send now has nothing pooled and opens fresh.
	resp3, _ := sendGetBlocks(t, clientP2P, pe, true, req2)
	assertLocatorEqual(t, resp3.Blocks, req2.Locator)

	if got := atomic.LoadInt32(&callCount); got != 2 {
		t.Fatalf("handler call count = %d, want 2 (the stale attempt should never have reached a handler, only the first request and the successful retry)", got)
	}
}

func TestSyncSendStaleEvictionClearsOutboundReuseLifecycle(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	clientHost := newTestHost(t)
	defer clientHost.Close()
	serverHost := newTestHost(t)
	defer serverHost.Close()
	clientP2P := newTestP2P(ctx, clientHost, &p2pcommon.Config{MaxPeers: 10, MaxInbound: 10, EnableStreamReuse: true})
	serverP2P := newTestP2P(ctx, serverHost, &p2pcommon.Config{MaxPeers: 10, MaxInbound: 10})
	var callCount int32
	registerOneShotGetBlocks(t, serverP2P, &callCount)
	connectHosts(t, clientHost, serverHost)
	pe := clientP2P.Peers().Fetch(serverHost.ID())
	pe.SetConnectionState(peers.PeerConnected)
	pe.SetChainState(&pb.ChainState{ProtocolVersion: uint32(bdagprotocol.StreamReuseProtocolVersion), GraphState: &pb.GraphState{Total: 1}})
	clientSync := NewSync(clientP2P)
	atomic.StoreInt32(&clientSync.peerSync.started, 1)

	if _, err := clientSync.Send(pe, RPCGetBlocks, newLocatorGetBlocks(0xAC, 1)); err != nil {
		t.Fatalf("first Sync.Send: %v", err)
	}
	if got := clientSync.outboundReuse.activeEntries(); got != 1 {
		t.Fatalf("outbound entries after first response = %d, want 1", got)
	}
	if _, err := clientSync.Send(pe, RPCGetBlocks, newLocatorGetBlocks(0xAD, 1)); err == nil {
		t.Fatal("stale pooled stream unexpectedly completed")
	}
	if got := clientSync.outboundReuse.activeEntries(); got != 0 {
		t.Fatalf("outbound entries after stale eviction = %d, want 0", got)
	}
	if got := pe.PooledStreamCountForTest(); got != 0 {
		t.Fatalf("pooled streams after stale eviction = %d, want 0", got)
	}
	if _, err := clientSync.Send(pe, RPCGetBlocks, newLocatorGetBlocks(0xAE, 1)); err != nil {
		t.Fatalf("recovery Sync.Send: %v", err)
	}
}

type sendRetryTestP2P struct {
	*testP2P
	encoding encoder.NetworkEncoding
}

func (p *sendRetryTestP2P) Encoding() encoder.NetworkEncoding { return p.encoding }

func newSendRetryFixture(t *testing.T, freshWriteErr error) (*Sync, *peers.Peer, *testStream, *testStream, *int32) {
	t.Helper()
	stale := &testStream{writeErr: errors.New("cached stream write failed")}
	fresh := &testStream{readData: []byte{byte(common.ErrNone)}, writeErr: freshWriteErr}
	newStreams := int32(0)
	h := &testHost{
		network: &testNetwork{connectedness: network.Connected},
		newStreamFunc: func() (network.Stream, error) {
			atomic.AddInt32(&newStreams, 1)
			return fresh, nil
		},
	}
	base := &testP2P{
		ctx:  context.Background(),
		host: h,
		cfg:  &p2pcommon.Config{EnableStreamReuse: true},
	}
	p2p := &sendRetryTestP2P{testP2P: base, encoding: fakeEncoding{}}
	base.peers = peers.NewStatus(p2p)
	s := NewSync(p2p)
	atomic.StoreInt32(&s.peerSync.started, 1)
	pe := p2p.Peers().Fetch(peer.ID("retry-peer"))
	pe.SetConnectionState(peers.PeerConnected)
	pe.SetChainState(&pb.ChainState{
		ProtocolVersion: uint32(bdagprotocol.StreamReuseProtocolVersion),
		GraphState:      &pb.GraphState{Total: 1},
	})
	pid := protocol.ID(getProtocol(p2p, RPCGetBlocks))
	pe.ReturnStream(pid, stale)
	t.Cleanup(func() {
		s.outboundReuse.stop()
		pe.EvictStream(pid, fresh)
	})
	return s, pe, stale, fresh, &newStreams
}

func TestSyncSendCachedWriteFailureRetriesExactlyOnceWithFreshSuccess(t *testing.T) {
	s, pe, stale, fresh, newStreams := newSendRetryFixture(t, nil)
	ret, err := s.Send(pe, RPCGetBlocks, newLocatorGetBlocks(0xE7, 1))
	if err != nil {
		t.Fatalf("Sync.Send: %v", err)
	}
	if _, ok := ret.(*pb.DagBlocks); !ok {
		t.Fatalf("response type = %T, want *pb.DagBlocks", ret)
	}
	if stale.writeCalls != 1 || stale.resetCalls != 1 {
		t.Fatalf("cached stream write/reset = %d/%d, want 1/1", stale.writeCalls, stale.resetCalls)
	}
	if fresh.writeCalls != 1 || fresh.resetCalls != 0 {
		t.Fatalf("fresh stream write/reset = %d/%d, want 1/0", fresh.writeCalls, fresh.resetCalls)
	}
	if got := atomic.LoadInt32(newStreams); got != 1 {
		t.Fatalf("fresh stream attempts = %d, want exactly 1", got)
	}
	if got := s.outboundReuse.activeEntries(); got != 1 {
		t.Fatalf("outbound tracker entries = %d, want 1 pooled fresh stream", got)
	}
	if got := pe.PooledStreamCountForTest(); got != 1 {
		t.Fatalf("peer pooled streams = %d, want 1", got)
	}
}

func TestSyncSendCachedWriteFailureFreshTerminalWriteFailureStops(t *testing.T) {
	s, pe, stale, fresh, newStreams := newSendRetryFixture(t, errors.New("fresh stream write failed"))
	if _, err := s.Send(pe, RPCGetBlocks, newLocatorGetBlocks(0xE8, 1)); err == nil {
		t.Fatal("Sync.Send unexpectedly succeeded after terminal fresh write failure")
	}
	if stale.writeCalls != 1 || stale.resetCalls != 1 {
		t.Fatalf("cached stream write/reset = %d/%d, want 1/1", stale.writeCalls, stale.resetCalls)
	}
	if fresh.writeCalls != 1 || fresh.resetCalls != 1 {
		t.Fatalf("fresh stream write/reset = %d/%d, want 1/1", fresh.writeCalls, fresh.resetCalls)
	}
	if got := atomic.LoadInt32(newStreams); got != 1 {
		t.Fatalf("fresh stream attempts = %d, want exactly 1", got)
	}
	if got := s.outboundReuse.activeEntries(); got != 0 {
		t.Fatalf("outbound tracker entries = %d, want 0 after terminal failure", got)
	}
	if got := pe.PooledStreamCountForTest(); got != 0 {
		t.Fatalf("peer pooled streams = %d, want 0 after terminal failure", got)
	}
}

func TestSendConcurrentCallersDoNotInterleave(t *testing.T) {
	s := newTwoHostSetup(t)

	const goroutines = 8
	const itersPerGoroutine = 20

	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		g := g
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < itersPerGoroutine; i++ {
				req := newLocatorGetBlocks(byte(g), 3+i%5)
				resp, _ := sendGetBlocks(t, s.clientP2P, s.pe, false, req)
				assertLocatorEqual(t, resp.Blocks, req.Locator)
			}
		}()
	}
	wg.Wait()

	if got := atomic.LoadInt32(&s.callCount); got != goroutines*itersPerGoroutine {
		t.Fatalf("handler call count = %d, want %d", got, goroutines*itersPerGoroutine)
	}
}

func TestRegisterRPCPublicPathIsOneShot(t *testing.T) {
	s := newTwoHostSetup(t)
	RegisterRPC(s.serverP2P, RPCGetBlocks, &pb.GetBlocks{}, echoGetBlocksHandler(s.serverP2P, &s.callCount))

	stream, err := s.clientHost.NewStream(context.Background(), s.serverHost.ID(), protocol.ID(getProtocol(s.clientP2P, RPCGetBlocks)))
	if err != nil {
		t.Fatalf("open manual stream: %v", err)
	}
	defer stream.Close()
	SetRPCStreamDeadlines(stream)
	req := newLocatorGetBlocks(0xA1, 2)
	if _, err := EncodeMessage(stream, s.clientP2P, req); err != nil {
		t.Fatalf("encode: %v", err)
	}
	if e := ReadRspCode(stream, s.clientP2P); !e.Code.IsSuccess() {
		t.Fatalf("response error: %v", e.ToError())
	}
	resp := &pb.DagBlocks{}
	if err := DecodeMessage(stream, s.clientP2P, resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	assertLocatorEqual(t, resp.Blocks, req.Locator)
	second := newLocatorGetBlocks(0xA2, 2)
	_, _ = EncodeMessage(stream, s.clientP2P, second)
	SetStreamReadDeadline(stream, time.Second)
	var extra [1]byte
	if _, err := stream.Read(extra[:]); err == nil {
		t.Fatal("public RegisterRPC kept the stream open after one response")
	}
	if got := atomic.LoadInt32(&s.callCount); got != 1 {
		t.Fatalf("public RegisterRPC executed %d requests, want 1", got)
	}
}

func assertInboundOneShot(t *testing.T, topic string, base, request any, localEnable bool, version uint32, advertise bool) {
	t.Helper()
	s := newTwoHostSetupWithPeerState(t, advertise)
	s.serverP2P.cfg.EnableStreamReuse = localEnable
	if version != 0 {
		s.serverP2P.Peers().Fetch(s.clientHost.ID()).SetChainState(&pb.ChainState{
			ProtocolVersion: version,
			GraphState:      &pb.GraphState{Total: 1},
		})
	}
	s.serverSync.registerRPC(topic, base, func(ctx context.Context, msg any, stream libp2pcore.Stream, pe *peers.Peer) *common.Error {
		atomic.AddInt32(&s.callCount, 1)
		return EncodeResponseMsg(s.serverP2P, stream, nil, common.ErrNone)
	})
	stream, err := s.clientHost.NewStream(context.Background(), s.serverHost.ID(), protocol.ID(getProtocol(s.clientP2P, topic)))
	if err != nil {
		t.Fatalf("open %s stream: %v", topic, err)
	}
	defer stream.Close()
	SetRPCStreamDeadlines(stream)
	if _, err := EncodeMessage(stream, s.clientP2P, request); err != nil {
		t.Fatalf("encode %s request: %v", topic, err)
	}
	if e := ReadRspCode(stream, s.clientP2P); !e.Code.IsSuccess() {
		t.Fatalf("%s response error: %v", topic, e.ToError())
	}
	_, _ = EncodeMessage(stream, s.clientP2P, request)
	SetStreamReadDeadline(stream, time.Second)
	var extra [1]byte
	if _, err := stream.Read(extra[:]); err == nil {
		t.Fatalf("%s handler kept stream open", topic)
	}
	if got := atomic.LoadInt32(&s.callCount); got != 1 {
		t.Fatalf("%s handler executed %d requests, want 1", topic, got)
	}
}

func TestInboundReuseEligibilityDenialsAreOneShot(t *testing.T) {
	oldVersion := uint32(bdagprotocol.StreamReuseProtocolVersion - 1)
	assertInboundOneShot(t, RPCGetBlocks, &pb.GetBlocks{}, newLocatorGetBlocks(0xE1, 1), false, uint32(bdagprotocol.StreamReuseProtocolVersion), true)
	assertInboundOneShot(t, RPCGetBlocks, &pb.GetBlocks{}, newLocatorGetBlocks(0xE2, 1), true, oldVersion, true)
	assertInboundOneShot(t, RPCGetBlocks, &pb.GetBlocks{}, newLocatorGetBlocks(0xE3, 1), true, 0, false)
	assertInboundOneShot(t, RPCChainState, &pb.ChainState{}, &pb.ChainState{
		GenesisHash: &pb.Hash{Hash: make([]byte, 32)},
		GraphState:  &pb.GraphState{},
	}, true, uint32(bdagprotocol.StreamReuseProtocolVersion), true)
}

func TestStreamReuseLimiterBoundsAndCleansUp(t *testing.T) {
	limiter := newStreamReuseLimiter()
	for i := 0; i < streamReuseMaxPerTopic; i++ {
		if !limiter.acquire(peer.ID(fmt.Sprintf("topic-peer-%d", i)), RPCGetBlocks) {
			t.Fatalf("acquire %d unexpectedly rejected", i)
		}
	}
	if limiter.acquire(peer.ID("topic-peer-overflow"), RPCGetBlocks) {
		t.Fatal("per-topic quota accepted an extra stream")
	}
	if limiter.acquire(peer.ID("topic-peer-0"), RPCGetBlocks) {
		t.Fatal("duplicate peer/topic slot accepted")
	}
	limiter.release(peer.ID("topic-peer-0"), RPCGetBlocks)
	if limiter.acquire(peer.ID("topic-peer-replacement"), RPCGetBlocks) == false {
		t.Fatal("released per-topic slot was not reusable")
	}

	limiter = newStreamReuseLimiter()
	topics := []string{RPCSyncDAG, RPCContinueSyncDAG, RPCGetBlockDatas, RPCGetBlocks}
	for _, topic := range topics {
		for i := 0; i < streamReuseMaxPerTopic; i++ {
			if !limiter.acquire(peer.ID(fmt.Sprintf("%s-peer-%d", topic, i)), topic) {
				t.Fatalf("acquire for %s/%d unexpectedly rejected", topic, i)
			}
		}
	}
	if limiter.total != streamReuseMaxTotal {
		t.Fatalf("total active slots = %d, want %d", limiter.total, streamReuseMaxTotal)
	}
	if limiter.acquire(peer.ID("total-overflow"), RPCGetBlocks) {
		t.Fatal("global quota accepted an extra stream")
	}
	limiter.release(peer.ID(fmt.Sprintf("%s-peer-%d", RPCGetBlocks, 0)), RPCGetBlocks)
	if limiter.total != streamReuseMaxTotal-1 {
		t.Fatalf("total active slots after release = %d, want %d", limiter.total, streamReuseMaxTotal-1)
	}
	for pid, peerTopics := range limiter.peerTopics {
		for topic := range peerTopics {
			limiter.release(pid, topic)
		}
	}
	if limiter.total != 0 || len(limiter.peerTopics) != 0 || len(limiter.topicUses) != 0 || len(limiter.streams) != 0 {
		t.Fatalf("limiter retained state after release: total=%d peers=%d topics=%d streams=%d", limiter.total, len(limiter.peerTopics), len(limiter.topicUses), len(limiter.streams))
	}
}

func TestStreamReuseLimiterConcurrentAcquireRelease(t *testing.T) {
	limiter := newStreamReuseLimiter()
	var wg sync.WaitGroup
	for i := 0; i < 64; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			pid := peer.ID(fmt.Sprintf("race-peer-%d", i%16))
			topic := []string{RPCSyncDAG, RPCContinueSyncDAG, RPCGetBlockDatas, RPCGetBlocks}[i%4]
			if limiter.acquire(pid, topic) {
				limiter.release(pid, topic)
			}
		}(i)
	}
	wg.Wait()
	if limiter.total < 0 || limiter.total > streamReuseMaxTotal {
		t.Fatalf("invalid total after concurrent operations: %d", limiter.total)
	}
}

func TestStreamReuseLimiterFairnessAcrossPeersAndTopics(t *testing.T) {
	limiter := newStreamReuseLimiter()
	topics := []string{RPCSyncDAG, RPCContinueSyncDAG, RPCGetBlockDatas, RPCGetBlocks}
	attacker := peer.ID("attacker")
	legitimate := peer.ID("legitimate")
	for _, topic := range topics {
		if !limiter.acquire(attacker, topic) {
			t.Fatalf("attacker could not acquire %s", topic)
		}
	}
	for _, topic := range topics {
		if !limiter.acquire(legitimate, topic) {
			t.Fatalf("legitimate peer lost capacity for %s", topic)
		}
	}
}

func TestReusableStreamsEnforceRealEightPerTopicAndThirtyTwoGlobalSlots(t *testing.T) {
	s := newReuseQuotaSetup(t)
	clients := make([]*reuseQuotaClient, streamReuseMaxPerTopic+1)
	for i := range clients {
		clients[i] = s.addClient(t)
	}
	streams := make([]network.Stream, 0, streamReuseMaxTotal)
	defer func() {
		for _, stream := range streams {
			_ = stream.Reset()
		}
	}()
	topics := []string{RPCSyncDAG, RPCContinueSyncDAG, RPCGetBlockDatas, RPCGetBlocks}
	for _, topic := range topics {
		for i := 0; i < streamReuseMaxPerTopic; i++ {
			streams = append(streams, holdReusableIdleSlot(t, s, clients[i], topic, byte(i)))
			wantTopic := i + 1
			waitFor(t, func() bool { return s.serverSync.streamReuse.activeTopicSlots(topic) == wantTopic })
		}
	}
	if got := s.serverSync.streamReuse.activeSlots(); got != streamReuseMaxTotal {
		t.Fatalf("real held reusable slots = %d, want %d", got, streamReuseMaxTotal)
	}

	quotaMiss := holdReusableIdleSlot(t, s, clients[streamReuseMaxPerTopic], RPCGetBlocks, 0xF1)
	defer quotaMiss.Close()
	SetStreamReadDeadline(quotaMiss, time.Second)
	var one [1]byte
	if _, err := quotaMiss.Read(one[:]); err == nil {
		t.Fatal("quota-miss stream remained open after completed first response")
	}
	if got := s.serverSync.streamReuse.activeSlots(); got != streamReuseMaxTotal {
		t.Fatalf("quota miss changed held-slot count to %d, want %d", got, streamReuseMaxTotal)
	}
	serverPeer := s.serverP2P.Peers().Fetch(clients[streamReuseMaxPerTopic].host.ID())
	if got := len(serverPeer.BadResponses()); got != 0 {
		t.Fatalf("quota miss penalized peer with %d bad responses", got)
	}
	if got := s.serverHost.Network().Connectedness(clients[streamReuseMaxPerTopic].host.ID()); got != network.Connected {
		t.Fatalf("quota miss disconnected peer: %s", got)
	}
}

func TestReusableFourTopicAttackerLeavesRealCapacityForLegitimatePeer(t *testing.T) {
	s := newReuseQuotaSetup(t)
	attacker := s.addClient(t)
	legitimate := s.addClient(t)
	topics := []string{RPCSyncDAG, RPCContinueSyncDAG, RPCGetBlockDatas, RPCGetBlocks}
	streams := make([]network.Stream, 0, len(topics)*2)
	defer func() {
		for _, stream := range streams {
			_ = stream.Reset()
		}
	}()
	for _, topic := range topics {
		streams = append(streams, holdReusableIdleSlot(t, s, attacker, topic, 0xA1))
		waitFor(t, func() bool { return s.serverSync.streamReuse.activeTopicSlots(topic) == 1 })
	}
	for _, topic := range topics {
		streams = append(streams, holdReusableIdleSlot(t, s, legitimate, topic, 0xB1))
		waitFor(t, func() bool { return s.serverSync.streamReuse.activeTopicSlots(topic) == 2 })
	}
	if got := s.serverSync.streamReuse.activeSlots(); got != len(streams) {
		t.Fatalf("held attacker/legitimate slots = %d, want %d", got, len(streams))
	}
}

func TestReusableOneByteStallConsumesOnlyOnePeerTopicSlot(t *testing.T) {
	s := newTwoHostSetup(t)
	first := openReusableRequest(t, s)
	defer first.Close()
	if _, err := first.Write([]byte{1}); err != nil {
		t.Fatalf("write stalled first byte: %v", err)
	}
	waitFor(t, func() bool { return s.serverSync.streamReuse.activeSlots() == 1 })
	second := openReusableRequest(t, s)
	defer second.Close()
	if got := s.serverSync.streamReuse.activeSlots(); got != 1 {
		t.Fatalf("active slots after same-peer second stream = %d, want 1", got)
	}
	_ = first.Reset()
	waitFor(t, func() bool { return s.serverSync.streamReuse.activeSlots() == 0 })
}

func TestRegisterRPCHandlesMultipleSequentialRequestsOnOneManualStream(t *testing.T) {
	s := newTwoHostSetup(t)

	stream, err := s.clientHost.NewStream(context.Background(), s.serverHost.ID(), protocol.ID(getProtocol(s.clientP2P, RPCGetBlocks)))
	if err != nil {
		t.Fatalf("open manual stream: %v", err)
	}
	defer stream.Close()
	SetRPCStreamDeadlines(stream)

	for i := 0; i < 3; i++ {
		req := newLocatorGetBlocks(byte(i+1), 2+i)
		if _, err := EncodeMessage(stream, s.clientP2P, req); err != nil {
			t.Fatalf("iter %d encode: %v", i, err)
		}
		e := ReadRspCode(stream, s.clientP2P)
		if !e.Code.IsSuccess() {
			t.Fatalf("iter %d response error: %v", i, e.ToError())
		}
		resp := &pb.DagBlocks{}
		if err := DecodeMessage(stream, s.clientP2P, resp); err != nil {
			t.Fatalf("iter %d decode: %v", i, err)
		}
		assertLocatorEqual(t, resp.Blocks, req.Locator)
	}

	if got := atomic.LoadInt32(&s.callCount); got != 3 {
		t.Fatalf("handler call count = %d, want 3", got)
	}
	if err := stream.Close(); err != nil {
		t.Fatalf("close reusable stream: %v", err)
	}
	waitFor(t, func() bool { return s.serverSync.streamReuse.activeSlots() == 0 })
}

func TestRegisterRPCClosesAfterMaximumReusableRequests(t *testing.T) {
	s := newTwoHostSetup(t)
	stream, err := s.clientHost.NewStream(context.Background(), s.serverHost.ID(), protocol.ID(getProtocol(s.clientP2P, RPCGetBlocks)))
	if err != nil {
		t.Fatalf("open manual stream: %v", err)
	}
	defer stream.Close()
	SetRPCStreamDeadlines(stream)
	for i := 0; i < streamReuseMaxRequests; i++ {
		req := newLocatorGetBlocks(byte(i), 1)
		if _, err := EncodeMessage(stream, s.clientP2P, req); err != nil {
			t.Fatalf("iter %d encode: %v", i, err)
		}
		if e := ReadRspCode(stream, s.clientP2P); !e.Code.IsSuccess() {
			t.Fatalf("iter %d response error: %v", i, e.ToError())
		}
		resp := &pb.DagBlocks{}
		if err := DecodeMessage(stream, s.clientP2P, resp); err != nil {
			t.Fatalf("iter %d decode: %v", i, err)
		}
		assertLocatorEqual(t, resp.Blocks, req.Locator)
	}
	SetStreamReadDeadline(stream, time.Second)
	var extra [1]byte
	if _, err := stream.Read(extra[:]); err == nil {
		t.Fatal("reusable stream remained open after request rollover")
	}
	if got := atomic.LoadInt32(&s.callCount); got != streamReuseMaxRequests {
		t.Fatalf("handler call count = %d, want %d", got, streamReuseMaxRequests)
	}
	if got := s.serverSync.streamReuse.activeSlots(); got != 0 {
		t.Fatalf("active slots after rollover = %d, want 0", got)
	}
}

func TestReusableIdleTimeoutReleasesSlot(t *testing.T) {
	s := newTwoHostSetup(t)
	s.serverSync.streamTiming = streamReuseTiming{firstByte: 250 * time.Millisecond, active: 100 * time.Millisecond}
	s.serverSync.registerRPC(RPCGetBlocks, &pb.GetBlocks{}, echoGetBlocksHandler(s.serverP2P, &s.callCount))
	stream, err := s.clientHost.NewStream(context.Background(), s.serverHost.ID(), protocol.ID(getProtocol(s.clientP2P, RPCGetBlocks)))
	if err != nil {
		t.Fatalf("open manual stream: %v", err)
	}
	defer stream.Close()
	SetRPCStreamDeadlines(stream)
	if _, err := EncodeMessage(stream, s.clientP2P, newLocatorGetBlocks(0xF1, 1)); err != nil {
		t.Fatalf("encode: %v", err)
	}
	if e := ReadRspCode(stream, s.clientP2P); !e.Code.IsSuccess() {
		t.Fatalf("response error: %v", e.ToError())
	}
	if err := DecodeMessage(stream, s.clientP2P, &pb.DagBlocks{}); err != nil {
		t.Fatalf("decode: %v", err)
	}
	waitFor(t, func() bool { return s.serverSync.streamReuse.activeSlots() == 1 })
	waitFor(t, func() bool { return s.serverSync.streamReuse.activeSlots() == 0 })
}

func TestReusableStreamTeardownReleasesSlot(t *testing.T) {
	tests := []struct {
		name    string
		prepare func(*testing.T, *twoHostSetup, network.Stream)
		action  func(*testing.T, *twoHostSetup, network.Stream)
	}{
		{name: "malformed varint", prepare: func(t *testing.T, s *twoHostSetup, stream network.Stream) {
			t.Helper()
			if _, err := stream.Write([]byte{0x80}); err != nil {
				t.Fatalf("write malformed varint prefix: %v", err)
			}
		}, action: func(t *testing.T, s *twoHostSetup, stream network.Stream) {
			_ = stream.CloseWrite()
		}},
		{name: "malformed payload", prepare: func(t *testing.T, s *twoHostSetup, stream network.Stream) {
			t.Helper()
			if _, err := stream.Write([]byte{2}); err != nil {
				t.Fatalf("write malformed payload length: %v", err)
			}
		}, action: func(t *testing.T, s *twoHostSetup, stream network.Stream) {
			if _, err := stream.Write([]byte{8}); err != nil {
				t.Fatalf("write malformed payload: %v", err)
			}
			_ = stream.CloseWrite()
		}},
		{name: "reset", prepare: func(t *testing.T, s *twoHostSetup, stream network.Stream) {
			t.Helper()
			if _, err := stream.Write([]byte{0x80}); err != nil {
				t.Fatalf("write reset prefix: %v", err)
			}
		}, action: func(t *testing.T, s *twoHostSetup, stream network.Stream) {
			_ = stream.Reset()
		}},
		{name: "disconnect", prepare: func(t *testing.T, s *twoHostSetup, stream network.Stream) {
			t.Helper()
			if _, err := stream.Write([]byte{0x80}); err != nil {
				t.Fatalf("write disconnect prefix: %v", err)
			}
		}, action: func(t *testing.T, s *twoHostSetup, stream network.Stream) {
			_ = s.clientHost.Network().ClosePeer(s.serverHost.ID())
		}},
		{name: "sync stop", prepare: func(t *testing.T, s *twoHostSetup, stream network.Stream) {
			t.Helper()
			if _, err := stream.Write([]byte{0x80}); err != nil {
				t.Fatalf("write stop prefix: %v", err)
			}
		}, action: func(t *testing.T, s *twoHostSetup, stream network.Stream) {
			if err := s.serverSync.Stop(); err != nil {
				t.Errorf("Sync.Stop() error = %v", err)
			}
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := newTwoHostSetup(t)
			stream := openReusableRequest(t, s)
			tc.prepare(t, s, stream)
			waitFor(t, func() bool { return s.serverSync.streamReuse.activeSlots() == 1 })
			if got := s.serverSync.streamReuse.trackedStreams(); got != 1 {
				t.Fatalf("tracked streams before %s = %d, want 1", tc.name, got)
			}
			tc.action(t, s, stream)
			waitFor(t, func() bool { return s.serverSync.streamReuse.activeSlots() == 0 })
			if got := s.serverSync.streamReuse.trackedStreams(); got != 0 {
				t.Fatalf("tracked streams after %s = %d, want 0", tc.name, got)
			}
			_ = stream.Close()
		})
	}
}

func TestSyncStopPreventsReusableStateRecreationAfterCleanup(t *testing.T) {
	s := newTwoHostSetup(t)
	acquireReached := make(chan struct{})
	resumeAcquire := make(chan struct{})
	s.serverSync.beforeReusableAcquire = func() {
		close(acquireReached)
		<-resumeAcquire
	}
	s.serverSync.registerRPC(RPCGetBlocks, &pb.GetBlocks{}, echoGetBlocksHandler(s.serverP2P, &s.callCount))

	stream := openReusableRequest(t, s)
	defer stream.Close()
	select {
	case <-acquireReached:
	case <-time.After(time.Second):
		t.Fatal("reusable handler did not pause before its next slot acquisition")
	}
	if got := s.serverSync.streamReuse.activeSlots(); got != 0 {
		t.Fatalf("active slots before shutdown cleanup = %d, want 0", got)
	}

	if err := s.serverSync.Stop(); err != nil {
		t.Fatalf("Sync.Stop() error = %v", err)
	}
	if _, err := s.serverSync.Send(s.serverP2P.Peers().Fetch(s.clientHost.ID()), RPCGetBlocks, newLocatorGetBlocks(0xFC, 1)); err == nil {
		t.Fatal("Sync.Send succeeded after shutdown")
	}
	if got := s.serverSync.outboundReuse.activeEntries(); got != 0 {
		t.Fatalf("outbound entries recreated after shutdown = %d, want 0", got)
	}

	close(resumeAcquire)
	SetStreamReadDeadline(stream, time.Second)
	var b [1]byte
	if _, err := stream.Read(b[:]); err == nil {
		t.Fatal("reusable stream remained open after shutdown")
	}
	waitFor(t, func() bool { return s.serverSync.streamReuse.activeSlots() == 0 })
	if got := s.serverSync.streamReuse.trackedStreams(); got != 0 {
		t.Fatalf("inbound slots recreated after shutdown cleanup = %d, want 0", got)
	}
}

func TestReusableHandlerFailureReleasesSlot(t *testing.T) {
	s := newTwoHostSetup(t)
	var calls int32
	s.serverSync.registerRPC(RPCGetBlocks, &pb.GetBlocks{}, func(ctx context.Context, msg any, stream libp2pcore.Stream, pe *peers.Peer) *common.Error {
		if atomic.AddInt32(&calls, 1) == 1 {
			return EncodeResponseMsg(s.serverP2P, stream, &pb.DagBlocks{}, common.ErrNone)
		}
		return ErrMessage(fmt.Errorf("deterministic handler failure"))
	})
	stream := openReusableRequest(t, s)
	defer stream.Close()
	var wire bytes.Buffer
	if _, err := s.clientP2P.Encoding().EncodeWithMaxLength(&wire, newLocatorGetBlocks(0xFB, 1)); err != nil {
		t.Fatalf("encode second request: %v", err)
	}
	if wire.Len() < 2 {
		t.Fatal("second request did not leave a partial reusable frame")
	}
	if _, err := stream.Write(wire.Bytes()[:1]); err != nil {
		t.Fatalf("write second request prefix: %v", err)
	}
	waitFor(t, func() bool { return s.serverSync.streamReuse.activeSlots() == 1 })
	if _, err := stream.Write(wire.Bytes()[1:]); err != nil {
		t.Fatalf("write second request body: %v", err)
	}
	if e := ReadRspCode(stream, s.clientP2P); e.Code.IsSuccess() {
		t.Fatal("handler failure unexpectedly returned success")
	}
	waitFor(t, func() bool { return s.serverSync.streamReuse.activeSlots() == 0 })
}

func TestReusableRequestNearIdleRetainsFullActiveBudget(t *testing.T) {
	s := newTwoHostSetup(t)
	s.serverSync.streamTiming = streamReuseTiming{firstByte: 100 * time.Millisecond, active: 200 * time.Millisecond}
	var secondReady chan struct{}
	secondReady = make(chan struct{}, 1)
	s.serverSync.registerRPC(RPCGetBlocks, &pb.GetBlocks{}, func(ctx context.Context, msg any, stream libp2pcore.Stream, pe *peers.Peer) *common.Error {
		atomic.AddInt32(&s.callCount, 1)
		if atomic.LoadInt32(&s.callCount) == 2 {
			select {
			case secondReady <- struct{}{}:
			default:
			}
			select {
			case <-time.After(150 * time.Millisecond):
			case <-ctx.Done():
				return ErrMessage(ctx.Err())
			}
		}
		return EncodeResponseMsg(s.serverP2P, stream, &pb.DagBlocks{Blocks: msg.(*pb.GetBlocks).Locator}, common.ErrNone)
	})

	stream, err := s.clientHost.NewStream(context.Background(), s.serverHost.ID(), protocol.ID(getProtocol(s.clientP2P, RPCGetBlocks)))
	if err != nil {
		t.Fatalf("open manual stream: %v", err)
	}
	defer stream.Close()
	SetRPCStreamDeadlines(stream)
	first := newLocatorGetBlocks(0xC1, 1)
	if _, err := EncodeMessage(stream, s.clientP2P, first); err != nil {
		t.Fatalf("first encode: %v", err)
	}
	if e := ReadRspCode(stream, s.clientP2P); !e.Code.IsSuccess() {
		t.Fatalf("first response error: %v", e.ToError())
	}
	if err := DecodeMessage(stream, s.clientP2P, &pb.DagBlocks{}); err != nil {
		t.Fatalf("first response decode: %v", err)
	}
	time.Sleep(80 * time.Millisecond)
	second := newLocatorGetBlocks(0xC2, 1)
	if _, err := EncodeMessage(stream, s.clientP2P, second); err != nil {
		t.Fatalf("second encode: %v", err)
	}
	select {
	case <-secondReady:
	case <-time.After(time.Second):
		t.Fatal("second request did not reach handler")
	}
	if e := ReadRspCode(stream, s.clientP2P); !e.Code.IsSuccess() {
		t.Fatalf("near-idle second response error: %v", e.ToError())
	}
}

func TestRegisterRPCExitsCleanlyOnClientClose(t *testing.T) {
	s := newTwoHostSetup(t)

	stream, err := s.clientHost.NewStream(context.Background(), s.serverHost.ID(), protocol.ID(getProtocol(s.clientP2P, RPCGetBlocks)))
	if err != nil {
		t.Fatalf("open manual stream: %v", err)
	}
	SetRPCStreamDeadlines(stream)

	req := newLocatorGetBlocks(0x01, 2)
	if _, err := EncodeMessage(stream, s.clientP2P, req); err != nil {
		t.Fatalf("encode: %v", err)
	}
	e := ReadRspCode(stream, s.clientP2P)
	if !e.Code.IsSuccess() {
		t.Fatalf("response error: %v", e.ToError())
	}
	resp := &pb.DagBlocks{}
	if err := DecodeMessage(stream, s.clientP2P, resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	assertLocatorEqual(t, resp.Blocks, req.Locator)

	// Simulate an old one-shot client: fully close instead of sending
	// another request. The server's loop must exit cleanly (not hang) --
	// proven by a fresh, unrelated stream still working immediately after.
	waitFor(t, func() bool { return s.serverSync.streamReuse.activeSlots() == 1 })
	if err := stream.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	waitFor(t, func() bool { return s.serverSync.streamReuse.activeSlots() == 0 })

	req2 := newLocatorGetBlocks(0x02, 3)
	resp2, _ := sendGetBlocks(t, s.clientP2P, s.pe, false, req2)
	assertLocatorEqual(t, resp2.Blocks, req2.Locator)
}

func TestPeerSupportsStreamReuse(t *testing.T) {
	t.Parallel()

	if peerSupportsStreamReuse(nil) {
		t.Fatal("nil peer should not support stream reuse")
	}

	pe := peers.NewPeer("test-reuse-peer", nil)
	if peerSupportsStreamReuse(pe) {
		t.Fatal("peer with no chain state should not support stream reuse")
	}

	withVersion := func(v uint32) *pb.ChainState {
		return &pb.ChainState{
			ProtocolVersion: v,
			GraphState:      &pb.GraphState{Total: 1},
		}
	}

	pe.SetChainState(withVersion(uint32(bdagprotocol.EVMCheckpointProtocolVersion)))
	if peerSupportsStreamReuse(pe) {
		t.Fatalf("v%d peer should not support stream reuse", bdagprotocol.EVMCheckpointProtocolVersion)
	}

	pe.SetChainState(withVersion(uint32(bdagprotocol.StreamReuseProtocolVersion)))
	if !peerSupportsStreamReuse(pe) {
		t.Fatalf("v%d peer should support stream reuse", bdagprotocol.StreamReuseProtocolVersion)
	}

	pe.SetChainState(withVersion(uint32(bdagprotocol.StreamReuseProtocolVersion) + 1))
	if !peerSupportsStreamReuse(pe) {
		t.Fatal("newer-than-v47 peer should support stream reuse")
	}
}
