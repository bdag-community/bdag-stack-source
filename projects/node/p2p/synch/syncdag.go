package synch

import (
	"context"
	"fmt"
	"strings"

	blockdag "github.com/BlockdagNetworkLabs/bdag/blockdag"
	"github.com/BlockdagNetworkLabs/bdag/core/protocol"
	"github.com/BlockdagNetworkLabs/bdag/p2p/common"
	"github.com/BlockdagNetworkLabs/bdag/p2p/peers"
	pb "github.com/BlockdagNetworkLabs/bdag/p2p/proto/v1"
	ethcommon "github.com/ethereum/go-ethereum/common"
	libp2pcore "github.com/libp2p/go-libp2p/core"
	"github.com/libp2p/go-libp2p/core/network"
)

// MaxBlockLocatorsPerMsg is the maximum number of block locator hashes allowed
// per message.
const MaxBlockLocatorsPerMsg = blockdag.MaxDAGSyncHashes

func validateDAGHashes(hashes []*pb.Hash, limit int) error {
	if len(hashes) > limit {
		return fmt.Errorf("DAG request exceeds hash limit")
	}
	for _, hash := range hashes {
		if hash == nil || len(hash.Hash) != ethcommon.HashLength || ethcommon.BytesToHash(hash.Hash) == (ethcommon.Hash{}) {
			return fmt.Errorf("DAG request requires exact nonzero hashes")
		}
	}
	return nil
}

func (s *Sync) sendSyncDAGRequest(stream network.Stream, pe *peers.Peer) (*pb.SubDAG, *common.Error) {
	e := ReadRspCode(stream, s.p2p)
	if !e.Code.IsSuccess() {
		e.Add("sync DAG request rsp")
		log.Debug("[DEBUG-syncdag] outbound syncdag response status failed",
			"protocol", stream.Protocol(),
			"peer", pe.GetID(),
			"remoteAddr", stream.Conn().RemoteMultiaddr(),
			"direction", stream.Stat().Direction,
			"err", e.String())
		return nil, e
	}

	msg := &pb.SubDAG{}
	if err := DecodeMessage(stream, s.p2p, msg); err != nil {
		log.Debug("[DEBUG-syncdag] outbound syncdag response decode failed",
			"protocol", stream.Protocol(),
			"peer", pe.GetID(),
			"remoteAddr", stream.Conn().RemoteMultiaddr(),
			"direction", stream.Stat().Direction,
			"messageType", fmt.Sprintf("%T", msg),
			"err", err)
		return nil, common.NewError(common.ErrStreamRead, err)
	}
	log.Debug("[DEBUG-syncdag] outbound syncdag response decoded",
		"protocol", stream.Protocol(),
		"peer", pe.GetID(),
		"remoteAddr", stream.Conn().RemoteMultiaddr(),
		"direction", stream.Stat().Direction,
		"syncPoint", debugHashPB(msg.SyncPoint),
		"graphState", debugGraphStatePB(msg.GraphState),
		"blocks", len(msg.Blocks),
		"responseSize", s.p2p.Encoding().GetSize(msg))
	return msg, nil
}

