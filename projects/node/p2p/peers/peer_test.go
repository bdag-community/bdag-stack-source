package peers

import (
	"bytes"
	"sync"
	"testing"
	"time"

	"github.com/BlockdagNetworkLabs/bdag/consensus/model"
	"github.com/BlockdagNetworkLabs/bdag/core/protocol"
	pb "github.com/BlockdagNetworkLabs/bdag/p2p/proto/v1"
	"github.com/BlockdagNetworkLabs/bdag/params"
	"github.com/ethereum/go-ethereum/common"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/stretchr/testify/assert"
)

func TestPeerStatsSnapshotOwnsCurrentCheckpointAndSyncPoint(t *testing.T) {
	p := NewPeer(peer.ID("snapshot-peer"), nil)
	hash, root, tip, point := common.HexToHash("0x01"), common.HexToHash("0x02"), common.HexToHash("0x03"), common.HexToHash("0x04")
	state := &pb.ChainState{
		Services: uint64(protocol.Full), ProtocolVersion: protocol.EVMCheckpointProtocolVersion,
		GraphState:    &pb.GraphState{MainOrder: 100, Tips: []*pb.Hash{{Hash: tip.Bytes()}}},
		EvmCheckpoint: &pb.EVMCheckpoint{Number: 95, Hash: &pb.Hash{Hash: hash.Bytes()}, StateRoot: &pb.Hash{Hash: root.Bytes()}},
	}
	p.SetChainState(state)
	p.UpdateSyncPoint(&point)
	first, err := p.StatsSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	if first.EVMCheckpoint == nil || first.SyncPoint != point || first.GraphState.GetMainOrder() != 100 {
		t.Fatalf("incomplete current snapshot: %+v", first)
	}
	first.EVMCheckpoint.Hash.Hash[0] ^= 1
	first.EVMCheckpoint.StateRoot.Hash[0] ^= 1
	first.SyncPoint[0] ^= 1
	first.GraphState.SetMainOrder(999)
	second, err := p.StatsSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(second.EVMCheckpoint.Hash.Hash, hash.Bytes()) || !bytes.Equal(second.EVMCheckpoint.StateRoot.Hash, root.Bytes()) || second.SyncPoint != point || second.GraphState.GetMainOrder() != 100 {
		t.Fatal("snapshot mutation leaked into peer state")
	}

	// Simultaneous replacement must give readers a complete old or new
	// checkpoint and graph, not references later overwritten by another task.
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := uint64(0); i < 200; i++ {
			p.SetChainState(&pb.ChainState{
				Services: uint64(protocol.Full), ProtocolVersion: protocol.EVMCheckpointProtocolVersion,
				GraphState:    &pb.GraphState{MainOrder: uint32(i + 100)},
				EvmCheckpoint: &pb.EVMCheckpoint{Number: i + 95, Hash: &pb.Hash{Hash: hash.Bytes()}, StateRoot: &pb.Hash{Hash: root.Bytes()}},
			})
		}
	}()
	for i := 0; i < 200; i++ {
		ss, err := p.StatsSnapshot()
		if err != nil || ss.EVMCheckpoint == nil || uint64(ss.GraphState.GetMainOrder()) != ss.EVMCheckpoint.Number+5 {
			t.Errorf("inconsistent snapshot: %+v, err=%v", ss, err)
			break
		}
	}
	wg.Wait()
}

func TestSetChainState_UserAgentParsing(t *testing.T) {

	tests := []struct {
		name        string
		userAgent   string
		wantDAGPort int
		wantEVMPort int
	}{
		{
			name:        "Standard UserAgent with Ports",
			userAgent:   "BlockDAG/1.0.0|DAG:18555|EVM:18556",
			wantDAGPort: 18555,
			wantEVMPort: 18556,
		},
		{
			name:        "UserAgent with only DAG Port",
			userAgent:   "BlockDAG/1.0.0|DAG:1234",
			wantDAGPort: 1234,
			wantEVMPort: 0,
		},
		{
			name:        "UserAgent with only EVM Port",
			userAgent:   "BlockDAG/1.0.0|EVM:5678",
			wantDAGPort: 0,
			wantEVMPort: 5678,
		},
		{
			name:        "Legacy UserAgent (No Ports)",
			userAgent:   "BlockDAG/1.0.0",
			wantDAGPort: 0,
			wantEVMPort: 0,
		},
		{
			name:        "Malformed Ports",
			userAgent:   "BlockDAG/1.0.0|DAG:abc|EVM:def",
			wantDAGPort: 0,
			wantEVMPort: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pid, _ := peer.IDFromBytes([]byte("testpeer"))
			p := NewPeer(pid, nil)

			cs := &pb.ChainState{
				UserAgent: []byte(tt.userAgent),
				GraphState: &pb.GraphState{
					MainOrder: 100,
				},
			}
			p.SetChainState(cs)

			assert.Equal(t, tt.wantDAGPort, p.dagPort)
			assert.Equal(t, tt.wantEVMPort, p.evmPort)
		})
	}
}

