package miner

import (
	"github.com/ethereum/go-ethereum/metrics"
)

var (
	totalGbtRequests    = metrics.NewRegisteredGauge("mining/totalGbtRequests", nil)
	totalGbts           = metrics.NewRegisteredGauge("mining/totalGbts", nil)
	totalSubmits        = metrics.NewRegisteredGauge("mining/totalSubmits", nil)
	totalTxEmptySubmits = metrics.NewRegisteredGauge("mining/totalTxEmptySubmits", nil)

	submitDuration        = metrics.NewRegisteredTimer("mining/submitDuration", nil)
	submitTxCount         = metrics.NewRegisteredGauge("mining/submitTxCount", nil)
	gbtRequestDuration    = metrics.NewRegisteredTimer("mining/gbtRequestDuration", nil)
	gbtDuration           = metrics.NewRegisteredTimer("mining/gbtDuration", nil)
	gbtResultDuration     = metrics.NewRegisteredTimer("mining/gbtResultDuration", nil)
	bdagPoolResetDuration = metrics.NewRegisteredTimer("mining/bdagPoolResetDuration", nil)
	mempoolEmptyDuration  = metrics.NewRegisteredTimer("mining/mempoolEmptyDuration", nil)

	gbtDownload = metrics.NewRegisteredGauge("mining/gbtDownload", nil)

	templateHealthAgeMs       = metrics.NewRegisteredGauge("mining/templateHealthAgeMs", nil)
	templateHealthSeq         = metrics.NewRegisteredGauge("mining/templateHealthSeq", nil)
	templateHealthMineable    = metrics.NewRegisteredGauge("mining/templateHealthMineable", nil)
	templateHealthSubmitReady = metrics.NewRegisteredGauge("mining/templateHealthSubmitReady", nil)

	templateInvalidationSeq                        = metrics.NewRegisteredGauge("mining/templateInvalidationSeq", nil)
	templateInvalidationParentChangedCounter       = metrics.NewRegisteredCounter("mining/templateInvalidation/parent_changed/total", nil)
	templateInvalidationStateChangedCounter        = metrics.NewRegisteredCounter("mining/templateInvalidation/state_changed/total", nil)
	templateInvalidationMempoolRootChangedCounter  = metrics.NewRegisteredCounter("mining/templateInvalidation/mempool_root_changed/total", nil)
	templateInvalidationSequenceObsoleteCounter    = metrics.NewRegisteredCounter("mining/templateInvalidation/template_sequence_obsolete/total", nil)
	templateInvalidationSubmitObsoleteDepthCounter = metrics.NewRegisteredCounter("mining/templateInvalidation/submit_obsolete_depth/total", nil)
	templateInvalidationSubmitObsoleteDepthGauge   = metrics.NewRegisteredGauge("mining/templateInvalidation/submit_obsolete_depth", nil)
	templateNonceDriftRecoveryCounter              = metrics.NewRegisteredCounter("mining/templateBuild/evm_pending_nonce_drift_recovery/total", nil)
	templateCanonicalSyncLatchBypassCounter        = metrics.NewRegisteredCounter("mining/templateBuild/canonical_sync_latch_bypass/total", nil)
)
