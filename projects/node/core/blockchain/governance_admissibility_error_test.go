package blockchain

import (
	"errors"
	"fmt"
	"testing"

	"github.com/BlockdagNetworkLabs/bdag/bevm/bdag"
	"github.com/ethereum/go-ethereum/core/vm"
)

func TestClassifyGovernanceAdmissibilityError(t *testing.T) {
	tests := []struct {
		name  string
		cause error
	}{
		{name: "blocked address", cause: bdag.ErrBlockedAddress},
		{name: "restricted address", cause: bdag.ErrRestrictedAddress},
		{name: "native transfer", cause: vm.ErrNativeTransferBlocked},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := classifyGovernanceAdmissibilityError(fmt.Errorf("wrapped: %w", tt.cause))
			var ruleErr RuleError
			if !errors.As(err, &ruleErr) {
				t.Fatalf("classified error %T is not RuleError", err)
			}
			if ruleErr.ErrorCode != ErrGovernanceAdmissibility {
				t.Fatalf("ErrorCode = %s, want %s", ruleErr.ErrorCode, ErrGovernanceAdmissibility)
			}
			if !errors.Is(err, tt.cause) {
				t.Fatalf("classified error does not wrap %v: %v", tt.cause, err)
			}
			if !isGovernanceAdmissibilityRuleError(err) {
				t.Fatalf("isGovernanceAdmissibilityRuleError(%v) = false", err)
			}
		})
	}
}

func TestIsGovernanceAdmissibilityRuleErrorAcceptsPointer(t *testing.T) {
	err := &RuleError{ErrorCode: ErrGovernanceAdmissibility, Description: "governance"}
	if !isGovernanceAdmissibilityRuleError(err) {
		t.Fatalf("isGovernanceAdmissibilityRuleError(%T) = false", err)
	}
}

func TestClassifyGovernanceAdmissibilityErrorLeavesOtherErrors(t *testing.T) {
	other := errors.New("other validation error")
	if got := classifyGovernanceAdmissibilityError(other); got != other {
		t.Fatalf("non-governance error changed: got %v want %v", got, other)
	}
	if isGovernanceAdmissibilityRuleError(other) {
		t.Fatalf("plain error classified as governance rule error")
	}
}
