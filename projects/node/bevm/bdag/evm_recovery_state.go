package bdag

import (
	"math/big"
	"strings"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
)

// LocalEVMRecoveryState describes an observation only. It never authorizes
// recovery, activation, checkpoint selection, or canonical state mutation.
type LocalEVMRecoveryState string

const (
	LocalEVMRecoveryHealthy                   LocalEVMRecoveryState = "healthy"
	LocalEVMRecoveryUnknown                   LocalEVMRecoveryState = "unknown"
	LocalEVMRecoveryDegradedBadCheckpoint     LocalEVMRecoveryState = "degraded_bad_checkpoint"
	LocalEVMRecoveryDegradedHeaderStateSplit  LocalEVMRecoveryState = "degraded_header_state_split"
	LocalEVMRecoveryDegradedNoStateBackedHead LocalEVMRecoveryState = "degraded_no_state_backed_head"
	LocalEVMRecoveryFailedInvalidMerkle       LocalEVMRecoveryState = "recovery_failed_invalid_merkle"
	LocalEVMRecoveryFailed                    LocalEVMRecoveryState = "recovery_failed"
	LocalEVMRecoveryCandidateReady            LocalEVMRecoveryState = "candidate_ready"
	LocalEVMRecoveryCandidateRejected         LocalEVMRecoveryState = "candidate_rejected"
)

type LocalEVMRecoveryReport struct {
	State           LocalEVMRecoveryState
	Reason          string
	SafeKnown       bool
	SafeAvailable   bool
	SafeNumber      uint64
	SafeHash        common.Hash
	SafeRoot        common.Hash
	UnsafeKnown     bool
	UnsafeAvailable bool
	UnsafeNumber    uint64
	UnsafeHash      common.Hash
	UnsafeRoot      common.Hash
}

// LocalEVMHeadObservation keeps header identity separate from the local trie
// observation. A header with a nonzero root does not prove that its state is
// present or readable on this node.
type LocalEVMHeadObservation struct {
	Header    *types.Header
	Known     bool
	Available bool
}

type LocalEVMCandidateObservation struct {
	LatestNumber            uint64
	LatestHash              common.Hash
	LatestRoot              common.Hash
	NumericNumber           uint64
	NumericHash             common.Hash
	NumericRoot             common.Hash
	LatestBalance           *big.Int
	NumericBalance          *big.Int
	ReferenceBalance        *big.Int
	RequireReferenceBalance bool
	LatestStateKnown        bool
	LatestStateAvailable    bool
	NumericStateKnown       bool
	NumericStateAvailable   bool
}

func AssessLocalEVMHeads(safeLatest *types.Header, unsafeHeader *types.Header) LocalEVMRecoveryReport {
	// This legacy header-only helper cannot establish trie availability. Callers
	// with local state evidence must use AssessLocalEVMHeadObservations.
	return AssessLocalEVMHeadObservations(
		LocalEVMHeadObservation{Header: safeLatest, Known: safeLatest != nil},
		LocalEVMHeadObservation{Header: unsafeHeader, Known: unsafeHeader != nil},
	)
}

func AssessLocalEVMHeadObservations(safeLatest, unsafeHeader LocalEVMHeadObservation) LocalEVMRecoveryReport {
	report := LocalEVMRecoveryReport{State: LocalEVMRecoveryHealthy, Reason: "state-backed latest is coherent"}
	report.SafeKnown = safeLatest.Known && headerIdentityBound(safeLatest.Header)
	report.SafeAvailable = report.SafeKnown && safeLatest.Available
	report.UnsafeKnown = unsafeHeader.Known && headerIdentityBound(unsafeHeader.Header)
	report.UnsafeAvailable = report.UnsafeKnown && unsafeHeader.Available
	copyHeaderSummary(&report, safeLatest.Header, unsafeHeader.Header)
	if !report.SafeKnown || !report.SafeAvailable {
		report.State = LocalEVMRecoveryDegradedNoStateBackedHead
		report.Reason = "state-backed latest header or local state is unavailable"
		return report
	}
	if !report.UnsafeKnown || !report.UnsafeAvailable {
		report.State = LocalEVMRecoveryUnknown
		report.Reason = "unsafe latest header or local state availability is unknown"
		return report
	}
	if report.UnsafeNumber != report.SafeNumber {
		report.State = LocalEVMRecoveryDegradedHeaderStateSplit
		report.Reason = "state-backed and unsafe latest headers differ in height"
		return report
	}
	if report.UnsafeHash != report.SafeHash || report.UnsafeRoot != report.SafeRoot {
		report.State = LocalEVMRecoveryDegradedHeaderStateSplit
		report.Reason = "unsafe header differs from state-backed latest at the same height"
	}
	return report
}

