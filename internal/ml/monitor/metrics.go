// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Hermod: real-time Postgres CDC and data pipelines in one Go binary.
// Each file is proprietary and confidential; see LICENSE for the terms.
// Resale, sublicensing or selling it to any third party is prohibited.
// Making copies, modifying or distributing it needs written permission
// Of the copyright holder, Gembit Soultan Shirazi.
// Distributed "AS IS", WITHOUT WARRANTY OF ANY KIND, express or implied.

package monitor

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// Why something the monitor was given never reached its end.
const (
	reasonQueueFull   = "queue_full"   // Observe found the queue full
	reasonUnsupported = "unsupported"  // the log store cannot hold prediction logs
	reasonWriteFailed = "write_failed" // the log store refused the batch
)

// One series per vhost and model, as hermod_ml_predictions_total; drift adds
// the feature, of which a model has as many as it was trained on.
var (
	featureDrift = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "hermod_ml_feature_drift",
		Help: "Population stability index of a model feature's live values against its training split, for the last full window.",
	}, []string{"vhost", "model", "feature"})

	// A recommendation model's coverage and popular share, for the last full
	// window (recommendations.go).
	recommendationCoverage = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "hermod_ml_recommendation_coverage",
		Help: "Share of a recommendation model's catalogue it recommended at least once, for the last full window.",
	}, []string{"vhost", "model"})

	recommendationPopularShare = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "hermod_ml_recommendation_popular_share",
		Help: "Share of a recommendation model's calls answered with popular items because the person or item was unknown, for the last full window.",
	}, []string{"vhost", "model"})

	droppedTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "hermod_ml_monitor_dropped_total",
		Help: "Prediction calls not monitored (neither logged nor counted for drift) because the monitor's queue was full.",
	}, []string{"vhost", "model", "reason"})

	logsWrittenTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "hermod_ml_prediction_logs_written_total",
		Help: "Sampled predictions written to the prediction log.",
	}, []string{"vhost", "model"})

	logsDroppedTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "hermod_ml_prediction_logs_dropped_total",
		Help: "Sampled predictions that could not be written to the prediction log.",
	}, []string{"vhost", "model", "reason"})
)
