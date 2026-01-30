package engine

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	MessagesProcessed = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "hermod_engine_messages_processed_total",
		Help: "The total number of processed messages",
	}, []string{"workflow_id", "source_id"})

	MessageErrors = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "hermod_engine_message_errors_total",
		Help: "The total number of message processing errors",
	}, []string{"workflow_id", "source_id", "stage"})

	SinkWriteCount = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "hermod_engine_sink_writes_total",
		Help: "The total number of successful sink writes",
	}, []string{"workflow_id", "sink_id"})

	SinkWriteErrors = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "hermod_engine_sink_write_errors_total",
		Help: "The total number of sink write errors",
	}, []string{"workflow_id", "sink_id"})

	ActiveEngines = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "hermod_engine_active_total",
		Help: "The total number of active engines",
	})

	ProcessingLatency = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "hermod_engine_processing_duration_seconds",
		Help:    "Time taken to process a message from source to sinks",
		Buckets: prometheus.DefBuckets,
	}, []string{"workflow_id"})

	DeadLetterCount = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "hermod_engine_dead_letter_total",
		Help: "The total number of messages sent to Dead Letter Sink",
	}, []string{"workflow_id", "sink_id"})

	WorkerSyncDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "hermod_worker_sync_duration_seconds",
		Help:    "Time taken for a worker sync cycle",
		Buckets: prometheus.DefBuckets,
	}, []string{"worker_id"})

	WorkerActiveWorkflows = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "hermod_worker_active_workflows_total",
		Help: "The number of active workflows managed by the worker",
	}, []string{"worker_id"})

	WorkerSyncErrors = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "hermod_worker_sync_errors_total",
		Help: "The total number of worker sync errors",
	}, []string{"worker_id"})

	WorkerLeasesOwned = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "hermod_worker_leases_owned_total",
		Help: "The number of workflow leases currently owned by the worker",
	}, []string{"worker_id"})

	LeaseAcquireTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "hermod_lease_acquire_total",
		Help: "Number of workflow leases successfully acquired",
	}, []string{"worker_id"})

	LeaseStealTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "hermod_lease_steal_total",
		Help: "Number of workflow leases stolen after TTL expiry",
	}, []string{"worker_id"})

	LeaseRenewErrorsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "hermod_lease_renew_errors_total",
		Help: "Number of errors while renewing workflow leases",
	}, []string{"worker_id"})

	WorkflowNodeProcessed = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "hermod_workflow_node_processed_total",
		Help: "The total number of messages processed by a workflow node",
	}, []string{"workflow_id", "node_id", "node_type"})

	WorkflowNodeErrors = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "hermod_workflow_node_errors_total",
		Help: "The total number of errors in a workflow node",
	}, []string{"workflow_id", "node_id", "node_type"})

	PostgresSlotLag = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "hermod_postgres_slot_lag_bytes",
		Help: "The replication lag in bytes for a Postgres slot",
	}, []string{"workflow_id", "slot_name"})

	// Idempotency metrics
	IdempotencyKeysTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "hermod_idempotency_keys_total",
		Help: "Total messages observed with an idempotency key",
	}, []string{"workflow_id"})

	IdempotencyMissingTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "hermod_idempotency_missing_total",
		Help: "Total messages missing an idempotency key",
	}, []string{"workflow_id"})

	IdempotencyDedupTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "hermod_idempotency_dedup_total",
		Help: "Total duplicate messages detected and skipped at sinks",
	}, []string{"workflow_id", "sink_id"})

	IdempotencyConflictsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "hermod_idempotency_conflicts_total",
		Help: "Total idempotency conflicts (e.g., key collision with differing payloads)",
	}, []string{"workflow_id", "sink_id"})

	IdempotencyLatency = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "hermod_idempotency_latency_seconds",
		Help:    "Latency added by idempotency checks per sink write",
		Buckets: prometheus.DefBuckets,
	}, []string{"workflow_id", "sink_id"})

	BackpressureDropTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "hermod_engine_backpressure_drop_total",
		Help: "Total messages dropped due to backpressure strategies",
	}, []string{"workflow_id", "sink_id", "strategy"})

	BackpressureSpillTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "hermod_engine_backpressure_spill_total",
		Help: "Total messages spilled to disk due to backpressure",
	}, []string{"workflow_id", "sink_id"})
)
