package miner

import (
	"errors"
	"fmt"
	"strings"

	"github.com/BlockdagNetworkLabs/bdag/core/blockchain"
	"github.com/BlockdagNetworkLabs/bdag/core/json"
)

const (
	submitOutcomeAccepted           = "accepted"
	submitOutcomeAlreadyAccepted    = "already_accepted"
	submitOutcomeAlreadyRejected    = "already_rejected"
	submitOutcomeQueued             = "queued"
	submitOutcomeRetryable          = "retryable"
	submitOutcomeDuplicate          = "duplicate"
	submitOutcomeAlreadyKnown       = "already_known"
	submitOutcomeStaleParent        = "stale_parent"
	submitOutcomeTipOverdue         = "tip_overdue"
	submitOutcomeExpired            = "expired"
	submitOutcomeTemplateSeqUnknown = "template_seq_unknown"
	submitOutcomeInvalidPOW         = "invalid_pow"
	submitOutcomeTxRootMismatch     = "txroot_mismatch"
	submitOutcomeMissingTrie        = "missing_trie"
	submitOutcomeNonceTooLow        = "nonce_too_low"
	submitOutcomeGovernanceRejected = "governance_rejected"
	submitOutcomeNodeSyncing        = "node_syncing"
	submitOutcomeP2PNotCurrent      = "p2p_not_current"
	submitOutcomeMinerDisabled      = "miner_disabled"
	submitOutcomeInternalError      = "internal_error"
)

type submitOutcomeError struct {
	code    string
	message string
	cause   error
}

func (e *submitOutcomeError) Error() string {
	return e.message
}

func (e *submitOutcomeError) Unwrap() error {
	return e.cause
}

func newSubmitOutcomeError(code string, cause error, format string, args ...any) *submitOutcomeError {
	return &submitOutcomeError{
		code:    code,
		message: fmt.Sprintf(format, args...),
		cause:   cause,
	}
}

func isSubmitOutcomeError(err error) bool {
	var outcome *submitOutcomeError
	return errors.As(err, &outcome)
}

func (m *Miner) submitOutcomeResult(code, message string) json.SubmitBlockResult {
	accepted, final, retryable := submitOutcomeProperties(code)
	return json.SubmitBlockResult{
		Accepted:    accepted,
		Final:       final,
		Retryable:   retryable,
		Code:        code,
		Message:     message,
		MinerType:   m.workerType(),
		TemplateSeq: m.templateSeq.Load(),
	}
}

func submitOutcomeProperties(code string) (accepted, final, retryable bool) {
	switch code {
	case submitOutcomeAccepted, submitOutcomeAlreadyAccepted:
		return true, true, false
	case submitOutcomeQueued, submitOutcomeRetryable,
		submitOutcomeDuplicate, submitOutcomeAlreadyKnown,
		submitOutcomeMissingTrie, submitOutcomeNonceTooLow,
		submitOutcomeNodeSyncing, submitOutcomeP2PNotCurrent,
		submitOutcomeMinerDisabled, submitOutcomeInternalError,
		submitOutcomeTemplateSeqUnknown:
		return false, false, true
	default:
		return false, true, false
	}
}

func (m *Miner) submitOutcomeForBlockStatus(status blockchain.BlockSubmissionStatus, blockHash string, cause error) (json.SubmitBlockResult, error, bool) {
	var code, message string
	switch status {
	case blockchain.BlockSubmissionAccepted:
		code = submitOutcomeAlreadyAccepted
		message = "exact block is already accepted by the local DAG"
	case blockchain.BlockSubmissionRejected:
		code = submitOutcomeAlreadyRejected
		message = "exact block is already recorded as rejected by the local DAG"
	case blockchain.BlockSubmissionQueued:
		code = submitOutcomeQueued
		message = "exact block is queued for local processing"
	default:
		return json.SubmitBlockResult{}, nil, false
	}
	outcomeErr := newSubmitOutcomeError(code, cause, "%s", message)
	result := m.submitOutcomeResult(code, message)
	result.BlockHash = blockHash
	return result, outcomeErr, true
}

func (m *Miner) submitOutcomeFromError(err error) (json.SubmitBlockResult, error) {
	if err == nil {
		return m.submitOutcomeResult(submitOutcomeAccepted, "accepted"), nil
	}

	var outcome *submitOutcomeError
	if errors.As(err, &outcome) {
		return m.submitOutcomeResult(outcome.code, outcome.message), err
	}

	code := classifySubmitError(err)
	return m.submitOutcomeResult(code, err.Error()), newSubmitOutcomeError(code, err, "%s", err.Error())
}

func classifySubmitError(err error) string {
	if err == nil {
		return submitOutcomeAccepted
	}

	var rErr blockchain.RuleError
	if errors.As(err, &rErr) {
		return submitOutcomeCodeFromRuleError(rErr.ErrorCode)
	}

	msg := strings.ToLower(err.Error())
	switch {
	case strings.Contains(msg, "exists in the queue"), strings.Contains(msg, "already queued"):
		return submitOutcomeQueued
	case strings.Contains(msg, "initial download"), strings.Contains(msg, "downloading blocks"):
		return submitOutcomeNodeSyncing
	case strings.Contains(msg, "p2p") && strings.Contains(msg, "not current"):
		return submitOutcomeP2PNotCurrent
	case strings.Contains(msg, "not current"):
		return submitOutcomeNodeSyncing
	case strings.Contains(msg, "already known"):
		return submitOutcomeAlreadyKnown
	case strings.Contains(msg, "duplicate"):
		return submitOutcomeDuplicate
	case strings.Contains(msg, "expired"):
		return submitOutcomeExpired
	case strings.Contains(msg, "overdue"):
		return submitOutcomeTipOverdue
	case strings.Contains(msg, "parent"), strings.Contains(msg, "orphan"):
		return submitOutcomeStaleParent
	case strings.Contains(msg, "high hash"), strings.Contains(msg, "pow"):
		return submitOutcomeInvalidPOW
	case strings.Contains(msg, "tx root mismatch"):
		return submitOutcomeTxRootMismatch
	case strings.Contains(msg, "missing trie"), strings.Contains(msg, "missing state"):
		return submitOutcomeMissingTrie
	case strings.Contains(msg, "nonce too low"):
		return submitOutcomeNonceTooLow
	default:
		return submitOutcomeInternalError
	}
}

func submitOutcomeCodeFromRuleError(code blockchain.ErrorCode) string {
	switch code {
	case blockchain.ErrDuplicateBlock:
		return submitOutcomeDuplicate
	case blockchain.ErrMissingParent, blockchain.ErrParentsBlockUnknown,
		blockchain.ErrInvalidAncestorBlock, blockchain.ErrInvalidTemplateParent,
		blockchain.ErrPrevBlockNotBest:
		return submitOutcomeStaleParent
	case blockchain.ErrHighHash:
		return submitOutcomeInvalidPOW
	case blockchain.ErrBadMerkleRoot:
		return submitOutcomeTxRootMismatch
	case blockchain.ErrGovernanceAdmissibility:
		return submitOutcomeGovernanceRejected
	default:
		return submitOutcomeInternalError
	}
}

func (m *Miner) workerType() string {
	if m.worker == nil {
		return ""
	}
	return m.worker.GetType()
}
