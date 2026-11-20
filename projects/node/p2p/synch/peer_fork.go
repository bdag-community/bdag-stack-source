package synch

import (
	"fmt"
	"time"

	blockdag "github.com/BlockdagNetworkLabs/bdag/blockdag"
	"github.com/BlockdagNetworkLabs/bdag/core/protocol"
	"github.com/BlockdagNetworkLabs/bdag/core/types"
	"github.com/BlockdagNetworkLabs/bdag/p2p/peers"
	pb "github.com/BlockdagNetworkLabs/bdag/p2p/proto/v1"
	ethcommon "github.com/ethereum/go-ethereum/common"
)

const (
	PeerForkObservationTask     = "PeerForkObservation"
	PeerForkObservationInterval = 30 * time.Second
)

type peerForkDAG interface {
	GetBlock(*ethcommon.Hash) blockdag.IBlock
	IsOnMainChain(uint) bool
}

// CurrentPeerForkObservation verifies the captured peer state against the
// current local chain. It does no network I/O and never overwrites the cached
// wire-probe evidence. The thirty-second wire-probe limit remains unchanged.
func (ps *PeerSync) CurrentPeerForkObservation(snapshot *peers.StatsSnap) *peers.PeerForkObservation {
	if ps == nil || ps.sy == nil || ps.sy.p2p == nil || ps.Chain() == nil {
		return currentPeerForkObservation(snapshot, nil, nil, nil)
	}
	chain := ps.Chain()
	// Follow the native chain's lock ordering: chain before DAG/EVM locks.
	// Membership, native tip and EVM verification must share one chain view.
	chain.ChainRLock()
	defer chain.ChainRUnlock()
	best := chain.BestSnapshot()
	if best == nil {
		return currentPeerForkObservation(snapshot, nil, nil, nil)
	}
	return currentPeerForkObservation(snapshot, best.GraphState, chain.Blockdag(), chain)
}

func currentPeerForkObservation(snapshot *peers.StatsSnap, localGraph *blockdag.GraphState, dag peerForkDAG, validate evmCheckpointValidator) *peers.PeerForkObservation {
	observation := &peers.PeerForkObservation{
		Status: peers.PeerForkStatusUnknown, Confidence: "none",
		Reason: "no peer graph state", LastChecked: time.Now(),
	}
	if snapshot == nil {
		return observation
	}
	// Preserve historical wire samples as evidence, but recompute every
	// native-fork and checkpoint verdict from the current captured inputs.
	if cached := snapshot.Fork; cached != nil {
		observation.SampledBlockHash = cached.SampledBlockHash
		observation.SampledBlockOrder = cached.SampledBlockOrder
		observation.SampledStateRootGot = cached.SampledStateRootGot
		observation.SampledStateRootWant = cached.SampledStateRootWant
		observation.LastError = cached.LastError
	}
	observation.SyncPoint = snapshot.SyncPoint
	fillForkObservationGraphState(observation, snapshot.GraphState)
	state := &pb.ChainState{ProtocolVersion: snapshot.Protocol, EvmCheckpoint: snapshot.EVMCheckpoint}
	observation.CommitmentStatus = checkpointCommitmentStatus(state, validate)
	if checkpoint := snapshot.EVMCheckpoint; checkpoint != nil {
		observation.EVMCheckpointNumber = checkpoint.GetNumber()
		observation.EVMCheckpointHash = hashFromPB(checkpoint.GetHash())
		observation.EVMCheckpointRoot = hashFromPB(checkpoint.GetStateRoot())
	}
	if localGraph == nil || dag == nil {
		observation.LastError = "missing current local chain context"
		return observation
	}
	observation.LocalMainOrder = uint64(localGraph.GetMainOrder())
	if tip := localGraph.GetMainChainTip(); tip != nil {
		observation.LocalMainTip = *tip
	}
	classifyPeerForkObservationOnDAG(observation, dag)
	return observation
}

