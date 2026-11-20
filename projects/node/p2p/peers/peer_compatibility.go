package peers

import (
	"fmt"
	"time"

	ethcommon "github.com/ethereum/go-ethereum/common"
)

const (
	peerCompatibilityStaleOrderGap    = uint64(100)
	peerCompatibilityProofFreshWindow = 2 * time.Minute
)

// PeerCompatibilityAssessment is a read-only interpretation of the fork and
// dataset evidence already collected for a peer. It does not affect peer
// selection, synchronization, mining, or connection lifecycle decisions.
type PeerCompatibilityAssessment struct {
	Compatibility       string
	CompatibilityReason string
	DatasetStatus       string
	DatasetReason       string
	DatasetProof        string
	ProofAgeSeconds     int64
	ProofFresh          bool
	Usefulness          string
	UsefulnessReason    string
	UsefulForTarget     bool
}

// AssessPeerCompatibility converts one peer observation into stable API and
// metric classifications. A missing observation or proof timestamp remains
// explicitly unknown rather than being treated as fresh evidence.
func AssessPeerCompatibility(observation *PeerForkObservation, now time.Time) PeerCompatibilityAssessment {
	datasetStatus, datasetReason := assessPeerDataset(observation)
	compatibility, compatibilityReason := assessPeerCompatibility(observation, datasetStatus, datasetReason)
	proofAge, proofFresh := assessPeerProofAge(observation, now)
	usefulness, usefulnessReason := assessPeerUsefulness(compatibility, compatibilityReason, proofFresh)

	return PeerCompatibilityAssessment{
		Compatibility:       compatibility,
		CompatibilityReason: compatibilityReason,
		DatasetStatus:       datasetStatus,
		DatasetReason:       datasetReason,
		DatasetProof:        peerDatasetProof(datasetStatus),
		ProofAgeSeconds:     proofAge,
		ProofFresh:          proofFresh,
		Usefulness:          usefulness,
		UsefulnessReason:    usefulnessReason,
		UsefulForTarget:     usefulness == "sync-mining-preferred",
	}
}

func assessPeerDataset(observation *PeerForkObservation) (string, string) {
	if observation == nil {
		return "unknown", "missing peer fork observation"
	}
	if peerStateRootsKnown(observation) && observation.SampledStateRootGot != observation.SampledStateRootWant {
		return "state_root_mismatch", "sampled peer state root differs from the local state root for the sampled block"
	}

	switch observation.CommitmentStatus {
	case "checkpoint_valid":
		return "checkpoint_valid", "peer EVM checkpoint hash and state root are known locally and match"
	case "checkpoint_mismatch":
		return "checkpoint_mismatch", "peer EVM checkpoint is known locally but its hash or state root differs"
	case "invalid_checkpoint":
		return "invalid_checkpoint", "peer advertised a malformed EVM checkpoint"
	case "missing_checkpoint":
		return "missing_checkpoint", "peer protocol supports checkpoints but did not provide one"
	case "legacy_v45", "legacy_no_checkpoint":
		return "legacy_no_checkpoint", "peer uses a legacy protocol without EVM checkpoint exchange"
	case "checkpoint_unknown":
		return "checkpoint_unknown", "peer EVM checkpoint is well-formed but not known locally yet"
	case "checkpoint_unverified":
		return "checkpoint_unverified", "local node could not verify the peer EVM checkpoint"
	case "unknown", "":
		if peerStateRootsKnown(observation) {
			return "sampled_state_root_match", "sampled peer state root matches the local state root for the sampled block"
		}
		return "unknown", "no checkpoint or sampled state-root proof is available yet"
	default:
		if peerStateRootsKnown(observation) {
			return "sampled_state_root_match", "sampled peer state root matches the local state root for the sampled block"
		}
		return "unknown", fmt.Sprintf("unrecognized commitment status %q", observation.CommitmentStatus)
	}
}

func assessPeerCompatibility(observation *PeerForkObservation, datasetStatus, datasetReason string) (string, string) {
	if observation == nil {
		return "unknown", "missing peer fork observation"
	}

	switch datasetStatus {
	case "invalid_checkpoint":
		return "unsafe", datasetReason
	case "checkpoint_mismatch", "state_root_mismatch":
		return "wrong-dataset", datasetReason
	}

	if observation.SameNativeForkKnown && !observation.SameNativeFork {
		return "wrong-fork", "peer native Main-chain evidence disagrees with the local node"
	}
	if observation.Status == PeerForkStatusDivergentMainChain {
		return "wrong-fork", firstPeerReason(observation.Reason, "peer native Main chain diverges from the local node")
	}
	if peerObservationStale(observation) {
		return "stale", "peer is on the same native Main chain but is far enough behind that it should not be preferred"
	}
	if observation.Status == PeerForkStatusSameMainChain && datasetStatus == "checkpoint_valid" {
		return "compatible", "peer is on the same native Main chain and has locally valid dataset checkpoint evidence"
	}
	return "unknown", "peer is reachable but has not yet proven native fork and dataset compatibility"
}

func assessPeerProofAge(observation *PeerForkObservation, now time.Time) (int64, bool) {
	if observation == nil || observation.LastChecked.IsZero() {
		return 0, false
	}
	if now.IsZero() {
		now = time.Now()
	}
	age := now.Sub(observation.LastChecked)
	if age < 0 {
		age = 0
	}
	return int64(age / time.Second), age <= peerCompatibilityProofFreshWindow
}

func assessPeerUsefulness(compatibility, compatibilityReason string, proofFresh bool) (string, string) {
	switch compatibility {
	case "compatible":
		if proofFresh {
			return "sync-mining-preferred", "compatible peer has fresh fork and dataset evidence"
		}
		return "sync-mining-usable", "compatible peer lacks a fresh timestamped proof"
	case "stale":
		return "deprioritized", compatibilityReason
	case "wrong-fork", "wrong-dataset", "unsafe":
		return "avoid", compatibilityReason
	default:
		return "discovery-relay-only", "peer compatibility remains unproven"
	}
}

func peerDatasetProof(datasetStatus string) string {
	switch datasetStatus {
	case "checkpoint_valid":
		return "evm_checkpoint"
	case "checkpoint_mismatch":
		return "evm_checkpoint_mismatch"
	case "invalid_checkpoint":
		return "invalid_evm_checkpoint"
	case "checkpoint_unknown", "checkpoint_unverified":
		return "evm_checkpoint_unverified"
	case "sampled_state_root_match":
		return "sampled_state_root"
	case "state_root_mismatch":
		return "sampled_state_root_mismatch"
	default:
		return "none"
	}
}

func peerStateRootsKnown(observation *PeerForkObservation) bool {
	return observation != nil &&
		observation.SampledStateRootGot != (ethcommon.Hash{}) &&
		observation.SampledStateRootWant != (ethcommon.Hash{})
}

func peerObservationStale(observation *PeerForkObservation) bool {
	if observation == nil || observation.Status != PeerForkStatusSameMainChain {
		return false
	}
	if observation.LocalMainOrder <= observation.MainOrder {
		return false
	}
	return observation.LocalMainOrder-observation.MainOrder >= peerCompatibilityStaleOrderGap
}

func firstPeerReason(reason, fallback string) string {
	if reason != "" {
		return reason
	}
	return fallback
}
