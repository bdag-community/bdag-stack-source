package peers

import (
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	blockdag "github.com/BlockdagNetworkLabs/bdag/blockdag"
	"github.com/BlockdagNetworkLabs/bdag/common/hash"
	"github.com/BlockdagNetworkLabs/bdag/common/roughtime"
	"github.com/BlockdagNetworkLabs/bdag/core/protocol"
	"github.com/BlockdagNetworkLabs/bdag/core/types"
	ethcommon "github.com/ethereum/go-ethereum/common"

	"github.com/BlockdagNetworkLabs/bdag/p2p/common"
	pb "github.com/BlockdagNetworkLabs/bdag/p2p/proto/v1"
	"github.com/BlockdagNetworkLabs/bdag/p2p/qnode"
	"github.com/BlockdagNetworkLabs/bdag/p2p/qnr"
	"github.com/BlockdagNetworkLabs/bdag/params"
	"github.com/gogo/protobuf/proto"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	libp2pprotocol "github.com/libp2p/go-libp2p/core/protocol"
	ma "github.com/multiformats/go-multiaddr"
)

var (
	// maxBadResponses is the maximum number of bad responses from a peer before we stop talking to it.
	MaxBadResponses = 50
)

const (
	MinBroadcastRecord  = 10
	BroadcastRecordLife = 30 * time.Minute
	BadResponseLife     = 30 * time.Second
)

// Peer represents a connected p2p network remote node.
type Peer struct {
	*peerStatus
	pid       peer.ID
	syncPoint *ethcommon.Hash
	// Use to fee filter
	feeFilter int64
	// filter    *bloom.Filter

	lock       *sync.RWMutex
	lastSend   time.Time
	lastRecv   time.Time
	bytesSent  uint64
	bytesRecv  uint64
	conTime    time.Time
	timeOffset int64

	bidChanCap time.Time

	HSlock         *sync.RWMutex
	graphStateTime time.Time

	rateTasks map[string]*time.Timer

	broadcast map[string]any

	reconnect uint64

	mempoolreq time.Time

	dagPort int
	evmPort int

	// streamPoolLock guards streamPool. Deliberately a separate lock from
	// `lock`: checking a stream in/out can involve resetting a broken
	// stream (network I/O), while `lock` is taken very frequently by cheap
	// accessors (GetID, IsConnected, GraphState, ...) from many goroutines
	// -- sharing one lock would let a slow stream teardown stall unrelated
	// peer-state reads.
	streamPoolLock sync.Mutex
	streamPool     map[libp2pprotocol.ID]network.Stream
}

func (p *Peer) GetID() peer.ID {
	p.lock.RLock()
	defer p.lock.RUnlock()

	return p.pid
}

// IDWithAddress returns the printable id and address of the remote peer.
// It's useful on printing out the trace log messages.
func (p *Peer) IDWithAddress() string {
	p.lock.RLock()
	defer p.lock.RUnlock()

	return p.idWithAddress()
}

func (p *Peer) idWithAddress() string {
	return fmt.Sprintf("%s %s", p.pid, p.address)
}

// BadResponses obtains the number of bad responses we have received from the given remote peer.
// This will error if the peer does not exist.
func (p *Peer) BadResponses() []*BadResponse {
	p.lock.RLock()
	defer p.lock.RUnlock()

	return p.badResponses
}

func (p *Peer) badResponseStrs() []string {
	brs := make([]string, 0, len(p.badResponses))
	for _, v := range p.badResponses {
		brs = append(brs, v.String())
	}
	return brs
}

// IsBad states if the peer is to be considered bad.
// If the peer is unknown this will return `false`, which makes using this function easier than returning an error.
func (p *Peer) IsBad() bool {
	p.lock.RLock()
	defer p.lock.RUnlock()
	return p.isBad()
}

func (p *Peer) isBad() bool {
	l := len(p.badResponses)
	if l <= 0 {
		return false
	}
	return time.Since(p.badResponses[l-1].Time) <= BadResponseLife
}

// IncrementBadResponses increments the number of bad responses we have received from the given remote peer.
func (p *Peer) IncrementBadResponses(err *common.Error) {
	p.lock.Lock()
	defer p.lock.Unlock()

	if p.badResponses == nil {
		p.badResponses = []*BadResponse{}
	}
	l := len(p.badResponses)
	br := &BadResponse{Time: time.Now(), Err: err}
	if l <= 0 {
		br.ID = 1
	} else {
		br.ID = p.badResponses[l-1].ID + 1
	}
	if l > 0 && p.badResponses[l-1].Err.String() == err.String() {
		p.badResponses[l-1] = br
	} else {
		p.badResponses = append(p.badResponses, br)
	}

	log.Debug("Bad responses", "peer", p.idWithAddress(), "err", err.String())
	//
	if len(p.badResponses) > MaxBadResponses {
		p.badResponses = p.badResponses[1:]
	}
}

