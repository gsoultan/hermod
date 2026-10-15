// Copyright (c) 2026 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Hermod: real-time Postgres CDC and data pipelines in one Go binary.
// Each file is proprietary and confidential; see LICENSE for the terms.
// Resale, sublicensing or selling it to any third party is prohibited.
// Making copies, modifying or distributing it needs written permission
// Of the copyright holder, Gembit Soultan Shirazi.
// Distributed "AS IS", WITHOUT WARRANTY OF ANY KIND, express or implied.

package ml

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"

	"github.com/gsoultan/hermod/internal/storage"
)

// One series per vhost and model: a vhost holds a handful of models, so the
// label set stays small. The rows and the outcome are what an operator asks
// about first — is the model answering, and how fast.
var (
	predictionsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "hermod_ml_predictions_total",
		Help: "Prediction calls to a model, by outcome.",
	}, []string{"vhost", "model", "outcome"})

	predictionRowsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "hermod_ml_prediction_rows_total",
		Help: "Rows sent to a model for prediction.",
	}, []string{"vhost", "model"})

	// The buckets below 5ms are for models scored in-process, which answer
	// in microseconds.
	predictionSeconds = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "hermod_ml_prediction_duration_seconds",
		Help:    "How long a model took to answer one prediction call, on its server or in-process.",
		Buckets: []float64{.0001, .00025, .0005, .001, .0025, .005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5, 10, 30},
	}, []string{"vhost", "model"})

	// For a model set to score in-process: calls scored in Hermod, and calls
	// handed to the worker because its graph or the rows could not be.
	scoringTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "hermod_ml_in_process_scoring_total",
		Help: "Prediction calls to a model set to score in-process, by where they were scored (in_process or fallback).",
	}, []string{"vhost", "model", "path"})

	// quotaRefusals counts what a vhost's ML quotas turned away, by quota.
	quotaRefusals = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "hermod_ml_quota_refusals_total",
		Help: "Requests refused because a vhost was at one of its ML quotas, by quota.",
	}, []string{"vhost", "quota"})
)

// Jobs: what was queued, how it ended and how long it ran, and how callbacks
// went. A queue that grows (submitted far ahead of finished) or callbacks
// that keep failing are what an operator looks at first.
var (
	jobsSubmitted = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "hermod_ml_jobs_submitted_total",
		Help: "ML jobs queued, by kind (predict, recommend or train).",
	}, []string{"vhost", "kind"})

	jobsFinished = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "hermod_ml_jobs_finished_total",
		Help: "ML jobs that ended, by kind and status (succeeded, failed or cancelled).",
	}, []string{"vhost", "kind", "status"})

	jobSeconds = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "hermod_ml_job_duration_seconds",
		Help:    "How long an ML job ran on the Hermod that finished it.",
		Buckets: []float64{.1, .5, 1, 5, 15, 60, 300, 900, 1800, 3600},
	}, []string{"kind"})

	callbacksTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "hermod_ml_job_callbacks_total",
		Help: "Deliveries of ML job callbacks, by outcome (delivered, retry or failed).",
	}, []string{"outcome"})
)

// recommendationsTotal counts a recommendation model's answers by what was
// asked (user or item) and what answered: the model, or popular items for a
// person or item it does not know.
var recommendationsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
	Name: "hermod_ml_recommendations_total",
	Help: "Recommendation calls answered, by what was asked (user or item) and what answered (model or popular).",
}, []string{"vhost", "model", "kind", "source"})

func countRecommendation(m storage.MLModel, forUser, known bool) {
	kind, source := "item", "model"
	if forUser {
		kind = "user"
	}
	if !known {
		source = "popular"
	}
	recommendationsTotal.WithLabelValues(m.VHost, m.Name, kind, source).Inc()
}

func observe(vhost, model string, rows int, took time.Duration, err error) {
	outcome := "ok"
	if err != nil {
		outcome = "error"
	}
	predictionsTotal.WithLabelValues(vhost, model, outcome).Inc()
	predictionRowsTotal.WithLabelValues(vhost, model).Add(float64(rows))
	predictionSeconds.WithLabelValues(vhost, model).Observe(took.Seconds())
}
