package gateway

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	requestCount = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "gateway_requests_total",
			Help: "Total number of HTTP requests handled by the gateway.",
		},
		[]string{"method"},
	)

	errorCount = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "gateway_errors_total",
			Help: "Total number of gateway request errors.",
		},
		[]string{"method"},
	)

	busyRejectCount = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "gateway_busy_rejects_total",
			Help: "Total number of gateway requests rejected because concurrency slots were exhausted.",
		},
		[]string{"class", "method"},
	)

	requestDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "gateway_request_duration_seconds",
			Help:    "Histogram of request processing durations.",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"method"},
	)
)
