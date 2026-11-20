package synch

import (
	"context"
	"encoding/hex"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/BlockdagNetworkLabs/bdag/common/hash"
	"github.com/BlockdagNetworkLabs/bdag/common/roughtime"
	"github.com/BlockdagNetworkLabs/bdag/core/protocol"
	"github.com/BlockdagNetworkLabs/bdag/p2p/common"
	"github.com/BlockdagNetworkLabs/bdag/p2p/peers"
	pb "github.com/BlockdagNetworkLabs/bdag/p2p/proto/v1"
	"github.com/BlockdagNetworkLabs/bdag/params"
	ethcommon "github.com/ethereum/go-ethereum/common"
	libp2pcore "github.com/libp2p/go-libp2p/core"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
)

const (
	MaxPBGraphStateTips = 100

	testEVMCheckpointOverrideEnv       = "BDAG_TEST_EVM_CHECKPOINT_OVERRIDE"
	testEVMCheckpointOverrideNumberEnv = "BDAG_TEST_EVM_CHECKPOINT_NUMBER"
	testEVMCheckpointOverrideHashEnv   = "BDAG_TEST_EVM_CHECKPOINT_HASH"
	testEVMCheckpointOverrideRootEnv   = "BDAG_TEST_EVM_CHECKPOINT_ROOT"
)

func (s *Sync) sendChainStateRequest(stream network.Stream, pe *peers.Peer) *common.Error {
	e := ReadRspCode(stream, s.p2p)
	if !e.Code.IsSuccess() && !e.Code.IsDAGConsensus() {
		e.Add("chain state request")
		return e
	}
	msg := &pb.ChainState{}
	if err := DecodeMessage(stream, s.p2p, msg); err != nil {
		return common.NewError(common.ErrStreamRead, err)
	}
	s.UpdateChainState(pe, msg, !e.Code.IsDAGConsensus())
	if !e.Code.IsSuccess() {
		return e
	}
	e = s.validateChainStateMessage(msg, pe)
	if e != nil {
		if e.Code.IsDAGConsensus() {
			go s.sendGoodByeAndDisconnectWithDetail(common.ErrDAGConsensus, pe, e.String())
		}
		return e
	}
	return e
}

func (s *Sync) chainStateHandler(ctx context.Context, msg any, stream libp2pcore.Stream, pe *peers.Peer) *common.Error {
	m, ok := msg.(*pb.ChainState)
	if !ok {
		return ErrMessage(fmt.Errorf("message is not type *pb.ChainState"))
	}
	e := s.validateChainStateMessage(m, pe)
	if e != nil {
		if e.Code.IsDAGConsensus() {
			// Respond with our status and disconnect with the peer.
			s.UpdateChainState(pe, m, false)
			if err := s.EncodeResponseMsgPro(stream, s.getChainState(), e.Code); err != nil {
				return err
			}
		}
		return e
	}
	if !s.bidirectionalChannelCapacity(pe, stream.Conn()) {
		s.UpdateChainState(pe, m, false)
		if err := s.EncodeResponseMsgPro(stream, s.getChainState(), common.ErrDAGConsensus); err != nil {
			return err
		}
		return ErrMessage(fmt.Errorf("bidirectional channel capacity"))
	}
	s.UpdateChainState(pe, m, true)
	return s.EncodeResponseMsg(stream, s.getChainState())
}

func (s *Sync) UpdateChainState(pe *peers.Peer, chainState *pb.ChainState, action bool) {
	pe.SetChainState(chainState)
	if !action {
		go s.peerSync.immediatelyDisconnected(pe)
		return
	}
	go s.peerSync.immediatelyConnected(pe)
}

