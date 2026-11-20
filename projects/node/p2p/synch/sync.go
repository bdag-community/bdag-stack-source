package synch

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	bdagprotocol "github.com/BlockdagNetworkLabs/bdag/core/protocol"
	"github.com/BlockdagNetworkLabs/bdag/p2p/common"
	"github.com/BlockdagNetworkLabs/bdag/p2p/encoder"
	"github.com/BlockdagNetworkLabs/bdag/p2p/peers"
	pb "github.com/BlockdagNetworkLabs/bdag/p2p/proto/v1"
	"github.com/BlockdagNetworkLabs/bdag/params"
	libp2pcore "github.com/libp2p/go-libp2p/core"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/protocol"
)

const (

	// RPCGoodByeTopic defines the topic for the goodbye rpc method.
	RPCGoodByeTopic = "/Blockdag/req/goodbye/1"
	// RPCPingTopic defines the topic for the ping rpc method.
	RPCPingTopic = "/Blockdag/req/ping/1"
	// RPCMetaDataTopic defines the topic for the metadata rpc method.
	RPCMetaDataTopic = "/Blockdag/req/metadata/1"
	// RPCChainState defines the topic for the chain state rpc method.
	RPCChainState = "/Blockdag/req/chainstate/1"
	// RPCGetBlocks defines the topic for the get blocks rpc method.
	RPCGetBlocks = "/Blockdag/req/getblocks/1"
	// RPCGetBlockDatas defines the topic for the get blocks rpc method.
	RPCGetBlockDatas = "/Blockdag/req/getblockdatas/1"
	// RPCGetBlocks defines the topic for the get blocks rpc method.
	RPCSyncDAG = "/Blockdag/req/syncdag/1"
	// RPCContinueSyncDAG defines the topic for the get blocks rpc method.
	RPCContinueSyncDAG = "/Blockdag/req/continuesyncdag/1"
	// RPCTransaction defines the topic for the transaction rpc method.
	RPCTransaction = "/Blockdag/req/transaction/1"
	// RPCInventory defines the topic for the inventory rpc method.
	RPCInventory = "/Blockdag/req/inventory/1"
	// RPCGraphState defines the topic for the graphstate rpc method.
	RPCGraphState = "/Blockdag/req/graphstate/1"
	// RPCSyncQNR defines the topic for the syncqnr rpc method.
	RPCSyncQNR = "/Blockdag/req/syncqnr/1"
	// RPCGetMerkleBlocks defines the topic for the get merkle blocks rpc method.
	RPCGetMerkleBlocks = "/Blockdag/req/getmerkles/1"
	// RPCFilterAdd defines the topic for the filter add rpc method.
	RPCFilterAdd = "/Blockdag/req/filteradd/1"
	// RPCFilterClear defines the topic for the filter add rpc method.
	RPCFilterClear = "/Blockdag/req/filterclear/1"
	// RPCFilterLoad defines the topic for the filter add rpc method.
	RPCFilterLoad = "/Blockdag/req/filterload/1"
	// RPCMemPool defines the topic for the mempool rpc method.
	RPCMemPool = "/Blockdag/req/mempool/1"
	// RPCMemPool defines the topic for the getdata rpc method.
	RPCGetData = "/Blockdag/req/getdata/1"
	// RPCStateRoot defines the topic for the stateroot rpc method.
	RPCStateRoot = "/Blockdag/req/stateroot/1"
	// RPCBroadcastBlock defines the topic for the broadcast block rpc method.
	RPCBroadcastBlock = "/Blockdag/req/broadcastblock/1"
	// RPCRelayTx defines the topic for relaying transactions while syncing.
	RPCRelayTx = "/Blockdag/req/relaytx/1"
)

type streamReuseTiming struct {
	firstByte time.Duration
	active    time.Duration
}

// Time to first byte timeout. The maximum time to wait for first byte of
// request response (time-to-first-byte). The client is expected to give up if
// they don't receive the first byte within 20 seconds.
const TtfbTimeout = 20 * time.Second

// poolableTopics are the RPC topics eligible for persistent-stream reuse
// (see Send, RegisterRPC). Limited to the hot, high-volume sync-loop
// requests -- these are the only ones issued repeatedly, sequentially,
// against the same peer during a catch-up sync, so they're the only ones
// where amortizing the per-stream libp2p protocol-negotiation cost over
// many requests actually matters. Deliberately excludes gossip-style topics
// (RPCRelayTx, RPCBroadcastBlock, RPCInventory, etc.) to keep the
// concurrency-safety surface small -- see peer_fork.go/getblockdatas.go for
// the two topics here (RPCSyncDAG, RPCGetBlockDatas) that do have a second,
// lower-frequency caller beyond the main sync loop.
//
// Reuse is also gated per peer on StreamReuseProtocolVersion: skipping the
// post-request half-close only works against peers whose decoder reads an
// exact message length rather than until EOF. Against older peers we keep
// today's one-shot half-close path so sync still progresses.
var poolableTopics = map[string]bool{
	RPCSyncDAG:         true,
	RPCContinueSyncDAG: true,
	RPCGetBlockDatas:   true,
	RPCGetBlocks:       true,
}

const (
	streamReuseMaxPerTopic = 8
	streamReuseMaxTotal    = 32
	streamReuseMaxRequests = 128
	streamReuseFirstByte   = 5 * time.Second
)

// streamReuseLimiter bounds streams waiting for their next request. The
// per-peer map contains only peers with an active slot and is therefore
// bounded by streamReuseMaxTotal.
type streamReuseLimiter struct {
	mu         sync.Mutex
	topicUses  map[string]int
	peerTopics map[peer.ID]map[string]struct{}
	streams    map[streamReuseSlotKey]network.Stream
	total      int
}

type streamReuseSlotKey struct {
	peer  peer.ID
	topic string
}

// outboundReuseTracker owns client-side request counts for streams that Sync
// has returned to a peer pool. It is deliberately instance-local and capped
// to the same 32 stream bound as reusable inbound waits: a disconnected or
// stopped Sync therefore cannot retain peer or stream metadata indefinitely.
type outboundReuseTracker struct {
	mu      sync.Mutex
	entries map[outboundReuseKey]*outboundReuseEntry
}