func (ps *PeerSync) UpdatePeerForkObservation(pe *peers.Peer) {
	if ps == nil || pe == nil || !ps.IsRunning() || !pe.IsConnected() || !pe.IsConsensus() {
		return
	}
	if observation := pe.ForkObservation(); observation != nil && time.Since(observation.LastChecked) < PeerForkObservationInterval {
		return
	}
	pe.RunRate(PeerForkObservationTask, 0, func() {
		ps.processPeerForkObservation(pe)
	})
}

func (ps *PeerSync) processPeerForkObservation(pe *peers.Peer) {
	observation := ps.buildPeerForkObservation(pe)
	ps.enrichPeerForkObservationFromSyncDAG(pe, observation)
	ps.classifyPeerForkObservation(observation)
	pe.SetForkObservation(observation)
	if observation.DivergentMainChain() {
		log.Warn("Peer appears to be on a different Main chain",
			"peer", pe.IDWithAddress(),
			"status", observation.Status,
			"reason", observation.Reason,
			"mainTip", hashString(observation.MainTip),
			"syncPoint", hashString(observation.SyncPoint),
			"divergenceOrder", observation.DivergenceOrder,
			"lastError", observation.LastError)
	}
}

func (ps *PeerSync) buildPeerForkObservation(pe *peers.Peer) *peers.PeerForkObservation {
	observation := &peers.PeerForkObservation{
		Status:      peers.PeerForkStatusUnknown,
		Confidence:  "none",
		Reason:      "no peer graph state",
		LastChecked: time.Now(),
	}
	if ps == nil || ps.Chain() == nil || pe == nil {
		observation.LastError = "missing peer sync context"
		return observation
	}

	if best := ps.Chain().BestSnapshot(); best != nil && best.GraphState != nil {
		observation.LocalMainOrder = uint64(best.GraphState.GetMainOrder())
		if localTip := best.GraphState.GetMainChainTip(); localTip != nil {
			observation.LocalMainTip = *localTip
		}
	}

	if syncPoint := pe.SyncPoint(); syncPoint != nil {
		observation.SyncPoint = *syncPoint
	}

	if chainState := pe.ChainState(); chainState != nil {
		observation.CommitmentStatus = peerCommitmentStatus(ps, chainState)
		if checkpoint := chainState.GetEvmCheckpoint(); checkpoint != nil {
			observation.EVMCheckpointNumber = checkpoint.GetNumber()
			if hash, err := evmHashFromPB(checkpoint.GetHash()); err == nil {
				observation.EVMCheckpointHash = hash
			}
			if root, err := evmHashFromPB(checkpoint.GetStateRoot()); err == nil {
				observation.EVMCheckpointRoot = root
			}
		}
	}

	gs := pe.GraphState()
	if gs == nil {
		return observation
	}
	fillForkObservationGraphState(observation, gs)
	ps.classifyPeerForkObservation(observation)
	return observation
}

func (ps *PeerSync) enrichPeerForkObservationFromSyncDAG(pe *peers.Peer, observation *peers.PeerForkObservation) {
	if ps == nil || ps.sy == nil || ps.dagSync == nil || pe == nil || observation == nil || !pe.IsConnected() {
		return
	}
	locator := ps.dagSync.GetMainLocator(nil)
	if len(locator) == 0 {
		observation.LastError = "local main locator is empty"
		return
	}
	req := &pb.SyncDAG{
		MainLocator: changeHashsToPBHashs(locator),
		GraphState:  ps.sy.getGraphState(),
	}
	ret, err := ps.sy.Send(pe, RPCSyncDAG, req)
	if err != nil {
		observation.LastError = err.Error()
		return
	}
	subDAG, ok := ret.(*pb.SubDAG)
	if !ok || subDAG == nil {
		observation.LastError = fmt.Sprintf("unexpected syncdag response %T", ret)
		return
	}
	ps.applyPeerForkSyncDAGResponse(pe, observation, subDAG)
}

