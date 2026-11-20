package blockchain

import "github.com/libp2p/go-libp2p/core/peer"

type PeerReorgDebugStatus struct {
	ProtocolVersion     uint32
	CommitmentStatus    string
	ForkStatus          string
	ForkConfidence      string
	ForkReason          string
	Address             string
	PeerMainOrder       uint64
	LocalMainOrder      uint64
	EVMCheckpointNumber uint64
	EVMCheckpointHash   string
	EVMCheckpointRoot   string
}

func (b *BlockChain) SetPeerReorgDebugStatus(pid peer.ID, status PeerReorgDebugStatus) {
	if b == nil || pid == "" {
		return
	}
	b.peerReorgStatuses.Store(pid, status)
}

func (b *BlockChain) peerReorgDebugStatus(source *peer.ID) (PeerReorgDebugStatus, bool) {
	if b == nil || source == nil || *source == "" {
		return PeerReorgDebugStatus{}, false
	}
	value, ok := b.peerReorgStatuses.Load(*source)
	if !ok {
		return PeerReorgDebugStatus{}, false
	}
	status, ok := value.(PeerReorgDebugStatus)
	return status, ok
}
