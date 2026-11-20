package synch

import (
	"fmt"

	"github.com/BlockdagNetworkLabs/bdag/p2p/common"
	"github.com/BlockdagNetworkLabs/bdag/p2p/peers"
	pb "github.com/BlockdagNetworkLabs/bdag/p2p/proto/v1"
	ethcommon "github.com/ethereum/go-ethereum/common"
	"github.com/libp2p/go-libp2p/core/peer"
)

const evmStateGuardPeerRejectThreshold = 3

type evmStateGuardStrikeKey struct {
	pid       peer.ID
	syncPoint ethcommon.Hash
}

type evmStateGuardFailureDecision struct {
	reject    bool
	strikes   int
	threshold int
	syncPoint ethcommon.Hash
}

type evmCheckpointValidator interface {
	ValidateEVMCheckpoint(number uint64, hash ethcommon.Hash, root ethcommon.Hash) (bool, bool)
}

type evmPeerCheckpointStore interface {
	SetPeerEVMCheckpoint(pid peer.ID, number uint64, hash ethcommon.Hash, root ethcommon.Hash)
}

type evmCheckpointChain interface {
	evmCheckpointValidator
	evmPeerCheckpointStore
}

func (ps *PeerSync) validatePeerEVMCheckpoint(pe *peers.Peer) error {
	if ps == nil || ps.sy == nil || ps.sy.p2p == nil || ps.sy.p2p.BlockChain() == nil || pe == nil {
		return nil
	}
	if !ps.sy.enforcePeerEVMCheckpoint(pe) {
		return nil
	}
	return validatePeerEVMCheckpointWithChain(ps.sy.p2p.BlockChain(), pe.GetID(), pe.ChainState())
}

func validatePeerEVMCheckpointWithChain(chain evmCheckpointChain, pid peer.ID, chainState *pb.ChainState) error {
	if chain == nil {
		return nil
	}
	if chainState == nil || chainState.GetEvmCheckpoint() == nil {
		return fmt.Errorf("peer %s is missing evm checkpoint", pid)
	}
	checkpoint := chainState.GetEvmCheckpoint()
	hash, err := evmHashFromPB(checkpoint.GetHash())
	if err != nil {
		return fmt.Errorf("peer %s has invalid evm checkpoint hash: %w", pid, err)
	}
	root, err := evmHashFromPB(checkpoint.GetStateRoot())
	if err != nil {
		return fmt.Errorf("peer %s has invalid evm checkpoint state root: %w", pid, err)
	}
	valid, known := chain.ValidateEVMCheckpoint(checkpoint.GetNumber(), hash, root)
	if !known {
		chain.SetPeerEVMCheckpoint(pid, checkpoint.GetNumber(), hash, root)
		return nil
	}
	if valid {
		chain.SetPeerEVMCheckpoint(pid, checkpoint.GetNumber(), hash, root)
		return nil
	}
	return fmt.Errorf("peer %s evm checkpoint mismatch at block %d", pid, checkpoint.GetNumber())
}

func (ps *PeerSync) rejectPeerForEVMCheckpoint(pe *peers.Peer, err error) {
	if err == nil || ps == nil || ps.sy == nil || pe == nil {
		return
	}
	log.Warn("Rejecting peer for EVM checkpoint mismatch", "peer", pe.IDWithAddress(), "err", err)
	go ps.sy.sendGoodByeAndDisconnectWithDetail(common.ErrDAGConsensus, pe, err.Error())
	ps.requestSyncPeerInterrupt()
}

func (ps *PeerSync) rejectPeerForEVMStateGuard(pe *peers.Peer, err error) {
	if err == nil || ps == nil || ps.sy == nil || pe == nil {
		return
	}
	decision := ps.recordPeerEVMStateGuardFailure(pe, err)
	if !decision.reject {
		log.Warn("EVM state commitment mismatch scored peer",
			"peer", pe.IDWithAddress(),
			"err", err,
			"strikes", decision.strikes,
			"threshold", decision.threshold,
			"syncPoint", decision.syncPoint.String())
		ps.requestSyncPeerInterrupt()
		return
	}
	detail := fmt.Sprintf("%s (strikes=%d/%d syncPoint=%s)", err.Error(), decision.strikes, decision.threshold, decision.syncPoint.String())
	log.Warn("Rejecting peer for repeated EVM state commitment mismatch",
		"peer", pe.IDWithAddress(),
		"err", err,
		"strikes", decision.strikes,
		"threshold", decision.threshold,
		"syncPoint", decision.syncPoint.String())
	ps.clearPeerEVMStateGuardFailures(pe)
	go ps.sy.sendGoodByeAndDisconnectWithDetail(common.ErrDAGConsensus, pe, detail)
	ps.requestSyncPeerInterrupt()
}

func (ps *PeerSync) recordPeerEVMStateGuardFailure(pe *peers.Peer, err error) evmStateGuardFailureDecision {
	decision := evmStateGuardFailureDecision{threshold: evmStateGuardPeerRejectThreshold}
	if ps == nil || pe == nil {
		return decision
	}
	if err != nil {
		pe.IncrementBadResponses(common.NewError(common.ErrDAGConsensus, err))
	}
	key := evmStateGuardStrikeKey{pid: pe.GetID(), syncPoint: peerSyncPointHash(pe)}

	ps.evmStateGuardStrikeLock.Lock()
	defer ps.evmStateGuardStrikeLock.Unlock()

	if ps.evmStateGuardStrikes == nil {
		ps.evmStateGuardStrikes = make(map[evmStateGuardStrikeKey]int)
	}
	for existing := range ps.evmStateGuardStrikes {
		if existing.pid == key.pid && existing.syncPoint != key.syncPoint {
			delete(ps.evmStateGuardStrikes, existing)
		}
	}
	ps.evmStateGuardStrikes[key]++

	decision.strikes = ps.evmStateGuardStrikes[key]
	decision.syncPoint = key.syncPoint
	decision.reject = decision.strikes >= decision.threshold
	return decision
}

func (ps *PeerSync) clearPeerEVMStateGuardFailures(pe *peers.Peer) {
	if ps == nil || pe == nil {
		return
	}
	pid := pe.GetID()

	ps.evmStateGuardStrikeLock.Lock()
	defer ps.evmStateGuardStrikeLock.Unlock()

	for key := range ps.evmStateGuardStrikes {
		if key.pid == pid {
			delete(ps.evmStateGuardStrikes, key)
		}
	}
}

func peerSyncPointHash(pe *peers.Peer) ethcommon.Hash {
	if pe == nil {
		return ethcommon.Hash{}
	}
	syncPoint := pe.SyncPoint()
	if syncPoint == nil {
		return ethcommon.Hash{}
	}
	return *syncPoint
}