func TestFreshnessSnapshotReportsConsensusGraphStateAge(t *testing.T) {
	now := time.Unix(1000, 0)
	freshID := peer.ID("freshpeer")
	staleID := peer.ID("stalepeer")
	observerID := peer.ID("observer")

	fresh := NewPeer(freshID, nil)
	fresh.SetConnectionState(PeerConnected)
	fresh.SetChainState(&pb.ChainState{
		Services: uint64(protocol.Full),
		GraphState: &pb.GraphState{
			MainOrder: 100,
		},
	})
	fresh.graphStateTime = now.Add(-time.Second)

	stale := NewPeer(staleID, nil)
	stale.SetConnectionState(PeerConnected)
	stale.SetChainState(&pb.ChainState{
		Services: uint64(protocol.Light),
		GraphState: &pb.GraphState{
			MainOrder: 99,
		},
	})
	stale.graphStateTime = now.Add(-5 * time.Second)

	observer := NewPeer(observerID, nil)
	observer.SetConnectionState(PeerConnected)
	observer.SetChainState(&pb.ChainState{
		Services: uint64(protocol.Observer),
		GraphState: &pb.GraphState{
			MainOrder: 101,
		},
	})
	observer.graphStateTime = now.Add(-30 * time.Second)

	disconnectedID := peer.ID("disconnected")
	disconnected := NewPeer(disconnectedID, nil)
	disconnected.SetConnectionState(PeerDisconnected)
	disconnected.SetChainState(&pb.ChainState{
		Services: uint64(protocol.Full),
		GraphState: &pb.GraphState{
			MainOrder: 102,
		},
	})
	disconnected.graphStateTime = now.Add(-20 * time.Second)

	status := &Status{peers: map[peer.ID]*Peer{
		freshID:        fresh,
		staleID:        stale,
		observerID:     observer,
		disconnectedID: disconnected,
	}}

	snapshot := status.FreshnessSnapshot(now, 2*time.Second, 10, 98, staleID)
	assert.Equal(t, 2, snapshot.ConsensusPeerCount)
	assert.Equal(t, 1, snapshot.FreshConsensusPeerCount)
	assert.Equal(t, 1, snapshot.StaleConsensusPeerCount)
	assert.Equal(t, uint64(100), snapshot.BestPeerMainOrder)
	assert.Equal(t, int64(2), snapshot.BestPeerLeadBlocks)
	assert.Equal(t, time.Second, snapshot.BestPeerGraphStateAge)
	assert.Equal(t, 3*time.Second, snapshot.AvgGraphStateAge)
	assert.Equal(t, 5*time.Second, snapshot.MaxGraphStateAge)
	assert.True(t, snapshot.SyncPeerPresent)
	assert.True(t, snapshot.SyncPeerObserved)
	assert.False(t, snapshot.SyncPeerFresh)
	assert.Equal(t, 5*time.Second, snapshot.SyncPeerGraphStateAge)
	assert.False(t, snapshot.MiningFresh)
	assert.Equal(t, model.P2PMiningFreshNotEnoughFreshPeers, snapshot.MiningFreshReasonCode)

	noSyncPeerSnapshot := status.FreshnessSnapshot(now, 2*time.Second, 10, 98, "")
	assert.False(t, noSyncPeerSnapshot.SyncPeerPresent)
	assert.False(t, noSyncPeerSnapshot.MiningFresh)
	assert.Equal(t, model.P2PMiningFreshNotEnoughFreshPeers, noSyncPeerSnapshot.MiningFreshReasonCode)
}

