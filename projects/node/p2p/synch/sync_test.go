package synch

import (
	"context"
	"errors"
	"io"
	"net"
	"testing"
	"time"

	"github.com/BlockdagNetworkLabs/bdag/p2p/common"
	"github.com/BlockdagNetworkLabs/bdag/p2p/encoder"
	"github.com/BlockdagNetworkLabs/bdag/p2p/peers"
	ethcommon "github.com/ethereum/go-ethereum/common"
	"github.com/libp2p/go-libp2p/core/connmgr"
	"github.com/libp2p/go-libp2p/core/event"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/peerstore"
	"github.com/libp2p/go-libp2p/core/protocol"
	ma "github.com/multiformats/go-multiaddr"
)

func TestProcessRspErrorSuccessClosesWithoutReadingPeer(t *testing.T) {
	stream := &testStream{panicOnRead: true, panicOnReadDeadline: true}
	pe := newProcessRspErrorTestPeer()

	if ok := processRspError(context.Background(), nil, stream, nil, pe); !ok {
		t.Fatal("processRspError returned false for success")
	}
	if stream.closeWriteCalls != 1 {
		t.Fatalf("CloseWrite calls = %d, want 1", stream.closeWriteCalls)
	}
	if stream.closeCalls != 1 {
		t.Fatalf("Close calls = %d, want 1", stream.closeCalls)
	}
	if stream.resetCalls != 0 {
		t.Fatalf("Reset calls = %d, want 0", stream.resetCalls)
	}
	if stream.readCalls != 0 || stream.setReadDeadlineCalls != 0 {
		t.Fatalf("success close performed peer reads/deadlines: reads=%d deadlines=%d", stream.readCalls, stream.setReadDeadlineCalls)
	}
	if got := len(pe.BadResponses()); got != 0 {
		t.Fatalf("bad responses = %d, want 0", got)
	}
}

func TestProcessRspErrorApplicationErrorUsesBoundedEOFGrace(t *testing.T) {
	withResponseCloseGrace(t, 20*time.Millisecond)
	stream := &testStream{blockReadUntilDeadline: true}
	pe := newProcessRspErrorTestPeer()
	err := common.NewErrorStr(common.ErrGeneric, "forced application error")

	start := time.Now()
	if ok := processRspError(context.Background(), err, stream, fakeP2PRPC{}, pe); !ok {
		t.Fatal("processRspError returned false for application error")
	}
	elapsed := time.Since(start)
	if elapsed > 250*time.Millisecond {
		t.Fatalf("processRspError waited %s, expected bounded close grace", elapsed)
	}
	if stream.closeWriteCalls != 1 || stream.closeCalls != 1 {
		t.Fatalf("CloseWrite/Close calls = %d/%d, want 1/1", stream.closeWriteCalls, stream.closeCalls)
	}
	if stream.readCalls != 1 || stream.setReadDeadlineCalls < 1 {
		t.Fatalf("application close grace reads/deadlines = %d/%d, want read and deadline", stream.readCalls, stream.setReadDeadlineCalls)
	}
	if got := len(pe.BadResponses()); got != 1 {
		t.Fatalf("bad responses = %d, want application error scored once", got)
	}
}

func TestProcessRspErrorContextDeadlineSkipsCloseGrace(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	stream := &testStream{panicOnRead: true, panicOnReadDeadline: true}
	pe := newProcessRspErrorTestPeer()
	err := common.NewErrorStr(common.ErrGeneric, "forced application error")

	if ok := processRspError(ctx, err, stream, fakeP2PRPC{}, pe); !ok {
		t.Fatal("processRspError returned false for application error")
	}
	if stream.readCalls != 0 || stream.setReadDeadlineCalls != 0 {
		t.Fatalf("canceled close grace performed peer reads/deadlines: reads=%d deadlines=%d", stream.readCalls, stream.setReadDeadlineCalls)
	}
	if got := len(pe.BadResponses()); got != 1 {
		t.Fatalf("bad responses = %d, want application error scored once", got)
	}
}

