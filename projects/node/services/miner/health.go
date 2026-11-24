package miner

import (
	"errors"
	"fmt"
	"strings"
	"time"

	bdagevm "github.com/BlockdagNetworkLabs/bdag/bevm/bdag"
	blockdag "github.com/BlockdagNetworkLabs/bdag/blockdag"
	"github.com/BlockdagNetworkLabs/bdag/consensus/model"
	"github.com/BlockdagNetworkLabs/bdag/core/json"
	"github.com/BlockdagNetworkLabs/bdag/core/types"
	"github.com/BlockdagNetworkLabs/bdag/params"
	"github.com/ethereum/go-ethereum/common"
)

const (
	templateHealthOK                    = string(json.MiningTemplateHealthOK)
	templateHealthMinerDisabled         = string(json.MiningTemplateHealthMinerDisabled)
	templateHealthMinerNotStarted       = string(json.MiningTemplateHealthMinerNotStarted)
	templateHealthMinerShutdown         = string(json.MiningTemplateHealthMinerShutdown)
	templateHealthTemplateUnavailable   = string(json.MiningTemplateHealthTemplateUnavailable)
	templateHealthNodeSyncing           = string(json.MiningTemplateHealthNodeSyncing)
	templateHealthP2PUnavailable        = string(json.MiningTemplateHealthP2PUnavailable)
	templateHealthChainUnavailable      = string(json.MiningTemplateHealthChainUnavailable)
	templateHealthTemplateBuildFailed   = string(json.MiningTemplateHealthTemplateBuildFailed)
	templateHealthEVMPendingUnavailable = string(json.MiningTemplateHealthEVMPendingUnavailable)
	templateHealthEVMPendingNonceDrift  = string(json.MiningTemplateHealthEVMPendingNonceDrift)
	templateHealthTemplateExpired       = string(json.MiningTemplateHealthTemplateExpired)
	templateHealthTemplateStaleParent   = string(json.MiningTemplateHealthTemplateStaleParent)
	templateHealthWorkerUnavailable     = string(json.MiningTemplateHealthWorkerUnavailable)
	templateHealthWorkerStopped         = string(json.MiningTemplateHealthWorkerStopped)
	templateHealthMempoolNotReady       = string(json.MiningTemplateHealthMempoolNotReady)
	templateHealthTimeUpdateFailed      = string(json.MiningTemplateHealthTimeUpdateFailed)
)

type templateUsability struct {
	available  bool
	usable     bool
	ageMs      int64
	maxAgeMs   int64
	reasonCode string
	reason     string
}

type miningRPCReadiness struct {
	allowed                bool
	reasonCode             string
	reason                 string
	submitNoSynced         bool
	nearlySyncMining       bool
	mainOrder              uint64
	chainCurrent           bool
	p2pAvailable           bool
	p2pCurrent             bool
	p2pNearlySynced        bool
	p2pFreshObserved       bool
	p2pMiningFresh         bool
	p2pFreshReason         string
	p2pFreshness           model.P2PMiningFreshnessSnapshot
	templateBackendSyncing bool
	canonicalBackendReady  bool
	canonicalBypassActive  bool
	// buildTemplateReady remains true when only the last generation failed.
	// That failure blocks publication of the retained template, while a
	// replacement build must still be allowed to repair the generation.
	buildTemplateReady    bool
	generationPublishHold bool
	getTemplateReady      bool
	getTemplateReason     string
	getTemplateCode       string
}

