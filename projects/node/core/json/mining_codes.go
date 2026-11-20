package json

// MiningTemplateHealthCode is the stable machine-readable code used by
// getTemplateHealth and nodeworker mining readiness decisions.
type MiningTemplateHealthCode string

const (
	MiningTemplateHealthOK                    MiningTemplateHealthCode = "ok"
	MiningTemplateHealthMinerDisabled         MiningTemplateHealthCode = "miner_disabled"
	MiningTemplateHealthMinerNotStarted       MiningTemplateHealthCode = "miner_not_started"
	MiningTemplateHealthMinerShutdown         MiningTemplateHealthCode = "miner_shutdown"
	MiningTemplateHealthTemplateUnavailable   MiningTemplateHealthCode = "template_unavailable"
	MiningTemplateHealthNodeSyncing           MiningTemplateHealthCode = "node_syncing"
	MiningTemplateHealthP2PUnavailable        MiningTemplateHealthCode = "p2p_unavailable"
	MiningTemplateHealthChainUnavailable      MiningTemplateHealthCode = "chain_unavailable"
	MiningTemplateHealthTemplateBuildFailed   MiningTemplateHealthCode = "template_build_failed"
	MiningTemplateHealthEVMPendingUnavailable MiningTemplateHealthCode = "evm_pending_template_unavailable"
	MiningTemplateHealthEVMPendingNonceDrift  MiningTemplateHealthCode = "evm_pending_nonce_drift"
	MiningTemplateHealthTemplateExpired       MiningTemplateHealthCode = "template_expired"
	MiningTemplateHealthTemplateStaleParent   MiningTemplateHealthCode = "template_parent_stale"
	MiningTemplateHealthWorkerUnavailable     MiningTemplateHealthCode = "worker_unavailable"
	MiningTemplateHealthWorkerStopped         MiningTemplateHealthCode = "worker_stopped"
	MiningTemplateHealthMempoolNotReady       MiningTemplateHealthCode = "mempool_not_ready"
	MiningTemplateHealthTimeUpdateFailed      MiningTemplateHealthCode = "template_time_update_failed"
)

func (c MiningTemplateHealthCode) String() string {
	return string(c)
}

// SubmitBlockOutcomeCode is the stable machine-readable code returned by
// structured block submission results.
type SubmitBlockOutcomeCode string

const (
	SubmitBlockOutcomeAccepted           SubmitBlockOutcomeCode = "accepted"
	SubmitBlockOutcomeAlreadyAccepted    SubmitBlockOutcomeCode = "already_accepted"
	SubmitBlockOutcomeAlreadyRejected    SubmitBlockOutcomeCode = "already_rejected"
	SubmitBlockOutcomeQueued             SubmitBlockOutcomeCode = "queued"
	SubmitBlockOutcomeRetryable          SubmitBlockOutcomeCode = "retryable"
	SubmitBlockOutcomeDuplicate          SubmitBlockOutcomeCode = "duplicate"
	SubmitBlockOutcomeAlreadyKnown       SubmitBlockOutcomeCode = "already_known"
	SubmitBlockOutcomeStaleParent        SubmitBlockOutcomeCode = "stale_parent"
	SubmitBlockOutcomeTipOverdue         SubmitBlockOutcomeCode = "tip_overdue"
	SubmitBlockOutcomeExpired            SubmitBlockOutcomeCode = "expired"
	SubmitBlockOutcomeTemplateSeqUnknown SubmitBlockOutcomeCode = "template_seq_unknown"
	SubmitBlockOutcomeInvalidPOW         SubmitBlockOutcomeCode = "invalid_pow"
	SubmitBlockOutcomeTxRootMismatch     SubmitBlockOutcomeCode = "txroot_mismatch"
	SubmitBlockOutcomeMissingTrie        SubmitBlockOutcomeCode = "missing_trie"
	SubmitBlockOutcomeNonceTooLow        SubmitBlockOutcomeCode = "nonce_too_low"
	SubmitBlockOutcomeNodeSyncing        SubmitBlockOutcomeCode = "node_syncing"
	SubmitBlockOutcomeP2PNotCurrent      SubmitBlockOutcomeCode = "p2p_not_current"
	SubmitBlockOutcomeMinerDisabled      SubmitBlockOutcomeCode = "miner_disabled"
	SubmitBlockOutcomeGovernanceRejected SubmitBlockOutcomeCode = "governance_rejected"
	SubmitBlockOutcomeInternalError      SubmitBlockOutcomeCode = "internal_error"
)

func (c SubmitBlockOutcomeCode) String() string {
	return string(c)
}