func TestFreshnessSnapshotIgnoresUnobservedSyncPeer(t *testing.T) {
	now := time.Unix(1000, 0)
	freshA := peer.ID("fresh-a")
	freshB := peer.ID("fresh-b")
	syncOnly := peer.ID("sync-only")

	a := NewPeer(freshA, nil)
	a.SetConnectionState(PeerConnected)
	a.SetChainState(&pb.ChainState{
		Services:   uint64(protocol.Full),
		GraphState: &pb.GraphState{MainOrder: 100},
	})
	a.graphStateTime = now.Add(-time.Second)

	b := NewPeer(freshB, nil)
	b.SetConnectionState(PeerConnected)
	b.SetChainState(&pb.ChainState{
		Services:   uint64(protocol.Full),
		GraphState: &pb.GraphState{MainOrder: 101},
	})
	b.graphStateTime = now.Add(-time.Second)

	// Sync peer is present in the peer set but has no evaluable graph state yet
	// (handshake incomplete). Previously SyncPeerFresh defaulted to false and
	// poisoned mining freshness with sync_peer_stale.
	syncPeer := NewPeer(syncOnly, nil)
	syncPeer.SetConnectionState(PeerConnected)

	status := &Status{peers: map[peer.ID]*Peer{
		freshA:   a,
		freshB:   b,
		syncOnly: syncPeer,
	}}

	snapshot := status.FreshnessSnapshot(now, 2*time.Second, 10, 100, syncOnly)
	assert.Equal(t, 2, snapshot.ConsensusPeerCount)
	assert.Equal(t, 2, snapshot.FreshConsensusPeerCount)
	assert.True(t, snapshot.SyncPeerPresent)
	assert.False(t, snapshot.SyncPeerObserved)
	assert.False(t, snapshot.SyncPeerFresh)
	assert.True(t, snapshot.MiningFresh)
	assert.Equal(t, model.P2PMiningFreshOK, snapshot.MiningFreshReasonCode)
}

func TestFreshnessSnapshotObservesNonConsensusSyncPeer(t *testing.T) {
	now := time.Unix(1000, 0)
	freshA := peer.ID("fresh-a")
	freshB := peer.ID("fresh-b")
	syncOnly := peer.ID("sync-only")

	a := NewPeer(freshA, nil)
	a.SetConnectionState(PeerConnected)
	a.SetChainState(&pb.ChainState{
		Services:   uint64(protocol.Full),
		GraphState: &pb.GraphState{MainOrder: 100},
	})
	a.graphStateTime = now.Add(-time.Second)

	b := NewPeer(freshB, nil)
	b.SetConnectionState(PeerConnected)
	b.SetChainState(&pb.ChainState{
		Services:   uint64(protocol.Full),
		GraphState: &pb.GraphState{MainOrder: 101},
	})
	b.graphStateTime = now.Add(-time.Second)

	// Services=0 is not consensus, but a fresh graph state on the sync peer
	// must still be observed so tip-chase does not report sync_peer_stale.
	syncPeer := NewPeer(syncOnly, nil)
	syncPeer.SetConnectionState(PeerConnected)
	syncPeer.SetChainState(&pb.ChainState{
		Services:   0,
		GraphState: &pb.GraphState{MainOrder: 101},
	})
	syncPeer.graphStateTime = now.Add(-500 * time.Millisecond)

	status := &Status{peers: map[peer.ID]*Peer{
		freshA:   a,
		freshB:   b,
		syncOnly: syncPeer,
	}}

	snapshot := status.FreshnessSnapshot(now, 2*time.Second, 10, 100, syncOnly)
	assert.Equal(t, 2, snapshot.ConsensusPeerCount)
	assert.True(t, snapshot.SyncPeerPresent)
	assert.True(t, snapshot.SyncPeerObserved)
	assert.True(t, snapshot.SyncPeerFresh)
	assert.True(t, snapshot.MiningFresh)
	assert.Equal(t, model.P2PMiningFreshOK, snapshot.MiningFreshReasonCode)
}

func TestFreshnessSnapshotClampsNegativePeerLead(t *testing.T) {
	now := time.Unix(1000, 0)
	behindA := peer.ID("behind-a")
	behindB := peer.ID("behind-b")

	a := NewPeer(behindA, nil)
	a.SetConnectionState(PeerConnected)
	a.SetChainState(&pb.ChainState{
		Services:   uint64(protocol.Full),
		GraphState: &pb.GraphState{MainOrder: 9_707_901},
	})
	a.graphStateTime = now.Add(-time.Second)

	b := NewPeer(behindB, nil)
	b.SetConnectionState(PeerConnected)
	b.SetChainState(&pb.ChainState{
		Services:   uint64(protocol.Full),
		GraphState: &pb.GraphState{MainOrder: 8_203_910},
	})
	b.graphStateTime = now.Add(-time.Second)

	status := &Status{peers: map[peer.ID]*Peer{
		behindA: a,
		behindB: b,
	}}

	// Local tip far ahead of connected peers (live failure mode: ~ -5.7M).
	snapshot := status.FreshnessSnapshot(now, 2*time.Second, 10, 15_452_973, "")
	assert.Equal(t, uint64(9_707_901), snapshot.BestPeerMainOrder)
	assert.Equal(t, int64(0), snapshot.BestPeerLeadBlocks)
	assert.True(t, snapshot.MiningFresh)
	assert.Equal(t, model.P2PMiningFreshOK, snapshot.MiningFreshReasonCode)
}

