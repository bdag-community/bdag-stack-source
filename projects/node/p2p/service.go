package p2p

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io/ioutil"
	"math/rand"
	"net"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/BlockdagNetworkLabs/bdag/common/roughtime"
	"github.com/BlockdagNetworkLabs/bdag/config"
	"github.com/BlockdagNetworkLabs/bdag/consensus/model"
	"github.com/BlockdagNetworkLabs/bdag/core/blockchain"
	"github.com/BlockdagNetworkLabs/bdag/core/event"
	"github.com/BlockdagNetworkLabs/bdag/core/json"
	pv "github.com/BlockdagNetworkLabs/bdag/core/protocol"
	"github.com/BlockdagNetworkLabs/bdag/core/types"
	"github.com/BlockdagNetworkLabs/bdag/node/service"
	"github.com/BlockdagNetworkLabs/bdag/p2p/common"
	"github.com/BlockdagNetworkLabs/bdag/p2p/encoder"
	"github.com/BlockdagNetworkLabs/bdag/p2p/peers"
	pb "github.com/BlockdagNetworkLabs/bdag/p2p/proto/v1"
	"github.com/BlockdagNetworkLabs/bdag/p2p/qnode"
	"github.com/BlockdagNetworkLabs/bdag/p2p/qnr"
	"github.com/BlockdagNetworkLabs/bdag/p2p/runutil"
	"github.com/BlockdagNetworkLabs/bdag/p2p/synch"
	"github.com/BlockdagNetworkLabs/bdag/params"
	"github.com/BlockdagNetworkLabs/bdag/services/notifymgr/notify"
	"github.com/dgraph-io/ristretto"
	ethcommon "github.com/ethereum/go-ethereum/common"
	ethtypes "github.com/ethereum/go-ethereum/core/types"

	"github.com/ethereum/go-ethereum/core/txpool/legacypool"
	"github.com/gogo/protobuf/proto"

	"github.com/libp2p/go-libp2p"
	dht "github.com/libp2p/go-libp2p-kad-dht"
	pubsub "github.com/libp2p/go-libp2p-pubsub"
	pubsub_pb "github.com/libp2p/go-libp2p-pubsub/pb"
	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/p2p/discovery/routing"
	"github.com/multiformats/go-multiaddr"
	"gopkg.in/yaml.v2"
)

const (
	// the default services supported by the node
	defaultServices = pv.Full | pv.CF
)

var (
	// In the event that we are at our peer limit, we
	// stop looking for new peers and instead poll
	// for the current peer limit status for the time period
	// defined below.
	pollingPeriod = 30 * time.Second

	// Refresh rate of QNR
	refreshRate = time.Hour

	peerDialInFlightTTL = 2 * time.Minute
)

type Service struct {
	service.Service
	cfg              *common.Config
	exclusionList    *ristretto.Cache
	isPreGenesis     bool
	privKey          crypto.PrivKey
	metaData         *pb.MetaData
	addrFilter       *multiaddr.Filters
	host             host.Host
	hostCloseOnce    sync.Once
	hostCloseErr     error
	stopOnce         sync.Once
	stopErr          error
	pubsub           *pubsub.PubSub
	peerSourcesMu    sync.RWMutex
	peerSources      map[peer.ID]peerSourceInfo
	recentPeersMu    sync.Mutex
	recentPeersDirty bool            // true when in-memory cache has unpersisted changes
	recentPeersTimer *time.Timer     // coalescing timer for deferred writes
	recentPeersCache recentPeerCache // in-memory cache for coalesced writes
	peerDialMu       sync.Mutex
	peerDials        map[peer.ID]time.Time
	reconnectMu      sync.Mutex
	reconnecting     map[peer.ID]struct{}
	reconnectAfter   map[peer.ID]time.Time
	reconnectSlots   chan struct{}
	staticPeerIDs    map[peer.ID]struct{}

	dv5Listener any
	kademliaDHT *dht.IpfsDHT
	routingDv   *routing.RoutingDiscovery

	events *event.Feed
	sy     *synch.Sync

	blockChain  *blockchain.BlockChain
	timeSource  model.MedianTimeSource
	txMemPool   *legacypool.LegacyPool
	notify      model.Notify
	rebroadcast *Rebroadcast
	relayScores *blockRelayScoreTracker

	consensus model.Consensus

	// relay test seams are nil in production and keep the concrete network
	// implementation behind the same paths exercised by focused tests.
	broadcastBlockPeers   func() []*peers.Peer
	broadcastBlockSend    func(*peers.Peer, any) (any, error)
	relayTransactionPeers func() []*peers.Peer
	relayTransactionSend  func(*peers.Peer, any) (any, error)
}