type outboundReuseKey struct {
	peer  peer.ID
	topic protocol.ID
}

type outboundReuseEntry struct {
	peer     *peers.Peer
	stream   network.Stream
	requests int
	busy     bool
}

type outboundReusePermit struct {
	key   outboundReuseKey
	entry *outboundReuseEntry
}

func newOutboundReuseTracker() *outboundReuseTracker {
	return &outboundReuseTracker{entries: make(map[outboundReuseKey]*outboundReuseEntry)}
}

// begin reserves the only reusable client stream for a peer/topic. A
// concurrent request falls back to the historical one-shot path rather than
// racing ReturnStream and losing lifecycle ownership of its counter.
func (t *outboundReuseTracker) begin(pe *peers.Peer, topic protocol.ID, stream network.Stream) *outboundReusePermit {
	return t.beginIfRunning(pe, topic, stream, nil)
}

func (t *outboundReuseTracker) beginIfRunning(pe *peers.Peer, topic protocol.ID, stream network.Stream, running func() bool) *outboundReusePermit {
	if t == nil || pe == nil {
		return nil
	}
	key := outboundReuseKey{peer: pe.GetID(), topic: topic}
	t.mu.Lock()
	defer t.mu.Unlock()
	if running != nil && !running() {
		return nil
	}
	entry := t.entries[key]
	if entry != nil {
		if entry.busy {
			return nil
		}
		// A pooled stream can be evicted by its transport or a peer disconnect
		// between requests. Its replacement starts a fresh 128-request epoch.
		if entry.stream != stream {
			entry.stream = stream
			entry.requests = 0
		}
		entry.busy = true
		return &outboundReusePermit{key: key, entry: entry}
	}
	if len(t.entries) >= streamReuseMaxTotal {
		return nil
	}
	entry = &outboundReuseEntry{peer: pe, stream: stream, busy: true}
	t.entries[key] = entry
	return &outboundReusePermit{key: key, entry: entry}
}

