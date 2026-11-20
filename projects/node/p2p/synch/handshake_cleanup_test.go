package synch

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/BlockdagNetworkLabs/bdag/core/blockchain"
	bdagprotocol "github.com/BlockdagNetworkLabs/bdag/core/protocol"
	"github.com/BlockdagNetworkLabs/bdag/p2p/common"
	"github.com/BlockdagNetworkLabs/bdag/p2p/peers"
	pb "github.com/BlockdagNetworkLabs/bdag/p2p/proto/v1"
	"github.com/BlockdagNetworkLabs/bdag/params"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
)

type cleanupTestP2P struct {
	*testP2P
	chain blockchain.BlockChain
}

func (p *cleanupTestP2P) BlockChain() *blockchain.BlockChain { return &p.chain }

func newCleanupTestP2P(ctx context.Context, h host.Host) *cleanupTestP2P {
	return &cleanupTestP2P{testP2P: newTestP2P(ctx, h, &common.Config{
		MaxPeers: 10, MaxInbound: 10,
		Params: &params.Params{P2PProtocol46ActivationLayer: ^uint64(0)},
	})}
}

// Keep real loopback connections and streams, but allow the test to age their
// immutable connection timestamps without waiting for the production deadline.
type agingHost struct {
	host.Host
	age atomic.Int64
}

func (h *agingHost) Network() network.Network {
	return agingNetwork{Network: h.Host.Network(), age: &h.age}
}

type agingNetwork struct {
	network.Network
	age *atomic.Int64
}

func (n agingNetwork) ConnsToPeer(id peer.ID) []network.Conn {
	conns := n.Network.ConnsToPeer(id)
	for i, conn := range conns {
		conns[i] = agingConn{Conn: conn, age: time.Duration(n.age.Load())}
	}
	return conns
}

type agingConn struct {
	network.Conn
	age time.Duration
}

func (c agingConn) Stat() network.ConnStats {
	stat := c.Conn.Stat()
	stat.Opened = stat.Opened.Add(-c.age)
	return stat
}

func TestCleanupPreservesDelayedChainStateHandshake(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	local := &agingHost{Host: newTestHost(t)}
	defer local.Close()
	remote := newTestHost(t)
	defer remote.Close()
	maintenanceCtx, stopMaintenance := context.WithCancel(ctx)
	defer stopMaintenance()
	clientRPC := newCleanupTestP2P(maintenanceCtx, local)
	serverRPC := newTestP2P(ctx, remote, &common.Config{MaxPeers: 10, MaxInbound: 10})
	client := NewSync(clientRPC)
	server := NewSync(serverRPC)
	received := make(chan struct{})
	release := make(chan struct{})
	server.registerRPC(RPCChainState, &pb.ChainState{}, func(ctx context.Context, msg any, stream network.Stream, pe *peers.Peer) *common.Error {
		close(received)
		select {
		case <-release:
		case <-ctx.Done():
			return common.NewError(common.ErrGeneric, ctx.Err())
		}
		state := msg.(*pb.ChainState)
		if err := server.validateChainStateMessage(state, pe); err != nil {
			return err
		}
		return EncodeResponseMsg(serverRPC, stream, state, common.ErrNone)
	})
	connectHosts(t, local, remote)
	pe := client.Peers().Fetch(remote.ID())
	pe.UpdateAddrDir(nil, remote.Addrs()[0], network.DirOutbound)
	request := &pb.ChainState{
		GenesisHash:     &pb.Hash{Hash: clientRPC.genesis.Bytes()},
		ProtocolVersion: bdagprotocol.ProtocolVersion,
		Services:        uint64(bdagprotocol.Full),
		GraphState:      &pb.GraphState{Total: 1},
		EvmCheckpoint:   &pb.EVMCheckpoint{Hash: &pb.Hash{Hash: make([]byte, 32)}, StateRoot: &pb.Hash{Hash: make([]byte, 32)}},
	}
	// Match the handshake's lock scope; use the real wire encoder and validator.
	pe.HSlock.Lock()
	stream, err := Send(ctx, clientRPC, request, RPCChainState, pe, false)
	if err != nil && !err.Code.IsSuccess() {
		pe.HSlock.Unlock()
		t.Fatalf("send chain state: %v", err)
	}
	defer stream.Close()
	select {
	case <-received:
	case <-time.After(2 * time.Second):
		pe.HSlock.Unlock()
		t.Fatal("server did not receive chain-state request")
	}
	client.PeerInterval = 10 * time.Millisecond
	client.maintainPeerStatuses()
	time.Sleep(100 * time.Millisecond) // several real maintenance passes
	connected := local.Network().Connectedness(remote.ID()) == network.Connected
	// This fixture has no ledger. Stop maintenance before completing the
	// handshake so it cannot start unrelated periodic ledger revalidation.
	stopMaintenance()
	close(release)
	if !connected {
		pe.HSlock.Unlock()
		t.Fatal("peer cleanup closed a transport during its in-progress chain-state handshake")
	}
	responseErr := client.sendChainStateRequest(stream, pe)
	pe.HSlock.Unlock()
	if responseErr != nil && !responseErr.Code.IsSuccess() {
		t.Fatalf("delayed valid chain state failed: %v", responseErr)
	}
	waitFor(t, pe.IsConnected)
	cancel()
}