func TestProcessRspErrorCloseGraceReadErrorDoesNotRescore(t *testing.T) {
	withResponseCloseGrace(t, 20*time.Millisecond)
	stream := &testStream{readErr: errors.New("peer close failed")}
	pe := newProcessRspErrorTestPeer()
	err := common.NewErrorStr(common.ErrGeneric, "forced application error")

	if ok := processRspError(context.Background(), err, stream, fakeP2PRPC{}, pe); !ok {
		t.Fatal("processRspError returned false for application error")
	}
	if stream.closeCalls != 1 {
		t.Fatalf("Close calls = %d, want 1", stream.closeCalls)
	}
	if got := len(pe.BadResponses()); got != 1 {
		t.Fatalf("bad responses = %d, want application error scored once", got)
	}
}

func TestProcessRspErrorReadDeadlineFailureClosesAndScoresOnce(t *testing.T) {
	withResponseCloseGrace(t, 20*time.Millisecond)
	stream := &testStream{setReadDeadlineErr: errors.New("deadline unsupported")}
	pe := newProcessRspErrorTestPeer()
	err := common.NewErrorStr(common.ErrGeneric, "forced application error")

	if ok := processRspError(context.Background(), err, stream, fakeP2PRPC{}, pe); !ok {
		t.Fatal("processRspError returned false for application error")
	}
	if stream.closeCalls != 1 {
		t.Fatalf("Close calls = %d, want 1", stream.closeCalls)
	}
	if got := len(pe.BadResponses()); got != 1 {
		t.Fatalf("bad responses = %d, want application error scored once", got)
	}
}

func TestProcessRspErrorCloseWriteFailureUsesBoundedCleanup(t *testing.T) {
	withResponseCloseGrace(t, 20*time.Millisecond)
	stream := &testStream{closeWriteErr: errors.New("close write failed"), readErr: io.EOF}
	pe := newProcessRspErrorTestPeer()
	err := common.NewErrorStr(common.ErrGeneric, "forced application error")

	start := time.Now()
	if ok := processRspError(context.Background(), err, stream, fakeP2PRPC{}, pe); !ok {
		t.Fatal("processRspError returned false for application error")
	}
	if elapsed := time.Since(start); elapsed > 250*time.Millisecond {
		t.Fatalf("processRspError waited %s, expected bounded close grace", elapsed)
	}
	if stream.closeWriteCalls != 1 || stream.closeCalls != 1 {
		t.Fatalf("CloseWrite/Close calls = %d/%d, want 1/1", stream.closeWriteCalls, stream.closeCalls)
	}
	if got := len(pe.BadResponses()); got != 1 {
		t.Fatalf("bad responses = %d, want application error scored once", got)
	}
}

func TestProcessRspErrorStreamErrorResets(t *testing.T) {
	stream := &testStream{}
	pe := newProcessRspErrorTestPeer()
	err := common.NewError(common.ErrStreamRead, errors.New("forced stream read"))

	if ok := processRspError(context.Background(), err, stream, nil, pe); ok {
		t.Fatal("processRspError returned true for stream error")
	}
	if stream.resetCalls != 1 {
		t.Fatalf("Reset calls = %d, want 1", stream.resetCalls)
	}
	if stream.closeWriteCalls != 0 || stream.closeCalls != 0 {
		t.Fatalf("CloseWrite/Close calls = %d/%d, want 0/0", stream.closeWriteCalls, stream.closeCalls)
	}
	if got := len(pe.BadResponses()); got != 1 {
		t.Fatalf("bad responses = %d, want 1", got)
	}
}

func TestProcessRspErrorApplicationErrorWritesResponseAndCloses(t *testing.T) {
	withResponseCloseGrace(t, 25*time.Millisecond)
	stream := &testStream{readErr: io.EOF}
	pe := newProcessRspErrorTestPeer()
	err := common.NewErrorStr(common.ErrGeneric, "forced application error")

	if ok := processRspError(context.Background(), err, stream, fakeP2PRPC{}, pe); !ok {
		t.Fatal("processRspError returned false for application error")
	}
	if len(stream.writes) == 0 {
		t.Fatal("expected application error response write")
	}
	if stream.closeWriteCalls != 1 || stream.closeCalls != 1 {
		t.Fatalf("CloseWrite/Close calls = %d/%d, want 1/1", stream.closeWriteCalls, stream.closeCalls)
	}
	if stream.resetCalls != 0 {
		t.Fatalf("Reset calls = %d, want 0", stream.resetCalls)
	}
	if got := len(pe.BadResponses()); got != 1 {
		t.Fatalf("bad responses = %d, want 1", got)
	}
}

