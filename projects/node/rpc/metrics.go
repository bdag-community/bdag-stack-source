package rpc

import "github.com/ethereum/go-ethereum/metrics"

var (
	rpcClientActiveMetric                  = metrics.NewRegisteredGauge("rpc/clients/active", nil)
	rpcClientCapacityMetric                = metrics.NewRegisteredGauge("rpc/clients/capacity", nil)
	rpcClientPerHostCapacityMetric         = metrics.NewRegisteredGauge("rpc/clients/perhost/capacity", nil)
	rpcClientCriticalReserveCapacityMetric = metrics.NewRegisteredGauge("rpc/clients/criticalreserve/capacity", nil)
	rpcClassificationActiveMetric          = metrics.NewRegisteredGauge("rpc/classification/active", nil)
	rpcClassificationCapacityMetric        = metrics.NewRegisteredGauge("rpc/classification/capacity", nil)
	rpcClientRejectMetric                  = metrics.NewRegisteredCounter("rpc/clients/rejected", nil)
	rpcNormalRequestActiveMetric           = metrics.NewRegisteredGauge("rpc/requests/normal/active", nil)
	rpcNormalRequestCapacityMetric         = metrics.NewRegisteredGauge("rpc/requests/normal/capacity", nil)
	rpcCriticalRequestActiveMetric         = metrics.NewRegisteredGauge("rpc/requests/critical/active", nil)
	rpcCriticalRequestCapacityMetric       = metrics.NewRegisteredGauge("rpc/requests/critical/capacity", nil)
	rpcRequestRejectMetric                 = metrics.NewRegisteredCounter("rpc/requests/rejected", nil)
)