func TestCleanupStillClosesExpiredUnvalidatedTransport(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	local := &agingHost{Host: newTestHost(t)}
	defer local.Close()
	remote := newTestHost(t)
	defer remote.Close()
	sy := NewSync(newCleanupTestP2P(ctx, local))
	connectHosts(t, remote, local)
	pe := sy.Peers().Fetch(remote.ID())
	pe.UpdateAddrDir(nil, remote.Addrs()[0], network.DirInbound)
	local.age.Store(int64(ReqTimeout + RespTimeout + time.Second))
	// An abandoned handshake lock must not reserve a connection forever.
	pe.HSlock.Lock()
	defer pe.HSlock.Unlock()
	sy.PeerInterval = 10 * time.Millisecond
	sy.maintainPeerStatuses()
	waitFor(t, func() bool { return local.Network().Connectedness(remote.ID()) != network.Connected })
	if pe.IsConnected() || pe.IsActive() {
		t.Fatal("unvalidated transport was admitted")
	}
}

func TestCleanupPreservesYoungInboundWithoutAdmittingIt(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	local := &agingHost{Host: newTestHost(t)}
	defer local.Close()
	remote := newTestHost(t)
	defer remote.Close()
	sy := NewSync(newCleanupTestP2P(ctx, local))
	connectHosts(t, remote, local)
	pe := sy.Peers().Fetch(remote.ID())
	pe.UpdateAddrDir(nil, remote.Addrs()[0], network.DirInbound)
	sy.PeerInterval = 10 * time.Millisecond
	sy.maintainPeerStatuses()
	time.Sleep(100 * time.Millisecond)
	if local.Network().Connectedness(remote.ID()) != network.Connected {
		t.Fatal("cleanup closed inbound transport before it could negotiate chain state")
	}
	if pe.IsConnected() || pe.IsActive() {
		t.Fatal("handshake grace must not grant admission")
	}
	local.age.Store(int64(ReqTimeout + RespTimeout + time.Second))
	waitFor(t, func() bool { return local.Network().Connectedness(remote.ID()) != network.Connected })
}

func TestCleanupHandshakeDeadlineBoundary(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	local := newTestHost(t)
	defer local.Close()
	remote := newTestHost(t)
	defer remote.Close()
	sy := NewSync(newCleanupTestP2P(ctx, local))
	connectHosts(t, remote, local)
	pe := sy.Peers().Fetch(remote.ID())
	deadline := local.Network().ConnsToPeer(remote.ID())[0].Stat().Opened.Add(ReqTimeout + RespTimeout)
	if sy.expireUnvalidatedConnections(pe, deadline.Add(-time.Nanosecond)) {
		t.Fatal("handshake expired before its deadline")
	}
	if local.Network().Connectedness(remote.ID()) != network.Connected {
		t.Fatal("transport closed before its deadline")
	}
	sy.expireUnvalidatedConnections(pe, deadline)
	waitFor(t, func() bool { return local.Network().Connectedness(remote.ID()) != network.Connected })
}

func TestCleanupRechecksValidatedPeerFromStaleSnapshot(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	local := newTestHost(t)
	defer local.Close()
	remote := newTestHost(t)
	defer remote.Close()
	sy := NewSync(newCleanupTestP2P(ctx, local))
	connectHosts(t, remote, local)
	pe := sy.Peers().Fetch(remote.ID())
	// Maintenance may have fetched this peer while it was still disconnected.
	pe.SetConnectionState(peers.PeerConnected)
	if sy.expireUnvalidatedConnections(pe, time.Now().Add(time.Hour)) {
		t.Fatal("stale cleanup snapshot treated a validated peer as expired")
	}
	if local.Network().Connectedness(remote.ID()) != network.Connected {
		t.Fatal("stale cleanup snapshot closed the validated peer")
	}
}

func TestCleanupBadPeerDoesNotReceiveHandshakeGrace(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	local := newTestHost(t)
	defer local.Close()
	remote := newTestHost(t)
	defer remote.Close()
	sy := NewSync(newCleanupTestP2P(ctx, local))
	connectHosts(t, remote, local)
	pe := sy.Peers().Fetch(remote.ID())
	pe.IncrementBadResponses(common.NewErrorStr(common.ErrDAGConsensus, "invalid genesis"))
	sy.expireUnvalidatedConnections(pe, time.Now())
	waitFor(t, func() bool { return local.Network().Connectedness(remote.ID()) != network.Connected })
	if pe.IsConnected() || pe.IsActive() {
		t.Fatal("bad peer received admission during grace")
	}
}