func TestRegisterRPCResetsInboundStreamWhenRPCIsNotRunning(t *testing.T) {
	running := false
	h := &testHost{}
	rpc := fakeP2PRPC{host: h, running: &running}
	stream := &testStream{}

	RegisterRPC(rpc, RPCPingTopic, nil, func(context.Context, any, network.Stream, *peers.Peer) *common.Error {
		t.Fatal("handler should not run when RPC is stopped")
		return nil
	})
	if h.handler == nil {
		t.Fatal("expected stream handler to be registered")
	}

	h.handler(stream)
	if stream.resetCalls != 1 {
		t.Fatalf("Reset calls = %d, want 1", stream.resetCalls)
	}
	if stream.closeCalls != 0 || stream.closeWriteCalls != 0 {
		t.Fatalf("Close/CloseWrite calls = %d/%d, want 0/0", stream.closeCalls, stream.closeWriteCalls)
	}
}

func TestHandleRPCOneShotResetsWhenHandlerWaitIsCanceled(t *testing.T) {
	oldConcurrency := rpcHandlerConcurrency
	rpcHandlerConcurrency = make(chan struct{}, 1)
	rpcHandlerConcurrency <- struct{}{}
	t.Cleanup(func() { rpcHandlerConcurrency = oldConcurrency })

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	stream := &testStream{}
	pe := newProcessRspErrorTestPeer()
	handleRPCOneShot(fakeP2PRPC{ctx: ctx}, RPCPingTopic, nil,
		func(context.Context, any, network.Stream, *peers.Peer) *common.Error {
			t.Fatal("handler should not run after cancellation")
			return nil
		}, stream, pe)
	if stream.resetCalls != 1 {
		t.Fatalf("Reset calls = %d, want 1", stream.resetCalls)
	}
	if stream.closeCalls != 0 || stream.closeWriteCalls != 0 {
		t.Fatalf("Close/CloseWrite calls = %d/%d, want 0/0", stream.closeCalls, stream.closeWriteCalls)
	}
}

func TestSendResetsOpenedStreamOnEncodeError(t *testing.T) {
	stream := &testStream{}
	rpc := fakeP2PRPC{
		host:     &testHost{stream: stream, network: &testNetwork{connectedness: network.Connected}},
		encoding: fakeEncoding{encodeErr: errors.New("forced encode failure")},
	}
	pe := newProcessRspErrorTestPeer()

	got, err := Send(context.Background(), rpc, struct{}{}, RPCPingTopic, pe, false)
	if got != nil {
		t.Fatalf("stream = %#v, want nil on encode failure", got)
	}
	if err == nil || err.Code != common.ErrStreamWrite {
		t.Fatalf("error = %#v, want ErrStreamWrite", err)
	}
	if stream.resetCalls != 1 {
		t.Fatalf("Reset calls = %d, want 1", stream.resetCalls)
	}
	if stream.closeCalls != 0 || stream.closeWriteCalls != 0 {
		t.Fatalf("Close/CloseWrite calls = %d/%d, want 0/0", stream.closeCalls, stream.closeWriteCalls)
	}
}

func TestSendResetsOpenedStreamOnCloseWriteError(t *testing.T) {
	stream := &testStream{closeWriteErr: errors.New("forced closewrite failure")}
	rpc := fakeP2PRPC{
		host:     &testHost{stream: stream, network: &testNetwork{connectedness: network.Connected}},
		encoding: fakeEncoding{},
	}
	pe := newProcessRspErrorTestPeer()

	got, err := Send(context.Background(), rpc, nil, RPCPingTopic, pe, false)
	if got != nil {
		t.Fatalf("stream = %#v, want nil on close-write failure", got)
	}
	if err == nil || err.Code != common.ErrStreamBase {
		t.Fatalf("error = %#v, want ErrStreamBase", err)
	}
	if stream.closeWriteCalls != 1 {
		t.Fatalf("CloseWrite calls = %d, want 1", stream.closeWriteCalls)
	}
	if stream.resetCalls != 1 {
		t.Fatalf("Reset calls = %d, want 1", stream.resetCalls)
	}
	if stream.closeCalls != 0 {
		t.Fatalf("Close calls = %d, want 0", stream.closeCalls)
	}
}

