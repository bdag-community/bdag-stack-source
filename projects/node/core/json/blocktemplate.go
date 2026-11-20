package json

import "github.com/ethereum/go-ethereum/common"

type PowDiffReference struct {
	NBits  string `json:"nbits"`
	Target string `json:"target"`
}

// LL(getblocktemplate RPC) 2018-10-28
// TemplateRequest is a request object as defined in BIP22
// (https://en.bitcoin.it/wiki/BIP_0022), it is optionally provided as an
// // argument to GetBlockTemplate RPC.
type TemplateRequest struct {
	Mode         string   `json:"mode,omitempty"`
	PowType      byte     `json:"pow_type"`
	Capabilities []string `json:"capabilities,omitempty"`

	// Optional long polling.
	LongPollID string `json:"longpollid,omitempty"`

	// Optional template tweaking.  SigOpLimit and SizeLimit can be int64
	// or bool.
	SigOpLimit any    `json:"sigoplimit,omitempty"`
	SizeLimit  any    `json:"sizelimit,omitempty"`
	MaxVersion uint32 `json:"maxversion,omitempty"`

	// Basic pool extension from BIP 0023.
	Target string `json:"target,omitempty"`

	// Block proposal from BIP 0023.  Data is only provided when Mode is
	// "proposal".
	Data       string `json:"data,omitempty"`
	WorkID     string `json:"workid,omitempty"`
	PayAddress string `json:"pay_address,omitempty"`
}

// GetBlockTemplateResultTx models the transactions field of the
// getblocktemplate command.
type GetBlockTemplateResultTx struct {
	Data    string  `json:"data"`
	Hash    string  `json:"hash"`
	Depends []int64 `json:"depends"`
	Fee     int64   `json:"fee"`
	SigOps  int64   `json:"sigops"`
	Weight  int64   `json:"weight"`
}

// GetBlockTemplateResultPt models the parents field of the
// getblocktemplate command.
type GetBlockTemplateResultPt struct {
	Data string `json:"data"`
	Hash string `json:"hash"`
}

// GetBlockTemplateResultAux models the coinbaseaux field of the
// getblocktemplate command.
type GetBlockTemplateResultAux struct {
	Flags string `json:"flags"`
}

// GetBlockTemplateResult models the data returned from the getblocktemplate
type GetBlockTemplateResult struct {
	// Base fields from BIP 0022.  CoinbaseAux is optional.  One of
	// CoinbaseTxn or CoinbaseValue must be specified, but not both.
	TxRoot        string                     `json:"txroot"`
	StateRoot     string                     `json:"stateroot"`
	CurTime       int64                      `json:"curtime"`
	Height        int64                      `json:"height"`
	Blues         int64                      `json:"blues"`
	PreviousHash  string                     `json:"previousblockhash"`
	SigOpLimit    int64                      `json:"sigoplimit,omitempty"`
	SizeLimit     int64                      `json:"sizelimit,omitempty"`
	WeightLimit   int64                      `json:"weightlimit,omitempty"`
	Parents       []GetBlockTemplateResultPt `json:"parents"`
	Transactions  []GetBlockTemplateResultTx `json:"transactions"`
	Version       uint32                     `json:"version"`
	CoinbaseAux   *GetBlockTemplateResultAux `json:"coinbaseaux,omitempty"`
	CoinbaseTxn   *GetBlockTemplateResultTx  `json:"coinbasetxn,omitempty"`
	CoinbaseValue *uint64                    `json:"coinbasevalue,omitempty"`
	WorkID        string                     `json:"workid,omitempty"`
	NodeInfo      string                     `json:"nodeinfo,omitempty"`

	// Witness commitment defined in BIP 0141.
	DefaultWitnessCommitment string `json:"default_witness_commitment,omitempty"`

	// Optional long polling from BIP 0022.
	LongPollID  string `json:"longpollid,omitempty"`
	LongPollURI string `json:"longpolluri,omitempty"`
	SubmitOld   *bool  `json:"submitold,omitempty"`

	// Basic pool extension from BIP 0023.
	Expires          int64            `json:"expires,omitempty"`
	PowDiffReference PowDiffReference `json:"pow_diff_reference"`
	// Mutations from BIP 0023.
	MaxTime    int64    `json:"maxtime,omitempty"`
	MinTime    int64    `json:"mintime,omitempty"`
	Mutable    []string `json:"mutable,omitempty"`
	NonceRange string   `json:"noncerange,omitempty"`

	// Block proposal from BIP 0023.
	// temp use
	WorkData        string         `json:"workdata"`
	Capabilities    []string       `json:"capabilities,omitempty"`
	RejectReasion   string         `json:"reject-reason,omitempty"`
	BlockFees       int64          `json:"block_fees"`
	CoinbaseVersion string         `json:"coinbase_version"`
	Reward          uint64         `json:"reward"`
	CoinbaseAddress common.Address `json:"coinbase_address"`
	PowRoot         string         `json:"pow_root"`
}

