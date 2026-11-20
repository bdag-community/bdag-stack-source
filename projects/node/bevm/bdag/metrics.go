package bdag

import "github.com/ethereum/go-ethereum/metrics"

var (
	bdagPoolTemplateUpdateDuration = metrics.NewRegisteredTimer("bdagpool/templateUpdateDuration", nil)
	bdagPoolTemplateCommitDuration = metrics.NewRegisteredTimer("bdagpool/templateCommitDuration", nil)
)
