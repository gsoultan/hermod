package engine

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	MessagesProcessed = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "hermod_engine_messages_processed_total",
		Help: "The total number of processed messages",
	}, []string{"connection_id", "source_id"})

	MessagesFiltered = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "hermod_engine_messages_filtered_total",
		Help: "The total number of messages filtered out by transformations",
	}, []string{"connection_id", "source_id"})

	MessageErrors = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "hermod_engine_message_errors_total",
		Help: "The total number of message processing errors",
	}, []string{"connection_id", "source_id", "stage"})

	SinkWriteCount = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "hermod_engine_sink_writes_total",
		Help: "The total number of successful sink writes",
	}, []string{"connection_id", "sink_id"})

	SinkWriteErrors = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "hermod_engine_sink_write_errors_total",
		Help: "The total number of sink write errors",
	}, []string{"connection_id", "sink_id"})

	ActiveEngines = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "hermod_engine_active_total",
		Help: "The total number of active engines",
	})

	ProcessingLatency = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "hermod_engine_processing_duration_seconds",
		Help:    "Time taken to process a message from source to sinks",
		Buckets: prometheus.DefBuckets,
	}, []string{"connection_id"})

	DeadLetterCount = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "hermod_engine_dead_letter_total",
		Help: "The total number of messages sent to Dead Letter Sink",
	}, []string{"connection_id", "sink_id"})

	WorkerSyncDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "hermod_worker_sync_duration_seconds",
		Help:    "Time taken for a worker sync cycle",
		Buckets: prometheus.DefBuckets,
	}, []string{"worker_id"})

	WorkerActiveConnections = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "hermod_worker_active_connections_total",
		Help: "The number of active connections managed by the worker",
	}, []string{"worker_id"})

	WorkerSyncErrors = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "hermod_worker_sync_errors_total",
		Help: "The total number of worker sync errors",
	}, []string{"worker_id"})
)
