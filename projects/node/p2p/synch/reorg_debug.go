package synch

import (
	"github.com/BlockdagNetworkLabs/bdag/core/blockchain"
	"github.com/BlockdagNetworkLabs/bdag/p2p/peers"
)

func (ps *PeerSync) recordPeerReorgDebugStatus(pe *peers.Peer) {
	if ps == nil || ps.sy == nil || ps.sy.p2p == nil || pe == nil {
		return
	}
	chain := ps.sy.p2p.BlockChain()
	if chain == nil {
		return
	}

	status := blockchain.PeerReorgDebugStatus{
		CommitmentStatus: "unknown",
		ForkStatus:       string(peers.PeerForkStatusUnknown),
	}
	if address := pe.Address(); address != nil {
		status.Address = address.String()
	}

	if chainState := pe.ChainState(); chainState != nil {
		status.ProtocolVersion = chainState.ProtocolVersion
		status.CommitmentStatus = peerCommitmentStatus(ps, chainState)
		if checkpoint := chainState.GetEvmCheckpoint(); checkpoint != nil {
			status.EVMCheckpointNumber = checkpoint.GetNumber()
			if hash, err := evmHashFromPB(checkpoint.GetHash()); err == nil {
				status.EVMCheckpointHash = hash.String()
			}
			if root, err := evmHashFromPB(checkpoint.GetStateRoot()); err == nil {
				status.EVMCheckpointRoot = root.String()
			}
		}
	}

	if observation := pe.ForkObservation(); observation != nil {
		status.ForkStatus = string(observation.Status)
		status.ForkConfidence = observation.Confidence
		status.ForkReason = observation.Reason
		status.PeerMainOrder = observation.MainOrder
		status.LocalMainOrder = observation.LocalMainOrder
		if status.CommitmentStatus == "unknown" && observation.CommitmentStatus != "" {
			status.CommitmentStatus = observation.CommitmentStatus
		}
		if status.EVMCheckpointNumber == 0 {
			status.EVMCheckpointNumber = observation.EVMCheckpointNumber
			if observation.EVMCheckpointNumber != 0 {
				status.EVMCheckpointHash = observation.EVMCheckpointHash.String()
				status.EVMCheckpointRoot = observation.EVMCheckpointRoot.String()
			}
		}
	}

	chain.SetPeerReorgDebugStatus(pe.GetID(), status)
}
