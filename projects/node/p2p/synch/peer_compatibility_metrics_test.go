package synch

import (
	"testing"
	"time"

	"github.com/BlockdagNetworkLabs/bdag/core/protocol"
	"github.com/BlockdagNetworkLabs/bdag/p2p/peers"
	"github.com/BlockdagNetworkLabs/bdag/params"
	ethcommon "github.com/ethereum/go-ethereum/common"
)

func TestPeerCompatibilityMetricCountsFromSnapshots(t *testing.T) {
	counts := peerCompatibilityMetricCountsFromSnapshots(testCompatibilityMetricSnapshots(time.Now()))

	if counts.Total != 7 || counts.Compatible != 1 || counts.Unknown != 1 ||
		counts.Stale != 1 || counts.WrongFork != 1 || counts.WrongDataset != 2 ||
		counts.Unsafe != 1 {
		t.Fatalf("counts = %+v, want current fork and dataset compatibility buckets", counts)
	}
}

func TestPeerUsefulnessMetricCountsFromSnapshots(t *testing.T) {
	now := time.Date(2026, 7, 9, 12, 0, 0, 0, time.UTC)
	counts := peerUsefulnessMetricCountsFromSnapshotsAt(testCompatibilityMetricSnapshots(now), now)

	if counts.Total != 7 || counts.UsefulForTarget != 1 || counts.SyncMiningPreferred != 1 ||
		counts.SyncMiningUsable != 0 || counts.StaleProofUsable != 0 ||
		counts.DiscoveryRelayOnly != 1 || counts.Deprioritized != 1 ||
		counts.Avoid != 4 || counts.Unknown != 0 {
		t.Fatalf("counts = %+v, want one useful peer and source-derived policy buckets", counts)
	}
}

func TestPeerUsefulnessMetricCountsDowngradeStaleCompatibilityProof(t *testing.T) {
	now := time.Date(2026, 7, 9, 12, 0, 0, 0, time.UTC)
	snapshots := []*peers.StatsSnap{
		testCompatibilityPeer(&peers.PeerForkObservation{
			Status:              peers.PeerForkStatusSameMainChain,
			SameNativeForkKnown: true,
			SameNativeFork:      true,
			CommitmentStatus:    "checkpoint_valid",
			EVMCheckpointNumber: 100,
			MainOrder:           2000,
			LocalMainOrder:      2000,
			LastChecked:         now.Add(-30 * time.Second),
		}),
		testCompatibilityPeer(&peers.PeerForkObservation{
			Status:              peers.PeerForkStatusSameMainChain,
			SameNativeForkKnown: true,
			SameNativeFork:      true,
			CommitmentStatus:    "checkpoint_valid",
			EVMCheckpointNumber: 100,
			MainOrder:           2000,
			LocalMainOrder:      2000,
			LastChecked:         now.Add(-10 * time.Minute),
		}),
	}

	counts := peerUsefulnessMetricCountsFromSnapshotsAt(snapshots, now)
	if counts.Total != 2 || counts.UsefulForTarget != 1 ||
		counts.SyncMiningPreferred != 1 || counts.SyncMiningUsable != 1 ||
		counts.StaleProofUsable != 1 {
		t.Fatalf("counts = %+v, want only the fresh proof to satisfy the target", counts)
	}
}

func TestPeerCompatibilityProofWithoutTimestampIsNotFresh(t *testing.T) {
	if peers.AssessPeerCompatibility(&peers.PeerForkObservation{}, time.Now()).ProofFresh {
		t.Fatal("proof without LastChecked timestamp must not be fresh")
	}
}

func testCompatibilityMetricSnapshots(now time.Time) []*peers.StatsSnap {
	rootA := ethcommon.HexToHash("0x1111111111111111111111111111111111111111111111111111111111111111")
	rootB := ethcommon.HexToHash("0x2222222222222222222222222222222222222222222222222222222222222222")

	return []*peers.StatsSnap{
		testCompatibilityPeer(&peers.PeerForkObservation{
			Status:              peers.PeerForkStatusSameMainChain,
			SameNativeForkKnown: true,
			SameNativeFork:      true,
			CommitmentStatus:    "checkpoint_valid",
			EVMCheckpointNumber: 100,
			MainOrder:           2000,
			LocalMainOrder:      2000,
			LastChecked:         now.Add(-30 * time.Second),
		}),
		testCompatibilityPeer(nil),
		testCompatibilityPeer(&peers.PeerForkObservation{
			Status:              peers.PeerForkStatusSameMainChain,
			SameNativeForkKnown: true,
			SameNativeFork:      true,
			CommitmentStatus:    "checkpoint_valid",
			EVMCheckpointNumber: 100,
			MainOrder:           1800,
			LocalMainOrder:      2000,
			LastChecked:         now.Add(-30 * time.Second),
		}),
		testCompatibilityPeer(&peers.PeerForkObservation{
			Status:              peers.PeerForkStatusSameMainChain,
			SameNativeForkKnown: true,
			SameNativeFork:      false,
			CommitmentStatus:    "checkpoint_valid",
			EVMCheckpointNumber: 100,
		}),
		testCompatibilityPeer(&peers.PeerForkObservation{
			Status:              peers.PeerForkStatusSameMainChain,
			SameNativeForkKnown: true,
			SameNativeFork:      true,
			CommitmentStatus:    "checkpoint_mismatch",
			EVMCheckpointNumber: 101,
		}),
		testCompatibilityPeer(&peers.PeerForkObservation{
			Status:               peers.PeerForkStatusSameMainChain,
			SameNativeForkKnown:  true,
			SameNativeFork:       true,
			SampledStateRootGot:  rootA,
			SampledStateRootWant: rootB,
		}),
		testCompatibilityPeer(&peers.PeerForkObservation{
			Status:           peers.PeerForkStatusSameMainChain,
			CommitmentStatus: "invalid_checkpoint",
		}),
		{
			State:    false,
			Network:  params.ActiveNetParams.Name,
			Services: protocol.Full,
			Fork:     &peers.PeerForkObservation{Status: peers.PeerForkStatusSameMainChain},
		},
	}
}

func testCompatibilityPeer(fork *peers.PeerForkObservation) *peers.StatsSnap {
	return &peers.StatsSnap{
		State:    true,
		Network:  params.ActiveNetParams.Name,
		Services: protocol.Full,
		Fork:     fork,
	}
}
