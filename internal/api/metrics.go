package api

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	// ReadinessStatus is 1 when a component is healthy, 0 otherwise.
	ReadinessStatus = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "hermod_readiness_status",
		Help: "Readiness status of Hermod components (1=ok, 0=error)",
	}, []string{"component"})

	// ReadinessLatencySeconds records per-component readiness check durations.
	ReadinessLatencySeconds = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "hermod_readiness_latency_seconds",
		Help:    "Latency of readiness sub-checks by component",
		Buckets: prometheus.DefBuckets,
	}, []string{"component"})
)
