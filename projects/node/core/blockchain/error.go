// Copyright (c) 2014-2016 The btcsuite developers
// Copyright (c) 2015-2016 The Decred developers
// Use of this source code is governed by an ISC
// license that can be found in the LICENSE file.

package blockchain

import (
	"fmt"
)

// HashError identifies an error that indicates a hash was specified that does
// not exist.
type HashError string

// Error returns the assertion error as a human-readable string and satisfies
// the error interface.
func (e HashError) Error() string {
	return fmt.Sprintf("hash %v does not exist", string(e))
}

// DeploymentError identifies an error that indicates a deployment ID was
// specified that does not exist.
type DeploymentError uint32

// Error returns the assertion error as a human-readable string and satisfies
// the error interface.
func (e DeploymentError) Error() string {
	return fmt.Sprintf("deployment ID %v does not exist", uint32(e))
}

// ErrorCode identifies a kind of error.
type ErrorCode int

// These constants are used to identify a specific RuleError.
const (
	// ErrDuplicateBlock indicates a block with the same hash already
	// exists.
	ErrDuplicateBlock ErrorCode = iota

	// ErrMissingParent indicates that the block was an orphan.
	ErrMissingParent

	// ErrBlockTooBig indicates the serialized block size exceeds the
	// maximum allowed size.
	ErrBlockTooBig

	ErrBadRewardValue

	// ErrWrongBlockSize indicates that the block size from the header was
	// not the actual serialized size of the block.
	ErrWrongBlockSize

	// ErrBlockVersionTooOld indicates the block version is too old and is
	// no longer accepted since the majority of the network has upgraded
	// to a newer version.
	ErrBlockVersionTooOld

	// ErrInvalidTime indicates the time in the passed block has a precision
	// that is more than one second.  The chain consensus rules require
	// timestamps to have a maximum precision of one second.
	ErrInvalidTime

	// ErrTimeTooOld indicates the time is either before the median time of
	// the last several blocks per the chain consensus rules or prior to the
	// most recent checkpoint.
	ErrTimeTooOld

	// ErrTimeTooNew indicates the time is too far in the future as compared
	// the current time.
	ErrTimeTooNew

	// ErrDifficultyTooLow indicates the difficulty for the block is lower
	// than the difficulty required by the most recent checkpoint.
	ErrDifficultyTooLow

	// ErrUnexpectedDifficulty indicates specified bits do not align with
	// the expected value either because it doesn't match the calculated
	// valued based on difficulty regarted rules or it is out of the valid
	// range.
	ErrUnexpectedDifficulty

	// ErrHighHash indicates the block does not hash to a value which is
	// lower than the required target difficultly.
	ErrHighHash

	// ErrBadMerkleRoot indicates the calculated merkle root does not match
	// the expected value.
	ErrBadMerkleRoot

	// ErrBadCheckpoint indicates a block that is expected to be at a
	// checkpoint height does not match the expected one.
	ErrBadCheckpoint

	// ErrForkTooOld indicates a block is attempting to fork the block chain
	// before the most recent checkpoint.
	ErrForkTooOld

	// ErrCheckpointTimeTooOld indicates a block has a timestamp before the
	// most recent checkpoint.
	ErrCheckpointTimeTooOld

	// ErrNoTransactions indicates the block does not have a least one
	// transaction.  A valid block must have at least the coinbase
	// transaction.

	ErrNoTransactions
	// ErrNoParents indicates the block does not have a least one
	// parent.
	ErrNoParents

	// ErrDuplicateParent indicates the block does not have a least one
	// parent.
	ErrDuplicateParent

	// ErrTooManyTransactions indicates the block has more transactions than
	// are allowed.
	ErrTooManyTransactions

	// ErrTxTooBig indicates a transaction exceeds the maximum allowed size
	// when serialized.
	ErrTxTooBig

	// ErrUnfinalizedTx indicates a transaction has not been finalized.
	// A valid block may only contain finalized transactions.
	ErrUnfinalizedTx

	// ErrDuplicateTx indicates a block contains an identical transaction
	// (or at least two transactions which hash to the same value).  A
	// valid block may only contain unique transactions.
	ErrDuplicateTx

	// ErrInvalidFinalState indicates that the final state of the PRNG included
	// in the the block differed from the calculated final state.
	ErrInvalidFinalState

	// ErrPoolSize indicates an error in the ticket pool size for this block.
	ErrPoolSize

	// ErrForceReorgWrongChain indicates that a reroganization was attempted
	// to be forced, but the chain indicated was not mirrored by b.bestChain.
	ErrForceReorgWrongChain

	// ErrForceReorgMissingChild indicates that a reroganization was attempted
	// to be forced, but the child node to reorganize to could not be found.
	ErrForceReorgMissingChild

	// ErrBadBlockHeight indicates that a block header's embedded block height
	// was different from where it was actually embedded in the block chain.
	ErrBadBlockHeight

	// ErrBlockOneTx indicates that block height 1 failed to correct generate
	// the block one premine transaction.
	ErrBlockOneTx

	// ErrFraudBlockHeight indicates the witness block height given was fraudulent.
	ErrFraudBlockHeight

	// ErrFraudBlockIndex indicates the witness block index given was fraudulent.
	ErrFraudBlockIndex

	// ErrInvalidEarlyFinalState indicates that a block before stake validation
	// height had a non-zero final state.
	ErrInvalidEarlyFinalState

	// ErrParentsBlockUnknown indicates that the parents block is not known.
	ErrParentsBlockUnknown

	// ErrInvalidAncestorBlock indicates that an ancestor of this block has
	// failed validation.
	ErrInvalidAncestorBlock

	// ErrInvalidTemplateParent indicates that a block template builds on a
	// block that is either not the current best chain tip or its parent.
	ErrInvalidTemplateParent

	// ErrPrevBlockNotBest indicates that the block's previous block is not the
	// current chain tip. This is not a block validation rule, but is required
	// for block proposals submitted via getblocktemplate RPC.
	ErrPrevBlockNotBest

	// ErrBadParentsMerkleRoot indicates the calculated parents merkle root does not match
	// the expected value.
	ErrBadParentsMerkleRoot

	//ErrBadCuckooNonces
	ErrBadCuckooNonces

	// ErrInValidPowType
	ErrInValidPowType

	// ErrInValidPow
	ErrInvalidPow

	// ErrGovernanceAdmissibility indicates that BEVM replay rejected the block
	// because on-chain governance policy blocks one of its transactions or
	// native value movements.
	ErrGovernanceAdmissibility

	// ErrEVMCheckpointMismatch indicates that BEVM replay produced a block
	// that does not match the peer-advertised safe checkpoint.
	ErrEVMCheckpointMismatch

	// ErrEVMStateGuard indicates that BEVM replay produced a block whose
	// committed EVM state does not match local execution.
	ErrEVMStateGuard

	// numErrorCodes is the maximum error code number used in tests.
	numErrorCodes
)