func (s *Sync) syncDAGHandler(ctx context.Context, msg any, stream libp2pcore.Stream, pe *peers.Peer) *common.Error {
	m, ok := msg.(*pb.SyncDAG)
	if !ok || m == nil {
		err := fmt.Errorf("message is not type *pb.Hash")
		return ErrMessage(err)
	}
	if err := validateDAGHashes(m.MainLocator, MaxBlockLocatorsPerMsg); err != nil {
		return ErrMessage(err)
	}
	locators, err := changePBHashsToHashs(m.MainLocator)
	if err != nil {
		return ErrMessage(fmt.Errorf("invalid syncdag locator: %w", err))
	}
	if _, err := changePBGraphStateToGraphState(m.GraphState); err != nil {
		return ErrMessage(fmt.Errorf("invalid syncdag graph state: %w", err))
	}
	if pe == nil || ctx == nil || ctx.Err() != nil {
		return ErrMessage(fmt.Errorf("DAG request peer/context is unavailable or cancelled"))
	}
	requestSize := s.p2p.Encoding().GetSize(m)
	pe.UpdateGraphState(m.GraphState)

	gs := pe.GraphState()
	blocks, point, err := s.PeerSync().dagSync.CalcSyncBlocksContext(ctx, gs, locators, blockdag.SubDAGMode, MaxBlockLocatorsPerMsg, s.p2p.BlockChain().IsCurrent())
	if err != nil {
		return ErrMessage(err)
	}
	if point == nil {
		err := fmt.Errorf("CalcSyncBlocks error by SubDAGMode: point=<nil> locatorLen=%d", len(m.MainLocator))
		log.Debug("[DEBUG-syncdag] inbound syncdag calc failed",
			"protocol", stream.Protocol(),
			"peer", pe.GetID(),
			"remoteAddr", stream.Conn().RemoteMultiaddr(),
			"direction", stream.Stat().Direction,
			"requestGraphState", debugGraphStatePB(m.GraphState),
			"locatorLen", len(m.MainLocator),
			"locatorFirst", debugHashPBAt(m.MainLocator, 0),
			"locatorLast", debugHashPBAt(m.MainLocator, len(m.MainLocator)-1),
			"requestSize", requestSize,
			"calcBlocks", len(blocks),
			"calcPoint", debugHash(point),
			"err", err)
		return ErrMessage(err)
	}
	pe.UpdateSyncPoint(point)
	sd := &pb.SubDAG{SyncPoint: &pb.Hash{Hash: point.Bytes()}, GraphState: s.getGraphState(), Blocks: changeHashsToPBHashs(blocks)}
	log.Debug("[DEBUG-syncdag] inbound syncdag request",
		"protocol", stream.Protocol(),
		"peer", pe.GetID(),
		"remoteAddr", stream.Conn().RemoteMultiaddr(),
		"direction", stream.Stat().Direction,
		"requestGraphState", debugGraphStatePB(m.GraphState),
		"locatorLen", len(m.MainLocator),
		"locatorFirst", debugHashPBAt(m.MainLocator, 0),
		"locatorLast", debugHashPBAt(m.MainLocator, len(m.MainLocator)-1),
		"requestSize", requestSize,
		"calcBlocks", len(blocks),
		"calcPoint", debugHash(point),
		"responseGraphState", debugGraphStatePB(sd.GraphState),
		"responseSize", s.p2p.Encoding().GetSize(sd))

	e := s.EncodeResponseMsg(stream, sd)
	if e != nil && !e.Code.IsSuccess() {
		log.Debug("[DEBUG-syncdag] inbound syncdag response encode failed",
			"protocol", stream.Protocol(),
			"peer", pe.GetID(),
			"remoteAddr", stream.Conn().RemoteMultiaddr(),
			"direction", stream.Stat().Direction,
			"err", e.String())
	} else {
		log.Debug("[DEBUG-syncdag] inbound syncdag response encoded",
			"protocol", stream.Protocol(),
			"peer", pe.GetID(),
			"remoteAddr", stream.Conn().RemoteMultiaddr(),
			"direction", stream.Stat().Direction,
			"blocks", len(sd.Blocks),
			"responseSize", s.p2p.Encoding().GetSize(sd))
	}
	return e
}