// applyPeerForkSyncDAGResponse validates the complete response before
// changing peer state, observation state, or issuing a block sample request.
// enrichPeerForkObservationFromSyncDAG is the production caller; keeping the
// response boundary together also gives focused tests the same mutation order.
func (ps *PeerSync) applyPeerForkSyncDAGResponse(pe *peers.Peer, observation *peers.PeerForkObservation, subDAG *pb.SubDAG) {
	if observation == nil {
		return
	}
	syncPoint, _, err := validateSubDAGHashes(subDAG)
	if err != nil {
		observation.LastError = err.Error()
		return
	}
	gs, err := changePBGraphStateToGraphState(subDAG.GraphState)
	if err != nil {
		observation.LastError = err.Error()
		return
	}
	if syncPoint != nil && !isZeroHash(*syncPoint) {
		observation.SyncPoint = *syncPoint
		pe.UpdateSyncPoint(syncPoint)
	}
	pe.UpdateGraphState(subDAG.GraphState)
	fillForkObservationGraphState(observation, gs)
	ps.samplePeerForkBlock(pe, observation, subDAG.Blocks)
}

func (ps *PeerSync) samplePeerForkBlock(pe *peers.Peer, observation *peers.PeerForkObservation, blocks []*pb.Hash) {
	if ps == nil || ps.sy == nil || pe == nil || observation == nil || len(blocks) == 0 {
		return
	}
	blockHash := hashFromPB(blocks[0])
	if isZeroHash(blockHash) {
		return
	}
	ret, err := ps.sy.Send(pe, RPCGetBlockDatas, &pb.GetBlockDatas{Locator: []*pb.Hash{{Hash: blockHash.Bytes()}}})
	if err != nil {
		observation.LastError = err.Error()
		return
	}
	blockDatas, ok := ret.(*pb.BlockDatas)
	if !ok || blockDatas == nil || len(blockDatas.Locator) == 0 || len(blockDatas.Locator[0].BlockBytes) == 0 {
		return
	}
	block, err := types.NewBlockFromBytes(blockDatas.Locator[0].BlockBytes)
	if err != nil {
		observation.LastError = err.Error()
		return
	}
	observation.SampledBlockHash = *block.Hash()
	observation.SampledStateRootGot = block.Block().Header.StateRoot
	if localBlock := ps.Chain().Blockdag().GetBlock(block.Hash()); localBlock != nil {
		observation.SampledBlockOrder = uint64(localBlock.GetOrder())
	}
}

func fillForkObservationGraphState(observation *peers.PeerForkObservation, gs *blockdag.GraphState) {
	if observation == nil || gs == nil {
		return
	}
	observation.MainOrder = uint64(gs.GetMainOrder())
	observation.MainHeight = uint64(gs.GetMainHeight())
	observation.Layer = uint64(gs.GetLayer())
	observation.Total = uint64(gs.GetTotal())
	observation.Tips = nil
	for _, tip := range gs.GetTipsList() {
		if tip != nil {
			observation.Tips = append(observation.Tips, *tip)
		}
	}
	if mainTip := gs.GetMainChainTip(); mainTip != nil {
		observation.MainTip = *mainTip
	}
}

func (ps *PeerSync) classifyPeerForkObservation(observation *peers.PeerForkObservation) {
	if ps == nil || ps.Chain() == nil || observation == nil {
		return
	}
	classifyPeerForkObservationOnDAG(observation, ps.Chain().Blockdag())
}

func classifyPeerForkObservationOnDAG(observation *peers.PeerForkObservation, dag peerForkDAG) {
	observation.Status = peers.PeerForkStatusUnknown
	observation.Confidence = "none"
	observation.Reason = "no peer main tip"
	observation.SameNativeForkKnown = false
	observation.SameNativeFork = false
	observation.DivergenceOrder = 0

	if isZeroHash(observation.MainTip) {
		classifyPeerForkObservationBySyncPoint(observation, dag)
		return
	}
	if !isZeroHash(observation.LocalMainTip) &&
		observation.MainTip == observation.LocalMainTip &&
		observation.MainOrder == observation.LocalMainOrder {
		markPeerSameMainChain(observation, "peer main tip matches local main tip", "main_tip")
		return
	}

	localBlock := dag.GetBlock(&observation.MainTip)
	if localBlock == nil {
		observation.Reason = "peer main tip is not known locally"
		classifyPeerForkObservationBySyncPoint(observation, dag)
		return
	}
	if !dag.IsOnMainChain(localBlock.GetID()) {
		observation.Status = peers.PeerForkStatusDivergentMainChain
		observation.Confidence = "main_tip"
		observation.Reason = "peer main tip is known locally but is not on the local Main chain"
		observation.SameNativeForkKnown = true
		observation.SameNativeFork = false
		observation.DivergenceOrder = minUint64(uint64(localBlock.GetOrder()), observation.MainOrder)
		return
	}
	if uint64(localBlock.GetOrder()) == observation.MainOrder {
		markPeerSameMainChain(observation, "peer main tip is known on the local Main chain", "main_tip")
		return
	}
	observation.Reason = fmt.Sprintf("peer main tip is on the local Main chain at order %d, peer reported order %d", localBlock.GetOrder(), observation.MainOrder)
	observation.Confidence = "main_tip_order_mismatch"
}