func evaluateMiningRPCReadiness(submitNoSynced, allowNearlySynced bool, mainOrder uint64, chainCurrent bool, p2pAvailable, p2pCurrent, p2pNearlySynced, p2pFreshObserved, p2pMiningFresh bool, p2pFreshReason string) miningRPCReadiness {
	result := miningRPCReadiness{
		allowed:            true,
		reasonCode:         templateHealthOK,
		reason:             "current",
		submitNoSynced:     submitNoSynced,
		nearlySyncMining:   allowNearlySynced,
		mainOrder:          mainOrder,
		chainCurrent:       chainCurrent,
		p2pAvailable:       p2pAvailable,
		p2pCurrent:         p2pCurrent,
		p2pNearlySynced:    p2pNearlySynced,
		p2pFreshObserved:   p2pFreshObserved,
		p2pMiningFresh:     p2pMiningFresh,
		p2pFreshReason:     p2pFreshReason,
		getTemplateReady:   true,
		buildTemplateReady: true,
		getTemplateCode:    templateHealthOK,
		getTemplateReason:  "getBlockTemplate sync preflight is ready",
	}
	if mainOrder == 0 && !p2pAvailable {
		result.reason = "chain bootstrap height permits mining"
		result.getTemplateReason = result.reason
		return result
	}
	if !p2pAvailable {
		result.allowed = false
		result.getTemplateReady = false
		result.reasonCode = templateHealthP2PUnavailable
		result.getTemplateCode = templateHealthP2PUnavailable
		result.reason = "p2p service is unavailable"
		result.getTemplateReason = result.reason
		return result
	}
	if mainOrder == 0 && p2pCurrent {
		result.reason = "chain bootstrap height permits mining"
		result.getTemplateReason = result.reason
		return result
	}

	submitAllowed := miningSyncAllowed(submitNoSynced, allowNearlySynced, p2pCurrent, p2pNearlySynced, chainCurrent, p2pFreshObserved, p2pMiningFresh, p2pFreshReason)
	templateAllowed := miningTemplateSyncAllowed(p2pCurrent, chainCurrent, p2pFreshObserved, p2pMiningFresh, p2pFreshReason)
	if submitAllowed {
		if !p2pCurrent && p2pMiningFreshnessAllowsMining(chainCurrent, p2pFreshObserved, p2pMiningFresh, p2pFreshReason) {
			reason := strings.TrimSpace(p2pFreshReason)
			result.reason = "node is mining-fresh by peer freshness: " + reason
			result.getTemplateReason = result.reason
		} else if !p2pCurrent && allowNearlySynced && p2pNearlySynced {
			result.reason = "node is nearly synced and nearly-synced mining is enabled"
			result.getTemplateReason = result.reason
		} else if !p2pCurrent && submitNoSynced {
			result.reason = "submit-without-sync override is enabled and no stale p2p freshness was observed"
			result.getTemplateReason = result.reason
		}
	} else {
		result.allowed = false
		result.reasonCode = templateHealthNodeSyncing
		result.reason = "node is still syncing with peers"
	}

	if !templateAllowed {
		result.getTemplateReady = false
		result.getTemplateCode = templateHealthNodeSyncing
		result.getTemplateReason = "node is still syncing with peers"
		if !p2pCurrent && submitNoSynced {
			result.getTemplateReason = "node is still syncing with peers; submit-without-sync does not permit mining template creation"
		} else if !p2pCurrent && allowNearlySynced && p2pNearlySynced {
			result.getTemplateReason = "node is nearly synced, but mining template creation waits for current or mining-fresh peers"
		}
	}
	if !submitAllowed {
		result.getTemplateReason = result.reason
	}
	return result
}

func (m *Miner) miningRPCReadiness() miningRPCReadiness {
	return m.miningRPCReadinessAt(time.Now())
}