func (s *Sync) conSyncDAGHandler(ctx context.Context, msg any, stream libp2pcore.Stream, pe *peers.Peer) *common.Error {
	m, ok := msg.(*pb.ContinueSyncDAG)
	if !ok || m == nil {
		err := fmt.Errorf("message is not type *pb.Hash")
		return ErrMessage(err)
	}
	if err := validateDAGHashes([]*pb.Hash{m.SyncPoint, m.Start}, 2); err != nil {
		return ErrMessage(err)
	}
	if pe == nil || ctx == nil || ctx.Err() != nil {
		return ErrMessage(fmt.Errorf("DAG request peer/context is unavailable or cancelled"))
	}
	syncPoint, syncPointErr := changePBHashToHash(m.SyncPoint)
	start, startErr := changePBHashToHash(m.Start)
	if syncPointErr != nil || startErr != nil {
		return ErrMessage(fmt.Errorf("invalid continue sync hash: sync point: %v, start: %v", syncPointErr, startErr))
	}
	requestSize := s.p2p.Encoding().GetSize(m)

	gs := pe.GraphState()
	blocks, point, err := s.PeerSync().dagSync.CalcSyncBlocksContext(ctx, gs, []*ethcommon.Hash{syncPoint, start}, blockdag.ContinueMode, MaxBlockLocatorsPerMsg, s.p2p.BlockChain().IsCurrent())
	if err != nil {
		return ErrMessage(err)
	}
	if point == nil || len(blocks) <= 0 {
		err := fmt.Errorf("CalcSyncBlocks error by ContinueMode: point=%s start=%s", debugHashPB(m.SyncPoint), debugHashPB(m.Start))
		log.Debug("[DEBUG-syncdag] inbound continuesyncdag calc failed",
			"protocol", stream.Protocol(),
			"peer", pe.GetID(),
			"remoteAddr", stream.Conn().RemoteMultiaddr(),
			"direction", stream.Stat().Direction,
			"syncPoint", debugHashPB(m.SyncPoint),
			"start", debugHashPB(m.Start),
			"requestSize", requestSize,
			"calcBlocks", len(blocks),
			"calcPoint", debugHash(point),
			"err", err)
		return ErrMessage(err)
	}
	pe.UpdateSyncPoint(point)
	sd := &pb.SubDAG{SyncPoint: &pb.Hash{Hash: point.Bytes()}, GraphState: s.getGraphState(), Blocks: changeHashsToPBHashs(blocks)}
	log.Debug("[DEBUG-syncdag] inbound continuesyncdag request",
		"protocol", stream.Protocol(),
		"peer", pe.GetID(),
		"remoteAddr", stream.Conn().RemoteMultiaddr(),
		"direction", stream.Stat().Direction,
		"syncPoint", debugHashPB(m.SyncPoint),
		"start", debugHashPB(m.Start),
		"requestSize", requestSize,
		"calcBlocks", len(blocks),
		"calcPoint", debugHash(point),
		"responseGraphState", debugGraphStatePB(sd.GraphState),
		"responseSize", s.p2p.Encoding().GetSize(sd))

	e := s.EncodeResponseMsg(stream, sd)
	if e != nil && !e.Code.IsSuccess() {
		log.Debug("[DEBUG-syncdag] inbound continuesyncdag response encode failed",
			"protocol", stream.Protocol(),
			"peer", pe.GetID(),
			"remoteAddr", stream.Conn().RemoteMultiaddr(),
			"direction", stream.Stat().Direction,
			"err", e.String())
	} else {
		log.Debug("[DEBUG-syncdag] inbound continuesyncdag response encoded",
			"protocol", stream.Protocol(),
			"peer", pe.GetID(),
			"remoteAddr", stream.Conn().RemoteMultiaddr(),
			"direction", stream.Stat().Direction,
			"blocks", len(sd.Blocks),
			"responseSize", s.p2p.Encoding().GetSize(sd))
	}
	return e
}

func debugSyncDAG(m *pb.SyncDAG) string {
	sb := strings.Builder{}
	sb.WriteString(fmt.Sprintf("SyncDAG: graphstate=(%v,%v,%v), ",
		m.GraphState.MainOrder, m.GraphState.MainHeight, m.GraphState.Layer,
	))
	sb.WriteString("locator=[")
	size := len(m.MainLocator)
	for i, h := range m.MainLocator {
		locator, err := changePBHashToHash(h)
		if err != nil {
			sb.WriteString("<invalid>")
		} else {
			sb.WriteString(locator.String())
		}
		if i+1 < size {
			sb.WriteString(",")
		}
	}
	sb.WriteString("]")
	sb.WriteString(fmt.Sprintf(", size=%d ", size))
	return sb.String()
}