func (s *Service) Start() error {
	if err := s.Service.Start(); err != nil {
		return err
	}
	log.Info("P2P Service Start", "peer_id", s.host.ID().String(), "services", s.cfg.Services.String())

	err := s.sy.Start()
	if err != nil {
		return err
	}

	s.isPreGenesis = false

	if s.staticPeersOnly() {
		log.Warn("P2P static peers only mode enabled; default bootstraps, DHT discovery, recent peers, and peerstore dialing are disabled")
	} else {
		err = s.startKademliaDHT()
		if err != nil {
			log.Error(fmt.Sprintf("Failed to start discovery:%v", err))
			return err
		}
		s.listenForNewNodes()
	}

	var peersToWatch []string
	if s.cfg.RelayNodeAddr != "" {
		peersToWatch = append(peersToWatch, s.cfg.RelayNodeAddr)
		if err := dialRelayNode(s.Context(), s.host, s.cfg.RelayNodeAddr); err != nil {
			log.Warn(fmt.Sprintf("Could not dial relay node:%v", err))
		}
	}
	_, bootstrapAddrs := parseGenericAddrs(s.cfg.BootstrapNodeAddr)
	if len(bootstrapAddrs) > 0 {
		peersToWatch = append(peersToWatch, bootstrapAddrs...)
	}
	if len(s.cfg.StaticPeers) > 0 {
		bootstrapAddrs = mergePeerAddrStrings(bootstrapAddrs, s.cfg.StaticPeers)
		peersToWatch = append(peersToWatch, s.cfg.StaticPeers...)
	}
	peersToWatch = mergePeerAddrStrings(peersToWatch)

	if len(bootstrapAddrs) > 0 {
		addrs, err := peersFromStringAddrs(bootstrapAddrs)
		if err != nil {
			log.Error(fmt.Sprintf("Could not connect to static peer: %v", err))
		} else {
			log.Info("Starting P2P peer dial attempts", "count", len(addrs))
			s.connectWithAllPeers(addrs, true)
		}
	}
	if !s.staticPeersOnly() {
		s.connectFromPeerStore()
	}

	// Periodic functions.
	if len(peersToWatch) > 0 {
		runutil.RunEvery(s.Context(), s.sy.PeerInterval, func() {
			s.ensurePeerConnections(peersToWatch)
		})
	} else {
		log.Warn("No bootstrap, static, or recent peers configured; node cannot discover peers without incoming P2P traffic")
	}
	s.publishPeerSourceMetrics()
	runutil.RunEvery(s.Context(), s.sy.PeerInterval, func() {
		s.publishPeerSourceMetrics()
	})

	if !s.staticPeersOnly() {
		runutil.RunEvery(s.Context(), refreshRate, func() {
			s.RefreshQNR()
		})
		runutil.RunEvery(s.Context(), recentPeerPersistInterval, func() {
			s.persistRecentPeersFromStatus("periodic")
			if path := s.recentPeerCachePath(); path != "" {
				s.flushRecentPeerCache(path)
			}
		})
	}

	multiAddrs := s.host.Network().ListenAddresses()
	logIPAddr(s.host.ID(), multiAddrs...)

	p2pHostAddress := s.cfg.HostAddress
	p2pTCPPort := s.cfg.TCPPort

	if p2pHostAddress != "" {
		logExternalIPAddr(s.host.ID(), p2pHostAddress, p2pTCPPort, s.cfg.WSOverHTTP)
		if _, err := verifyConnectivity(p2pHostAddress, p2pTCPPort, "tcp"); err != nil {
			log.Error(fmt.Sprintf("Connectivity check failed: %v", err))
		}
	}

	p2pHostDNS := s.cfg.HostDNS
	if p2pHostDNS != "" {
		logExternalDNSAddr(s.host.ID(), p2pHostDNS, p2pTCPPort, s.cfg.WSOverHTTP)
	}

	s.rebroadcast.Start()
	return nil
}

// Started returns true if the p2p service has successfully started.
//func (s *Service) Started() bool {
//	return s.started
//}

func (s *Service) Stop() error {
	s.stopOnce.Do(func() { s.stopErr = s.stop() })
	return s.stopErr
}

func (s *Service) stop() error {
	log.Info("P2P Service Stop")
	s.persistRecentPeersFromStatus("stop")
	if path := s.recentPeerCachePath(); path != "" {
		s.flushRecentPeerCache(path)
	}

	// first stop the underlying service
	serviceErr := s.Service.Stop()

	// stop the rebroadcast loop
	rebroadcastErr := s.rebroadcast.Stop()
	if rebroadcastErr != nil {
		log.Error(fmt.Sprintf("Failed to stop rebroadcast: %v", rebroadcastErr))
	}

	// now stop Sync itself (flushes any final work, etc)
	syncErr := s.sy.Stop()
	return errors.Join(serviceErr, rebroadcastErr, syncErr, s.closeHost())
}

func (s *Service) closeHost() error {
	s.hostCloseOnce.Do(func() {
		var hostErr, dhtErr error
		// RoutedHost does not own the DHT, so stop host handlers first and
		// still release DHT storage if host closure reports an error.
		if s.host != nil {
			hostErr = s.host.Close()
		}
		if s.kademliaDHT != nil {
			dhtErr = s.kademliaDHT.Close()
		}
		s.hostCloseErr = errors.Join(hostErr, dhtErr)
	})
	return s.hostCloseErr
}