func (p *Peer) ResetBad() {
	p.lock.Lock()
	defer p.lock.Unlock()

	p.badResponses = []*BadResponse{}

	log.Debug(fmt.Sprintf("Bad responses reset:%s", p.pid.String()))
}

func (p *Peer) UpdateAddrDir(record *qnr.Record, address ma.Multiaddr, direction network.Direction) {
	p.lock.Lock()
	defer p.lock.Unlock()

	p.address = address
	p.direction = direction
	if record != nil {
		p.qnr = record
	}
}

// Address returns the multiaddress of the given remote peer.
// This will error if the peer does not exist.
func (p *Peer) Address() ma.Multiaddr {
	p.lock.RLock()
	defer p.lock.RUnlock()

	return p.address
}

func (p *Peer) QAddress() *common.QMultiaddr {
	p.lock.RLock()
	defer p.lock.RUnlock()

	return p.qaddress()
}

func (p *Peer) qaddress() *common.QMultiaddr {
	if p.address == nil {
		return nil
	}
	qma, err := common.QMultiAddrFromString(fmt.Sprintf("%s", p.address.String()+"/p2p/"+p.pid.String()))
	if err != nil {
		return nil
	}
	return qma
}

// Direction returns the direction of the given remote peer.
// This will error if the peer does not exist.
func (p *Peer) Direction() network.Direction {
	p.lock.RLock()
	defer p.lock.RUnlock()

	return p.direction
}

// QNR returns the enr for the corresponding peer id.
func (p *Peer) QNR() *qnr.Record {
	p.lock.RLock()
	defer p.lock.RUnlock()

	return p.qnr
}

func (p *Peer) Node() *qnode.Node {
	p.lock.RLock()
	defer p.lock.RUnlock()
	return p.node()
}

// func (p *Peer) Filter() *bloom.Filter {
// 	p.lock.RLock()
// 	defer p.lock.RUnlock()
// 	return p.filter
// }

func (p *Peer) node() *qnode.Node {
	if p.qnr == nil {
		return nil
	}

	n, err := qnode.New(qnode.ValidSchemes, p.qnr)
	if err != nil {
		log.Error("qnode: can't verify local record: %v", err)
		return nil
	}
	return n
}

// ConnectionState gets the connection state of the given remote peer.
// This will error if the peer does not exist.
func (p *Peer) ConnectionState() PeerConnectionState {
	p.lock.RLock()
	defer p.lock.RUnlock()

	return p.peerState
}

// IsActive checks if a peers is active and returns the result appropriately.
func (p *Peer) IsActive() bool {
	p.lock.RLock()
	defer p.lock.RUnlock()

	if p.isBad() {
		return false
	}
	if !p.canConnectWithNetwork() {
		return false
	}
	return p.peerState.IsConnected()
}

func (p *Peer) IsConnected() bool {
	p.lock.RLock()
	defer p.lock.RUnlock()

	return p.peerState.IsConnected()
}

// SetConnectionState sets the connection state of the given remote peer.
func (p *Peer) SetConnectionState(state PeerConnectionState) {
	p.lock.Lock()
	defer p.lock.Unlock()

	p.peerState = state

	if state.IsConnected() || state.IsDisconnected() {
		p.conTime = time.Now()
	}
}

// SetChainState sets the chain state of the given remote peer.
func (p *Peer) SetChainState(chainState *pb.ChainState) {
	p.lock.Lock()
	defer p.lock.Unlock()

	p.chainState = chainState
	p.chainStateLastUpdated = time.Now()
	p.timeOffset = int64(p.chainState.Timestamp) - roughtime.Now().Unix()
	p.graphStateTime = time.Now()
	p.stateRootOrder = uint64(chainState.GraphState.MainOrder)
	log.Trace(fmt.Sprintf("SetChainState(%s) : MainHeight=%d", p.pid.ShortString(), chainState.GraphState.MainHeight))

	// Parse UserAgent for ports
	p.dagPort = 0
	p.evmPort = 0
	ua := string(chainState.UserAgent)
	parts := strings.Split(ua, "|")
	for _, part := range parts {
		if strings.HasPrefix(part, "DAG:") {
			if port, err := strconv.Atoi(strings.TrimPrefix(part, "DAG:")); err == nil {
				p.dagPort = port
			}
		} else if strings.HasPrefix(part, "EVM:") {
			if port, err := strconv.Atoi(strings.TrimPrefix(part, "EVM:")); err == nil {
				p.evmPort = port
			}
		}
	}
}

