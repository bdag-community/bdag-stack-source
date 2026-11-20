package miner

import (
	"time"

	ethcommon "github.com/ethereum/go-ethereum/common"
)

type templateBuildKind uint8

const (
	templateBuildMempool templateBuildKind = iota + 1
	templateBuildStall
	templateBuildBlockChain
	templateBuildSubmit
)

type templateBuildRequest struct {
	kind                 templateBuildKind
	reason               string
	force                bool
	ephemeralCoinbase    *ethcommon.Address
	invalidationCause    templateInvalidationCause
	invalidationRecorded bool
	submitObsoleteDepth  int64
}

func (r templateBuildRequest) priority() int {
	switch r.kind {
	case templateBuildSubmit:
		return 40
	case templateBuildBlockChain:
		return 30
	case templateBuildStall:
		return 20
	case templateBuildMempool:
		return 10
	default:
		return 0
	}
}

func (m *Miner) pendingTemplateBuildRequestSnapshot() (templateBuildRequest, bool) {
	m.templateReqMu.Lock()
	defer m.templateReqMu.Unlock()
	if m.pendingTemplateReq == nil {
		return templateBuildRequest{}, false
	}
	return *m.pendingTemplateReq, true
}

func mergeTemplateBuildRequests(current, next templateBuildRequest) templateBuildRequest {
	current = normalizeTemplateBuildRequest(current)
	next = normalizeTemplateBuildRequest(next)
	merged := current
	merged.force = current.force || next.force
	if next.ephemeralCoinbase != nil {
		merged.ephemeralCoinbase = next.ephemeralCoinbase
	}
	if next.priority() >= current.priority() {
		merged.kind = next.kind
		merged.reason = next.reason
		merged.invalidationCause = next.invalidationCause
		merged.invalidationRecorded = current.invalidationRecorded || next.invalidationRecorded
		merged.submitObsoleteDepth = next.submitObsoleteDepth
	}
	return merged
}

func (m *Miner) enqueueTemplateBuildRequest(req templateBuildRequest) {
	req = normalizeTemplateBuildRequest(req)
	var recordReq *templateBuildRequest

	m.templateReqMu.Lock()
	if m.templateReqChan == nil {
		m.templateReqChan = make(chan struct{}, 1)
	}
	if m.pendingTemplateReq == nil {
		if shouldRecordTemplateInvalidation(nil, req) {
			req.invalidationRecorded = true
			reqCopy := req
			recordReq = &reqCopy
		}
		reqCopy := req
		m.pendingTemplateReq = &reqCopy
	} else {
		if shouldRecordTemplateInvalidation(m.pendingTemplateReq, req) {
			req.invalidationRecorded = true
			reqCopy := req
			recordReq = &reqCopy
		}
		merged := mergeTemplateBuildRequests(*m.pendingTemplateReq, req)
		m.pendingTemplateReq = &merged
	}
	reqChan := m.templateReqChan
	m.templateReqMu.Unlock()

	if recordReq != nil {
		m.recordTemplateInvalidation(*recordReq, time.Now())
	}

	select {
	case reqChan <- struct{}{}:
	default:
	}
}

func (m *Miner) takeTemplateBuildRequest() (templateBuildRequest, bool) {
	m.templateReqMu.Lock()
	defer m.templateReqMu.Unlock()
	if m.pendingTemplateReq == nil {
		return templateBuildRequest{}, false
	}
	req := *m.pendingTemplateReq
	m.pendingTemplateReq = nil
	return req, true
}

func (m *Miner) takePendingTemplateBuildRequestAtLeast(priority int) (templateBuildRequest, bool) {
	m.templateReqMu.Lock()
	defer m.templateReqMu.Unlock()
	if m.pendingTemplateReq == nil || m.pendingTemplateReq.priority() < priority {
		return templateBuildRequest{}, false
	}
	req := *m.pendingTemplateReq
	m.pendingTemplateReq = nil
	select {
	case <-m.templateReqChan:
	default:
	}
	return req, true
}

func (m *Miner) applyPendingTemplateBuildBeforeGBT() (bool, error) {
	req, ok := m.takePendingTemplateBuildRequestAtLeast(templateBuildRequest{kind: templateBuildBlockChain}.priority())
	if !ok {
		return false, nil
	}
	err := m.applyTemplateBuildRequest(req)
	return err == nil, err
}

func (m *Miner) applyTemplateBuildRequest(req templateBuildRequest) error {
	req = normalizeTemplateBuildRequest(req)
	force := req.force
	if req.kind == templateBuildMempool && m.template != nil && len(m.template.Block.Transactions) <= 1 {
		// Recreate an empty transaction template on mempool activity so new
		// fee-bearing work is not held behind the normal template TTL.
		force = true
	}
	req.force = force
	return m.updateBlockTemplateForRequest(req)
}
