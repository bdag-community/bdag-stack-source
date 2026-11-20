package p2p

import (
	"sync"
	"testing"
	"time"

	"github.com/BlockdagNetworkLabs/bdag/config"
	"github.com/BlockdagNetworkLabs/bdag/consensus/model"
	"github.com/BlockdagNetworkLabs/bdag/core/event"
	corejson "github.com/BlockdagNetworkLabs/bdag/core/json"
	"github.com/BlockdagNetworkLabs/bdag/core/protocol"
	p2ppeers "github.com/BlockdagNetworkLabs/bdag/p2p/peers"
	pb "github.com/BlockdagNetworkLabs/bdag/p2p/proto/v1"
	"github.com/BlockdagNetworkLabs/bdag/p2p/synch"
	"github.com/BlockdagNetworkLabs/bdag/params"
	ethcommon "github.com/ethereum/go-ethereum/common"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/peerstore"
	"github.com/libp2p/go-libp2p/p2p/host/peerstore/pstoremem"
	ma "github.com/multiformats/go-multiaddr"
)

type peerAPITestConsensus struct {
	model.Consensus
	events event.Feed
}

func (c *peerAPITestConsensus) Config() *config.Config { return &config.Config{} }

func (c *peerAPITestConsensus) Events() *event.Feed { return &c.events }

func (c *peerAPITestConsensus) GenesisHash() *ethcommon.Hash { return nil }

type peerAPITestContext struct {
	*Service
	genesis ethcommon.Hash
}

func (c *peerAPITestContext) GetGenesisHash() *ethcommon.Hash {
	genesis := c.genesis
	return &genesis
}

type mutatingPeerstore struct {
	peerstore.Peerstore
	onLatency func()
	once      sync.Once
}

func (p *mutatingPeerstore) LatencyEWMA(id peer.ID) time.Duration {
	latency := p.Peerstore.LatencyEWMA(id)
	p.once.Do(p.onLatency)
	return latency
}

type peerstoreHost struct {
	host.Host
	store peerstore.Peerstore
}

func (h *peerstoreHost) Peerstore() peerstore.Peerstore { return h.store }

func newPeerAPIService(t *testing.T) (*Service, *p2ppeers.Peer) {
	t.Helper()
	cfg := &config.Config{DataDir: t.TempDir(), DisableListen: true, NodeName: "peer-api-test"}
	service, err := NewService(cfg, &peerAPITestConsensus{}, params.ActiveNetParams.Params)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	baseHost := service.host
	t.Cleanup(func() { _ = baseHost.Close() })
	store, err := pstoremem.NewPeerstore()
	if err != nil {
		t.Fatalf("NewPeerstore: %v", err)
	}
	service.host = &peerstoreHost{Host: baseHost, store: store}
	t.Cleanup(func() { _ = store.Close() })
	if params.ActiveNetParams.GenesisHash == nil {
		t.Fatal("fixture network has no genesis")
	}
	// Keep the actual peer store/sync/API path without starting a chain or
	// synchronization loop merely to supply the peer's genesis identity.
	service.sy = synch.NewSync(&peerAPITestContext{Service: service, genesis: *params.ActiveNetParams.GenesisHash})
	if service.PeerSync().IsRunning() {
		t.Fatal("fixture PeerSync must remain unstarted")
	}

	pid := peer.ID("peer-api-fixture")
	pe := service.Peers().Fetch(pid)
	pe.UpdateAddrDir(nil, ma.StringCast("/ip4/127.0.0.1/tcp/30303"), network.DirOutbound)
	pe.SetConnectionState(p2ppeers.PeerConnected)
	pe.SetChainState(&pb.ChainState{
		Services:   uint64(protocol.Full),
		UserAgent:  []byte("peer-api-test|1.0|network"),
		GraphState: &pb.GraphState{},
	})
	return service, pe
}

func installPeerAPISyncPeerMutation(service *Service, store peerstore.Peerstore, clear func()) {
	service.host = &peerstoreHost{Host: service.host, store: &mutatingPeerstore{Peerstore: store, onLatency: clear}}
}