// ChainState gets the chain state of the given remote peer.
// This can return nil if there is no known chain state for the peer.
// This will error if the peer does not exist.
func (p *Peer) ChainState() *pb.ChainState {
	p.lock.RLock()
	defer p.lock.RUnlock()

	return p.chainState
}

// ChainStateLastUpdated gets the last time the chain state of the given remote peer was updated.
// This will error if the peer does not exist.
func (p *Peer) ChainStateLastUpdated() time.Time {
	p.lock.RLock()
	defer p.lock.RUnlock()

	return p.chainStateLastUpdated
}

// SetMetadata sets the metadata of the given remote peer.
func (p *Peer) SetMetadata(metaData *pb.MetaData) {
	p.lock.Lock()
	defer p.lock.Unlock()

	p.metaData = metaData
}

// Metadata returns a copy of the metadata corresponding to the provided
// peer id.
func (p *Peer) Metadata() *pb.MetaData {
	p.lock.RLock()
	defer p.lock.RUnlock()

	return proto.Clone(p.metaData).(*pb.MetaData)
}

// CommitteeIndices retrieves the committee subnets the peer is subscribed to.
func (p *Peer) CommitteeIndices() []uint64 {
	p.lock.RLock()
	defer p.lock.RUnlock()

	if p.qnr == nil || p.metaData == nil {
		return []uint64{}
	}
	return retrieveIndicesFromBitfield(p.metaData.Subnets)
}

func (p *Peer) StatsSnapshot() (*StatsSnap, error) {
	p.lock.RLock()
	defer p.lock.RUnlock()

	parseErr, name, version, networkName := p.userAgentParts()
	if parseErr != nil {
		name = p.userAgent()
		version = ""
		networkName = ""
	}
	ss := &StatsSnap{
		PeerID:         p.pid,
		Protocol:       p.protocolVersion(),
		Genesis:        p.genesis(),
		Services:       p.services(),
		Name:           name,
		Version:        version,
		Network:        networkName,
		State:          p.peerState.IsConnected(),
		Direction:      p.direction,
		TimeOffset:     p.timeOffset,
		ConnTime:       time.Since(p.conTime),
		LastSend:       p.lastSend,
		LastRecv:       p.lastRecv,
		BytesSent:      p.bytesSent,
		BytesRecv:      p.bytesRecv,
		IsCircuit:      p.isCircuit(),
		Bads:           p.badResponseStrs(),
		ReConnect:      p.reconnect,
		StateRoot:      p.stateRootAndOrder(),
		MempoolReqTime: p.mempoolreq,
		Tasks:          len(p.rateTasks),
		Broadcast:      len(p.broadcast),
		DAGPort:        p.dagPort,
		EVMPort:        p.evmPort,
		Fork:           p.forkObservation.Clone(),
	}
	if p.chainState != nil && p.chainState.EvmCheckpoint != nil {
		ss.EVMCheckpoint = proto.Clone(p.chainState.EvmCheckpoint).(*pb.EVMCheckpoint)
	}
	if p.syncPoint != nil {
		ss.SyncPoint = *p.syncPoint
	}
	n := p.node()
	if n != nil {
		ss.NodeID = n.ID().String()
		ss.QNR = n.String()
	}
	if p.qaddress() != nil {
		ss.Address = p.qaddress().String()
	}
	if p.isConsensus() {
		ss.GraphState = p.graphState()
		ss.GraphStateDur = time.Since(p.graphStateTime)
	}
	return ss, nil
}

func (p *Peer) Timestamp() time.Time {
	p.lock.RLock()
	defer p.lock.RUnlock()

	if p.chainState == nil {
		return time.Time{}
	}
	return time.Unix(int64(p.chainState.Timestamp), 0)
}

func (p *Peer) SetQNR(record *qnr.Record) {
	p.lock.Lock()
	defer p.lock.Unlock()

	p.qnr = record
}

func (p *Peer) protocolVersion() uint32 {
	if p.chainState == nil {
		return 0
	}
	return p.chainState.ProtocolVersion
}

func (p *Peer) genesis() *ethcommon.Hash {
	if p.chainState == nil {
		return nil
	}
	if p.chainState.GenesisHash == nil {
		return nil
	}
	genesisHash, err := hash.NewHash(p.chainState.GenesisHash.Hash)
	if err != nil {
		return nil
	}
	return genesisHash
}

