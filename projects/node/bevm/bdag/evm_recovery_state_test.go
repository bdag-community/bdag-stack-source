package bdag

import (
	"errors"
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
)

func TestLocalEVMRecoveryStateRejectsUnknownObservations(t *testing.T) {
	unsafe := recoveryStateTestHeader(12408810, "0x02", "unsafe")
	if report := AssessLocalEVMHeads(nil, unsafe); report.State != LocalEVMRecoveryDegradedNoStateBackedHead {
		t.Fatalf("missing safe head state = %s", report.State)
	}
	if report := AssessLocalEVMHeadObservations(LocalEVMHeadObservation{Header: recoveryStateTestHeader(1, "0x01", "safe"), Known: true, Available: true}, LocalEVMHeadObservation{}); report.State != LocalEVMRecoveryUnknown {
		t.Fatalf("missing unsafe head state = %s", report.State)
	}
	if report := AssessLocalEVMCandidateInvariants(LocalEVMCandidateObservation{}); report.State != LocalEVMRecoveryCandidateRejected {
		t.Fatalf("zero candidate state = %s", report.State)
	}
	if report := ClassifyLocalEVMRecoveryError(nil); report.State != LocalEVMRecoveryUnknown {
		t.Fatalf("absent recovery error state = %s", report.State)
	}
}

func TestLocalEVMHeadObservationsRequireReadableState(t *testing.T) {
	safe := recoveryStateTestHeader(12, "0x01", "safe")
	unsafe := recoveryStateTestHeader(12, "0x01", "unsafe")
	available := LocalEVMHeadObservation{Header: safe, Known: true, Available: true}
	if report := AssessLocalEVMHeadObservations(available, LocalEVMHeadObservation{Header: unsafe, Known: true, Available: false}); report.State != LocalEVMRecoveryUnknown {
		t.Fatalf("unavailable unsafe state = %s", report.State)
	}
	if report := AssessLocalEVMHeadObservations(LocalEVMHeadObservation{Header: safe, Known: true, Available: false}, available); report.State != LocalEVMRecoveryDegradedNoStateBackedHead {
		t.Fatalf("unavailable safe state = %s", report.State)
	}
	zeroRoot := recoveryStateTestHeader(12, "0x00", "safe")
	if report := AssessLocalEVMHeadObservations(LocalEVMHeadObservation{Header: zeroRoot, Known: true, Available: true}, available); report.State != LocalEVMRecoveryDegradedNoStateBackedHead {
		t.Fatalf("zero-root safe state = %s", report.State)
	}
	coherent := AssessLocalEVMHeadObservations(available, available)
	if coherent.State != LocalEVMRecoveryHealthy || !coherent.SafeKnown || !coherent.SafeAvailable {
		t.Fatalf("coherent observation = %#v", coherent)
	}
}

func TestLocalEVMRecoveryStateClassifiesDiagnostics(t *testing.T) {
	checkpoint := ClassifyLocalEVMRecoveryError(errors.New("invalid mainnet epoch recovery checkpoint"))
	if checkpoint.State != LocalEVMRecoveryDegradedBadCheckpoint {
		t.Fatalf("checkpoint state = %s", checkpoint.State)
	}
	merkle := ClassifyLocalEVMRecoveryError(errors.New("BAD BLOCK: invalid merkle root"))
	if merkle.State != LocalEVMRecoveryFailedInvalidMerkle {
		t.Fatalf("merkle state = %s", merkle.State)
	}
}

func TestLocalEVMCandidateRequiresIdentityAndBalances(t *testing.T) {
	hash := common.HexToHash("0x1234")
	root := common.HexToHash("0xabcd")
	balance := big.NewInt(9800000)
	base := LocalEVMCandidateObservation{
		LatestNumber: 123, LatestHash: hash, LatestRoot: root,
		NumericNumber: 123, NumericHash: hash, NumericRoot: root,
		LatestBalance: balance, NumericBalance: new(big.Int).Set(balance),
		ReferenceBalance: new(big.Int).Set(balance), RequireReferenceBalance: true,
		LatestStateKnown: true, LatestStateAvailable: true,
		NumericStateKnown: true, NumericStateAvailable: true,
	}
	if report := AssessLocalEVMCandidateInvariants(base); report.State != LocalEVMRecoveryCandidateReady {
		t.Fatalf("coherent candidate state = %s (%s)", report.State, report.Reason)
	}
	base.LatestBalance = nil
	if report := AssessLocalEVMCandidateInvariants(base); report.State != LocalEVMRecoveryCandidateRejected {
		t.Fatalf("unknown balance state = %s", report.State)
	}
}

func recoveryStateTestHeader(number uint64, root, extra string) *types.Header {
	return &types.Header{Number: new(big.Int).SetUint64(number), Root: common.HexToHash(root), Extra: []byte(extra)}
}
