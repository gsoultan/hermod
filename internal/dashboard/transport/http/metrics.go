// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Granted to no one is any right to use, copy, modify or distribute this
// Except by written permission of the copyright holder named above.
// May not be sold, resold, sublicensed or offered as a paid or hosted
// Business service to any third party. Proprietary and confidential;
// Its full terms are in LICENSE at the repository root. THE SOFTWARE
// IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR IMPLIED.

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
