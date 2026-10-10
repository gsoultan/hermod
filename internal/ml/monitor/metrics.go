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