func (s *Service) connectWithAllPeers(multiAddrs []multiaddr.Multiaddr, allowPrivate bool) {
	addrInfos, err := peer.AddrInfosFromP2pAddrs(multiAddrs...)
	if err != nil {
		log.Error(fmt.Sprintf("Could not convert to peer address info's from multiaddresses: %v", err))
		return
	}
	for _, info := range addrInfos {
		s.connectWithPeerAsync(info, false, allowPrivate, "startup")
	}
}

func (s *Service) ConnectToPeer(qmaddr string, force bool) error {
	mulAddr, err := MultiAddrFromString(qmaddr)
	if err != nil {
		log.Error(err.Error())
		return err
	}

	addrInfo, err := peer.AddrInfoFromP2pAddr(mulAddr)
	if err != nil {
		log.Error(err.Error())
		return err
	}
	if !s.configuredStaticPeerAllowed(addrInfo.ID) {
		return fmt.Errorf("peer %s is not in the configured static peer set", addrInfo.ID.String())
	}
	err = s.connectWithPeer(*addrInfo, force, force)
	if err != nil {
		log.Debug(fmt.Sprintf("Could not connect with peer %s :%v", addrInfo.String(), err))
		return err
	}
	s.recordPeerSource(addrInfo.ID, peerSourceManual, "connected through AddPeer RPC")
	return nil
}

func (s *Service) connectFromPeerStore() {
	if s.staticPeersOnly() {
		return
	}
	for _, pid := range s.host.Peerstore().Peers() {
		if pid == s.PeerID() {
			continue
		}
		info := s.host.Peerstore().PeerInfo(pid)
		log.Debug(fmt.Sprintf("Try to connect from peer store:%s", info.String()))
		s.connectWithPeerAsync(info, false, false, "peerstore")
	}
}

func (s *Service) connectWithPeerAsync(info peer.AddrInfo, force bool, allowPrivate bool, reason string) bool {
	if !s.configuredStaticPeerAllowed(info.ID) {
		log.Trace(fmt.Sprintf("Skipped dial to peer %s outside configured static peer set", info.ID.String()))
		return false
	}
	token, ok := s.beginPeerDial(info.ID)
	if !ok {
		log.Trace(fmt.Sprintf("Skipped duplicate in-flight dial to peer %s", info.ID.String()))
		return false
	}
	go func(info peer.AddrInfo, token time.Time) {
		defer s.endPeerDial(info.ID, token)
		if err := s.connectWithPeer(info, force, allowPrivate); err != nil {
			log.Debug(fmt.Sprintf("Could not connect with peer %s :%v", info.String(), err))
		} else {
			if reason == peerSourcePeerstore {
				s.recordPeerSource(info.ID, peerSourcePeerstore, "reconnected from libp2p peerstore")
			}
			log.Info(fmt.Sprintf("Connected to peer %s", info.String()), "reason", reason)
		}
	}(info, token)
	return true
}

func (s *Service) beginPeerDial(id peer.ID) (time.Time, bool) {
	if id == "" {
		return time.Time{}, false
	}
	now := time.Now()
	s.peerDialMu.Lock()
	defer s.peerDialMu.Unlock()
	if s.peerDials == nil {
		s.peerDials = make(map[peer.ID]time.Time)
	}
	if started, ok := s.peerDials[id]; ok && now.Sub(started) < peerDialInFlightTTL {
		return time.Time{}, false
	}
	s.peerDials[id] = now
	return now, true
}

func (s *Service) endPeerDial(id peer.ID, token time.Time) {
	s.peerDialMu.Lock()
	defer s.peerDialMu.Unlock()
	if started, ok := s.peerDials[id]; ok && started.Equal(token) {
		delete(s.peerDials, id)
	}
}

func (s *Service) connectWithPeer(info peer.AddrInfo, force bool, allowPrivate bool) error {
	return s.connectWithPeerContext(s.Context(), info, force, allowPrivate)
}

