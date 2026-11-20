package peers

import (
	"context"

	"github.com/BlockdagNetworkLabs/bdag/consensus/model"
	"github.com/BlockdagNetworkLabs/bdag/core/blockchain"
	"github.com/BlockdagNetworkLabs/bdag/p2p/common"
	"github.com/BlockdagNetworkLabs/bdag/p2p/encoder"
	pb "github.com/BlockdagNetworkLabs/bdag/p2p/proto/v1"
	"github.com/BlockdagNetworkLabs/bdag/p2p/qnode"
	ethcommon "github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/txpool/legacypool"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/peer"
)

type P2P interface {
	GetGenesisHash() *ethcommon.Hash
	BlockChain() *blockchain.BlockChain
	Host() host.Host
	Disconnect(pid peer.ID) error
	Context() context.Context
	Encoding() encoder.NetworkEncoding
	Config() *common.Config
	TxMemPool() *legacypool.LegacyPool
	Metadata() *pb.MetaData
	MetadataSeq() uint64
	TimeSource() model.MedianTimeSource
	Notify() model.Notify
	ConnectTo(node *qnode.Node)
	Resolve(n *qnode.Node) *qnode.Node
	Node() *qnode.Node
	RelayNodeInfo() *peer.AddrInfo
	IncreaseBytesSent(pid peer.ID, size int)
	IncreaseBytesRecv(pid peer.ID, size int)
	ConnectToPeer(qmaddr string, force bool) error
	RegainMempool()
	IsCurrent() bool
	Peers() *Status
	IsRunning() bool
	Consensus() model.Consensus
}

type P2PRPC interface {
	Host() host.Host
	Context() context.Context
	Encoding() encoder.NetworkEncoding
	Disconnect(pid peer.ID) error
	IncreaseBytesSent(pid peer.ID, size int)
	IncreaseBytesRecv(pid peer.ID, size int)
	Peers() *Status
	IsRunning() bool
	GetGenesisHash() *ethcommon.Hash
}