// finish records a completed response and returns whether the exact stream
// may be put back in the peer pool. A failed, replaced, stopped, or rollover
// stream has its bounded entry removed before any transport cleanup occurs.
func (t *outboundReuseTracker) finish(permit *outboundReusePermit, stream network.Stream, keep bool) bool {
	if t == nil || permit == nil {
		return false
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	entry := t.entries[permit.key]
	if entry != permit.entry {
		return false
	}
	if !keep {
		delete(t.entries, permit.key)
		return false
	}
	if entry.stream != stream {
		entry.stream = stream
		entry.requests = 0
	}
	entry.requests++
	if entry.requests >= streamReuseMaxRequests {
		delete(t.entries, permit.key)
		return false
	}
	return true
}

func (t *outboundReuseTracker) markReturned(permit *outboundReusePermit, stream network.Stream) bool {
	if t == nil || permit == nil {
		return false
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	entry := t.entries[permit.key]
	if entry != permit.entry || entry.stream != stream {
		return false
	}
	entry.busy = false
	return true
}

func (t *outboundReuseTracker) forget(permit *outboundReusePermit) {
	if t == nil || permit == nil {
		return
	}
	t.mu.Lock()
	if t.entries[permit.key] == permit.entry {
		delete(t.entries, permit.key)
	}
	t.mu.Unlock()
}

// forgetPeer and stop reset every lifecycle-owned pooled stream before
// dropping its metadata. Both maps are bounded, so this work is bounded too.
func (t *outboundReuseTracker) forgetPeer(pid peer.ID) {
	if t == nil {
		return
	}
	type cleanup struct {
		key   outboundReuseKey
		entry *outboundReuseEntry
	}
	var entries []cleanup
	t.mu.Lock()
	for key, entry := range t.entries {
		if key.peer == pid {
			delete(t.entries, key)
			entries = append(entries, cleanup{key: key, entry: entry})
		}
	}
	t.mu.Unlock()
	for _, item := range entries {
		item.entry.peer.EvictStream(item.key.topic, item.entry.stream)
	}
}

func (t *outboundReuseTracker) stop() {
	if t == nil {
		return
	}
	t.mu.Lock()
	entries := t.entries
	t.entries = make(map[outboundReuseKey]*outboundReuseEntry)
	t.mu.Unlock()
	for key, entry := range entries {
		entry.peer.EvictStream(key.topic, entry.stream)
	}
}

func (t *outboundReuseTracker) activeEntries() int {
	if t == nil {
		return 0
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.entries)
}

func newStreamReuseLimiter() *streamReuseLimiter {
	return &streamReuseLimiter{
		topicUses:  make(map[string]int),
		peerTopics: make(map[peer.ID]map[string]struct{}),
		streams:    make(map[streamReuseSlotKey]network.Stream),
	}
}

func (l *streamReuseLimiter) acquire(pid peer.ID, topic string, held ...network.Stream) bool {
	var stream network.Stream
	if len(held) > 0 {
		stream = held[0]
	}
	return l.acquireIfRunning(pid, topic, stream, nil)
}

func (l *streamReuseLimiter) acquireIfRunning(pid peer.ID, topic string, stream network.Stream, running func() bool) bool {
	if l == nil {
		return false
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if running != nil && !running() {
		return false
	}
	if l.total >= streamReuseMaxTotal || l.topicUses[topic] >= streamReuseMaxPerTopic {
		return false
	}
	peerTopics := l.peerTopics[pid]
	if peerTopics == nil {
		peerTopics = make(map[string]struct{})
		l.peerTopics[pid] = peerTopics
	}
	if _, exists := peerTopics[topic]; exists {
		return false
	}
	peerTopics[topic] = struct{}{}
	l.streams[streamReuseSlotKey{peer: pid, topic: topic}] = stream
	l.topicUses[topic]++
	l.total++
	return true
}

func (l *streamReuseLimiter) release(pid peer.ID, topic string) {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	peerTopics := l.peerTopics[pid]
	if peerTopics == nil {
		return
	}
	if _, exists := peerTopics[topic]; !exists {
		return
	}
	delete(peerTopics, topic)
	delete(l.streams, streamReuseSlotKey{peer: pid, topic: topic})
	if len(peerTopics) == 0 {
		delete(l.peerTopics, pid)
	}
	if l.topicUses[topic] <= 1 {
		delete(l.topicUses, topic)
	} else {
		l.topicUses[topic]--
	}
	if l.total > 0 {
		l.total--
	}
}

// stop resets every held reusable stream before forgetting its slot. There are
// at most 32 entries, so Sync.Stop cannot leave an unbounded idle reader.
func (l *streamReuseLimiter) stop() {
	if l == nil {
		return
	}
	l.mu.Lock()
	streams := l.streams
	l.streams = make(map[streamReuseSlotKey]network.Stream)
	l.topicUses = make(map[string]int)
	l.peerTopics = make(map[peer.ID]map[string]struct{})
	l.total = 0
	l.mu.Unlock()
	for _, stream := range streams {
		if stream != nil {
			_ = stream.Reset()
		}
	}
}

func (l *streamReuseLimiter) activeSlots() int {
	if l == nil {
		return 0
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.total
}

func (l *streamReuseLimiter) activeTopicSlots(topic string) int {
	if l == nil {
		return 0
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.topicUses[topic]
}

func (l *streamReuseLimiter) trackedStreams() int {
	if l == nil {
		return 0
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.streams)
}

// peerSupportsStreamReuse reports whether pe has advertised a protocol
// version that includes the length-delimited multi-message decoder.
// A nil/unknown chain state is treated as unsupported (safe default:
// half-close after each request).
func peerSupportsStreamReuse(pe *peers.Peer) bool {
	if pe == nil {
		return false
	}
	cs := pe.ChainState()
	if cs == nil {
		return false
	}
	return cs.ProtocolVersion >= uint32(bdagprotocol.StreamReuseProtocolVersion)
}

// toProtocolID exists because Sync.Send's own parameter is itself named
// `protocol`, shadowing the go-libp2p `protocol` package within that
// method's body.
func toProtocolID(topic string) protocol.ID {
	return protocol.ID(topic)
}

// rpcHandler is responsible for handling and responding to any incoming message.
// This method may return an error to internal monitoring, but the error will
// not be relayed to the peer.
type rpcHandler func(context.Context, any, libp2pcore.Stream, *peers.Peer) *common.Error

// RespTimeout is the maximum time for complete response transfer.
const RespTimeout = 20 * time.Second

var responseCloseGrace = time.Second

// ReqTimeout is the maximum time for complete request transfer.
const ReqTimeout = 20 * time.Second

// HandleTimeout is the maximum time for complete handler.
const HandleTimeout = 20 * time.Second

type Sync struct {
	peers         *peers.Status
	peerSync      *PeerSync
	p2p           peers.P2P
	PeerInterval  time.Duration
	LANPeers      map[peer.ID]struct{}
	goodbyes      *goodbyeRecorder
	streamReuse   *streamReuseLimiter
	outboundReuse *outboundReuseTracker
	streamTiming  streamReuseTiming
	stopping      atomic.Bool

	// sendOverride is nil in production and is limited to focused tests that
	// need to exercise callers of the concrete Send path without a transport.
	sendOverride func(*peers.Peer, string, any) (any, error)

	// beforeReusableAcquire is a test seam immediately before an inbound
	// reusable slot would be claimed. Production code leaves it nil.
	beforeReusableAcquire func()

	disconnectionNotify *network.NotifyBundle
	connectionNotify    *network.NotifyBundle
	outboundReuseNotify *network.NotifyBundle
}

func (s *Sync) Start() error {
	s.registerHandlers()

	s.AddConnectionHandler()
	s.AddDisconnectionHandler()
	s.addOutboundReuseDisconnectHandler()

	s.maintainPeerStatuses()

	return s.peerSync.Start()
}

func (s *Sync) Stop() error {
	s.stopping.Store(true)
	var stopErr error
	if s.peerSync != nil {
		stopErr = s.peerSync.Stop()
	}
	if s.streamReuse != nil {
		s.streamReuse.stop()
	}
	if s.outboundReuse != nil {
		s.outboundReuse.stop()
	}
	if s.connectionNotify != nil {
		s.p2p.Host().Network().StopNotify(s.connectionNotify)
	}
	if s.disconnectionNotify != nil {
		s.p2p.Host().Network().StopNotify(s.disconnectionNotify)
	}
	if s.outboundReuseNotify != nil {
		s.p2p.Host().Network().StopNotify(s.outboundReuseNotify)
	}
	return stopErr
}

func (s *Sync) reuseRunning() bool {
	return s != nil && !s.stopping.Load()
}

// addOutboundReuseDisconnectHandler keeps client-side stream counters bound
// even when a transport disconnect races ordinary RPC error cleanup.
func (s *Sync) addOutboundReuseDisconnectHandler() {
	if s == nil || s.p2p == nil || s.outboundReuse == nil {
		return
	}
	s.outboundReuseNotify = &network.NotifyBundle{
		DisconnectedF: func(_ network.Network, conn network.Conn) {
			s.outboundReuse.forgetPeer(conn.RemotePeer())
		},
	}
	s.p2p.Host().Network().Notify(s.outboundReuseNotify)
}

func (s *Sync) registerHandlers() {
	s.registerRPCHandlers()
	//s.registerSubscribers()
}

// registerRPCHandlers for p2p RPC.
func (s *Sync) registerRPCHandlers() {

	s.registerRPC(
		RPCGoodByeTopic,
		new(uint64),
		s.goodbyeRPCHandler,
	)

	s.registerRPC(
		RPCPingTopic,
		new(uint64),
		s.pingHandler,
	)

	s.registerRPC(
		RPCMetaDataTopic,
		nil,
		s.metaDataHandler,
	)

	s.registerRPC(
		RPCChainState,
		&pb.ChainState{},
		s.chainStateHandler,
	)

	s.registerRPC(
		RPCGetBlocks,
		&pb.GetBlocks{},
		s.getBlocksHandler,
	)

	s.registerRPC(
		RPCGetBlockDatas,
		&pb.GetBlockDatas{},
		s.getBlockDataHandler,
	)

	s.registerRPC(
		RPCSyncDAG,
		&pb.SyncDAG{},
		s.syncDAGHandler,
	)

	s.registerRPC(
		RPCContinueSyncDAG,
		&pb.ContinueSyncDAG{},
		s.conSyncDAGHandler,
	)

	s.registerRPC(
		RPCTransaction,
		&pb.GetTxs{},
		s.txHandler,
	)

	s.registerRPC(
		RPCInventory,
		&pb.Inventory{},
		s.inventoryHandler,
	)

	s.registerRPC(
		RPCGraphState,
		&pb.GraphState{},
		s.graphStateHandler,
	)

	s.registerRPC(
		RPCSyncQNR,
		&pb.SyncQNR{},
		s.QNRHandler,
	)

	s.registerRPC(
		RPCGetMerkleBlocks,
		&pb.MerkleBlockRequest{},
		s.getMerkleBlockDataHandler,
	)

	// s.registerRPC(
	// 	RPCFilterAdd,
	// 	&pb.FilterAddRequest{},
	// 	s.HandlerFilterMsgAdd,
	// )

	// s.registerRPC(
	// 	RPCFilterClear,
	// 	&pb.FilterClearRequest{},
	// 	s.HandlerFilterMsgClear,
	// )

	// s.registerRPC(
	// 	RPCFilterLoad,
	// 	&pb.FilterLoadRequest{},
	// 	s.HandlerFilterMsgLoad,
	// )

	s.registerRPC(
		RPCMemPool,
		&pb.MemPoolRequest{},
		s.HandlerMemPool,
	)

	s.registerRPC(
		RPCGetData,
		&pb.Inventory{},
		s.GetDataHandler,
	)

	s.registerRPC(
		RPCStateRoot,
		&pb.StateRootReq{},
		s.stateRootHandler,
	)

	s.registerRPC(
		RPCBroadcastBlock,
		&pb.BroadcastBlock{},
		s.broadcastBlockHandler,
	)

	s.registerRPC(
		RPCRelayTx,
		&pb.Transaction{},
		s.relayTxHandler,
	)

}

// registerRPC for a given topic with an expected protobuf message type.
func (s *Sync) registerRPC(topic string, base any, handle rpcHandler) {
	if s == nil || s.p2p == nil {
		return
	}
	config := s.p2p.Config()
	localEnable := config != nil && config.EnableStreamReuse
	if s.streamReuse == nil {
		s.streamReuse = newStreamReuseLimiter()
	}
	registerRPCWithReuse(s.p2p, topic, base, handle, localEnable, s.streamReuse, s.streamTiming, s.reuseRunning, s.beforeReusableAcquire)
}

func (s *Sync) Send(pe *peers.Peer, protocol string, message any) (any, error) {
	if !s.reuseRunning() || !s.peerSync.IsRunning() {
		return nil, fmt.Errorf("No run PeerSync\n")
	}

	log.Trace("Send message", "protocol", getProtocol(s.p2p, protocol), "peer", lazyTraceValue(func() any { return pe.IDWithAddress() }))

	reuse := s.p2p.Config().EnableStreamReuse && poolableTopics[protocol] && peerSupportsStreamReuse(pe)
	pid := toProtocolID(getProtocol(s.p2p, protocol))
	var permit *outboundReusePermit
	var reusableStream network.Stream
	if reuse {
		reusableStream = pe.CheckoutStream(pid)
		permit = s.outboundReuse.beginIfRunning(pe, pid, reusableStream, s.reuseRunning)
		if permit == nil {
			if reusableStream != nil {
				pe.ReturnStream(pid, reusableStream)
			}
			reuse = false
			reusableStream = nil
		}
	}
	if !s.reuseRunning() {
		if permit != nil {
			s.outboundReuse.forget(permit)
		}
		if reusableStream != nil {
			pe.EvictStream(pid, reusableStream)
		}
		return nil, fmt.Errorf("No run PeerSync\n")
	}

	ctx, cancel := context.WithTimeout(s.p2p.Context(), ReqTimeout)
	defer cancel()

	var e *common.Error
	stream, e := sendWithReusableStream(ctx, s.p2p, message, protocol, pe, reuse, reusableStream)
	if e != nil && !e.Code.IsSuccess() {
		processSyncReqError(e, stream, pe, pid, reuse, s.outboundReuse, permit)
		return nil, e.ToError()
	}

	var ret any
	switch protocol {
	case RPCChainState:
		e = s.sendChainStateRequest(stream, pe)
	case RPCGoodByeTopic:
		e = s.sendGoodByeMessage(message, pe)
	case RPCGetBlockDatas:
		ret, e = s.sendGetBlockDataRequest(stream, pe)
	case RPCGetBlocks:
		ret, e = s.sendGetBlocksRequest(stream, pe)
	case RPCGraphState:
		ret, e = s.sendGraphStateRequest(stream, pe)
	case RPCInventory:
		e = s.sendInventoryRequest(stream, pe)
	case RPCMemPool:
		e = s.SendMempoolRequest(stream, pe)
	case RPCMetaDataTopic:
		ret, e = s.sendMetaDataRequest(stream, pe)
	case RPCPingTopic:
		e = s.SendPingRequest(stream, pe)
	case RPCSyncDAG:
		ret, e = s.sendSyncDAGRequest(stream, pe)
	case RPCContinueSyncDAG:
		ret, e = s.sendSyncDAGRequest(stream, pe)
	case RPCSyncQNR:
		ret, e = s.sendQNRRequest(stream, pe)
	case RPCTransaction:
		ret, e = s.sendTxRequest(stream, pe)
	case RPCRelayTx:
		ret, e = s.sendRelayTxRequest(stream, pe)
	case RPCStateRoot:
		ret, e = s.sendStateRootRequest(stream, pe)
	case RPCBroadcastBlock:
		e = s.sendBroadcastBlockRequest(stream, pe)
	default:
		return nil, fmt.Errorf("Can't support:%s", protocol)
	}
	processSyncReqError(e, stream, pe, pid, reuse, s.outboundReuse, permit)

	if e != nil && !e.Code.IsSuccess() {
		return nil, e.ToError()
	}
	return ret, nil
}

func (s *Sync) send(pe *peers.Peer, protocol string, message any) (any, error) {
	if s.sendOverride != nil {
		return s.sendOverride(pe, protocol, message)
	}
	return s.Send(pe, protocol, message)
}

func (s *Sync) PeerSync() *PeerSync {
	return s.peerSync
}

// Peers returns the peer status interface.
func (s *Sync) Peers() *peers.Status {
	return s.peers
}

func (s *Sync) Encoding() encoder.NetworkEncoding {
	return s.p2p.Encoding()
}

// SetStreamHandler sets the protocol handler on the p2p host multiplexer.
// This method is a pass through to libp2pcore.Host.SetStreamHandler.
func (s *Sync) SetStreamHandler(topic string, handler network.StreamHandler) {
	s.p2p.Host().SetStreamHandler(protocol.ID(topic), handler)
}

func (s *Sync) EncodeResponseMsg(stream libp2pcore.Stream, msg any) *common.Error {
	return EncodeResponseMsg(s.p2p, stream, msg, common.ErrNone)
}

func (s *Sync) EncodeResponseMsgPro(stream libp2pcore.Stream, msg any, retCode common.ErrorCode) *common.Error {
	return EncodeResponseMsg(s.p2p, stream, msg, retCode)
}

func NewSync(p2p peers.P2P) *Sync {
	sy := &Sync{p2p: p2p, peers: peers.NewStatus(p2p),
		// Maintenance can skip a tick when a response arrives just after it.
		// One target interval leaves that phase plus wire latency inside the
		// unchanged three-target-interval mining freshness budget.
		PeerInterval:  params.ActiveNetParams.TargetTimePerBlock,
		LANPeers:      map[peer.ID]struct{}{},
		goodbyes:      newGoodbyeRecorder(),
		streamReuse:   newStreamReuseLimiter(),
		outboundReuse: newOutboundReuseTracker(),
		streamTiming:  streamReuseTiming{firstByte: streamReuseFirstByte, active: RespTimeout}}
	sy.peerSync = NewPeerSync(sy)

	if n := p2p.Config().RPCHandlerConcurrency; n > 0 {
		rpcHandlerConcurrency = make(chan struct{}, n)
	}

	for _, pid := range p2p.Config().LANPeers {
		peid, err := peer.Decode(pid)
		if err != nil {
			log.Warn(fmt.Sprintf("LANPeers configuration error:%s", pid))
			continue
		}
		sy.LANPeers[peid] = struct{}{}
	}
	return sy
}

// defaultRPCHandlerConcurrency is used when the node config doesn't set
// P2PRPCConcurrency (e.g. programs other than the main node, like
// cmd/crawler and cmd/relaynode, that construct a Sync directly).
const defaultRPCHandlerConcurrency = 32

// rpcHandlerConcurrency bounds how many inbound RPC stream handlers --
// across all topics registered via RegisterRPC (inventory, getBlockDatas,
// chainstate, etc.) -- can run their actual handle() logic at once.
// libp2p dispatches each inbound stream on its own goroutine with no
// built-in concurrency limit, so with many connected peers this can
// otherwise spawn hundreds of simultaneous handler invocations, all
// contending for the same blockdag.BDAG.stateLock that the single
// blockchain handler goroutine also needs for block commits -- starving it
// out under a large sync backlog. This doesn't change what any handler
// does, just how many can be doing it at once; excess arrivals wait for a
// slot (bounded by the request's own timeout) rather than piling onto the
// lock in parallel.
//
// Sized from config.P2PRPCConcurrency (see NewSync) rather than hardcoded,
// since the right value depends on real traffic/lock-hold-time tradeoffs
// that need tuning against actual sync load, not a guess made in isolation.
var rpcHandlerConcurrency = make(chan struct{}, defaultRPCHandlerConcurrency)

// registerRPC for a given topic with an expected protobuf message type.
// newMsgFactory returns a function producing a fresh zero-value instance of
// base's underlying type on each call, for repeatedly decoding into across
// a loop. Computed once per RegisterRPC call rather than per stream/request,
// since it only depends on base, which is fixed per topic registration.
func newMsgFactory(base any) func() any {
	if base == nil {
		return nil
	}
	t := reflect.TypeOf(base)
	ty := t
	if t.Kind() == reflect.Ptr {
		ty = t.Elem()
	}
	return func() any {
		return reflect.New(ty).Interface()
	}
}

// RegisterRPC wires an inbound stream handler with the historical one-shot
// behavior. Reuse is deliberately available only through Sync.registerRPC,
// where local configuration and the peer's advertised capability are known.
func RegisterRPC(rpc peers.P2PRPC, basetopic string, base any, handle rpcHandler) {
	registerRPCOneShot(rpc, basetopic, base, handle)
}

func registerRPCWithReuse(rpc peers.P2PRPC, basetopic string, base any, handle rpcHandler, localEnable bool, limiter *streamReuseLimiter, timing streamReuseTiming, running func() bool, beforeAcquire func()) {
	topic := getProtocol(rpc, basetopic)
	newMsg := newMsgFactory(base)

	rpc.Host().SetStreamHandler(protocol.ID(topic), func(stream network.Stream) {
		if !rpc.IsRunning() || (running != nil && !running()) {
			log.Debug("PeerSync is not running, ignore the handling", "protocol", topic)
			if err := resetStream(stream); err != nil {
				log.Debug("failed to reset ignored inbound stream", "protocol", topic, "error", err)
			}
			return
		}

		pe := rpc.Peers().Fetch(stream.Conn().RemotePeer())
		pe.UpdateAddrDir(nil, stream.Conn().RemoteMultiaddr(), stream.Conn().Stat().Direction)
		reuse := localEnable && base != nil && poolableTopics[basetopic] && peerSupportsStreamReuse(pe)
		if !reuse {
			handleRPCOneShot(rpc, topic, newMsg, handle, stream, pe)
			return
		}
		handleRPCWithReuse(rpc, topic, basetopic, newMsg, handle, stream, pe, limiter, timing, running, beforeAcquire)
	})
}

func registerRPCOneShot(rpc peers.P2PRPC, basetopic string, base any, handle rpcHandler) {
	topic := getProtocol(rpc, basetopic)
	newMsg := newMsgFactory(base)
	rpc.Host().SetStreamHandler(protocol.ID(topic), func(stream network.Stream) {
		if !rpc.IsRunning() {
			log.Debug("PeerSync is not running, ignore the handling", "protocol", topic)
			if err := resetStream(stream); err != nil {
				log.Debug("failed to reset ignored inbound stream", "protocol", topic, "error", err)
			}
			return
		}
		pe := rpc.Peers().Fetch(stream.Conn().RemotePeer())
		pe.UpdateAddrDir(nil, stream.Conn().RemoteMultiaddr(), stream.Conn().Stat().Direction)
		handleRPCOneShot(rpc, topic, newMsg, handle, stream, pe)
	})
}

func handleRPCOneShot(rpc peers.P2PRPC, topic string, newMsg func() any, handle rpcHandler, stream network.Stream, pe *peers.Peer) {
	common.IngressConnectMeter.Mark(1)
	log.Trace("Stream handler", "protocol", topic, "peer", lazyTraceValue(func() any { return pe.IDWithAddress() }))
	ctx, cancel := context.WithTimeout(rpc.Context(), RespTimeout)
	defer cancel()
	SetRPCStreamDeadlines(stream)

	var msg any
	if newMsg != nil {
		msg = newMsg()
		if err := DecodeMessage(stream, rpc, msg); err != nil {
			e := common.NewError(common.ErrStreamRead, err)
			if strings.Contains(topic, RPCGoodByeTopic) {
				e.Add("Failed to decode goodbye stream message")
			} else {
				e.Add("Failed to decode stream message")
			}
			processRspError(ctx, e, stream, rpc, pe)
			return
		}
		size := rpc.Encoding().GetSize(msg)
		rpc.IncreaseBytesRecv(stream.Conn().RemotePeer(), size)
	}

	select {
	case rpcHandlerConcurrency <- struct{}{}:
	case <-ctx.Done():
		if err := resetStream(stream); err != nil {
			log.Debug("failed to reset one-shot stream after handler wait", "protocol", topic, "error", err)
		}
		return
	}
	e := handle(ctx, msg, stream, pe)
	<-rpcHandlerConcurrency
	processRspError(ctx, e, stream, rpc, pe)
}

func handleRPCWithReuse(rpc peers.P2PRPC, topic string, limitTopic string, newMsg func() any, handle rpcHandler, stream network.Stream, pe *peers.Peer, limiter *streamReuseLimiter, timing streamReuseTiming, running func() bool, beforeAcquire func()) {
	if timing.firstByte <= 0 {
		timing.firstByte = streamReuseFirstByte
	}
	if timing.active <= 0 {
		timing.active = RespTimeout
	}
	requestNum := 0
	for {
		if running != nil && !running() {
			closeReusableStream(stream, topic)
			return
		}
		common.IngressConnectMeter.Mark(1)
		log.Trace("Stream handler", "protocol", topic, "peer", lazyTraceValue(func() any { return pe.IDWithAddress() }))
		var ctx context.Context
		var cancel context.CancelFunc
		var msg any
		if newMsg != nil {
			msg = newMsg()
			if requestNum > 0 {
				if beforeAcquire != nil {
					beforeAcquire()
				}
				if !limiter.acquireIfRunning(pe.GetID(), limitTopic, stream, running) {
					closeReusableStream(stream, topic)
					return
				}
				err := decodeReusableRequest(stream, rpc, msg, timing.firstByte)
				limiter.release(pe.GetID(), limitTopic)
				if err != nil {
					if errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
						closeReusableStream(stream, topic)
						return
					}
					ctx, cancel = context.WithTimeout(rpc.Context(), timing.active)
					e := common.NewError(common.ErrStreamRead, err)
					if strings.Contains(topic, RPCGoodByeTopic) {
						e.Add("Failed to decode goodbye stream message")
					} else {
						e.Add("Failed to decode stream message")
					}
					processRspError(ctx, e, stream, rpc, pe)
					cancel()
					return
				}
				// The idle first-byte wait is deliberately outside the request
				// context. Start the full active budget only after decoding.
				ctx, cancel = context.WithTimeout(rpc.Context(), timing.active)
			} else {
				ctx, cancel = context.WithTimeout(rpc.Context(), RespTimeout)
				SetRPCStreamDeadlines(stream)
				err := DecodeMessage(stream, rpc, msg)
				if err != nil {
					e := common.NewError(common.ErrStreamRead, err)
					if strings.Contains(topic, RPCGoodByeTopic) {
						e.Add("Failed to decode goodbye stream message")
					} else {
						e.Add("Failed to decode stream message")
					}
					processRspError(ctx, e, stream, rpc, pe)
					cancel()
					return
				}
			}
			size := rpc.Encoding().GetSize(msg)
			rpc.IncreaseBytesRecv(stream.Conn().RemotePeer(), size)
		}

		if ctx == nil {
			ctx, cancel = context.WithTimeout(rpc.Context(), timing.active)
		}
		select {
		case rpcHandlerConcurrency <- struct{}{}:
		case <-ctx.Done():
			cancel()
			closeReusableStream(stream, topic)
			return
		}
		e := handle(ctx, msg, stream, pe)
		<-rpcHandlerConcurrency
		if e != nil && !e.Code.IsSuccess() {
			processRspError(ctx, e, stream, rpc, pe)
			cancel()
			return
		}
		requestNum++
		cancel()
		if requestNum >= streamReuseMaxRequests {
			closeReusableStream(stream, topic)
			return
		}
	}
}

func decodeReusableRequest(stream network.Stream, rpc peers.P2PRPC, msg any, firstByteTimeout time.Duration) error {
	firstByte := []byte{0}
	SetStreamReadDeadline(stream, firstByteTimeout)
	if _, err := io.ReadFull(stream, firstByte); err != nil {
		return err
	}
	SetRPCStreamDeadlines(stream)
	return rpc.Encoding().DecodeWithMaxLength(io.MultiReader(bytes.NewReader(firstByte), stream), msg)
}

func closeReusableStream(stream network.Stream, topic string) {
	if err := closeWriteStream(stream); err != nil {
		log.Debug("Failed to close write side of reused stream", "protocol", topic, "err", err)
	}
	if err := closeStream(stream); err != nil {
		log.Debug("Failed to close reused stream", "protocol", topic, "err", err)
	}
}

func processRspError(ctx context.Context, e *common.Error, stream network.Stream, rpc peers.P2PRPC, pe *peers.Peer) bool {
	streamOK := true
	if e == nil {
		e = common.NewSuccess()
	}
	if e.Code.IsStream() {
		streamOK = false
	} else if !e.Code.IsDAGConsensus() && !e.Code.IsSuccess() {
		resp, err := generateErrorResponse(e, rpc.Encoding())
		if err != nil {
			e.Add(fmt.Sprintf("Failed to generate a response error:%v", err))
			streamOK = false
		} else {
			_, err := stream.Write(resp)
			if err != nil {
				e.Add(fmt.Sprintf("Failed to write to stream:%v", err))
				streamOK = false
			}
		}
	}
	if streamOK {
		closeWriteErr := closeWriteStream(stream)
		if closeWriteErr != nil {
			e.AddError(closeWriteErr)
		}
		badRecorded := !e.Code.IsSuccess() || closeWriteErr != nil
		if badRecorded {
			pe.IncrementBadResponses(e)
		}
		// A successful one-shot response has already completed its ownership
		// transfer at CloseWrite. Waiting for peer EOF here can retain a handler
		// indefinitely and is unnecessary for a stream that is about to close.
		// Error responses retain the historical bounded wait so peers that still
		// have a response in flight can finish cleanly without the old 20-second
		// RespTimeout hold.
		if !e.Code.IsSuccess() || closeWriteErr != nil {
			if err := waitForPeerClose(ctx, stream); err != nil {
				e.AddError(err)
			}
		}
		closeErr := closeStream(stream)
		if closeErr != nil {
			e.AddError(closeErr)
			if !badRecorded {
				pe.IncrementBadResponses(e)
			}
		}
	} else {
		err := resetStream(stream)
		if err != nil {
			e.AddError(err)
		}
		pe.IncrementBadResponses(e)
	}
	return streamOK
}

func waitForPeerClose(ctx context.Context, stream network.Stream) error {
	if responseCloseGrace <= 0 {
		return nil
	}
	select {
	case <-ctx.Done():
		return nil
	default:
	}

	deadline := time.Now().Add(responseCloseGrace)
	if ctxDeadline, ok := ctx.Deadline(); ok && ctxDeadline.Before(deadline) {
		deadline = ctxDeadline
	}
	if err := stream.SetReadDeadline(deadline); err != nil {
		return fmt.Errorf("failed to set stream close read deadline: %w", err)
	}
	defer func() {
		_ = stream.SetReadDeadline(time.Time{})
	}()

	var buf [1]byte
	if _, err := stream.Read(buf[:]); err != nil {
		if errors.Is(err, io.EOF) || isTimeoutError(err) {
			return nil
		}
		return fmt.Errorf("failed waiting for peer stream close: %w", err)
	}
	return nil
}

func isTimeoutError(err error) bool {
	var netErr net.Error
	return errors.As(err, &netErr) && netErr.Timeout()
}

// processSyncReqError is the Sync-owned counterpart to processReqError. It
// keeps the 128-request counter coupled to the peer-pool lifecycle, including
// the otherwise benign ReturnStream collision path.
func processSyncReqError(e *common.Error, stream network.Stream, pe *peers.Peer, pid protocol.ID, poolIt bool, tracker *outboundReuseTracker, permit *outboundReusePermit) bool {
	if !poolIt || permit == nil {
		return processReqError(e, stream, pe, pid, poolIt)
	}
	streamOK := true
	if e == nil {
		e = common.NewSuccess()
	}
	if e.Code.IsStream() {
		streamOK = false
	}
	if streamOK && tracker.finish(permit, stream, true) && returnTrackedStream(tracker, permit, pe, pid, stream) {
		if !e.Code.IsSuccess() {
			pe.IncrementBadResponses(e)
		}
		return true
	}
	tracker.finish(permit, stream, false)
	if streamOK {
		if err := closeStream(stream); err != nil {
			e.AddError(err)
			pe.IncrementBadResponses(e)
		}
		if !e.Code.IsSuccess() {
			pe.IncrementBadResponses(e)
		}
		return true
	}
	pe.EvictStream(pid, stream)
	pe.IncrementBadResponses(e)
	return false
}

// returnTrackedStream verifies that the stream we counted is the stream the
// peer pool retained. ReturnStream resets a collision loser, so retaining its
// counter would otherwise create stale bookkeeping even though the tracker is
// capacity-bounded.
func returnTrackedStream(tracker *outboundReuseTracker, permit *outboundReusePermit, pe *peers.Peer, pid protocol.ID, stream network.Stream) bool {
	pe.ReturnStream(pid, stream)
	pooled := pe.CheckoutStream(pid)
	if pooled != stream {
		if pooled != nil {
			pe.ReturnStream(pid, pooled)
		}
		tracker.forget(permit)
		return false
	}
	pe.ReturnStream(pid, stream)
	if tracker.markReturned(permit, stream) {
		return true
	}
	pe.EvictStream(pid, stream)
	return false
}

// processReqError finalizes the client-side stream after a request/response
// round trip. When poolIt is true and the stream is transport-healthy
// (streamOK), the stream is returned to pe's pool for reuse instead of
// being closed; on a transport-level failure it's evicted (removed from the
// pool, if present, and reset) instead of just reset. poolIt must exactly
// match the reuse decision made when the stream was obtained from Send, so
// a stream is never pooled that Send didn't intend to keep open.
func processReqError(e *common.Error, stream network.Stream, pe *peers.Peer, pid protocol.ID, poolIt bool) bool {
	streamOK := true
	if e == nil {
		e = common.NewSuccess()
	}
	if e.Code.IsStream() {
		streamOK = false
	}
	if streamOK {
		if poolIt {
			pe.ReturnStream(pid, stream)
			if !e.Code.IsSuccess() {
				pe.IncrementBadResponses(e)
			}
		} else {
			err := closeStream(stream)
			if err != nil {
				e.AddError(err)
			}
			if !e.Code.IsSuccess() || err != nil {
				pe.IncrementBadResponses(e)
			}
		}
	} else {
		if poolIt {
			pe.EvictStream(pid, stream)
		} else {
			err := resetStream(stream)
			if err != nil {
				e.AddError(err)
			}
		}
		pe.IncrementBadResponses(e)
	}
	return streamOK
}

// Send a message to a specific peer. The returned stream may be used for reading, but has been
// closed for writing -- unless reuse is true, in which case the stream is
// left open in both directions so the caller can return it to the peer's
// stream pool afterwards (see processReqError) for a later Send call to
// reuse, amortizing the per-stream libp2p protocol-negotiation cost across
// many sequential requests to the same peer.
//
// When reuse is true, Send first tries to check out an already-open pooled
// stream for this topic instead of opening a new one. A pooled stream may
// be stale (e.g. the peer simply timed out, or a prior round-trip raced a
// disconnect) -- if writing the request to a reused stream fails, that's
// always safe to retry transparently on a fresh stream, since nothing has
// been sent yet; a failure past that point (encoding/writing succeeded,
// only the exact point of failure differs) is surfaced as a normal request
// error rather than retried, since the request may have already partially
// landed and blind retry could double-send it.
//
// Callers (Sync.Send) must only pass reuse=true for peers that advertise
// StreamReuseProtocolVersion: skipping the post-request half-close is only
// safe against peers whose decoder reads an exact message length. Against
// older peers (ReadAll-until-EOF) the half-close is load-bearing -- without
// it the peer hangs until timeout and sync stalls.
func Send(ctx context.Context, rpc peers.P2PRPC, message any, baseTopic string, pe *peers.Peer, reuse bool) (network.Stream, *common.Error) {
	return sendWithReusableStream(ctx, rpc, message, baseTopic, pe, reuse, nil)
}

// sendWithReusableStream is Send's internal form for Sync-owned pooling. When
// selected is non-nil it was checked out under the matching tracker permit,
// which prevents a second Sync request from racing its lifecycle.
func sendWithReusableStream(ctx context.Context, rpc peers.P2PRPC, message any, baseTopic string, pe *peers.Peer, reuse bool, selected network.Stream) (network.Stream, *common.Error) {
	curState := rpc.Host().Network().Connectedness(pe.GetID())
	if curState == network.CannotConnect {
		return nil, common.NewErrorStr(common.ErrLibp2pConnect, curState.String())
	}

	topic := getProtocol(rpc, baseTopic)
	pid := protocol.ID(topic)

	triedFresh := false
	for {
		var stream network.Stream
		reused := false
		if selected != nil {
			stream = selected
			selected = nil
			reused = true
		} else if reuse {
			if s := pe.CheckoutStream(pid); s != nil {
				stream = s
				reused = true
			}
		}
		if stream == nil {
			var deadline = ReqTimeout + RespTimeout
			newStreamCtx, cancel := context.WithTimeout(ctx, deadline)
			var err error
			stream, err = rpc.Host().NewStream(newStreamCtx, pe.GetID(), pid)
			cancel()
			if err != nil {
				return nil, common.NewErrorStr(common.ErrStreamBase, fmt.Sprintf("open stream on topic %v failed", topic))
			}
			common.EgressConnectMeter.Mark(1)
		}
		SetRPCStreamDeadlines(stream)

		// do not encode anything if we are sending a metadata request
		if message == nil {
			if !reuse {
				if err := closeWriteStream(stream); err != nil {
					_ = resetStream(stream)
					return nil, common.NewError(common.ErrStreamBase, err)
				}
			}
			return stream, nil
		}

		size, err := EncodeMessage(stream, rpc, message)
		if err != nil {
			if reused && !triedFresh {
				pe.EvictStream(pid, stream)
				triedFresh = true
				continue
			}
			_ = resetStream(stream)
			return nil, common.NewErrorStr(common.ErrStreamWrite, fmt.Sprintf("encocde rpc message %v to stream failed:%v", getMessageString(message), err))
		}
		rpc.IncreaseBytesSent(pe.GetID(), size)
		if !reuse {
			// Close stream for writing.
			if err := closeWriteStream(stream); err != nil {
				_ = resetStream(stream)
				return nil, common.NewError(common.ErrStreamBase, err)
			}
		}
		return stream, nil
	}
}

func EncodeResponseMsg(rpc peers.P2PRPC, stream libp2pcore.Stream, msg any, retCode common.ErrorCode) *common.Error {
	_, err := stream.Write([]byte{byte(retCode)})
	if err != nil {
		return common.NewError(common.ErrStreamWrite, fmt.Errorf("%s, %s", err, retCode.String()))
	}
	if msg != nil {
		size, err := EncodeMessage(stream, rpc, msg)
		if err != nil {
			return common.NewError(common.ErrStreamWrite, fmt.Errorf("%s, %s", err, retCode.String()))
		}
		rpc.IncreaseBytesSent(stream.Conn().RemotePeer(), size)
	}
	return nil
}

func getTopic(baseTopic string) string {
	if baseTopic == RPCChainState || baseTopic == RPCGoodByeTopic {
		return baseTopic
	}
	return baseTopic + "/" + params.ActiveNetParams.Name
}

func getProtocol(rpc peers.P2PRPC, base string) string {
	return getTopic(base) + rpc.Encoding().ProtocolSuffix()
}

func processUnderlyingError(rpc peers.P2PRPC, pid peer.ID, err error) {
	if err.Error() == io.EOF.Error() {
		return
	}
	log.Info(fmt.Sprintf("An underlying error(%s), try to terminate the connection:%s", err, pid.String()))
	err = rpc.Disconnect(pid)
	if err != nil {
		log.Error(err.Error())
	}
}
