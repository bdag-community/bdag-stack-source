package peers

import (
	"testing"
	"time"

	ethcommon "github.com/ethereum/go-ethereum/common"
)

func TestAssessPeerCompatibilityClassifiesCurrentEvidence(t *testing.T) {
	now := time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)
	rootA := ethcommon.HexToHash("0x1111111111111111111111111111111111111111111111111111111111111111")
	rootB := ethcommon.HexToHash("0x2222222222222222222222222222222222222222222222222222222222222222")

	tests := []struct {
		name          string
		observation   *PeerForkObservation
		compatibility string
		dataset       string
		usefulness    string
		useful        bool
	}{
		{name: "unknown", compatibility: "unknown", dataset: "unknown", usefulness: "discovery-relay-only"},
		{
			name: "fresh compatible checkpoint",
			observation: &PeerForkObservation{
				Status:              PeerForkStatusSameMainChain,
				SameNativeForkKnown: true,
				SameNativeFork:      true,
				CommitmentStatus:    "checkpoint_valid",
				LastChecked:         now.Add(-30 * time.Second),
			},
			compatibility: "compatible",
			dataset:       "checkpoint_valid",
			usefulness:    "sync-mining-preferred",
			useful:        true,
		},
		{
			name: "compatible proof without timestamp",
			observation: &PeerForkObservation{
				Status:              PeerForkStatusSameMainChain,
				SameNativeForkKnown: true,
				SameNativeFork:      true,
				CommitmentStatus:    "checkpoint_valid",
			},
			compatibility: "compatible",
			dataset:       "checkpoint_valid",
			usefulness:    "sync-mining-usable",
		},
		{
			name: "stale proof",
			observation: &PeerForkObservation{
				Status:              PeerForkStatusSameMainChain,
				SameNativeForkKnown: true,
				SameNativeFork:      true,
				CommitmentStatus:    "checkpoint_valid",
				LastChecked:         now.Add(-10 * time.Minute),
			},
			compatibility: "compatible",
			dataset:       "checkpoint_valid",
			usefulness:    "sync-mining-usable",
		},
		{
			name: "native fork mismatch",
			observation: &PeerForkObservation{
				Status:              PeerForkStatusSameMainChain,
				SameNativeForkKnown: true,
				SameNativeFork:      false,
				CommitmentStatus:    "checkpoint_valid",
			},
			compatibility: "wrong-fork",
			dataset:       "checkpoint_valid",
			usefulness:    "avoid",
		},
		{
			name: "checkpoint mismatch",
			observation: &PeerForkObservation{
				Status:           PeerForkStatusSameMainChain,
				CommitmentStatus: "checkpoint_mismatch",
			},
			compatibility: "wrong-dataset",
			dataset:       "checkpoint_mismatch",
			usefulness:    "avoid",
		},
		{
			name: "sampled state root mismatch",
			observation: &PeerForkObservation{
				Status:               PeerForkStatusSameMainChain,
				SampledStateRootGot:  rootA,
				SampledStateRootWant: rootB,
			},
			compatibility: "wrong-dataset",
			dataset:       "state_root_mismatch",
			usefulness:    "avoid",
		},
		{
			name: "invalid checkpoint",
			observation: &PeerForkObservation{
				Status:           PeerForkStatusSameMainChain,
				CommitmentStatus: "invalid_checkpoint",
			},
			compatibility: "unsafe",
			dataset:       "invalid_checkpoint",
			usefulness:    "avoid",
		},
		{
			name: "same chain but far behind",
			observation: &PeerForkObservation{
				Status:              PeerForkStatusSameMainChain,
				SameNativeForkKnown: true,
				SameNativeFork:      true,
				CommitmentStatus:    "checkpoint_valid",
				MainOrder:           1800,
				LocalMainOrder:      2000,
				LastChecked:         now.Add(-30 * time.Second),
			},
			compatibility: "stale",
			dataset:       "checkpoint_valid",
			usefulness:    "deprioritized",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := AssessPeerCompatibility(test.observation, now)
			if got.Compatibility != test.compatibility || got.DatasetStatus != test.dataset ||
				got.Usefulness != test.usefulness || got.UsefulForTarget != test.useful {
				t.Fatalf("assessment = %+v, want compatibility=%s dataset=%s usefulness=%s useful=%t",
					got, test.compatibility, test.dataset, test.usefulness, test.useful)
			}
		})
	}
}

func TestAssessPeerCompatibilityProofAge(t *testing.T) {
	now := time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)

	fresh := AssessPeerCompatibility(&PeerForkObservation{LastChecked: now.Add(-90 * time.Second)}, now)
	if fresh.ProofAgeSeconds != 90 || !fresh.ProofFresh {
		t.Fatalf("fresh assessment = %+v, want 90-second fresh proof", fresh)
	}

	missing := AssessPeerCompatibility(&PeerForkObservation{}, now)
	if missing.ProofAgeSeconds != 0 || missing.ProofFresh {
		t.Fatalf("missing assessment = %+v, want unknown-age non-fresh proof", missing)
	}

	future := AssessPeerCompatibility(&PeerForkObservation{LastChecked: now.Add(time.Minute)}, now)
	if future.ProofAgeSeconds != 0 || !future.ProofFresh {
		t.Fatalf("future assessment = %+v, want clock-skew-clamped fresh proof", future)
	}
}