func (s *Sync) validateChainStateMessage(msg *pb.ChainState, pe *peers.Peer) *common.Error {
	if msg == nil {
		return common.NewErrorStr(common.ErrGeneric, "msg is nil")
	}
	if protocol.HasServices(protocol.ServiceFlag(msg.Services), protocol.Relay) {
		return nil
	}
	if protocol.HasServices(protocol.ServiceFlag(msg.Services), protocol.Observer) {
		return nil
	}
	genesisHash := s.p2p.GetGenesisHash()
	msgGenesisHash, err := hash.NewHash(msg.GenesisHash.Hash)
	if err != nil {
		return common.NewErrorStr(common.ErrGeneric, "invalid genesis")
	}
	if msgGenesisHash.Cmp(*genesisHash) != 0 {
		return common.NewErrorStr(common.ErrDAGConsensus, "invalid genesis")
	}
	// Notify and disconnect clients that have a protocol version that is
	// too old.
	if msg.ProtocolVersion < uint32(protocol.InitialProcotolVersion) {
		return common.NewError(common.ErrDAGConsensus, fmt.Errorf("protocol version must be %d or greater",
			protocol.InitialProcotolVersion))
	}
	requiredProtocol := s.requiredP2PProtocolVersion()
	if msg.ProtocolVersion < requiredProtocol {
		return common.NewError(common.ErrDAGConsensus, fmt.Errorf("protocol version must be %d or greater at local layer %d",
			requiredProtocol, s.localP2PProtocolLayer()))
	}
	if s.enforceEVMCheckpoint() && msg.ProtocolVersion >= uint32(protocol.EVMCheckpointProtocolVersion) {
		var pid peer.ID
		if pe != nil {
			pid = pe.GetID()
		}
		if err := s.validateChainStateEVMCheckpoint(msg, pid); err != nil {
			peerDesc := "<unknown>"
			if pe != nil {
				peerDesc = pe.IDWithAddress()
			}
			log.Warn("Rejecting peer chain state", "peer", peerDesc, "err", err.String())
			return err
		}
	}
	if msg.GraphState.Total <= 0 {
		return common.NewErrorStr(common.ErrDAGConsensus, "invalid graph state")
	}
	if pe != nil && pe.Direction() == network.DirInbound {
		// Reject outbound peers that are not full nodes.
		wantServices := protocol.Full
		if !protocol.HasServices(protocol.ServiceFlag(msg.Services), wantServices) {
			// missingServices := wantServices & ^msg.Services
			missingServices := protocol.MissingServices(protocol.ServiceFlag(msg.Services), wantServices)
			return common.NewErrorStr(common.ErrDAGConsensus, fmt.Sprintf("Rejecting peer %s with services %v "+
				"due to not providing desired services %v\n", pe.GetID().String(), msg.Services, missingServices))
		}
	}

	return nil
}

func (s *Sync) localP2PProtocolLayer() uint64 {
	if s == nil || s.p2p == nil || s.p2p.BlockChain() == nil {
		return 0
	}
	snapshot := s.p2p.BlockChain().BestSnapshot()
	if snapshot == nil || snapshot.GraphState == nil {
		return 0
	}
	return uint64(snapshot.GraphState.GetLayer())
}

func (s *Sync) requiredP2PProtocolVersion() uint32 {
	return s.p2pProtocolVersionAtLayer(s.localP2PProtocolLayer())
}

func (s *Sync) p2pProtocolVersionAtLayer(layer uint64) uint32 {
	return s.p2pChainParams().RequiredP2PProtocolVersion(layer)
}

func (s *Sync) advertisedP2PProtocolVersion() uint32 {
	return protocol.ProtocolVersion
}

func (s *Sync) p2pChainParams() *params.Params {
	if s != nil && s.p2p != nil {
		if cfg := s.p2p.Config(); cfg != nil && cfg.Params != nil {
			return cfg.Params
		}
		if chain := s.p2p.BlockChain(); chain != nil && chain.ChainParams() != nil {
			return chain.ChainParams()
		}
	}
	if params.ActiveNetParams != nil {
		return params.ActiveNetParams.Params
	}
	return nil
}

func (s *Sync) enforcePeerEVMCheckpoint(pe *peers.Peer) bool {
	return s.enforceEVMCheckpoint()
}

func (s *Sync) enforceEVMCheckpoint() bool {
	return s.requiredP2PProtocolVersion() >= uint32(protocol.EVMCheckpointProtocolVersion)
}

func (s *Sync) getChainState() *pb.ChainState {
	genesisHash := s.p2p.GetGenesisHash()

	cs := &pb.ChainState{
		GenesisHash:     &pb.Hash{Hash: genesisHash.Bytes()},
		ProtocolVersion: s.advertisedP2PProtocolVersion(),
		Timestamp:       uint64(roughtime.Now().Unix()),
		Services:        uint64(s.p2p.Config().Services),
		GraphState:      s.getGraphState(),
		UserAgent:       []byte(s.p2p.Config().UserAgent),
		DisableRelayTx:  s.p2p.Config().DisableRelayTx,
		EvmCheckpoint:   s.getEVMCheckpoint(),
	}

	return cs
}

func (s *Sync) getEVMCheckpoint() *pb.EVMCheckpoint {
	if checkpoint, ok := testEVMCheckpointOverride(); ok {
		return checkpoint
	}
	if s == nil || s.p2p == nil || s.p2p.BlockChain() == nil {
		return nil
	}
	number, hash, root, ok := s.p2p.BlockChain().SafeEVMCheckpoint()
	if !ok {
		return nil
	}
	return &pb.EVMCheckpoint{
		Number:    number,
		Hash:      &pb.Hash{Hash: hash.Bytes()},
		StateRoot: &pb.Hash{Hash: root.Bytes()},
	}
}

func (s *Sync) validateChainStateEVMCheckpoint(msg *pb.ChainState, pid peer.ID) *common.Error {
	if s == nil || s.p2p == nil || s.p2p.BlockChain() == nil {
		return nil
	}
	return validateChainStateEVMCheckpointWithChain(s.p2p.BlockChain(), pid, msg)
}