// Map of ErrorCode values back to their constant names for pretty printing.
var errorCodeStrings = map[ErrorCode]string{
	ErrDuplicateBlock:         "ErrDuplicateBlock",
	ErrMissingParent:          "ErrMissingParent",
	ErrBlockTooBig:            "ErrBlockTooBig",
	ErrWrongBlockSize:         "ErrWrongBlockSize",
	ErrBlockVersionTooOld:     "ErrBlockVersionTooOld",
	ErrInvalidTime:            "ErrInvalidTime",
	ErrTimeTooOld:             "ErrTimeTooOld",
	ErrTimeTooNew:             "ErrTimeTooNew",
	ErrDifficultyTooLow:       "ErrDifficultyTooLow",
	ErrUnexpectedDifficulty:   "ErrUnexpectedDifficulty",
	ErrHighHash:               "ErrHighHash",
	ErrBadMerkleRoot:          "ErrBadMerkleRoot",
	ErrBadCheckpoint:          "ErrBadCheckpoint",
	ErrForkTooOld:             "ErrForkTooOld",
	ErrCheckpointTimeTooOld:   "ErrCheckpointTimeTooOld",
	ErrNoTransactions:         "ErrNoTransactions",
	ErrTooManyTransactions:    "ErrTooManyTransactions",
	ErrTxTooBig:               "ErrTxTooBig",
	ErrUnfinalizedTx:          "ErrUnfinalizedTx",
	ErrDuplicateTx:            "ErrDuplicateTx",
	ErrInvalidFinalState:      "ErrInvalidFinalState",
	ErrPoolSize:               "ErrPoolSize",
	ErrForceReorgWrongChain:   "ErrForceReorgWrongChain",
	ErrForceReorgMissingChild: "ErrForceReorgMissingChild",
	ErrBadBlockHeight:         "ErrBadBlockHeight",
	ErrBlockOneTx:             "ErrBlockOneTx",
	ErrFraudBlockHeight:       "ErrFraudBlockHeight",
	ErrFraudBlockIndex:        "ErrFraudBlockIndex",
	ErrInvalidEarlyFinalState: "ErrInvalidEarlyFinalState",
	ErrInvalidAncestorBlock:   "ErrInvalidAncestorBlock",
	ErrInvalidTemplateParent:  "ErrInvalidTemplateParent",
	//cuckoo,begin
	ErrBadCuckooNonces: "ErrBadCuckooNonces",
	ErrInValidPowType:  "ErrInValidPowType",
	ErrInvalidPow:      "ErrInvalidPow",

	ErrGovernanceAdmissibility: "ErrGovernanceAdmissibility",
	ErrEVMCheckpointMismatch:   "ErrEVMCheckpointMismatch",
	ErrEVMStateGuard:           "ErrEVMStateGuard",
}

// String returns the ErrorCode as a human-readable name.
func (e ErrorCode) String() string {
	if s := errorCodeStrings[e]; s != "" {
		return s
	}
	return fmt.Sprintf("Unknown ErrorCode (%d)", int(e))
}

// RuleError identifies a rule violation.  It is used to indicate that
// processing of a block or transaction failed due to one of the many validation
// rules.  The caller can use type assertions to determine if a failure was
// specifically due to a rule violation and access the ErrorCode field to
// ascertain the specific reason for the rule violation.
type RuleError struct {
	ErrorCode   ErrorCode // Describes the kind of error
	Description string    // Human readable description of the issue
	Err         error     // Wrapped root cause, when a lower layer provided one
}

// Error satisfies the error interface and prints human-readable errors.
func (e RuleError) Error() string {
	return e.Description
}

// Unwrap returns the lower-level root cause when one is available.
func (e RuleError) Unwrap() error {
	return e.Err
}

// ruleError creates an RuleError given a set of arguments.
func ruleError(c ErrorCode, desc string) RuleError {
	return RuleError{ErrorCode: c, Description: desc}
}

// ruleErrorWithCause creates a RuleError while preserving the lower-level
// cause for errors.Is/errors.As based tests and callers.
func ruleErrorWithCause(c ErrorCode, desc string, cause error) RuleError {
	return RuleError{ErrorCode: c, Description: desc, Err: cause}
}