func (p *Peer) SetStateRoot(stateRoot *ethcommon.Hash, order uint64) {
	p.lock.Lock()
	defer p.lock.Unlock()

	p.stateRoot = stateRoot
	p.stateRootOrder = order
}

func (p *Peer) stateRootAndOrder() string {
	if p.stateRoot == nil {
		return ""
	}
	return fmt.Sprintf("%s (order:%d)", p.stateRoot.String(), p.stateRootOrder)
}

func (p *Peer) Services() protocol.ServiceFlag {
	p.lock.RLock()
	defer p.lock.RUnlock()

	return p.services()
}

func (p *Peer) services() protocol.ServiceFlag {
	if p.chainState == nil {
		return protocol.Unknown
	}
	return protocol.ServiceFlag(p.chainState.Services)
}

func (p *Peer) userAgent() string {
	if p.chainState == nil {
		return ""
	}
	return string(p.chainState.UserAgent)
}

func (p *Peer) graphState() *blockdag.GraphState {
	if p.chainState == nil || p.chainState.GraphState == nil || len(p.chainState.GraphState.Tips) > blockdag.MaxTips {
		return nil
	}
	gs := blockdag.NewGraphState()
	gs.SetTotal(uint(p.chainState.GraphState.Total))
	gs.SetLayer(uint(p.chainState.GraphState.Layer))
	gs.SetMainHeight(uint(p.chainState.GraphState.MainHeight))
	gs.SetMainOrder(uint(p.chainState.GraphState.MainOrder))
	tips := make([]*ethcommon.Hash, 0, len(p.chainState.GraphState.Tips))
	for _, tip := range p.chainState.GraphState.Tips {
		if tip == nil || len(tip.Hash) != hash.HashSize {
			return nil
		}
		h, err := hash.NewHash(tip.Hash)
		if err != nil {
			return nil
		}
		tips = append(tips, h)
	}
	gs.SetTips(tips)
	return gs
}

func (p *Peer) GraphState() *blockdag.GraphState {
	p.lock.RLock()
	defer p.lock.RUnlock()

	return p.graphState()
}

func (p *Peer) UpdateGraphState(gs *pb.GraphState) {
	p.lock.Lock()
	defer p.lock.Unlock()

	if gs == nil {
		return
	}
	// Require handshake ChainState so Services/consensus classification stays
	// intact. Inventing an empty ChainState (Services=0) made the sync peer
	// drop out of consensus freshness while still being SyncPeerPresent, which
	// produced false sync_peer_stale verdicts during tip-chase.
	if p.chainState == nil {
		return
	}
	p.chainState.GraphState = gs
	log.Trace(fmt.Sprintf("UpdateGraphState(%s) : MainHeight=%d", p.pid.ShortString(), gs.MainHeight))

	p.graphStateTime = time.Now()
}

func (p *Peer) UpdateSyncPoint(point *ethcommon.Hash) {
	p.lock.Lock()
	defer p.lock.Unlock()

	p.syncPoint = point
}

func (p *Peer) SyncPoint() *ethcommon.Hash {
	p.lock.RLock()
	defer p.lock.RUnlock()

	return p.syncPoint
}

func (p *Peer) SetForkObservation(observation *PeerForkObservation) {
	p.lock.Lock()
	defer p.lock.Unlock()

	p.forkObservation = observation.Clone()
}

func (p *Peer) ForkObservation() *PeerForkObservation {
	p.lock.RLock()
	defer p.lock.RUnlock()

	return p.forkObservation.Clone()
}

func (p *Peer) DisableRelayTx() bool {
	p.lock.RLock()
	defer p.lock.RUnlock()

	if p.chainState == nil {
		return false
	}
	return p.chainState.DisableRelayTx
}

func (p *Peer) FeeFilter() int64 {
	p.lock.RLock()
	defer p.lock.RUnlock()
	return p.feeFilter
}

func (p *Peer) ConnectionTime() time.Time {
	p.lock.RLock()
	defer p.lock.RUnlock()

	return p.conTime
}

func (p *Peer) IsRelay() bool {
	p.lock.RLock()
	defer p.lock.RUnlock()

	if p.chainState == nil {
		return false
	}
	return protocol.HasServices(protocol.ServiceFlag(p.chainState.Services), protocol.Relay)
}

func (p *Peer) IsConsensus() bool {
	p.lock.RLock()
	defer p.lock.RUnlock()

	return p.isConsensus()
}

func (p *Peer) isConsensus() bool {
	if p.chainState == nil {
		return false
	}
	return HasConsensusService(protocol.ServiceFlag(p.chainState.Services))
}

