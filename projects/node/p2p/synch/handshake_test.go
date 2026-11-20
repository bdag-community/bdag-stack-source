package synch

import (
	"context"
	"testing"
	"time"

	"github.com/BlockdagNetworkLabs/bdag/consensus/model"
	"github.com/BlockdagNetworkLabs/bdag/core/blockchain"
	p2pcommon "github.com/BlockdagNetworkLabs/bdag/p2p/common"
	"github.com/BlockdagNetworkLabs/bdag/p2p/encoder"
	"github.com/BlockdagNetworkLabs/bdag/p2p/peers"
	pb "github.com/BlockdagNetworkLabs/bdag/p2p/proto/v1"
	"github.com/BlockdagNetworkLabs/bdag/p2p/qnode"
	ethcommon "github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/txpool/legacypool"
	"github.com/libp2p/go-libp2p"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
)

type testP2P struct {
	ctx     context.Context
	host    host.Host
	cfg     *p2pcommon.Config
	peers   *peers.Status
	genesis ethcommon.Hash
}

func newTestP2P(ctx context.Context, h host.Host, cfg *p2pcommon.Config) *testP2P {
	p := &testP2P{
		ctx:  ctx,
		host: h,
		cfg:  cfg,
	}
	p.peers = peers.NewStatus(p)
	return p
}

func (p *testP2P) GetGenesisHash() *ethcommon.Hash               { return &p.genesis }
func (p *testP2P) BlockChain() *blockchain.BlockChain            { return nil }
func (p *testP2P) Host() host.Host                               { return p.host }
func (p *testP2P) Disconnect(pid peer.ID) error                  { return p.host.Network().ClosePeer(pid) }
func (p *testP2P) Context() context.Context                      { return p.ctx }
func (p *testP2P) Encoding() encoder.NetworkEncoding             { return &encoder.SszNetworkEncoder{} }
func (p *testP2P) Config() *p2pcommon.Config                     { return p.cfg }
func (p *testP2P) TxMemPool() *legacypool.LegacyPool             { return nil }
func (p *testP2P) Metadata() *pb.MetaData                        { return &pb.MetaData{} }
func (p *testP2P) MetadataSeq() uint64                           { return 0 }
func (p *testP2P) TimeSource() model.MedianTimeSource            { return nil }
func (p *testP2P) Notify() model.Notify                          { return nil }
func (p *testP2P) ConnectTo(node *qnode.Node)                    {}
func (p *testP2P) Resolve(n *qnode.Node) *qnode.Node             { return nil }
func (p *testP2P) Node() *qnode.Node                             { return nil }
func (p *testP2P) RelayNodeInfo() *peer.AddrInfo                 { return nil }
func (p *testP2P) IncreaseBytesSent(pid peer.ID, size int)       {}
func (p *testP2P) IncreaseBytesRecv(pid peer.ID, size int)       {}
func (p *testP2P) ConnectToPeer(qmaddr string, force bool) error { return nil }
func (p *testP2P) RegainMempool()                                {}
func (p *testP2P) IsCurrent() bool                               { return false }
func (p *testP2P) Peers() *peers.Status                          { return p.peers }
func (p *testP2P) IsRunning() bool                               { return true }
func (p *testP2P) Consensus() model.Consensus                    { return nil }

func TestDisconnectedNotificationMarksPeerDisconnected(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	local := newTestHost(t)
	defer local.Close()
	remote := newTestHost(t)
	defer remote.Close()

	sy := NewSync(newTestP2P(ctx, local, &p2pcommon.Config{MaxPeers: 10, MaxInbound: 10}))
	pe := sy.Peers().Fetch(remote.ID())
	pe.SetConnectionState(peers.PeerConnected)

	sy.peerSync.Disconnected(remote.ID(), nil)

	waitFor(t, func() bool {
		return pe.ConnectionState().IsDisconnected()
	})
}

func TestIsPeerAtLimitCountsLibp2pConnectedPeers(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	local := newTestHost(t)
	defer local.Close()
	remote := newTestHost(t)
	defer remote.Close()

	sy := NewSync(newTestP2P(ctx, local, &p2pcommon.Config{MaxPeers: 1, MaxInbound: 10}))
	connectHosts(t, remote, local)

	if !sy.IsPeerAtLimit() {
		t.Fatal("expected peer limit to include libp2p connected peers")
	}
}

func TestIsInboundPeerAtLimitCountsLibp2pInboundConnections(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	local := newTestHost(t)
	defer local.Close()
	remote := newTestHost(t)
	defer remote.Close()

	sy := NewSync(newTestP2P(ctx, local, &p2pcommon.Config{MaxPeers: 10, MaxInbound: 1}))
	connectHosts(t, remote, local)

	if !sy.IsInboundPeerAtLimit() {
		t.Fatal("expected inbound peer limit to include libp2p inbound connections")
	}
}

func newTestHost(t *testing.T) host.Host {
	t.Helper()
	h, err := libp2p.New(libp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/0"))
	if err != nil {
		t.Fatalf("new libp2p host: %v", err)
	}
	return h
}

func connectHosts(t *testing.T, dialer host.Host, listener host.Host) {
	t.Helper()
	err := dialer.Connect(context.Background(), peer.AddrInfo{
		ID:    listener.ID(),
		Addrs: listener.Addrs(),
	})
	if err != nil {
		t.Fatalf("connect hosts: %v", err)
	}
	waitFor(t, func() bool {
		return listener.Network().Connectedness(dialer.ID()) == network.Connected
	})
}

func waitFor(t *testing.T, f func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if f() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("condition was not met")
}