func peerInfoRows(t *testing.T, value any) []*corejson.GetPeerInfoResult {
	t.Helper()
	rows, ok := value.([]*corejson.GetPeerInfoResult)
	if !ok {
		t.Fatalf("GetPeerInfo result type = %T", value)
	}
	return rows
}

func peerForkRows(t *testing.T, value any) []*corejson.PeerForkInfo {
	t.Helper()
	rows, ok := value.([]*corejson.PeerForkInfo)
	if !ok {
		t.Fatalf("GetPeerForks result type = %T", value)
	}
	return rows
}

func TestPeerAPICapturesSyncPeerOncePerResponse(t *testing.T) {
	service, syncPeer := newPeerAPIService(t)
	api := NewPublicP2PAPI(service)
	verbose := true

	service.PeerSync().SetSyncPeer(nil)
	initial, err := api.GetPeerInfo(&verbose, nil)
	if err != nil {
		t.Fatalf("initial GetPeerInfo: %v", err)
	}
	initialRows := peerInfoRows(t, initial)
	if len(initialRows) != 1 || initialRows[0].SyncNode || initialRows[0].Fork == nil || initialRows[0].Fork.SyncNode {
		t.Fatalf("initial nil sync peer rows = %+v, want one non-sync row", initialRows)
	}

	store, err := pstoremem.NewPeerstore()
	if err != nil {
		t.Fatalf("NewPeerstore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	service.PeerSync().SetSyncPeer(syncPeer)
	installPeerAPISyncPeerMutation(service, store, func() { service.PeerSync().SetSyncPeer(nil) })
	value, err := api.GetPeerInfo(&verbose, nil)
	if err != nil {
		t.Fatalf("captured GetPeerInfo: %v", err)
	}
	rows := peerInfoRows(t, value)
	if len(rows) != 1 || !rows[0].SyncNode || rows[0].Fork == nil || !rows[0].Fork.SyncNode {
		t.Fatalf("captured sync peer rows = %+v, want outer and nested sync=true", rows)
	}

	next, err := api.GetPeerInfo(&verbose, nil)
	if err != nil {
		t.Fatalf("cleared GetPeerInfo: %v", err)
	}
	nextRows := peerInfoRows(t, next)
	if len(nextRows) != 1 || nextRows[0].SyncNode || nextRows[0].Fork == nil || nextRows[0].Fork.SyncNode {
		t.Fatalf("cleared sync peer rows = %+v, want outer and nested sync=false", nextRows)
	}

	storeForks, err := pstoremem.NewPeerstore()
	if err != nil {
		t.Fatalf("NewPeerstore forks: %v", err)
	}
	t.Cleanup(func() { _ = storeForks.Close() })
	service.PeerSync().SetSyncPeer(syncPeer)
	installPeerAPISyncPeerMutation(service, storeForks, func() { service.PeerSync().SetSyncPeer(nil) })
	forks, err := api.GetPeerForks(&verbose, nil)
	if err != nil {
		t.Fatalf("captured GetPeerForks: %v", err)
	}
	forkRows := peerForkRows(t, forks)
	if len(forkRows) != 1 || !forkRows[0].SyncNode {
		t.Fatalf("captured GetPeerForks rows = %+v, want sync=true", forkRows)
	}

	// The deterministic transition above proves the response owns one
	// snapshot. This bounded stress supplements it by exercising concurrent
	// peer/nil updates while both public response paths read that snapshot.
	service.PeerSync().SetSyncPeer(syncPeer)
	var stressWG sync.WaitGroup
	for i := 0; i < 2; i++ {
		stressWG.Add(1)
		go func() {
			defer stressWG.Done()
			for j := 0; j < 100; j++ {
				service.PeerSync().SetSyncPeer(syncPeer)
				service.PeerSync().SetSyncPeer(nil)
			}
		}()
	}
	for i := 0; i < 2; i++ {
		stressWG.Add(1)
		go func() {
			defer stressWG.Done()
			for j := 0; j < 100; j++ {
				if _, err := api.GetPeerInfo(&verbose, nil); err != nil {
					t.Errorf("concurrent GetPeerInfo: %v", err)
					return
				}
				if _, err := api.GetPeerForks(&verbose, nil); err != nil {
					t.Errorf("concurrent GetPeerForks: %v", err)
					return
				}
			}
		}()
	}
	stressWG.Wait()
}

func TestPeerAPIDoesNotExportCachedForkVerdictForCurrentGraph(t *testing.T) {
	for _, same := range []bool{false, true} {
		t.Run(map[bool]string{false: "stale_warning", true: "stale_healthy"}[same], func(t *testing.T) {
			service, pe := newPeerAPIService(t)
			oldTip := ethcommon.HexToHash("0xebbe6d928b1eb3c3c98396473d7f464e937e8f9be14dbbbe7ca434f650c10023")
			currentTip := ethcommon.HexToHash("0x0e151e933bf461ac14c50ff429764815e49fe28894dab11082d6d9c8affa585b")
			cached := &p2ppeers.PeerForkObservation{
				MainTip: oldTip, MainOrder: 112801,
				SameNativeForkKnown: true, SameNativeFork: same,
				Status:      p2ppeers.PeerForkStatusDivergentMainChain,
				LastChecked: time.Now(), CommitmentStatus: "checkpoint_valid",
			}
			if same {
				cached.Status = p2ppeers.PeerForkStatusSameMainChain
			}
			pe.SetForkObservation(cached)
			pe.UpdateGraphState(&pb.GraphState{
				MainOrder: 112801, MainHeight: 109587,
				Tips: []*pb.Hash{{Hash: currentTip.Bytes()}},
			})
			api := NewPublicP2PAPI(service)
			verbose := true
			value, err := api.GetPeerInfo(&verbose, nil)
			if err != nil {
				t.Fatal(err)
			}
			info := peerInfoRows(t, value)[0]
			value, err = api.GetPeerForks(&verbose, nil)
			if err != nil {
				t.Fatal(err)
			}
			for _, row := range []*corejson.PeerForkInfo{info.Fork, peerForkRows(t, value)[0]} {
				// This fixture deliberately has no local chain verifier. It must
				// report current graph data, but cannot verify either fork verdict.
				if row.SameNativeFork != nil || row.ForkStatus != "unknown" {
					t.Fatalf("exported cached verdict for a different graph: %+v", row)
				}
				if row.PeerMainTipHash != currentTip.String() || row.PeerMainOrder != 112801 {
					t.Fatalf("fork result does not use current graph snapshot: %+v", row)
				}
				if row.CommitmentStatus == "checkpoint_valid" {
					t.Fatal("cached checkpoint verdict exported without current validation")
				}
			}
			if got := pe.ForkObservation(); got.MainTip != oldTip || got.SameNativeFork != same {
				t.Fatal("API mutated the cached diagnostic evidence")
			}
		})
	}
}

func TestPeerForkInfoIncludesObservedEVMCheckpoint(t *testing.T) {
	hash := ethcommon.HexToHash("0x1111111111111111111111111111111111111111111111111111111111111111")
	root := ethcommon.HexToHash("0x2222222222222222222222222222222222222222222222222222222222222222")

	info := peerForkInfoFromStats(nil, &p2ppeers.StatsSnap{
		PeerID:   peer.ID("v46-peer"),
		Protocol: protocol.EVMCheckpointProtocolVersion,
		Services: protocol.Full,
		Fork: &p2ppeers.PeerForkObservation{
			CommitmentStatus:    "checkpoint_valid",
			EVMCheckpointNumber: 123,
			EVMCheckpointHash:   hash,
			EVMCheckpointRoot:   root,
		},
	}, true, "", false)

	if info.EVMCheckpointNumber != 123 {
		t.Fatalf("checkpoint number = %d, want 123", info.EVMCheckpointNumber)
	}
	if info.EVMCheckpointHash != hash.String() {
		t.Fatalf("checkpoint hash = %q, want %q", info.EVMCheckpointHash, hash.String())
	}
	if info.EVMCheckpointRoot != root.String() {
		t.Fatalf("checkpoint root = %q, want %q", info.EVMCheckpointRoot, root.String())
	}
	if info.CommitmentStatus != "checkpoint_valid" {
		t.Fatalf("commitment status = %q, want checkpoint_valid", info.CommitmentStatus)
	}
}

func TestPeerForkInfoOmitsEVMCheckpointWhenPeerDidNotProvideOne(t *testing.T) {
	info := peerForkInfoFromStats(nil, &p2ppeers.StatsSnap{
		PeerID:   peer.ID("legacy-peer"),
		Protocol: protocol.BroadcastblockProtocolVersion,
		Services: protocol.Full,
		Fork: &p2ppeers.PeerForkObservation{
			CommitmentStatus: "legacy_v45",
		},
	}, true, "", false)

	if info.EVMCheckpointNumber != 0 || info.EVMCheckpointHash != "" || info.EVMCheckpointRoot != "" {
		t.Fatalf("checkpoint fields = (%d, %q, %q), want empty", info.EVMCheckpointNumber, info.EVMCheckpointHash, info.EVMCheckpointRoot)
	}
	if info.CommitmentStatus != "legacy_v45" {
		t.Fatalf("commitment status = %q, want legacy_v45", info.CommitmentStatus)
	}
}

func TestPeerForkInfoIncludesCompatibilityAssessment(t *testing.T) {
	checkedAt := time.Now().Add(-30 * time.Second)
	info := peerForkInfoFromStats(nil, &p2ppeers.StatsSnap{
		PeerID: peer.ID("compatible-peer"),
		Fork: &p2ppeers.PeerForkObservation{
			Status:              p2ppeers.PeerForkStatusSameMainChain,
			SameNativeForkKnown: true,
			SameNativeFork:      true,
			CommitmentStatus:    "checkpoint_valid",
			LastChecked:         checkedAt,
		},
	}, true, "", false)

	if info.PeerCompatibility != "compatible" || info.DatasetStatus != "checkpoint_valid" ||
		info.DatasetProof != "evm_checkpoint" || info.PeerUsefulness != "sync-mining-preferred" ||
		!info.CompatibilityProofFresh || !info.UsefulForTarget {
		t.Fatalf("compatibility assessment = %+v, want fresh compatible preferred peer", info)
	}
	if info.CompatibilityReason == "" || info.DatasetReason == "" || info.PeerUsefulnessReason == "" {
		t.Fatalf("compatibility reasons = %+v, want non-empty evidence explanations", info)
	}
	if info.CompatibilityProofAge < 0 || info.CompatibilityProofAge > 120 {
		t.Fatalf("compatibility proof age = %d, want fresh bounded age", info.CompatibilityProofAge)
	}

	outer := &corejson.GetPeerInfoResult{}
	applyPeerForkInfo(outer, info)
	if outer.PeerCompatibility != info.PeerCompatibility || outer.DatasetStatus != info.DatasetStatus ||
		outer.DatasetProof != info.DatasetProof || outer.PeerUsefulness != info.PeerUsefulness ||
		outer.CompatibilityProofFresh != info.CompatibilityProofFresh || outer.UsefulForTarget != info.UsefulForTarget {
		t.Fatalf("outer compatibility assessment = %+v, want nested assessment mirrored", outer)
	}
}

func TestPeerForkInfoDoesNotTreatMissingTimestampAsFresh(t *testing.T) {
	info := peerForkInfoFromStats(nil, &p2ppeers.StatsSnap{
		PeerID: peer.ID("untimestamped-compatible-peer"),
		Fork: &p2ppeers.PeerForkObservation{
			Status:           p2ppeers.PeerForkStatusSameMainChain,
			CommitmentStatus: "checkpoint_valid",
		},
	}, true, "", false)

	if info.PeerCompatibility != "compatible" || info.CompatibilityProofFresh ||
		info.CompatibilityProofAge != 0 || info.PeerUsefulness != "sync-mining-usable" || info.UsefulForTarget {
		t.Fatalf("untimestamped assessment = %+v, want compatible but non-fresh usable peer", info)
	}
}