func TestFreshnessSnapshotStillFlagsObservedStaleSyncPeer(t *testing.T) {
	now := time.Unix(1000, 0)
	freshA := peer.ID("fresh-a")
	freshB := peer.ID("fresh-b")
	staleSync := peer.ID("stale-sync")

	a := NewPeer(freshA, nil)
	a.SetConnectionState(PeerConnected)
	a.SetChainState(&pb.ChainState{
		Services:   uint64(protocol.Full),
		GraphState: &pb.GraphState{MainOrder: 100},
	})
	a.graphStateTime = now.Add(-time.Second)

	b := NewPeer(freshB, nil)
	b.SetConnectionState(PeerConnected)
	b.SetChainState(&pb.ChainState{
		Services:   uint64(protocol.Full),
		GraphState: &pb.GraphState{MainOrder: 101},
	})
	b.graphStateTime = now.Add(-time.Second)

	syncPeer := NewPeer(staleSync, nil)
	syncPeer.SetConnectionState(PeerConnected)
	syncPeer.SetChainState(&pb.ChainState{
		Services:   uint64(protocol.Full),
		GraphState: &pb.GraphState{MainOrder: 101},
	})
	syncPeer.graphStateTime = now.Add(-5 * time.Second)

	status := &Status{peers: map[peer.ID]*Peer{
		freshA:    a,
		freshB:    b,
		staleSync: syncPeer,
	}}

	snapshot := status.FreshnessSnapshot(now, 2*time.Second, 10, 100, staleSync)
	assert.Equal(t, 3, snapshot.ConsensusPeerCount)
	assert.Equal(t, 2, snapshot.FreshConsensusPeerCount)
	assert.True(t, snapshot.SyncPeerObserved)
	assert.False(t, snapshot.SyncPeerFresh)
	assert.False(t, snapshot.MiningFresh)
	assert.Equal(t, model.P2PMiningFreshSyncPeerStale, snapshot.MiningFreshReasonCode)
}

func TestUpdateGraphStateDoesNotInventEmptyChainState(t *testing.T) {
	p := NewPeer(peer.ID("no-handshake"), nil)
	p.SetConnectionState(PeerConnected)
	p.UpdateGraphState(&pb.GraphState{MainOrder: 101, MainHeight: 100})
	assert.Nil(t, p.ChainState())
	assert.True(t, p.graphStateTime.IsZero())
}

func TestGraphStateConversionBoundsAndCopiesTips(t *testing.T) {
	p := NewPeer(peer.ID("graph-state-boundary"), nil)
	input := make([]*pb.Hash, 100)
	for i := range input {
		input[i] = &pb.Hash{Hash: bytes.Repeat([]byte{byte(i + 1)}, 32)}
	}
	p.chainState = &pb.ChainState{GraphState: &pb.GraphState{Tips: input}}
	got := p.graphState()
	if got == nil || len(got.GetTipsList()) != 100 {
		t.Fatalf("valid graph state = %v, want 100 tips", got)
	}
	input[0].Hash[0] = 0xff
	if got.GetTipsList()[0].Bytes()[0] != 1 {
		t.Fatal("peer graph-state conversion aliased input tip bytes")
	}

	for name, state := range map[string]*pb.GraphState{
		"nil":       nil,
		"oversized": {Tips: append(append([]*pb.Hash(nil), input...), &pb.Hash{Hash: bytes.Repeat([]byte{0x7f}, 32)})},
		"nil-tip":   {Tips: []*pb.Hash{nil}},
		"short":     {Tips: []*pb.Hash{{Hash: make([]byte, 31)}}},
	} {
		t.Run(name, func(t *testing.T) {
			p.chainState = &pb.ChainState{GraphState: state}
			if got := p.graphState(); got != nil {
				t.Fatalf("graphState() = %v, want fail-closed nil", got)
			}
		})
	}
}