func ClassifyLocalEVMRecoveryError(err error) LocalEVMRecoveryReport {
	if err == nil {
		return LocalEVMRecoveryReport{State: LocalEVMRecoveryUnknown, Reason: "recovery error observation is absent"}
	}
	msg := err.Error()
	lower := strings.ToLower(msg)
	switch {
	case strings.Contains(lower, "epoch recovery checkpoint") || strings.Contains(lower, "invalid mainnet epoch recovery checkpoint"):
		return LocalEVMRecoveryReport{State: LocalEVMRecoveryDegradedBadCheckpoint, Reason: msg}
	case strings.Contains(lower, "bad block") || strings.Contains(lower, "invalid merkle root") || strings.Contains(lower, "invalid merkle"):
		return LocalEVMRecoveryReport{State: LocalEVMRecoveryFailedInvalidMerkle, Reason: msg}
	case strings.Contains(lower, "state-backed latest header is unavailable") || strings.Contains(lower, "current evm head is unavailable"):
		return LocalEVMRecoveryReport{State: LocalEVMRecoveryDegradedNoStateBackedHead, Reason: msg}
	default:
		return LocalEVMRecoveryReport{State: LocalEVMRecoveryFailed, Reason: msg}
	}
}

func AssessLocalEVMCandidateInvariants(obs LocalEVMCandidateObservation) LocalEVMRecoveryReport {
	reject := func(reason string) LocalEVMRecoveryReport {
		return LocalEVMRecoveryReport{State: LocalEVMRecoveryCandidateRejected, Reason: reason}
	}
	if obs.LatestHash == (common.Hash{}) || obs.NumericHash == (common.Hash{}) || obs.LatestRoot == (common.Hash{}) || obs.NumericRoot == (common.Hash{}) {
		return reject("candidate chain identity or state root is unbound")
	}
	if obs.LatestNumber != obs.NumericNumber {
		return reject("latest and numeric block numbers differ")
	}
	if obs.LatestHash != obs.NumericHash {
		return reject("latest and numeric block hashes differ")
	}
	if obs.LatestRoot != obs.NumericRoot {
		return reject("latest and numeric state roots differ")
	}
	if !obs.LatestStateKnown || !obs.LatestStateAvailable || !obs.NumericStateKnown || !obs.NumericStateAvailable {
		return reject("candidate local state observation is unknown or unavailable")
	}
	if obs.LatestBalance == nil || obs.NumericBalance == nil {
		return reject("latest and numeric balances are unknown")
	}
	if !equalBigInt(obs.LatestBalance, obs.NumericBalance) {
		return reject("latest and numeric balances differ")
	}
	if obs.RequireReferenceBalance {
		if obs.ReferenceBalance == nil {
			return reject("reference balance is unknown")
		}
		if !equalBigInt(obs.LatestBalance, obs.ReferenceBalance) {
			return reject("candidate balance differs from reference balance")
		}
	}
	return LocalEVMRecoveryReport{State: LocalEVMRecoveryCandidateReady, Reason: "latest, numeric, and balance invariants are coherent", SafeKnown: true, SafeAvailable: true, SafeNumber: obs.LatestNumber, SafeHash: obs.LatestHash, SafeRoot: obs.LatestRoot}
}

func headerIdentityBound(header *types.Header) bool {
	return header != nil && header.Number != nil && header.Hash() != (common.Hash{}) && header.Root != (common.Hash{})
}

func copyHeaderSummary(report *LocalEVMRecoveryReport, safeLatest *types.Header, unsafeHeader *types.Header) {
	if safeLatest != nil {
		if safeLatest.Number != nil {
			report.SafeNumber = safeLatest.Number.Uint64()
		}
		report.SafeHash = safeLatest.Hash()
		report.SafeRoot = safeLatest.Root
	}
	if unsafeHeader != nil {
		if unsafeHeader.Number != nil {
			report.UnsafeNumber = unsafeHeader.Number.Uint64()
		}
		report.UnsafeHash = unsafeHeader.Hash()
		report.UnsafeRoot = unsafeHeader.Root
	}
}

func equalBigInt(left, right *big.Int) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return left.Cmp(right) == 0
}