func classifyPeerForkObservationBySyncPoint(observation *peers.PeerForkObservation, dag peerForkDAG) {
	if observation == nil || dag == nil || isZeroHash(observation.SyncPoint) {
		return
	}
	syncPoint := dag.GetBlock(&observation.SyncPoint)
	if syncPoint == nil {
		observation.Reason = "peer sync point is not known locally"
		observation.Confidence = "sync_point_unknown"
		return
	}
	if !dag.IsOnMainChain(syncPoint.GetID()) {
		observation.Reason = "peer sync point is known locally but is not on the local Main chain"
		observation.Confidence = "sync_point_not_main"
		return
	}
	observation.Status = peers.PeerForkStatusCommonMainChainPrefix
	observation.Confidence = "sync_point"
	observation.Reason = "peer shares a local Main chain prefix; peer tip still needs confirmation"
}

func markPeerSameMainChain(observation *peers.PeerForkObservation, reason string, confidence string) {
	observation.Status = peers.PeerForkStatusSameMainChain
	observation.Confidence = confidence
	observation.Reason = reason
	observation.SameNativeForkKnown = true
	observation.SameNativeFork = true
}

func peerCommitmentStatus(ps *PeerSync, chainState *pb.ChainState) string {
	var validate evmCheckpointValidator
	if ps != nil && ps.sy != nil && ps.sy.p2p != nil && ps.sy.p2p.BlockChain() != nil {
		validate = ps.sy.p2p.BlockChain()
	}
	return checkpointCommitmentStatus(chainState, validate)
}

func checkpointCommitmentStatus(chainState *pb.ChainState, validate evmCheckpointValidator) string {
	if chainState == nil || chainState.ProtocolVersion == 0 {
		return "unknown"
	}
	if chainState.ProtocolVersion < uint32(protocol.EVMCheckpointProtocolVersion) {
		return "legacy_v45"
	}
	checkpoint := chainState.GetEvmCheckpoint()
	if checkpoint == nil {
		return "missing_checkpoint"
	}
	hash, hashErr := evmHashFromPB(checkpoint.GetHash())
	root, rootErr := evmHashFromPB(checkpoint.GetStateRoot())
	if hashErr != nil || rootErr != nil {
		return "invalid_checkpoint"
	}
	if validate == nil {
		return "checkpoint_unverified"
	}
	valid, known := validate.ValidateEVMCheckpoint(checkpoint.GetNumber(), hash, root)
	if !known {
		return "checkpoint_unknown"
	}
	if valid {
		return "checkpoint_valid"
	}
	return "checkpoint_mismatch"
}

func hashFromPB(pbHash *pb.Hash) ethcommon.Hash {
	if pbHash == nil {
		return ethcommon.Hash{}
	}
	hash, err := evmHashFromPB(pbHash)
	if err != nil {
		return ethcommon.Hash{}
	}
	return hash
}

func hashString(hash ethcommon.Hash) string {
	if isZeroHash(hash) {
		return ""
	}
	return hash.String()
}

func isZeroHash(hash ethcommon.Hash) bool {
	return hash == (ethcommon.Hash{})
}

func minUint64(a, b uint64) uint64 {
	if a < b {
		return a
	}
	return b
}
