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

	predictionSeconds = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "hermod_ml_prediction_duration_seconds",
		Help:    "How long a model server took to answer one prediction call.",
		Buckets: []float64{.005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5, 10, 30},
	}, []string{"vhost", "model"})
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
