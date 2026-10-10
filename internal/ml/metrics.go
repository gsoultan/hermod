package ml

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
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
)

func observe(vhost, model string, rows int, took time.Duration, err error) {
	outcome := "ok"
	if err != nil {
		outcome = "error"
	}
	predictionsTotal.WithLabelValues(vhost, model, outcome).Inc()
	predictionRowsTotal.WithLabelValues(vhost, model).Add(float64(rows))
	predictionSeconds.WithLabelValues(vhost, model).Observe(took.Seconds())
}