// GetBlockTemplateResult models the data returned from the getblocktemplate
type SubmitBlockResult struct {
	Accepted       bool   `json:"accepted"`
	Final          bool   `json:"final"`
	Retryable      bool   `json:"retryable"`
	Code           string `json:"code"`
	Message        string `json:"message,omitempty"`
	BlockHash      string `json:"block_hash"`
	CoinbaseTxID   string `json:"coinbase_txid"`
	Order          string `json:"order"`
	Height         int64  `json:"height"`
	CoinbaseAmount uint64 `json:"coinbase_amount"`
	MinerType      string `json:"miner_type"`
	TemplateSeq    uint64 `json:"template_seq,omitempty"`
}

// MiningTemplateHealthResult is a low-cost status snapshot for pool software.
// It lets pools make routing decisions from explicit node state instead of
// inferring health from log text or transient submit errors.
type MiningTemplateHealthResult struct {
	CheckedAtUnixMs int64 `json:"checked_at_unix_ms"`

	Enabled     bool   `json:"enabled"`
	Started     bool   `json:"started"`
	Shutdown    bool   `json:"shutdown"`
	ReasonCode  string `json:"reason_code"`
	Reason      string `json:"reason"`
	MineableNow bool   `json:"mineable_now"`
	SubmitReady bool   `json:"submit_ready"`
	// GetBlockTemplateReady is the same low-cost sync preflight used by
	// getBlockTemplate before a caller asks the miner to build or refresh work.
	// It is intentionally separate from MineableNow, which describes whether
	// the currently cached template can be mined immediately.
	GetBlockTemplateReady      bool   `json:"get_block_template_ready"`
	GetBlockTemplateReasonCode string `json:"get_block_template_reason_code"`
	GetBlockTemplateReason     string `json:"get_block_template_reason"`
	WorkerReady                bool   `json:"worker_ready"`
	WorkerType                 string `json:"worker_type,omitempty"`
	WorkerRunning              bool   `json:"worker_running"`

	TemplateAvailable                              bool   `json:"template_available"`
	TemplateUsable                                 bool   `json:"template_usable"`
	TemplateAgeMs                                  int64  `json:"template_age_ms"`
	TemplateMaxAgeMs                               int64  `json:"template_max_age_ms"`
	TemplateTimestampUnix                          int64  `json:"template_timestamp_unix,omitempty"`
	LatestTemplateSeq                              uint64 `json:"latest_template_seq"`
	TemplateHeight                                 int64  `json:"template_height,omitempty"`
	TemplateParentRoot                             string `json:"template_parent_root,omitempty"`
	TemplateTxCount                                int    `json:"template_tx_count"`
	TemplateCoinbaseAddress                        string `json:"template_coinbase_address,omitempty"`
	LastGoodTemplateAvailable                      bool   `json:"last_good_template_available"`
	LastGoodTemplateAgeMs                          int64  `json:"last_good_template_age_ms"`
	LastTemplateBuildAtUnixMs                      int64  `json:"last_template_build_at_unix_ms,omitempty"`
	LastTemplateBuildAgeMs                         int64  `json:"last_template_build_age_ms,omitempty"`
	LastTemplateBuildDurationMs                    int64  `json:"last_template_build_duration_ms,omitempty"`
	LastTemplateBuildReason                        string `json:"last_template_build_reason,omitempty"`
	LastTemplateBuildErrorCode                     string `json:"last_template_build_error_code,omitempty"`
	LastTemplateBuildError                         string `json:"last_template_build_error,omitempty"`
	LastTemplateBuildBlocking                      bool   `json:"last_template_build_error_blocking"`
	LastTemplateInvalidationAtUnixMs               int64  `json:"last_template_invalidation_at_unix_ms,omitempty"`
	LastTemplateInvalidationSeq                    uint64 `json:"last_template_invalidation_seq,omitempty"`
	LastTemplateInvalidationCause                  string `json:"last_template_invalidation_cause,omitempty"`
	LastTemplateInvalidationReason                 string `json:"last_template_invalidation_reason,omitempty"`
	LastTemplateInvalidationSubmitObsoleteDepth    int64  `json:"last_template_invalidation_submit_obsolete_depth,omitempty"`
	PendingTemplateBuild                           bool   `json:"pending_template_build"`
	PendingTemplateBuildReason                     string `json:"pending_template_build_reason,omitempty"`
	PendingTemplateBuildPriority                   int    `json:"pending_template_build_priority,omitempty"`
	PendingTemplateInvalidationCause               string `json:"pending_template_invalidation_cause,omitempty"`
	PendingTemplateInvalidationSubmitObsoleteDepth int64  `json:"pending_template_invalidation_submit_obsolete_depth,omitempty"`

	P2PCurrent                  bool   `json:"p2p_current"`
	P2PNearlySynced             bool   `json:"p2p_nearly_synced"`
	P2PFreshnessMaxAgeMs        int64  `json:"p2p_freshness_max_age_ms"`
	P2PPeerLeadToleranceBlocks  uint64 `json:"p2p_peer_lead_tolerance_blocks"`
	P2PConsensusPeerCount       int    `json:"p2p_consensus_peer_count"`
	P2PFreshConsensusPeerCount  int    `json:"p2p_fresh_consensus_peer_count"`
	P2PStaleConsensusPeerCount  int    `json:"p2p_stale_consensus_peer_count"`
	P2PBestPeerMainOrder        uint64 `json:"p2p_best_peer_main_order"`
	P2PBestPeerLeadBlocks       int64  `json:"p2p_best_peer_lead_blocks"`
	P2PBestPeerGraphStateAgeMs  int64  `json:"p2p_best_peer_graph_state_age_ms"`
	P2PAvgGraphStateAgeMs       int64  `json:"p2p_avg_graph_state_age_ms"`
	P2PMaxGraphStateAgeMs       int64  `json:"p2p_max_graph_state_age_ms"`
	P2PSyncPeerPresent          bool   `json:"p2p_sync_peer_present"`
	P2PSyncPeerFresh            bool   `json:"p2p_sync_peer_fresh"`
	P2PSyncPeerGraphStateAgeMs  int64  `json:"p2p_sync_peer_graph_state_age_ms"`
	P2PMiningFresh              bool   `json:"p2p_mining_fresh"`
	P2PMiningFreshReasonCode    string `json:"p2p_mining_fresh_reason_code"`
	SyncAllowed                 bool   `json:"sync_allowed"`
	SyncReasonCode              string `json:"sync_reason_code"`
	SyncReason                  string `json:"sync_reason"`
	ChainCurrent                bool   `json:"chain_current"`
	MainOrder                   uint64 `json:"main_order"`
	TemplateBackendSyncing      bool   `json:"template_backend_syncing"`
	CanonicalMiningBackendReady bool   `json:"canonical_mining_backend_ready"`
	CanonicalMiningBypassActive bool   `json:"canonical_mining_bypass_active"`
	SelfAdd                     int64  `json:"self_add"`
	SubmitNoSynced              bool   `json:"submit_no_synced"`
	NearlySyncMining            bool   `json:"nearly_sync_mining"`
}

