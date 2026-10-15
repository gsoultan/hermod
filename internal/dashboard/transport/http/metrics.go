// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

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