func (m *Miner) miningRPCReadinessAt(now time.Time) miningRPCReadiness {
	var submitNoSynced bool
	var allowNearlySynced bool
	if m != nil && m.cfg != nil {
		submitNoSynced = m.cfg.SubmitNoSynced
		allowNearlySynced = m.cfg.AllowMiningWhenNearlySynced
	}

	var mainOrder uint64
	var chainCurrent bool
	if m != nil && m.consensus != nil && m.consensus.BlockChain() != nil {
		bc := m.consensus.BlockChain()
		mainOrder = uint64(bc.GetMainOrder())
		if current, ok := bc.(interface{ IsCurrent() bool }); ok {
			chainCurrent = current.IsCurrent()
		}
	}

	var p2pAvailable bool
	var p2pCurrent bool
	var p2pNearlySynced bool
	var p2pFreshObserved bool
	var p2pFreshness model.P2PMiningFreshnessSnapshot
	if m != nil && m.p2pSer != nil {
		p2pAvailable = true
		p2pCurrent = m.p2pSer.IsCurrent()
		if !p2pCurrent && allowNearlySynced {
			p2pNearlySynced = m.p2pSer.IsNearlySynced()
		}
		p2pFreshness = m.p2pSer.MiningPeerFreshnessSnapshot(now)
		p2pFreshObserved = p2pMiningFreshnessObserved(p2pFreshness)
	}

	readiness := evaluateMiningRPCReadiness(
		submitNoSynced,
		allowNearlySynced,
		mainOrder,
		chainCurrent,
		p2pAvailable,
		p2pCurrent,
		p2pNearlySynced,
		p2pFreshObserved,
		p2pFreshness.MiningFresh,
		p2pFreshness.MiningFreshReasonCode,
	)
	// Generation failures are publication holds. Copy the ordinary sync
	// readiness before applying that hold so the replacement build can still
	// run when all canonical, local-EVM, and peer conditions are healthy.
	readiness.buildTemplateReady = readiness.getTemplateReady
	readiness.templateBackendSyncing = m.templateBackendSyncing()
	readiness.canonicalBackendReady = m.canonicalMiningBackendReady()
	readiness.canonicalBypassActive = canonicalMiningBypassAllowed(
		readiness.templateBackendSyncing, chainCurrent, p2pFreshness, readiness.canonicalBackendReady,
	)
	if readiness.templateBackendSyncing && !readiness.canonicalBypassActive {
		readiness.getTemplateReady = false
		readiness.buildTemplateReady = false
		readiness.getTemplateCode = templateHealthNodeSyncing
		readiness.getTemplateReason = "node pending-template backend is syncing"
		if !readiness.allowed {
			readiness.reasonCode = templateHealthNodeSyncing
			readiness.reason = readiness.getTemplateReason
		} else {
			readiness.reason = "current template can accept submissions while pending-template backend is syncing"
		}
	}
	readiness.p2pFreshness = p2pFreshness
	if held, reason := m.localEVMRecoveryStatus(); held {
		readiness.allowed = false
		readiness.getTemplateReady = false
		readiness.buildTemplateReady = false
		readiness.canonicalBackendReady = false
		readiness.canonicalBypassActive = false
		readiness.reasonCode = templateHealthNodeSyncing
		readiness.getTemplateCode = templateHealthNodeSyncing
		readiness.reason = reason
		readiness.getTemplateReason = reason
	}
	if m != nil {
		if err := m.gbtGenerationError(); err != nil {
			readiness.generationPublishHold = true
			readiness.getTemplateReady = false
			if readiness.buildTemplateReady {
				readiness.getTemplateCode = templateHealthTemplateBuildFailed
				readiness.getTemplateReason = "template generation failed: " + err.Error()
			}
		}
	}
	return readiness
}

func (m *Miner) localEVMRecoveryStatus() (bool, string) {
	if m == nil || m.consensus == nil {
		return false, ""
	}
	if provider, ok := m.consensus.BlockChain().(interface{ UpgradeReconciliationStatus() (bool, string) }); ok {
		if held, reason := provider.UpgradeReconciliationStatus(); held {
			return true, reason
		}
	}
	provider, ok := m.consensus.BlockChain().(model.LocalEVMRecoveryProvider)
	if !ok {
		return false, ""
	}
	return provider.LocalEVMRecoveryStatus()
}

func (m *Miner) templateBackendSyncing() bool {
	if m == nil || m.bdagPool == nil {
		return false
	}
	syncer, ok := m.bdagPool.(syncingPendingTemplateResetter)
	return ok && syncer.IsSyncing()
}

func (m *Miner) canonicalMiningBackendReady() bool {
	if m == nil || m.bdagPool == nil {
		return false
	}
	resetter, ok := m.bdagPool.(canonicalMiningPendingTemplateResetter)
	return ok && resetter.CanonicalMiningTemplateReady()
}

func canonicalMiningBypassAllowed(backendSyncing, chainCurrent bool, freshness model.P2PMiningFreshnessSnapshot, backendReady bool) bool {
	return backendSyncing && backendReady && freshness.AllowsMining(chainCurrent)
}

func p2pMiningFreshnessObserved(snapshot model.P2PMiningFreshnessSnapshot) bool {
	return snapshot.Observed()
}

func (r miningRPCReadiness) rpcInitialDownloadMessage() string {
	if r.reason != "" {
		return "Blockdag is downloading blocks: " + r.reason
	}
	return "Blockdag is downloading blocks..."
}

func (m *Miner) recordTemplateBuildSuccess(reason string, started time.Time) {
	m.templateHealthMu.Lock()
	defer m.templateHealthMu.Unlock()

	m.lastTemplateBuildAt = started
	m.lastTemplateBuildDuration = time.Since(started)
	m.lastTemplateBuildReason = reason
	m.lastTemplateBuildErrorCode = ""
	m.lastTemplateBuildErrorMessage = ""
}