type MinerInfoResult struct {
	Type          string `json:"type"`
	Pow           string `json:"pow"`
	Running       bool   `json:"running"`
	Coinbase      string `json:"coinbase"`
	CoinbaseFlags string `json:"coinbaseflags"`
	Height        uint64 `json:"height"`
	Difficulty    string `json:"difficulty"`
	Target        string `json:"target"`
	Timestamp     string `json:"timestamp"`
	TotalSubmit   int    `json:"totalsubmit"`
	SuccessSubmit int    `json:"successsubmit"`
}

// RemoteGBTResult is the payload delivered to remote GBT callers and to
// subscribers of the "blockTemplate" WebSocket stream. It carries exactly the
// fields needed to build a Stratum job without having to re-issue a
// getBlockTemplate HTTP call, plus a monotonically increasing Seq that allows
// subscribers to detect reordering or lost pushes.
type RemoteGBTResult struct {
	// Stream-ordering metadata. Seq is monotonically increasing per node
	// process and increments on every template emission.
	Seq uint64 `json:"seq"`

	// Legacy remote-GBT payload (kept for backwards compatibility).
	HeaderHex    string   `json:"headerhex,omitempty"`
	TxMerklePath []string `json:"txmerklepath,omitempty"`

	// Template fields. These mirror the subset of GetBlockTemplateResult a
	// stratum pool needs to construct a Stratum "mining.notify" job without
	// touching the full transaction list.
	Version         uint32 `json:"version"`
	PreviousHash    string `json:"previousblockhash"`
	TxRoot          string `json:"txroot"`
	StateRoot       string `json:"stateroot"`
	NBits           string `json:"nbits"`
	CurTime         int64  `json:"curtime"`
	Height          int64  `json:"height"`
	CoinbaseAddress string `json:"coinbase_address"`
	Reward          uint64 `json:"reward"`
	BlockFees       int64  `json:"block_fees"`
}
