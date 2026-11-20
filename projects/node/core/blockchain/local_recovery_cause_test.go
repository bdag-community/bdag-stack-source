package blockchain

import (
	"errors"
	"testing"
)

func TestLocalEVMRecoveryRollbackFailurePreservesCause(t *testing.T) {
	original := errors.New("rewind state missing")
	bc := &BlockChain{}
	got := bc.abortEVMStateGuardNativeCandidate(nil, evmStateGuardRecoveryError{recoveryErr: original})
	if !errors.Is(got, original) {
		t.Fatalf("rollback failure lost original state cause: %v", got)
	}
	if isEVMStateGuardRuleError(got) {
		t.Fatal("local rollback failure classified as peer violation")
	}
}