func (m *Miner) recordTemplateBuildError(reason string, started time.Time, err error) {
	m.templateHealthMu.Lock()
	defer m.templateHealthMu.Unlock()

	m.lastTemplateBuildAt = started
	m.lastTemplateBuildDuration = time.Since(started)
	m.lastTemplateBuildReason = reason
	m.lastTemplateBuildErrorCode = classifyTemplateBuildError(err)
	if err != nil {
		m.lastTemplateBuildErrorMessage = err.Error()
	} else {
		m.lastTemplateBuildErrorMessage = ""
	}
}

func classifyTemplateBuildError(err error) string {
	if err == nil {
		return ""
	}
	msg := strings.ToLower(err.Error())
	var resetErr *bdagevm.PendingTemplateResetError
	switch {
	case isEVMPendingNonceDriftError(err):
		return templateHealthEVMPendingNonceDrift
	case errors.As(err, &resetErr):
		if resetErr.Code == bdagevm.PendingTemplateResetStopped {
			return templateHealthTemplateBuildFailed
		}
		return templateHealthEVMPendingUnavailable
	case strings.Contains(msg, "bdag pool stopped"):
		return templateHealthTemplateBuildFailed
	case strings.Contains(msg, "initial download"), strings.Contains(msg, "downloading blocks"):
		return templateHealthNodeSyncing
	case strings.Contains(msg, "bdagpool template"),
		strings.Contains(msg, "bdagpool reset"),
		strings.Contains(msg, "bdag pool syncing"),
		strings.Contains(msg, "evm pending"):
		return templateHealthEVMPendingUnavailable
	case strings.Contains(msg, "mempool"), strings.Contains(msg, "downloading tx"):
		return templateHealthMempoolNotReady
	case strings.Contains(msg, "time"):
		return templateHealthTimeUpdateFailed
	default:
		return templateHealthTemplateBuildFailed
	}
}

func miningTemplateMaxAge() time.Duration {
	maxAge := params.ActiveNetParams.TargetTimePerBlock * 3
	if maxAge < time.Minute {
		return time.Minute
	}
	return maxAge
}

func assessTemplateUsability(tpl *types.BlockTemplate, lastTemplate time.Time, now time.Time, maxAge time.Duration, currentParents []*common.Hash) templateUsability {
	if now.IsZero() {
		now = time.Now()
	}
	result := templateUsability{
		maxAgeMs:   maxAge.Milliseconds(),
		reasonCode: templateHealthOK,
		reason:     "template is usable",
	}
	if tpl == nil || tpl.Block == nil {
		result.reasonCode = templateHealthTemplateUnavailable
		result.reason = "no mining template is available"
		return result
	}
	result.available = true
	result.usable = true
	if !lastTemplate.IsZero() {
		age := now.Sub(lastTemplate)
		if age < 0 {
			age = 0
		}
		result.ageMs = age.Milliseconds()
		if maxAge > 0 && age > maxAge {
			result.usable = false
			result.reasonCode = templateHealthTemplateExpired
			result.reason = fmt.Sprintf("mining template age %s exceeds max age %s", age.Truncate(time.Millisecond), maxAge)
		}
	}
	if result.usable && len(currentParents) > 0 && !templateParentsMatch(tpl, currentParents) {
		result.usable = false
		result.reasonCode = templateHealthTemplateStaleParent
		result.reason = "mining template parents no longer match current mining tips"
	}
	return result
}

func templateParentsMatch(tpl *types.BlockTemplate, currentParents []*common.Hash) bool {
	if tpl == nil || tpl.Block == nil {
		return false
	}
	templateParents := tpl.Block.Parents
	if len(templateParents) == 0 && len(currentParents) == 0 {
		return true
	}
	// A nil parent is malformed input, not a zero hash. Fail closed before
	// either the ordered fast path or HashSet dereferences it.
	for _, parent := range templateParents {
		if parent == nil {
			return false
		}
	}
	for _, parent := range currentParents {
		if parent == nil {
			return false
		}
	}
	if len(templateParents) == len(currentParents) {
		orderedMatch := true
		for i, templateParent := range templateParents {
			if templateParent.Cmp(*currentParents[i]) != 0 {
				orderedMatch = false
				break
			}
		}
		if orderedMatch {
			return true
		}
	}
	parentsSet := blockdag.NewHashSet()
	parentsSet.AddList(currentParents)
	templateParentsSet := blockdag.NewHashSet()
	templateParentsSet.AddList(templateParents)
	return parentsSet.IsEqual(templateParentsSet)
}