func (p *Peer) IncreaseBytesSent(size int) {
	p.lock.Lock()
	defer p.lock.Unlock()

	p.bytesSent += uint64(size)
	p.lastSend = time.Now()
}

func (p *Peer) BytesSent() uint64 {
	p.lock.RLock()
	defer p.lock.RUnlock()

	return p.bytesSent
}

func (p *Peer) IncreaseBytesRecv(size int) {
	p.lock.Lock()
	defer p.lock.Unlock()

	p.bytesRecv += uint64(size)
	p.lastRecv = time.Now()
}

func (p *Peer) BytesRecv() uint64 {
	p.lock.RLock()
	defer p.lock.RUnlock()

	return p.bytesRecv
}

func (p *Peer) GetName() string {
	p.lock.RLock()
	defer p.lock.RUnlock()
	return p.getName()
}

func (p *Peer) getName() string {
	err, name, _, _ := p.userAgentParts()
	if err != nil {
		return p.userAgent()
	}
	return name
}

func (p *Peer) GetVersion() string {
	p.lock.RLock()
	defer p.lock.RUnlock()
	return p.getVersion()
}

func (p *Peer) getVersion() string {

	err, _, version, _ := p.userAgentParts()
	if err != nil {
		return ""
	}
	return version
}

func (p *Peer) GetNetwork() string {
	p.lock.RLock()
	defer p.lock.RUnlock()
	return p.getNetwork()
}

func (p *Peer) getNetwork() string {
	err, _, _, network := p.userAgentParts()
	if err != nil {
		return ""
	}
	return network
}

func (p *Peer) userAgentParts() (error, string, string, string) {
	return ParseUserAgent(p.userAgent())
}

func (p *Peer) CanConnectWithNetwork() bool {
	p.lock.RLock()
	defer p.lock.RUnlock()
	return p.canConnectWithNetwork()
}

func (p *Peer) canConnectWithNetwork() bool {
	network := p.getNetwork()
	if len(network) <= 0 {
		return true
	}
	return params.ActiveNetParams.Name == network
}

func (p *Peer) GetBidChanCap() time.Time {
	p.lock.RLock()
	defer p.lock.RUnlock()
	return p.bidChanCap
}

func (p *Peer) SetBidChanCap(life time.Time) {
	p.lock.Lock()
	defer p.lock.Unlock()
	p.bidChanCap = life
}

func (p *Peer) isCircuit() bool {
	if p.direction == network.DirOutbound {
		return true
	}
	return !p.bidChanCap.IsZero()
}

func (p *Peer) RunRate(task string, delay time.Duration, f func()) {
	p.lock.Lock()
	defer p.lock.Unlock()

	rt, ok := p.rateTasks[task]
	if !ok {
		rt = time.NewTimer(delay)
		p.rateTasks[task] = rt
		go func() {
			select {
			case <-rt.C:
				f()
			}
			p.lock.Lock()
			delete(p.rateTasks, task)
			p.lock.Unlock()
		}()

		return
	}
	rt.Reset(delay)
}

func (p *Peer) Broadcast(key string, record any) {
	p.lock.Lock()
	defer p.lock.Unlock()
	p.broadcast[key] = record
}

func (p *Peer) HasBroadcast(key string) bool {
	p.lock.RLock()
	defer p.lock.RUnlock()
	_, ok := p.broadcast[key]
	return ok
}

func (p *Peer) UpdateBroadcast() {
	p.lock.Lock()
	defer p.lock.Unlock()

	for key, data := range p.broadcast {
		switch value := data.(type) {
		case *types.TxDesc:
			if time.Since(value.Added) > BroadcastRecordLife && len(p.broadcast) > MinBroadcastRecord {
				delete(p.broadcast, key)
			}
		}
	}
}

func (p *Peer) IncreaseReConnect() {
	p.lock.Lock()
	defer p.lock.Unlock()

	p.reconnect++
}

func (p *Peer) GetMempoolReqTime() time.Time {
	p.lock.RLock()
	defer p.lock.RUnlock()

	return p.mempoolreq
}

func (p *Peer) SetMempoolReqTime(t time.Time) {
	p.lock.Lock()
	defer p.lock.Unlock()

	p.mempoolreq = t
}

func NewPeer(pid peer.ID, point *ethcommon.Hash) *Peer {
	return &Peer{
		peerStatus: &peerStatus{
			peerState: PeerDisconnected,
		},
		pid:       pid,
		lock:      &sync.RWMutex{},
		HSlock:    &sync.RWMutex{},
		syncPoint: point,
		// filter:    bloom.LoadFilter(nil),
		rateTasks: map[string]*time.Timer{},
		broadcast: map[string]any{},
	}
}
