package Blockdag

import (
	"github.com/ethereum/go-ethereum/metrics"
)

var (
	mainOrderGauge  = metrics.NewRegisteredGauge("Blockdag/mainorder", nil)
	mainHeightGauge = metrics.NewRegisteredGauge("Blockdag/mainheight", nil)
	mainLayerGauge  = metrics.NewRegisteredGauge("Blockdag/mainlayer", nil)

	tipsTotalGauge   = metrics.NewRegisteredGauge("Blockdag/tips/total", nil)
	unsequencedGauge = metrics.NewRegisteredGauge("Blockdag/unsequenced", nil)
	reorganizeGauge  = metrics.NewRegisteredGauge("Blockdag/reorganize", nil)
)