func (s *Service) connectWithPeerContext(ctx context.Context, info peer.AddrInfo, force bool, allowPrivate bool) error {
	if info.ID == s.host.ID() {
		return nil
	}
	if !s.configuredStaticPeerAllowed(info.ID) {
		return fmt.Errorf("peer %s is not in the configured static peer set", info.ID.String())
	}
	pe := s.Peers().Fetch(info.ID)
	if pe == nil {
		return nil
	}
	if !force {
		if pe.IsBad() {
			return nil
		}
	} else {
		pe.ResetBad()
	}
	filtered, ok := sanitizeAutomaticPeerAddrInfo(info, allowPrivate)
	if !ok {
		return fmt.Errorf("no public dialable addresses for peer %s", info.ID.String())
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := s.host.Connect(ctx, filtered); err != nil {
		return err
	}
	s.recordPeerSource(filtered.ID, peerSourceConnect, "successful outbound connect")
	s.recordRecentPeerInfo(filtered, "connect")
	return nil
}

// Peers returns the peer status interface.
func (s *Service) Peers() *peers.Status {
	return s.sy.Peers()
}

func (s *Service) IncreaseBytesSent(pid peer.ID, size int) {
	if size <= 0 {
		return
	}
	common.EgressTrafficMeter.Mark(int64(size))
	if s.Peers() != nil {
		pe := s.Peers().Get(pid)
		if pe != nil {
			pe.IncreaseBytesSent(size)
		}
	}
}

func (s *Service) IncreaseBytesRecv(pid peer.ID, size int) {
	if size <= 0 {
		return
	}
	common.IngressTrafficMeter.Mark(int64(size))
	if s.Peers() != nil {
		pe := s.Peers().Get(pid)
		if pe != nil {
			pe.IncreaseBytesRecv(size)
		}
	}
}

func (s *Service) pingPeers() {
	for _, pe := range s.Peers().ConnectedPeers() {
		go func(pe *peers.Peer) {
			metadataSeq := s.MetadataSeq()
			if _, err := s.sy.Send(pe, synch.RPCPingTopic, &metadataSeq); err != nil {
				log.Error("Failed to ping peer:id=%s  %v", pe.GetID(), err)
			}
		}(pe)
	}
}

// PubSub returns the p2p pubsub framework.
func (s *Service) PubSub() *pubsub.PubSub {
	return s.pubsub
}

// Host returns the currently running libp2p
// host of the service.
func (s *Service) Host() host.Host {
	return s.host
}

// PeerID returns the Peer ID of the local peer.
func (s *Service) PeerID() peer.ID {
	return s.host.ID()
}

// Disconnect from a peer.
func (s *Service) Disconnect(pid peer.ID) error {
	return s.host.Network().ClosePeer(pid)
}

// Connect to a specific peer.
func (s *Service) Connect(pi peer.AddrInfo) error {
	if !s.configuredStaticPeerAllowed(pi.ID) {
		return fmt.Errorf("peer %s is not in the configured static peer set", pi.ID.String())
	}
	return s.host.Connect(s.Context(), pi)
}

// QNR returns the local node's current QNR.
func (s *Service) QNR() *qnr.Record {
	return nil
}

func (s *Service) Node() *qnode.Node {
	return nil
}

// Metadata returns a copy of the peer's metadata.
func (s *Service) Metadata() *pb.MetaData {
	return proto.Clone(s.metaData).(*pb.MetaData)
}

// MetadataSeq returns the metadata sequence number.
func (s *Service) MetadataSeq() uint64 {
	return s.metaData.SeqNumber
}

// Encoding returns the configured networking encoding.
func (s *Service) Encoding() encoder.NetworkEncoding {
	encoding := s.cfg.Encoding
	switch encoding {
	case encoder.SSZ:
		return &encoder.SszNetworkEncoder{}
	case encoder.SSZSnappy:
		return &encoder.SszNetworkEncoder{UseSnappyCompression: true}
	default:
		panic("Invalid Network Encoding Flag Provided")
	}
}

func (s *Service) GetGenesisHash() *ethcommon.Hash {
	return s.blockChain.Blockdag().GetGenesisHash()
}

func (s *Service) SetBlockChain(blockChain *blockchain.BlockChain) {
	s.blockChain = blockChain
}

func (s *Service) BlockChain() *blockchain.BlockChain {
	return s.blockChain
}

func (s *Service) SetTxMemPool(txMemPool *legacypool.LegacyPool) {
	s.txMemPool = txMemPool
}

func (s *Service) TxMemPool() *legacypool.LegacyPool {
	return s.txMemPool
}

func (s *Service) SetTimeSource(timeSource model.MedianTimeSource) {
	s.timeSource = timeSource
}

func (s *Service) TimeSource() model.MedianTimeSource {
	return s.timeSource
}

func (s *Service) SetNotify(notify model.Notify) {
	s.notify = notify
}

func (s *Service) Notify() model.Notify {
	return s.notify
}

func (s *Service) Config() *common.Config {
	return s.cfg
}

func (s *Service) PeerSync() *synch.PeerSync {
	return s.sy.PeerSync()
}

func (s *Service) RelayInventory(nds []*notify.NotifyData) {
	s.PeerSync().RelayInventory(nds)
}

func (s *Service) BroadcastMessage(data any) {

}

func (s *Service) BroadcastBlock(block *types.SerializedBlock, source *peer.ID, acceptedAt time.Time) error {
	var peerList []*peers.Peer
	if s.broadcastBlockPeers != nil {
		peerList = s.broadcastBlockPeers()
	} else {
		peerList = s.Peers().CanSyncPeers()
	}
	broadcastPeers := eligibleBroadcastBlockPeers(peerList, source)
	fanout := len(broadcastPeers)
	blockBroadcastFanoutGauge.Update(int64(fanout))
	blockBroadcastFanoutTotal.Inc(int64(fanout))
	if fanout == 0 {
		return nil
	}
	localMainOrder := uint64(0)
	if s.blockChain != nil {
		localMainOrder = uint64(s.blockChain.BestSnapshot().GraphState.GetMainOrder())
	}
	relayOrder := peers.SortMiningRelayPeers(
		broadcastPeers,
		time.Now(),
		params.ActiveNetParams.TargetTimePerBlock*3,
		10,
		localMainOrder,
	)
	relayScores := s.relayScores
	if relayScores != nil {
		relayScores.sortBroadcastPeers(broadcastPeers, relayOrder.Scores, time.Now())
	}
	blockBroadcastFanoutFreshGauge.Update(int64(relayOrder.FreshFanout))
	blockBroadcastFanoutStaleGauge.Update(int64(relayOrder.StaleFanout))

	serializeStart := time.Now()
	blockBytes, err := block.Bytes()
	blockBroadcastSerializationTimer.Update(time.Since(serializeStart))
	if err != nil {
		blockBroadcastSerializationErrors.Inc(1)
		return err
	}

	var firstRelay sync.Once
	for _, pe := range broadcastPeers {
		go func(pe *peers.Peer) {
			blockBroadcastSendAttempts.Inc(1)
			sendStart := time.Now()
			var err error
			if s.broadcastBlockSend != nil {
				_, err = s.broadcastBlockSend(pe, &pb.BroadcastBlock{Block: &pb.BlockData{BlockBytes: append([]byte(nil), blockBytes...)}})
			} else {
				_, err = s.sy.Send(pe, synch.RPCBroadcastBlock, &pb.BroadcastBlock{Block: &pb.BlockData{BlockBytes: append([]byte(nil), blockBytes...)}})
			}
			if err != nil {
				latency := time.Since(sendStart)
				blockBroadcastSendLatencyTimer.Update(latency)
				if relayScores != nil {
					relayScores.recordFailure(pe.GetID(), latency, time.Now())
				}
				blockBroadcastSendErrors.Inc(1)
				markBlockBroadcastPeerSendError(pe.GetID().ShortString())
				log.Error("broadcast block failed", "peer", pe.GetID().ShortString(), "err", err)
				return
			}
			latency := time.Since(sendStart)
			blockBroadcastSendLatencyTimer.Update(latency)
			if relayScores != nil {
				relayScores.recordSuccess(pe.GetID(), latency, time.Now())
			}
			blockBroadcastSendSuccesses.Inc(1)
			if !acceptedAt.IsZero() {
				firstRelay.Do(func() {
					blockBroadcastFirstRelayTimer.Update(time.Since(acceptedAt))
					if score, ok := relayOrder.Scores[pe.GetID()]; ok && score.HasGraphState {
						blockBroadcastFirstRelayPeerAge.Update(score.GraphStateAge.Milliseconds())
						blockBroadcastFirstRelayPeerLead.Update(score.LeadBlocks)
					}
					if relayScores != nil {
						blockBroadcastFirstRelayPeerScore.Update(relayScores.score(pe.GetID(), time.Now()).quality)
					}
				})
			}
		}(pe)
	}
	return nil
}

func eligibleBroadcastBlockPeers(peerList []*peers.Peer, source *peer.ID) []*peers.Peer {
	broadcastPeers := make([]*peers.Peer, 0, len(peerList))
	for _, pe := range peerList {
		if source != nil && pe.GetID() == *source {
			continue
		}
		if pe.ChainState().ProtocolVersion < uint32(pv.BroadcastblockProtocolVersion) {
			continue
		}
		broadcastPeers = append(broadcastPeers, pe)
	}
	return broadcastPeers
}

func (s *Service) RelayTransaction(tx *ethtypes.Transaction) (*ethcommon.Hash, error) {
	var peerList []*peers.Peer
	if s.relayTransactionPeers != nil {
		peerList = s.relayTransactionPeers()
	} else {
		peerList = s.Peers().CanSyncPeers()
	}
	eligiblePeers := eligibleRelayTransactionPeers(peerList)
	if len(eligiblePeers) == 0 {
		return nil, fmt.Errorf("no peer available to relay transaction")
	}
	txBytes, err := encodeRelayTransaction(tx)
	if err != nil {
		return nil, err
	}
	for _, pe := range eligiblePeers {
		var ret any
		var err error
		if s.relayTransactionSend != nil {
			ret, err = s.relayTransactionSend(pe, &pb.Transaction{TxBytes: append([]byte(nil), txBytes...)})
		} else {
			ret, err = s.sy.Send(pe, synch.RPCRelayTx, &pb.Transaction{TxBytes: append([]byte(nil), txBytes...)})
		}
		if err != nil {
			log.Debug(err.Error())
			continue
		}
		hashMsg, ok := ret.(*pb.Hash)
		if !ok || hashMsg == nil {
			continue
		}
		if len(hashMsg.Hash) != ethcommon.HashLength {
			continue
		}
		h := ethcommon.BytesToHash(hashMsg.Hash)
		return &h, nil
	}
	return nil, fmt.Errorf("no peer available to relay transaction")
}

func eligibleRelayTransactionPeers(peerList []*peers.Peer) []*peers.Peer {
	eligiblePeers := make([]*peers.Peer, 0, len(peerList))
	for _, pe := range peerList {
		if pe.DisableRelayTx() {
			continue
		}
		eligiblePeers = append(eligiblePeers, pe)
	}
	return eligiblePeers
}

func encodeRelayTransaction(tx *ethtypes.Transaction) ([]byte, error) {
	var buf bytes.Buffer
	buf.Grow(int(tx.Size()))
	if err := tx.EncodeRLP(&buf); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func (s *Service) GetBanlist() map[peer.ID][]*json.BadResponse {
	result := map[peer.ID][]*json.BadResponse{}
	bads := s.Peers().Bad()
	for _, bad := range bads {
		pe := s.Peers().Get(bad)
		if pe == nil {
			continue
		}
		brs := []*json.BadResponse{}
		for _, br := range pe.BadResponses() {
			brj := &json.BadResponse{
				ID:    br.ID,
				Time:  br.Time.String(),
				Error: br.Err.String(),
			}
			brs = append(brs, brj)
		}
		result[pe.GetID()] = brs
	}
	return result
}

func (s *Service) RemoveBan(id string) {
	bads := s.Peers().Bad()
	for _, bad := range bads {
		pe := s.Peers().Get(bad)
		if pe == nil {
			continue
		}
		pe.ResetBad()
	}
}

func (s *Service) ConnectTo(node *qnode.Node) {
	addr, err := ConvertToSingleMultiAddr(node)
	if err != nil {
		log.Error(err.Error())
		return
	}
	s.connectWithAllPeers([]multiaddr.Multiaddr{addr}, true)
}

func (s *Service) Resolve(n *qnode.Node) *qnode.Node {
	return nil
}

func (s *Service) HostAddress() []string {
	hms := s.host.Addrs()
	if len(hms) <= 0 {
		return nil
	}
	result := []string{}
	for _, hm := range hms {
		result = append(result, fmt.Sprintf("%s/p2p/%s", hm.String(), s.Host().ID().String()))
	}
	return result
}

func (s *Service) HostDNS() multiaddr.Multiaddr {
	if len(s.cfg.HostDNS) <= 0 {
		return nil
	}
	var multiAddrStr string
	if s.cfg.WSOverHTTP {
		multiAddrStr = fmt.Sprintf("/dns4/%s/tcp/%d/ws/p2p/%s", s.cfg.HostDNS, s.cfg.TCPPort, s.Host().ID().String())
	} else {
		multiAddrStr = fmt.Sprintf("/dns4/%s/tcp/%d/p2p/%s", s.cfg.HostDNS, s.cfg.TCPPort, s.Host().ID().String())
	}
	external, err := multiaddr.NewMultiaddr(multiAddrStr)
	if err != nil {
		log.Error(err.Error())
		return nil
	}
	return external
}

func (s *Service) RelayNodeInfo() *peer.AddrInfo {
	if len(s.cfg.RelayNodeAddr) <= 0 {
		return nil
	}
	pi, err := MakePeer(s.cfg.RelayNodeAddr)
	if err != nil {
		log.Error(err.Error())
		return nil
	}
	return pi
}

func (s *Service) Rebroadcast() *Rebroadcast {
	return s.rebroadcast
}

func (s *Service) RegainMempool() {
	s.Rebroadcast().RegainMempool()
}

func (s *Service) IsCurrent() bool {
	return s.PeerSync().IsCurrent()
}

func (s *Service) IsNearlySynced() bool {
	return s.PeerSync().IsNearlySynced()
}

func (s *Service) MiningPeerFreshnessSnapshot(now time.Time) model.P2PMiningFreshnessSnapshot {
	return s.PeerSync().MiningPeerFreshnessSnapshot(now)
}

func (s *Service) IsRunning() bool {
	return !s.IsShutdown() && s.IsStarted()
}

func (s *Service) Consensus() model.Consensus {
	return s.consensus
}

func NewService(cfg *config.Config, consensus model.Consensus, param *params.Params) (*Service, error) {
	rand.Seed(roughtime.Now().UnixNano())

	var err error
	cache, err := ristretto.NewCache(&ristretto.Config{
		NumCounters: 1000,
		MaxCost:     1000,
		BufferItems: 64,
	})

	if err != nil {
		return nil, err
	}

	bootnodeAddrs := make([]string, 0) //dest of final list of nodes

	bootnodesTemp := cfg.BootstrapNodes
	if len(bootnodesTemp) <= 0 && !cfg.P2PStaticPeersOnly {
		bootnodesTemp = param.Bootstrap
	}
	for _, addr := range bootnodesTemp {
		if filepath.Ext(addr) == ".yaml" {
			fileNodes, err := readbootNodes(addr)
			if err != nil {
				return nil, err
			}
			bootnodeAddrs = append(bootnodeAddrs, fileNodes...)
		} else {
			bootnodeAddrs = append(bootnodeAddrs, addr)
		}
	}

	allowListCIDR := ""
	lanPeers := []string{}

	if len(cfg.Whitelist) > 0 {
		for _, wl := range cfg.Whitelist {
			if strings.Contains(wl, "/") {
				allowListCIDR = wl
			} else {
				lanPeers = append(lanPeers, wl)
			}
		}
	}

	if cfg.MaxBadResp > 0 {
		peers.MaxBadResponses = cfg.MaxBadResp
	}

	staticPeerIDs := map[peer.ID]struct{}{}
	if cfg.P2PStaticPeersOnly {
		staticPeerAddrs := mergePeerAddrStrings(cfg.AddPeers, bootnodeAddrs)
		if cfg.RelayNode != "" {
			staticPeerAddrs = append(staticPeerAddrs, cfg.RelayNode)
		}
		staticPeerIDs, err = configuredPeerIDs(staticPeerAddrs)
		if err != nil {
			return nil, err
		}
		if len(staticPeerIDs) == 0 {
			return nil, fmt.Errorf("p2p static peers only mode requires at least one explicit addpeer, bootstrapnode, or relaynode /p2p/<peer-id> multiaddress")
		}
	}

	// Calculate DAG RPC port (fallback to RPCListeners if GatewayDagPort is not set)
	dagPort := cfg.GatewayDagPort
	if dagPort == 0 {
		rpcListen := ":" + param.DefaultPort
		if len(cfg.RPCListeners) > 0 {
			rpcListen = cfg.RPCListeners[0]
		}
		_, portStr, err := net.SplitHostPort(rpcListen)
		if err == nil {
			if p, err := strconv.Atoi(portStr); err == nil {
				dagPort = p
			}
		}
	}
	evmPort := cfg.GatewayEVMAdvertisePort()
	services := defaultServices

	s := &Service{
		cfg: &common.Config{
			EnableUPnP:                 cfg.Upnp,
			StaticPeers:                cfg.AddPeers,
			StaticPeersOnly:            cfg.P2PStaticPeersOnly,
			BootstrapNodeAddr:          bootnodeAddrs,
			DiscoveryTargetLivePeers:   cfg.P2PDiscoveryTargetLivePeers,
			DiscoveryTargetUsefulPeers: cfg.P2PDiscoveryTargetUsefulPeers,
			DataDir:                    cfg.DataDir,
			MaxPeers:                   uint(cfg.MaxPeers),
			RPCHandlerConcurrency:      cfg.P2PRPCConcurrency,
			EnableStreamReuse:          cfg.P2PStreamReuse,
			MaxInbound:                 cfg.MaxInbound,
			ReadWritePermissions:       0600, //-rw------- Read and Write permissions for user
			MetaDataDir:                cfg.MetaDataDir,
			TCPPort:                    uint(cfg.P2PTCPPort),
			WSOverHTTP:                 cfg.P2PWSOverHTTP,
			Encoding:                   "ssz-snappy",
			Services:                   services,
			UserAgent:                  fmt.Sprintf("%s|DAG:%d|EVM:%d", BuildUserAgent(cfg.NodeName), dagPort, evmPort),
			DisableRelayTx:             cfg.BlocksOnly,
			MaxOrphanTxs:               cfg.MaxOrphanTxs,
			Params:                     param,
			HostAddress:                cfg.HostIP,
			HostDNS:                    cfg.HostDNS,
			RelayNodeAddr:              cfg.RelayNode,
			AllowListCIDR:              allowListCIDR,
			DenyListCIDR:               cfg.Blacklist,
			Banning:                    cfg.Banning,
			DisableListen:              cfg.DisableListen,
			LANPeers:                   lanPeers,
			IsCircuit:                  cfg.Circuit,
			Consistency:                cfg.Consistency,
		},
		exclusionList: cache,
		isPreGenesis:  true,
		events:        consensus.Events(),
		consensus:     consensus,
		relayScores:   newBlockRelayScoreTracker(),
		staticPeerIDs: staticPeerIDs,
		peerSources:   make(map[peer.ID]peerSourceInfo),
	}
	s.InitContext()

	var ipAddr net.IP
	if len(cfg.Listener) > 0 {
		ipAddr = net.ParseIP(cfg.Listener)
	}
	if ipAddr == nil && !cfg.DisableListen {
		ipAddr = IpAddr()
	}
	s.privKey, err = privKey(s.cfg)
	if err != nil {
		log.Error(fmt.Sprintf("Failed to generate p2p private key:%v", err))
		return nil, err
	}
	s.metaData, err = metaDataFromConfig(s.cfg)
	if err != nil {
		log.Error(fmt.Sprintf("Failed to create peer metadata:%v", err))
		return nil, err
	}
	s.addrFilter, err = configureFilter(s.cfg)
	if err != nil {
		log.Error(fmt.Sprintf("Failed to create address filter:%v", err))
		return nil, err
	}
	opts := s.buildOptions(ipAddr, s.privKey)
	if cfg.DisableListen {
		opts = append(opts, libp2p.DisableMetrics())
		s.host, err = libp2p.NewWithoutDefaults(opts...)
	} else {
		s.host, err = libp2p.New(opts...)
	}
	if err != nil {
		log.Error("Failed to create p2p host")
		return nil, err
	}

	s.registerPeerSourcesFromAddrs(s.cfg.BootstrapNodeAddr, peerSourceBootstrap, "configured bootstrap seed")
	s.registerPeerSourcesFromAddrs(s.cfg.StaticPeers, peerSourceStatic, "configured addpeer seed")
	if s.cfg.RelayNodeAddr != "" {
		s.registerPeerSourcesFromAddrs([]string{s.cfg.RelayNodeAddr}, peerSourceRelay, "configured relay seed")
	}
	if !s.staticPeersOnly() {
		recentPeerAddrs := s.loadRecentPeerAddrs()
		s.registerPeerSourcesFromAddrs(recentPeerAddrs, peerSourceRecent, "retained recent peer cache")
		s.cfg.StaticPeers = mergePeerAddrStrings(s.cfg.StaticPeers, recentPeerAddrs)
	}
	s.cfg.BootstrapNodeAddr = filterBootStrapAddrs(s.host.ID().String(), s.cfg.BootstrapNodeAddr)
	s.cfg.StaticPeers = filterBootStrapAddrs(s.host.ID().String(), s.cfg.StaticPeers)

	psOpts := []pubsub.Option{
		pubsub.WithMessageSigning(false),
		pubsub.WithStrictSignatureVerification(false),
		pubsub.WithMessageIdFn(msgIDFunction),
	}

	gs, err := pubsub.NewGossipSub(s.Context(), s.host, psOpts...)
	if err != nil {
		log.Error(fmt.Sprintf("Failed to start pubsub:%v", err))
		return nil, err
	}
	s.pubsub = gs

	s.sy = synch.NewSync(s)
	s.rebroadcast = NewRebroadcast(s)
	return s, nil
}

func configuredPeerIDs(addrs []string) (map[peer.ID]struct{}, error) {
	result := make(map[peer.ID]struct{})
	for _, raw := range mergePeerAddrStrings(addrs) {
		multiAddr, err := MultiAddrFromString(raw)
		if err != nil {
			return nil, fmt.Errorf("configured static peer %q must be a /p2p/<peer-id> multiaddress: %w", raw, err)
		}
		info, err := peer.AddrInfoFromP2pAddr(multiAddr)
		if err != nil || info == nil || info.ID == "" {
			return nil, fmt.Errorf("configured static peer %q must end in a valid /p2p/<peer-id> identity", raw)
		}
		result[info.ID] = struct{}{}
	}
	return result, nil
}

func readbootNodes(fileName string) ([]string, error) {
	fileContent, err := ioutil.ReadFile(fileName)
	if err != nil {
		return nil, err
	}
	listNodes := make([]string, 0)
	err = yaml.Unmarshal(fileContent, &listNodes)
	if err != nil {
		return nil, err
	}
	return listNodes, nil
}

func msgIDFunction(pmsg *pubsub_pb.Message) string {
	h := common.FastSum256(pmsg.Data)
	return base64.URLEncoding.EncodeToString(h[:])
}

func logIPAddr(id peer.ID, addrs ...multiaddr.Multiaddr) {
	var tcpAddr, wsAddr multiaddr.Multiaddr
	for _, addr := range addrs {
		addrStr := addr.String()
		if !strings.Contains(addrStr, "/ip4/") && !strings.Contains(addrStr, "/ip6/") {
			continue
		}
		// Check if this is a WebSocket address
		if strings.Contains(addrStr, "/ws") {
			if wsAddr == nil {
				wsAddr = addr
			}
		} else if tcpAddr == nil {
			tcpAddr = addr
		}
	}

	if tcpAddr != nil {
		log.Info(fmt.Sprintf("Node started p2p server (TCP):multiAddr=%s", tcpAddr.String()+"/p2p/"+id.String()))
	}
	if wsAddr != nil {
		log.Info(fmt.Sprintf("Node started p2p server (WebSocket):multiAddr=%s", wsAddr.String()+"/p2p/"+id.String()))
	}
	if tcpAddr == nil && wsAddr == nil {
		log.Warn("BDAG P2P server will be useless, neither dialing nor listening")
	}
}

func logExternalIPAddr(id peer.ID, addr string, port uint, ws bool) {
	if addr != "" {
		var multiAddr multiaddr.Multiaddr
		var err error
		if ws {
			multiAddr, err = wsAddressBuilder(addr, port)
		} else {
			multiAddr, err = multiAddressBuilder(addr, port)
		}
		if err != nil {
			log.Error(fmt.Sprintf("Could not create multiaddress: %v", err))
			return
		}
		log.Info(fmt.Sprintf("Node started external p2p server:multiAddr=%s", multiAddr.String()+"/p2p/"+id.String()))
	}
}

func logExternalDNSAddr(id peer.ID, addr string, port uint, ws bool) {
	if addr != "" {
		p := strconv.FormatUint(uint64(port), 10)
		var multiAddrStr string
		if ws {
			multiAddrStr = fmt.Sprintf("/dns4/%s/tcp/%s/ws/p2p/%s", addr, p, id.String())
		} else {
			multiAddrStr = fmt.Sprintf("/dns4/%s/tcp/%s/p2p/%s", addr, p, id.String())
		}
		log.Info(fmt.Sprintf("Node started external p2p server:multiAddr=%s", multiAddrStr))
	}
}