func validateChainStateEVMCheckpointWithChain(chain evmCheckpointChain, pid peer.ID, msg *pb.ChainState) *common.Error {
	if chain == nil {
		return nil
	}
	checkpoint := msg.GetEvmCheckpoint()
	if checkpoint == nil {
		return common.NewErrorStr(common.ErrDAGConsensus, "missing evm checkpoint")
	}
	hash, err := evmHashFromPB(checkpoint.GetHash())
	if err != nil {
		return common.NewError(common.ErrDAGConsensus, fmt.Errorf("invalid evm checkpoint hash: %w", err))
	}
	root, err := evmHashFromPB(checkpoint.GetStateRoot())
	if err != nil {
		return common.NewError(common.ErrDAGConsensus, fmt.Errorf("invalid evm checkpoint state root: %w", err))
	}
	number := checkpoint.GetNumber()
	valid, known := chain.ValidateEVMCheckpoint(number, hash, root)
	if known && !valid {
		return common.NewErrorStr(common.ErrDAGConsensus, fmt.Sprintf("evm checkpoint mismatch at block %d", number))
	}
	if pid != "" {
		chain.SetPeerEVMCheckpoint(pid, number, hash, root)
	}
	return nil
}

func testEVMCheckpointOverride() (*pb.EVMCheckpoint, bool) {
	if os.Getenv(testEVMCheckpointOverrideEnv) != "1" {
		return nil, false
	}
	checkpoint, err := parseTestEVMCheckpointOverrideFromEnv()
	if err != nil {
		log.Warn("Invalid test EVM checkpoint override", "err", err)
		return nil, true
	}
	return checkpoint, true
}

func parseTestEVMCheckpointOverrideFromEnv() (*pb.EVMCheckpoint, error) {
	number := uint64(0)
	if value := strings.TrimSpace(os.Getenv(testEVMCheckpointOverrideNumberEnv)); value != "" {
		parsed, err := strconv.ParseUint(value, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("%s must be a uint64: %w", testEVMCheckpointOverrideNumberEnv, err)
		}
		number = parsed
	}

	hash, err := parseTestEVMCheckpointHash(os.Getenv(testEVMCheckpointOverrideHashEnv), testEVMCheckpointOverrideHashEnv)
	if err != nil {
		return nil, err
	}

	root := ethcommon.Hash{}
	if value := strings.TrimSpace(os.Getenv(testEVMCheckpointOverrideRootEnv)); value != "" {
		root, err = parseTestEVMCheckpointHash(value, testEVMCheckpointOverrideRootEnv)
		if err != nil {
			return nil, err
		}
	}

	return &pb.EVMCheckpoint{
		Number:    number,
		Hash:      &pb.Hash{Hash: hash.Bytes()},
		StateRoot: &pb.Hash{Hash: root.Bytes()},
	}, nil
}

func parseTestEVMCheckpointHash(value string, env string) (ethcommon.Hash, error) {
	hexValue := strings.TrimSpace(value)
	hexValue = strings.TrimPrefix(strings.TrimPrefix(hexValue, "0x"), "0X")
	if hexValue == "" {
		return ethcommon.Hash{}, fmt.Errorf("%s is required when %s=1", env, testEVMCheckpointOverrideEnv)
	}
	if len(hexValue) != ethcommon.HashLength*2 {
		return ethcommon.Hash{}, fmt.Errorf("%s must be exactly 32 bytes of hex", env)
	}
	decoded, err := hex.DecodeString(hexValue)
	if err != nil {
		return ethcommon.Hash{}, fmt.Errorf("%s must be hex: %w", env, err)
	}
	return ethcommon.BytesToHash(decoded), nil
}

func evmHashFromPB(h *pb.Hash) (ethcommon.Hash, error) {
	if h == nil {
		return ethcommon.Hash{}, fmt.Errorf("missing hash")
	}
	if len(h.Hash) != ethcommon.HashLength {
		return ethcommon.Hash{}, fmt.Errorf("invalid hash length %d", len(h.Hash))
	}
	return ethcommon.BytesToHash(h.Hash), nil
}

func (s *Sync) getGraphState() *pb.GraphState {
	bs := s.p2p.BlockChain().BestSnapshot()
	tips := bs.GraphState.GetTipsList()
	if len(tips) > MaxPBGraphStateTips {
		tips = tips[:MaxPBGraphStateTips]
	}

	gs := &pb.GraphState{
		Total:      uint32(bs.GraphState.GetTotal()),
		Layer:      uint32(bs.GraphState.GetLayer()),
		MainHeight: uint32(bs.GraphState.GetMainHeight()),
		MainOrder:  uint32(bs.GraphState.GetMainOrder()),
		Tips:       make([]*pb.Hash, 0, len(tips)),
	}
	for _, tip := range tips {
		gs.Tips = append(gs.Tips, &pb.Hash{Hash: tip.Bytes()})
	}

	return gs
}