func withResponseCloseGrace(t *testing.T, grace time.Duration) {
	t.Helper()
	old := responseCloseGrace
	responseCloseGrace = grace
	t.Cleanup(func() { responseCloseGrace = old })
}

func newProcessRspErrorTestPeer() *peers.Peer {
	return peers.NewPeer(peer.ID("peer-1"), nil)
}

type testStream struct {
	writes                 []byte
	readData               []byte
	readErr                error
	writeErr               error
	closeWriteErr          error
	resetErr               error
	blockReadUntilDeadline bool
	panicOnRead            bool
	panicOnReadDeadline    bool
	setReadDeadlineErr     error
	readDeadline           time.Time
	readCalls              int
	setReadDeadlineCalls   int
	writeCalls             int
	closeWriteCalls        int
	closeCalls             int
	resetCalls             int
}

func (s *testStream) Read(p []byte) (int, error) {
	s.readCalls++
	if s.panicOnRead {
		panic("unexpected peer read")
	}
	if s.readErr != nil {
		return 0, s.readErr
	}
	if len(s.readData) > 0 {
		n := copy(p, s.readData)
		s.readData = s.readData[n:]
		return n, nil
	}
	if s.blockReadUntilDeadline {
		if s.readDeadline.IsZero() {
			time.Sleep(time.Hour)
			return 0, io.EOF
		}
		if wait := time.Until(s.readDeadline); wait > 0 {
			time.Sleep(wait)
		}
		return 0, timeoutError{}
	}
	return 0, io.EOF
}

func (s *testStream) Write(p []byte) (int, error) {
	s.writeCalls++
	if s.writeErr != nil {
		return 0, s.writeErr
	}
	s.writes = append(s.writes, p...)
	return len(p), nil
}

func (s *testStream) Close() error {
	s.closeCalls++
	return nil
}

func (s *testStream) CloseWrite() error {
	s.closeWriteCalls++
	return s.closeWriteErr
}

func (s *testStream) CloseRead() error { return nil }

func (s *testStream) Reset() error {
	s.resetCalls++
	return s.resetErr
}

func (s *testStream) SetDeadline(t time.Time) error {
	s.readDeadline = t
	return nil
}

func (s *testStream) SetReadDeadline(t time.Time) error {
	s.setReadDeadlineCalls++
	if s.panicOnReadDeadline {
		panic("unexpected read deadline")
	}
	s.readDeadline = t
	return s.setReadDeadlineErr
}

func (s *testStream) SetWriteDeadline(time.Time) error { return nil }
func (s *testStream) ID() string                       { return "stream-1" }
func (s *testStream) Protocol() protocol.ID            { return protocol.ID(RPCPingTopic) }
func (s *testStream) SetProtocol(protocol.ID) error    { return nil }
func (s *testStream) Stat() network.Stats              { return network.Stats{Direction: network.DirInbound} }
func (s *testStream) Conn() network.Conn               { return nil }
func (s *testStream) Scope() network.StreamScope       { return nil }

type timeoutError struct{}

func (timeoutError) Error() string   { return "timeout" }
func (timeoutError) Timeout() bool   { return true }
func (timeoutError) Temporary() bool { return true }

var _ net.Error = timeoutError{}
var _ network.Stream = (*testStream)(nil)

type fakeP2PRPC struct {
	host     host.Host
	encoding encoder.NetworkEncoding
	running  *bool
	ctx      context.Context
}

func (f fakeP2PRPC) Host() host.Host { return f.host }
func (f fakeP2PRPC) Context() context.Context {
	if f.ctx != nil {
		return f.ctx
	}
	return context.Background()
}
func (f fakeP2PRPC) Encoding() encoder.NetworkEncoding {
	if f.encoding != nil {
		return f.encoding
	}
	return fakeEncoding{}
}
func (fakeP2PRPC) Disconnect(peer.ID) error       { return nil }
func (fakeP2PRPC) IncreaseBytesSent(peer.ID, int) {}
func (fakeP2PRPC) IncreaseBytesRecv(peer.ID, int) {}
func (fakeP2PRPC) Peers() *peers.Status           { return nil }
func (f fakeP2PRPC) IsRunning() bool {
	if f.running != nil {
		return *f.running
	}
	return true
}
func (fakeP2PRPC) GetGenesisHash() *ethcommon.Hash { return nil }

