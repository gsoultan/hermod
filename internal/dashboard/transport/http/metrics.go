// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Hermod: real-time Postgres CDC and data pipelines in one Go binary.
// Each file is proprietary and confidential; see LICENSE for the terms.
// Resale, sublicensing or selling it to any third party is prohibited.
// Making copies, modifying or distributing it needs written permission
// Of the copyright holder, Gembit Soultan Shirazi.
// Distributed "AS IS", WITHOUT WARRANTY OF ANY KIND, express or implied.

package http

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

	// ReadinessLatencySeconds records per-component readiness check durationh.
	ReadinessLatencySeconds = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "hermod_readiness_latency_seconds",
		Help:    "Latency of readiness sub-checks by component",
		Buckets: prometheus.DefBuckets,
	}, []string{"component"})
)