func syncDAGBlockDataStopReason(result *ProcessResult, pe *peers.Peer) string {
	if result == nil {
		return "blockdata processing returned no result"
	}
	if result.add > 0 {
		return "blockdata added blocks; restarting sync round from updated local state"
	}
	if result.act.IsTryAgain() {
		return "blockdata requested retry: " + result.reasonOr("unspecified retry reason")
	}
	if result.act.IsNothing() {
		return "blockdata made no decision"
	}
	if pe != nil && pe.ChainState() != nil && pe.ChainState().ProtocolVersion < uint32(protocol.ConSyncDAGProtocolVersion) {
		return "peer does not support continue syncdag protocol"
	}
	return "blockdata stopped sync round"
}

func (ps *PeerSync) processSyncDAGBlocks(pe *peers.Peer) *ProcessResult {
	log.Trace("processSyncDAGBlocks peer", "peer", lazyTraceValue(func() any { return pe.GetID() }), "processID", ps.processID)
	isSyncPeer := ps.isSyncPeer(pe)
	connected := pe.IsConnected()
	if !isSyncPeer || !connected {
		log.Debug("[DEBUG-syncdag] outbound syncdag abort unavailable peer",
			"peer", pe.GetID(),
			"isSyncPeer", isSyncPeer,
			"connected", connected,
			"syncPoint", debugHash(pe.SyncPoint()),
			"peerGraphState", debugGraphState(pe.GraphState()),
			"localGraphState", debugGraphStatePB(ps.sy.getGraphState()),
			"processID", ps.processID)
		return nil
	}
	point := pe.SyncPoint()
	mainLocator := ps.dagSync.GetMainLocator(point)
	sd := &pb.SyncDAG{MainLocator: changeHashsToPBHashs(mainLocator), GraphState: ps.sy.getGraphState()}
	log.Trace("processSyncDAGBlocks sendSyncDAG", "point", lazyTraceValue(func() any { return debugHash(point) }), "sd", lazyTraceValue(func() any { return debugSyncDAG(sd) }), "processID", ps.processID)
	log.Debug("[DEBUG-syncdag] outbound syncdag request",
		"peer", pe.GetID(),
		"peerGraphState", debugGraphState(pe.GraphState()),
		"localGraphState", debugGraphStatePB(ps.sy.getGraphState()),
		"syncPoint", debugHash(point),
		"locatorLen", len(mainLocator),
		"locatorFirst", debugHashAt(mainLocator, 0),
		"locatorLast", debugHashAt(mainLocator, len(mainLocator)-1),
		"requestSize", ps.sy.p2p.Encoding().GetSize(sd),
		"processID", ps.processID)
	ret, err := ps.sy.Send(pe, RPCSyncDAG, sd)
	if err != nil {
		log.Trace("processSyncDAGBlocks error", "error", lazyTraceValue(func() any { return err.Error() }), "processID", ps.processID)
		log.Debug("[DEBUG-syncdag] outbound syncdag request failed",
			"peer", pe.GetID(),
			"peerGraphState", debugGraphState(pe.GraphState()),
			"localGraphState", debugGraphStatePB(ps.sy.getGraphState()),
			"syncPoint", debugHash(point),
			"locatorLen", len(mainLocator),
			"locatorFirst", debugHashAt(mainLocator, 0),
			"locatorLast", debugHashAt(mainLocator, len(mainLocator)-1),
			"requestSize", ps.sy.p2p.Encoding().GetSize(sd),
			"err", err,
			"processID", ps.processID)
		return processResult(ProcessResultActionTryAgain, "syncdag request failed", false, 0).withErr(err)
	}
	subd, ok := ret.(*pb.SubDAG)
	if !ok || subd == nil {
		return processResult(ProcessResultActionTryAgain, "invalid syncdag response", false, 0).withErr(fmt.Errorf("unexpected response %T", ret))
	}
	if ps.IsInterrupt() {
		log.Debug("[DEBUG-syncdag] outbound syncdag abort interrupted after response",
			"peer", pe.GetID(),
			"syncPoint", debugHashPB(subd.SyncPoint),
			"graphState", debugGraphStatePB(subd.GraphState),
			"blocks", len(subd.Blocks),
			"connected", pe.IsConnected(),
			"isSyncPeer", ps.isSyncPeer(pe),
			"processID", ps.processID)
		return nil
	}
	syncPoint, blocks, responseSize, err := validateSubDAGBeforeSize(subd, func() int {
		return ps.sy.p2p.Encoding().GetSize(subd)
	})
	if err != nil {
		return processResult(ProcessResultActionTryAgain, "invalid syncdag response", false, 0).withErr(err)
	}
	log.Debug("[DEBUG-syncdag] outbound syncdag response",
		"peer", pe.GetID(),
		"syncPoint", debugHashPB(subd.SyncPoint),
		"graphState", debugGraphStatePB(subd.GraphState),
		"blocks", len(subd.Blocks),
		"responseSize", responseSize,
		"processID", ps.processID)
	log.Trace("processSyncDAGBlocks result", "mainOrder", subd.GraphState.MainOrder,
		"mainHeight", subd.GraphState.MainHeight, "layer", subd.GraphState.Layer,
		"blocks", len(subd.Blocks), "processID", ps.processID)
	pe.UpdateSyncPoint(syncPoint)
	pe.UpdateGraphState(subd.GraphState)

	if len(subd.Blocks) <= 0 {
		pe.UpdateSyncPoint(ps.Chain().Blockdag().GetGenesisHash())
		log.Debug("[DEBUG-syncdag] outbound syncdag empty response",
			"peer", pe.GetID(),
			"syncPoint", debugHashPB(subd.SyncPoint),
			"graphState", debugGraphStatePB(subd.GraphState),
			"processID", ps.processID)
		return processResult(ProcessResultActionTryAgain, "syncdag response had no blocks", false, 0)
	}
	var pret *ProcessResult
	conCount := 0
	for len(blocks) > 0 {
		log.Trace("processSyncDAGBlocks do GetBlockDatas", "blocks", len(subd.Blocks), "processID", ps.processID)
		pret = ps.processGetBlockDatas(pe, blocks)
		if pret == nil {
			log.Debug("[DEBUG-syncdag] outbound blockdata result",
				"peer", pe.GetID(),
				"action", "<nil>",
				"reason", "processGetBlockDatas returned nil",
				"blocks", len(blocks),
				"connected", pe.IsConnected(),
				"isSyncPeer", ps.isSyncPeer(pe),
				"processID", ps.processID)
			return pret
		}
		log.Debug("[DEBUG-syncdag] outbound blockdata result",
			"peer", pe.GetID(),
			"action", pret.act.String(),
			"reason", pret.reasonOr(""),
			"block", pret.block,
			"err", pret.errString(),
			"add", pret.add,
			"orphan", pret.orphan,
			"blocks", len(blocks),
			"connected", pe.IsConnected(),
			"isSyncPeer", ps.isSyncPeer(pe),
			"processID", ps.processID)
		if pret.add > 0 ||
			pret.act.IsTryAgain() ||
			pret.act.IsNothing() ||
			pe.ChainState().ProtocolVersion < uint32(protocol.ConSyncDAGProtocolVersion) {
			continueSupported := pe.ChainState().ProtocolVersion >= uint32(protocol.ConSyncDAGProtocolVersion)
			log.Debug("[DEBUG-syncdag] outbound syncdag return after blockdata",
				"peer", pe.GetID(),
				"reason", syncDAGBlockDataStopReason(pret, pe),
				"blockdataReason", pret.reasonOr(""),
				"blockdataBlock", pret.block,
				"blockdataErr", pret.errString(),
				"action", pret.act.String(),
				"add", pret.add,
				"orphan", pret.orphan,
				"protocolVersion", pe.ChainState().ProtocolVersion,
				"continueProtocolVersion", protocol.ConSyncDAGProtocolVersion,
				"continueSupported", continueSupported,
				"syncPoint", debugHash(pe.SyncPoint()),
				"peerGraphState", debugGraphState(pe.GraphState()),
				"localGraphState", debugGraphStatePB(ps.sy.getGraphState()),
				"processID", ps.processID)
			return pret
		}
		endBlock := blocks[len(blocks)-1]
		if endBlock.Cmp(*pe.GraphState().GetMainChainTip()) == 0 {
			log.Debug("[DEBUG-syncdag] outbound syncdag return reached peer tip",
				"peer", pe.GetID(),
				"endBlock", endBlock.String(),
				"peerGraphState", debugGraphState(pe.GraphState()),
				"action", pret.act.String(),
				"add", pret.add,
				"orphan", pret.orphan,
				"processID", ps.processID)
			return pret
		}
		isSyncPeer = ps.isSyncPeer(pe)
		connected = pe.IsConnected()
		if !isSyncPeer {
			log.Debug("[DEBUG-syncdag] outbound syncdag return unavailable before continue",
				"peer", pe.GetID(),
				"isSyncPeer", isSyncPeer,
				"connected", connected,
				"endBlock", endBlock.String(),
				"action", pret.act.String(),
				"add", pret.add,
				"orphan", pret.orphan,
				"processID", ps.processID)
			return pret
		}
		if !connected {
			log.Debug("[DEBUG-syncdag] outbound continuesyncdag attempting with disconnected peer",
				"peer", pe.GetID(),
				"endBlock", endBlock.String(),
				"action", pret.act.String(),
				"add", pret.add,
				"orphan", pret.orphan,
				"processID", ps.processID)
		}
		point = pe.SyncPoint()
		log.Trace("Try continue sync DAG blocks", "point", lazyTraceValue(func() any { return debugHash(point) }), "start", lazyTraceValue(func() any { return endBlock.String() }), "continue", conCount, "processID", ps.processID)
		csd := &pb.ContinueSyncDAG{SyncPoint: &pb.Hash{Hash: point.Bytes()}, Start: &pb.Hash{Hash: endBlock.Bytes()}}
		log.Debug("[DEBUG-syncdag] outbound continuesyncdag request",
			"peer", pe.GetID(),
			"syncPoint", debugHash(point),
			"start", endBlock.String(),
			"requestSize", ps.sy.p2p.Encoding().GetSize(csd),
			"continue", conCount,
			"processID", ps.processID)
		ret, err := ps.sy.Send(pe, RPCContinueSyncDAG, csd)
		if err != nil {
			log.Trace("processSyncDAGBlocks error", "error", lazyTraceValue(func() any { return err.Error() }), "processID", ps.processID)
			log.Debug("[DEBUG-syncdag] outbound continuesyncdag request failed",
				"peer", pe.GetID(),
				"syncPoint", debugHash(point),
				"start", endBlock.String(),
				"requestSize", ps.sy.p2p.Encoding().GetSize(csd),
				"err", err,
				"continue", conCount,
				"processID", ps.processID)
			return processResult(ProcessResultActionTryAgain, "continuesyncdag request failed", pret.orphan, pret.add).withErr(err)
		}
		var ok bool
		subd, ok = ret.(*pb.SubDAG)
		if !ok || subd == nil {
			return processResult(ProcessResultActionTryAgain, "invalid continuesyncdag response", pret.orphan, pret.add).withErr(fmt.Errorf("unexpected response %T", ret))
		}
		if ps.IsInterrupt() {
			log.Debug("[DEBUG-syncdag] outbound syncdag abort interrupted after continuesyncdag response",
				"peer", pe.GetID(),
				"syncPoint", debugHashPB(subd.SyncPoint),
				"graphState", debugGraphStatePB(subd.GraphState),
				"blocks", len(subd.Blocks),
				"connected", pe.IsConnected(),
				"isSyncPeer", ps.isSyncPeer(pe),
				"continue", conCount,
				"processID", ps.processID)
			return pret
		}
		var responseSize int
		_, blocks, responseSize, err = validateSubDAGBeforeSize(subd, func() int {
			return ps.sy.p2p.Encoding().GetSize(subd)
		})
		if err != nil {
			return processResult(ProcessResultActionTryAgain, "invalid continuesyncdag response", pret.orphan, pret.add).withErr(err)
		}
		log.Debug("[DEBUG-syncdag] outbound continuesyncdag response",
			"peer", pe.GetID(),
			"syncPoint", debugHashPB(subd.SyncPoint),
			"graphState", debugGraphStatePB(subd.GraphState),
			"blocks", len(subd.Blocks),
			"responseSize", responseSize,
			"continue", conCount,
			"processID", ps.processID)
		log.Trace("processSyncDAGBlocks result", "mainOrder", subd.GraphState.MainOrder,
			"mainHeight", subd.GraphState.MainHeight, "layer", subd.GraphState.Layer,
			"blocks", len(subd.Blocks), "continue", conCount, "processID", ps.processID)
		pe.UpdateGraphState(subd.GraphState)
		conCount++
	}
	log.Debug("[DEBUG-syncdag] outbound syncdag return loop drained",
		"peer", pe.GetID(),
		"resultNil", pret == nil,
		"processID", ps.processID)
	return pret
}