func TestSortMiningRelayPeersPrefersFreshConsensusPeers(t *testing.T) {
	now := time.Unix(1000, 0)
	freshNearID := peer.ID("fresh-near")
	staleID := peer.ID("stale")
	freshFarAheadID := peer.ID("fresh-far-ahead")
	unknownID := peer.ID("unknown")

	freshNear := newMiningRelayTestPeer(freshNearID, protocol.Full, PeerConnected, 101, now.Add(-time.Second))
	stale := newMiningRelayTestPeer(staleID, protocol.Full, PeerConnected, 100, now.Add(-8*time.Second))
	freshFarAhead := newMiningRelayTestPeer(freshFarAheadID, protocol.Full, PeerConnected, 120, now.Add(-time.Second))
	unknown := NewPeer(unknownID, nil)
	unknown.SetConnectionState(PeerConnected)

	candidates := []*Peer{stale, unknown, freshFarAhead, freshNear}
	result := SortMiningRelayPeers(candidates, now, 3*time.Second, 10, 100)

	assert.Equal(t, freshNearID, candidates[0].GetID())
	assert.Equal(t, freshFarAheadID, candidates[1].GetID())
	assert.Equal(t, staleID, candidates[2].GetID())
	assert.Equal(t, unknownID, candidates[3].GetID())
	assert.Equal(t, 1, result.FreshFanout)
	assert.Equal(t, 3, result.StaleFanout)
	assert.True(t, result.Scores[freshNearID].Fresh)
	assert.False(t, result.Scores[freshNearID].LeadTooHigh)
	assert.True(t, result.Scores[freshFarAheadID].LeadTooHigh)
	assert.Equal(t, int64(20), result.Scores[freshFarAheadID].LeadBlocks)
}

func newMiningRelayTestPeer(id peer.ID, services protocol.ServiceFlag, state PeerConnectionState, mainOrder uint64, graphStateTime time.Time) *Peer {
	p := NewPeer(id, nil)
	p.SetConnectionState(state)
	p.SetChainState(&pb.ChainState{
		Services: uint64(services),
		GraphState: &pb.GraphState{
			MainOrder: uint32(mainOrder),
		},
	})
	p.graphStateTime = graphStateTime
	return p
}

func TestPeerStatsSnapshotUsesParsedUserAgentFields(t *testing.T) {
	pid, _ := peer.IDFromBytes([]byte("testpeer"))
	p := NewPeer(pid, nil)

	const (
		name     = "BlockDAG"
		version  = "1.0.0"
		dagPort  = 18555
		evmPort  = 18556
		byteSize = 64
	)
	cs := &pb.ChainState{
		UserAgent: []byte(name + "|" + version + "|" + params.ActiveNetParams.Name + "|DAG:18555|EVM:18556"),
		GraphState: &pb.GraphState{
			MainOrder: 100,
		},
	}
	p.SetChainState(cs)
	p.IncreaseBytesSent(byteSize)
	p.IncreaseBytesRecv(byteSize * 2)
	p.Broadcast("tx", "seen")

	ss, err := p.StatsSnapshot()

	assert.NoError(t, err)
	assert.Equal(t, name, ss.Name)
	assert.Equal(t, version, ss.Version)
	assert.Equal(t, params.ActiveNetParams.Name, ss.Network)
	assert.Equal(t, dagPort, ss.DAGPort)
	assert.Equal(t, evmPort, ss.EVMPort)
	assert.Equal(t, uint64(byteSize), ss.BytesSent)
	assert.Equal(t, uint64(byteSize*2), ss.BytesRecv)
	assert.Equal(t, 1, ss.Broadcast)
	assert.Equal(t, p.GetName(), ss.Name)
	assert.Equal(t, p.GetVersion(), ss.Version)
	assert.Equal(t, p.GetNetwork(), ss.Network)
	assert.True(t, ss.IsTheSameNetwork())
	assert.True(t, p.CanConnectWithNetwork())
}

func TestPeerStatsSnapshotHandlesMissingUserAgent(t *testing.T) {
	pid, _ := peer.IDFromBytes([]byte("testpeer"))
	p := NewPeer(pid, nil)
	reqTime := time.Now().Add(-time.Minute)

	p.SetChainState(&pb.ChainState{
		GraphState: &pb.GraphState{
			MainOrder: 100,
		},
	})
	p.SetMempoolReqTime(reqTime)

	ss, err := p.StatsSnapshot()

	assert.NoError(t, err)
	assert.Equal(t, "", ss.Name)
	assert.Equal(t, "", ss.Version)
	assert.Equal(t, "", ss.Network)
	assert.Equal(t, reqTime, ss.MempoolReqTime)
	assert.Equal(t, "", p.GetName())
	assert.Equal(t, "", p.GetVersion())
	assert.Equal(t, "", p.GetNetwork())
	assert.True(t, p.CanConnectWithNetwork())
}