type fakeEncoding struct {
	encodeErr error
}

func (fakeEncoding) DecodeGossip([]byte, any) error           { return nil }
func (fakeEncoding) DecodeWithMaxLength(io.Reader, any) error { return nil }
func (fakeEncoding) EncodeGossip(io.Writer, any) (int, error) { return 0, nil }
func (e fakeEncoding) EncodeWithMaxLength(w io.Writer, _ any) (int, error) {
	if e.encodeErr != nil {
		return 0, e.encodeErr
	}
	return w.Write([]byte("encoded"))
}
func (fakeEncoding) ProtocolSuffix() string  { return "/test" }
func (fakeEncoding) GetMaxChunkSize() uint64 { return 1024 }
func (fakeEncoding) GetSize(any) int         { return 1 }

type testHost struct {
	stream        network.Stream
	network       network.Network
	handler       network.StreamHandler
	newStreamFunc func() (network.Stream, error)
}

func (h *testHost) ID() peer.ID                                  { return peer.ID("host-1") }
func (h *testHost) Peerstore() peerstore.Peerstore               { return nil }
func (h *testHost) Addrs() []ma.Multiaddr                        { return nil }
func (h *testHost) Network() network.Network                     { return h.network }
func (h *testHost) Mux() protocol.Switch                         { return nil }
func (h *testHost) Connect(context.Context, peer.AddrInfo) error { return nil }
func (h *testHost) SetStreamHandler(_ protocol.ID, handler network.StreamHandler) {
	h.handler = handler
}
func (h *testHost) SetStreamHandlerMatch(protocol.ID, func(protocol.ID) bool, network.StreamHandler) {
}
func (h *testHost) RemoveStreamHandler(protocol.ID) {}
func (h *testHost) NewStream(context.Context, peer.ID, ...protocol.ID) (network.Stream, error) {
	if h.newStreamFunc != nil {
		return h.newStreamFunc()
	}
	return h.stream, nil
}
func (h *testHost) Close() error                     { return nil }
func (h *testHost) ConnManager() connmgr.ConnManager { return nil }
func (h *testHost) EventBus() event.Bus              { return nil }

type testNetwork struct {
	connectedness network.Connectedness
}

func (n *testNetwork) Peerstore() peerstore.Peerstore                          { return nil }
func (n *testNetwork) LocalPeer() peer.ID                                      { return peer.ID("host-1") }
func (n *testNetwork) DialPeer(context.Context, peer.ID) (network.Conn, error) { return nil, nil }
func (n *testNetwork) ClosePeer(peer.ID) error                                 { return nil }
func (n *testNetwork) Connectedness(peer.ID) network.Connectedness             { return n.connectedness }
func (n *testNetwork) Peers() []peer.ID                                        { return nil }
func (n *testNetwork) Conns() []network.Conn                                   { return nil }
func (n *testNetwork) ConnsToPeer(peer.ID) []network.Conn                      { return nil }
func (n *testNetwork) Notify(network.Notifiee)                                 {}
func (n *testNetwork) StopNotify(network.Notifiee)                             {}
func (n *testNetwork) CanDial(peer.ID, ma.Multiaddr) bool                      { return true }
func (n *testNetwork) Close() error                                            { return nil }
func (n *testNetwork) SetStreamHandler(network.StreamHandler)                  {}
func (n *testNetwork) NewStream(context.Context, peer.ID) (network.Stream, error) {
	return nil, nil
}
func (n *testNetwork) Listen(...ma.Multiaddr) error                      { return nil }
func (n *testNetwork) ListenAddresses() []ma.Multiaddr                   { return nil }
func (n *testNetwork) InterfaceListenAddresses() ([]ma.Multiaddr, error) { return nil, nil }
func (n *testNetwork) ResourceManager() network.ResourceManager          { return nil }