func debugGraphState(gs *blockdag.GraphState) string {
	if gs == nil {
		return "<nil>"
	}
	return gs.String()
}

func debugGraphStatePB(gs *pb.GraphState) string {
	converted, err := changePBGraphStateToGraphState(gs)
	if err != nil {
		return "<invalid>"
	}
	return debugGraphState(converted)
}

func validateSubDAGHashes(subd *pb.SubDAG) (*ethcommon.Hash, []*ethcommon.Hash, error) {
	if subd == nil {
		return nil, nil, fmt.Errorf("missing subdag response")
	}
	if _, err := changePBGraphStateToGraphState(subd.GraphState); err != nil {
		return nil, nil, fmt.Errorf("invalid graph state: %w", err)
	}
	syncPoint, err := changePBHashToHash(subd.SyncPoint)
	if err != nil {
		return nil, nil, fmt.Errorf("invalid sync point: %w", err)
	}
	blocks, err := changePBHashsToHashs(subd.Blocks)
	if err != nil {
		return nil, nil, fmt.Errorf("invalid subdag block hash: %w", err)
	}
	return syncPoint, blocks, nil
}

// validateSubDAGBeforeSize keeps generated SSZ sizing after complete input
// validation. Some generated SizeSSZ methods normalize nil nested messages;
// sizing an untrusted response first can therefore erase the distinction
// between a malformed graph state and a valid one before validation runs.
func validateSubDAGBeforeSize(subd *pb.SubDAG, size func() int) (*ethcommon.Hash, []*ethcommon.Hash, int, error) {
	syncPoint, blocks, err := validateSubDAGHashes(subd)
	if err != nil {
		return nil, nil, 0, err
	}
	if size == nil {
		return syncPoint, blocks, 0, nil
	}
	return syncPoint, blocks, size(), nil
}

func debugHash(h *ethcommon.Hash) string {
	if h == nil {
		return "<nil>"
	}
	return h.String()
}

func debugHashAt(hs []*ethcommon.Hash, idx int) string {
	if idx < 0 || idx >= len(hs) {
		return "<none>"
	}
	return debugHash(hs[idx])
}

func debugHashPB(h *pb.Hash) string {
	if h == nil || len(h.Hash) != ethcommon.HashLength {
		return "<nil>"
	}
	return ethcommon.BytesToHash(h.Hash).String()
}

func debugHashPBAt(hs []*pb.Hash, idx int) string {
	if idx < 0 || idx >= len(hs) {
		return "<none>"
	}
	return debugHashPB(hs[idx])
}