func (m *Miner) buildTemplateHealthResult(now time.Time) *json.MiningTemplateHealthResult {
	readiness := m.miningRPCReadinessAt(now)
	result := &json.MiningTemplateHealthResult{
		CheckedAtUnixMs:             now.UnixMilli(),
		ReasonCode:                  templateHealthOK,
		Reason:                      "ready",
		GetBlockTemplateReady:       readiness.getTemplateReady,
		GetBlockTemplateReasonCode:  readiness.getTemplateCode,
		GetBlockTemplateReason:      readiness.getTemplateReason,
		SyncAllowed:                 readiness.allowed,
		SyncReasonCode:              readiness.reasonCode,
		SyncReason:                  readiness.reason,
		P2PCurrent:                  readiness.p2pCurrent,
		P2PNearlySynced:             readiness.p2pNearlySynced,
		MainOrder:                   readiness.mainOrder,
		SubmitNoSynced:              readiness.submitNoSynced,
		NearlySyncMining:            readiness.nearlySyncMining,
		TemplateBackendSyncing:      readiness.templateBackendSyncing,
		CanonicalMiningBackendReady: readiness.canonicalBackendReady,
		CanonicalMiningBypassActive: readiness.canonicalBypassActive,
	}
	var currentParents []*common.Hash
	if m.consensus != nil && m.consensus.BlockChain() != nil {
		currentParents = m.consensus.BlockChain().GetMiningTips(blockdag.MaxPriority)
	}
	templateState := assessTemplateUsability(m.template, m.lastTemplate, now, miningTemplateMaxAge(), currentParents)
	result.TemplateUsable = templateState.usable
	result.TemplateMaxAgeMs = templateState.maxAgeMs
	result.LastGoodTemplateAvailable = templateState.available
	result.LastGoodTemplateAgeMs = templateState.ageMs

	if m.cfg != nil {
		result.Enabled = m.cfg.Miner
	}
	result.Started = m.IsStarted()
	result.Shutdown = m.IsShutdown()
	if result.Shutdown {
		result.GetBlockTemplateReady = false
		result.GetBlockTemplateReasonCode = templateHealthMinerShutdown
		result.GetBlockTemplateReason = "miner is shutting down"
	}

	if m.worker != nil {
		result.WorkerReady = true
		result.WorkerType = m.worker.GetType()
		result.WorkerRunning = m.worker.IsRunning()
	}

	if m.p2pSer != nil {
		freshness := readiness.p2pFreshness
		result.P2PFreshnessMaxAgeMs = freshness.FreshnessMaxAge.Milliseconds()
		result.P2PPeerLeadToleranceBlocks = freshness.PeerLeadToleranceBlocks
		result.P2PConsensusPeerCount = freshness.ConsensusPeerCount
		result.P2PFreshConsensusPeerCount = freshness.FreshConsensusPeerCount
		result.P2PStaleConsensusPeerCount = freshness.StaleConsensusPeerCount
		result.P2PBestPeerMainOrder = freshness.BestPeerMainOrder
		result.P2PBestPeerLeadBlocks = freshness.BestPeerLeadBlocks
		result.P2PBestPeerGraphStateAgeMs = freshness.BestPeerGraphStateAge.Milliseconds()
		result.P2PAvgGraphStateAgeMs = freshness.AvgGraphStateAge.Milliseconds()
		result.P2PMaxGraphStateAgeMs = freshness.MaxGraphStateAge.Milliseconds()
		result.P2PSyncPeerPresent = freshness.SyncPeerPresent
		result.P2PSyncPeerFresh = freshness.SyncPeerFresh
		result.P2PSyncPeerGraphStateAgeMs = freshness.SyncPeerGraphStateAge.Milliseconds()
		result.P2PMiningFresh = freshness.MiningFresh
		result.P2PMiningFreshReasonCode = freshness.MiningFreshReasonCode
	}

	if m.consensus != nil {
		bc := m.BlockChain()
		result.ChainCurrent = bc.IsCurrent()
		result.MainOrder = uint64(bc.GetMainOrder())
		result.SelfAdd = bc.GetSelfAdd()
	}

	if m.template != nil && m.template.Block != nil {
		result.TemplateAvailable = true
		result.TemplateHeight = int64(m.template.Height)
		result.TemplateParentRoot = m.template.Block.Header.ParentRoot.String()
		result.TemplateTimestampUnix = m.template.Block.Header.Timestamp.Unix()
		result.TemplateTxCount = len(m.template.Block.Transactions)
		result.TemplateCoinbaseAddress = m.template.Block.Header.CoinbaseAddress.String()
		result.TemplateAgeMs = templateState.ageMs
	}
	result.LatestTemplateSeq = m.templateSeq.Load()

	m.templateHealthMu.RLock()
	result.LastTemplateBuildReason = m.lastTemplateBuildReason
	result.LastTemplateBuildErrorCode = m.lastTemplateBuildErrorCode
	result.LastTemplateBuildError = m.lastTemplateBuildErrorMessage
	result.LastTemplateInvalidationSeq = m.lastTemplateInvalidationSeq
	result.LastTemplateInvalidationCause = m.lastTemplateInvalidationCause.String()
	result.LastTemplateInvalidationReason = m.lastTemplateInvalidationReason
	result.LastTemplateInvalidationSubmitObsoleteDepth = m.lastTemplateInvalidationSubmitObsoleteDepth
	if !m.lastTemplateBuildAt.IsZero() {
		result.LastTemplateBuildAtUnixMs = m.lastTemplateBuildAt.UnixMilli()
		buildAge := now.Sub(m.lastTemplateBuildAt)
		if buildAge < 0 {
			buildAge = 0
		}
		result.LastTemplateBuildAgeMs = buildAge.Milliseconds()
	}
	if !m.lastTemplateInvalidationAt.IsZero() {
		result.LastTemplateInvalidationAtUnixMs = m.lastTemplateInvalidationAt.UnixMilli()
	}
	result.LastTemplateBuildDurationMs = m.lastTemplateBuildDuration.Milliseconds()
	m.templateHealthMu.RUnlock()
	result.LastTemplateBuildBlocking = result.LastTemplateBuildErrorCode != "" && !templateState.usable
	if pending, ok := m.pendingTemplateBuildRequestSnapshot(); ok {
		pending = normalizeTemplateBuildRequest(pending)
		result.PendingTemplateBuild = true
		result.PendingTemplateBuildReason = pending.reason
		result.PendingTemplateBuildPriority = pending.priority()
		result.PendingTemplateInvalidationCause = pending.invalidationCause.String()
		result.PendingTemplateInvalidationSubmitObsoleteDepth = pending.submitObsoleteDepth
	}

	cachedTemplateSubmitReady := result.SyncAllowed &&
		templateState.available &&
		templateState.usable &&
		!result.LastTemplateBuildBlocking &&
		result.WorkerReady &&
		result.WorkerRunning

	switch {
	case !result.Enabled:
		result.ReasonCode = templateHealthMinerDisabled
		result.Reason = "miner is disabled"
	case result.Shutdown:
		result.ReasonCode = templateHealthMinerShutdown
		result.Reason = "miner is shutting down"
	case !result.Started:
		result.ReasonCode = templateHealthMinerNotStarted
		result.Reason = "miner is not started"
	case templateState.available && !templateState.usable:
		// Prefer concrete parent/expiry failures over sync masking. Tip-chase
		// often coincides with parent changes; reporting node_syncing here made
		// the pool idle ASICs instead of invalidating+reissuing.
		result.ReasonCode = templateState.reasonCode
		result.Reason = templateState.reason
	case !result.GetBlockTemplateReady && !cachedTemplateSubmitReady:
		result.ReasonCode = result.GetBlockTemplateReasonCode
		result.Reason = result.GetBlockTemplateReason
	case !result.SyncAllowed:
		result.ReasonCode = result.SyncReasonCode
		result.Reason = result.SyncReason
	case result.LastTemplateBuildBlocking:
		result.ReasonCode = result.LastTemplateBuildErrorCode
		result.Reason = result.LastTemplateBuildError
	case !templateState.available:
		result.ReasonCode = templateState.reasonCode
		result.Reason = templateState.reason
	case !result.WorkerReady:
		result.ReasonCode = templateHealthWorkerUnavailable
		result.Reason = "mining worker is unavailable"
	case !result.WorkerRunning:
		result.ReasonCode = templateHealthWorkerStopped
		result.Reason = "mining worker is stopped"
	}

	result.MineableNow = result.ReasonCode == templateHealthOK && cachedTemplateSubmitReady
	result.SubmitReady = result.MineableNow

	templateHealthAgeMs.Update(result.TemplateAgeMs)
	templateHealthSeq.Update(int64(result.LatestTemplateSeq))
	templateHealthMineable.Update(boolGauge(result.MineableNow))
	templateHealthSubmitReady.Update(boolGauge(result.SubmitReady))
	templateInvalidationSeq.Update(int64(result.LastTemplateInvalidationSeq))

	return result
}

func boolGauge(v bool) int64 {
	if v {
		return 1
	}
	return 0
}
