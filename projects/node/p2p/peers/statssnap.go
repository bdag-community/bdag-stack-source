package peers

import (
	"time"

	blockdag "github.com/BlockdagNetworkLabs/bdag/blockdag"
	"github.com/BlockdagNetworkLabs/bdag/core/protocol"
	pb "github.com/BlockdagNetworkLabs/bdag/p2p/proto/v1"
	"github.com/BlockdagNetworkLabs/bdag/params"
	"github.com/ethereum/go-ethereum/common"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
)

// StatsSnap is a snapshot of peer stats at a point in time.
type StatsSnap struct {
	NodeID         string
	PeerID         peer.ID
	QNR            string
	Address        string
	Protocol       uint32
	Genesis        *common.Hash
	Services       protocol.ServiceFlag
	Name           string
	Version        string
	Network        string
	State          bool
	Direction      network.Direction
	GraphState     *blockdag.GraphState
	GraphStateDur  time.Duration
	TimeOffset     int64
	ConnTime       time.Duration
	LastSend       time.Time
	LastRecv       time.Time
	BytesSent      uint64
	BytesRecv      uint64
	IsCircuit      bool
	Bads           []string
	ReConnect      uint64
	StateRoot      string
	MempoolReqTime time.Time
	Tasks          int
	Broadcast      int
	DAGPort        int
	EVMPort        int
	Fork           *PeerForkObservation
	// Copied under the same peer lock as GraphState; current verification
	// must not combine a new tip with an older checkpoint or sync point.
	EVMCheckpoint *pb.EVMCheckpoint
	SyncPoint     common.Hash
}

func (p *StatsSnap) IsRelay() bool {
	return protocol.HasServices(protocol.ServiceFlag(p.Services), protocol.Relay)
}

func (p *StatsSnap) IsTheSameNetwork() bool {
	return params.ActiveNetParams.Name == p.Network
}
