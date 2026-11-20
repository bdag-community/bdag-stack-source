package mining

import "github.com/ethereum/go-ethereum/metrics"

var (
	templateBuildTotalDuration        = metrics.NewRegisteredTimer("mining/templateBuildTotalDuration", nil)
	templateBuildPendingDuration      = metrics.NewRegisteredTimer("mining/templatePendingDuration", nil)
	templateBuildMerkleDuration       = metrics.NewRegisteredTimer("mining/templateMerkleDuration", nil)
	templateBuildStateRootDuration    = metrics.NewRegisteredTimer("mining/templateStateRootDuration", nil)
	templateBuildCheckConnectDuration = metrics.NewRegisteredTimer("mining/templateCheckConnectDuration", nil)
)
