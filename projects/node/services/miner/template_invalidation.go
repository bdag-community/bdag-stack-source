package miner

import (
	"strconv"
	"strings"
	"time"
)

type templateInvalidationCause string

const (
	templateInvalidationParentChanged       templateInvalidationCause = "parent_changed"
	templateInvalidationStateChanged        templateInvalidationCause = "state_changed"
	templateInvalidationMempoolRootChanged  templateInvalidationCause = "mempool_root_changed"
	templateInvalidationSequenceObsolete    templateInvalidationCause = "template_sequence_obsolete"
	templateInvalidationSubmitObsoleteDepth templateInvalidationCause = "submit_obsolete_depth"
)

func (c templateInvalidationCause) String() string {
	return string(c)
}

func (c templateInvalidationCause) valid() bool {
	switch c {
	case templateInvalidationParentChanged,
		templateInvalidationStateChanged,
		templateInvalidationMempoolRootChanged,
		templateInvalidationSequenceObsolete,
		templateInvalidationSubmitObsoleteDepth:
		return true
	default:
		return false
	}
}

func normalizeTemplateBuildRequest(req templateBuildRequest) templateBuildRequest {
	if !req.invalidationCause.valid() {
		switch req.kind {
		case templateBuildBlockChain:
			req.invalidationCause = templateInvalidationParentChanged
		case templateBuildMempool:
			req.invalidationCause = templateInvalidationMempoolRootChanged
		case templateBuildStall:
			req.invalidationCause = templateInvalidationSequenceObsolete
		case templateBuildSubmit:
			req.invalidationCause = templateInvalidationSubmitObsoleteDepth
		default:
			if req.force {
				req.invalidationCause = templateInvalidationStateChanged
			}
		}
	}
	if req.reason == "" && req.invalidationCause.valid() {
		req.reason = req.invalidationCause.String()
	}
	return req
}

func shouldRecordTemplateInvalidation(current *templateBuildRequest, next templateBuildRequest) bool {
	if !next.invalidationCause.valid() {
		return false
	}
	if current == nil || !current.invalidationCause.valid() {
		return true
	}
	if next.priority() > current.priority() {
		return true
	}
	return next.priority() >= current.priority() && next.invalidationCause != current.invalidationCause
}

func (m *Miner) recordTemplateInvalidation(req templateBuildRequest, now time.Time) {
	req = normalizeTemplateBuildRequest(req)
	if !req.invalidationCause.valid() {
		return
	}
	if now.IsZero() {
		now = time.Now()
	}

	m.templateHealthMu.Lock()
	m.lastTemplateInvalidationSeq++
	seq := m.lastTemplateInvalidationSeq
	m.lastTemplateInvalidationCause = req.invalidationCause
	m.lastTemplateInvalidationReason = req.reason
	m.lastTemplateInvalidationAt = now
	m.lastTemplateInvalidationSubmitObsoleteDepth = req.submitObsoleteDepth
	m.templateHealthMu.Unlock()

	templateInvalidationSeq.Update(int64(seq))
	recordTemplateInvalidationMetric(req.invalidationCause)
	if req.invalidationCause == templateInvalidationSubmitObsoleteDepth {
		templateInvalidationSubmitObsoleteDepthGauge.Update(req.submitObsoleteDepth)
	}
}

func recordTemplateInvalidationMetric(cause templateInvalidationCause) {
	switch cause {
	case templateInvalidationParentChanged:
		templateInvalidationParentChangedCounter.Inc(1)
	case templateInvalidationStateChanged:
		templateInvalidationStateChangedCounter.Inc(1)
	case templateInvalidationMempoolRootChanged:
		templateInvalidationMempoolRootChangedCounter.Inc(1)
	case templateInvalidationSequenceObsolete:
		templateInvalidationSequenceObsoleteCounter.Inc(1)
	case templateInvalidationSubmitObsoleteDepth:
		templateInvalidationSubmitObsoleteDepthCounter.Inc(1)
	}
}

func (m *Miner) invalidateForSubmitObsoleteDepth(depth int64) {
	m.enqueueTemplateBuildRequest(templateBuildRequest{
		kind:                templateBuildSubmit,
		reason:              templateInvalidationSubmitObsoleteDepth.String(),
		force:               true,
		invalidationCause:   templateInvalidationSubmitObsoleteDepth,
		submitObsoleteDepth: depth,
	})
}

func obsoleteDepthFromError(err error) int64 {
	if err == nil {
		return 0
	}
	const marker = "Obsolete depth:"
	msg := err.Error()
	idx := strings.Index(msg, marker)
	if idx < 0 {
		return 0
	}
	rest := strings.TrimSpace(msg[idx+len(marker):])
	end := 0
	for end < len(rest) && rest[end] >= '0' && rest[end] <= '9' {
		end++
	}
	if end == 0 {
		return 0
	}
	depth, parseErr := strconv.ParseInt(rest[:end], 10, 64)
	if parseErr != nil {
		return 0
	}
	return depth
}
