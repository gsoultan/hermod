package telemetry

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"

	"github.com/gsoultan/Hermod/pkg/comm/message"
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

	// MessagesDroppedNoTarget counts messages acknowledged to the source and
	// then discarded because a workflow that HAS sinks resolved none of them.
	// Every one of these is data that will not be delivered and is not in a
	// dead-letter queue, so any non-zero value is an incident.
	MessagesDroppedNoTarget = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "hermod_engine_messages_dropped_no_target_total",
		Help: "Messages acknowledged but delivered nowhere because no sink target resolved",
	}, []string{"workflow_id"})

	// MessageOverReleases mirrors message.OverReleaseCount. A message released
	// after its refcount reached zero is back in the pool while an owner still
	// holds it, so that owner reads whatever the pool refilled it with. The
	// symptom is duplicated and lost payloads with the totals still balancing —
	// no error, no dead-letter record. Any non-zero value is a correctness bug
	// and should page, not merely graph.
	//
	// A GaugeFunc reads the live counter at scrape time, so there is no polling
	// loop to keep in sync and no window where the exported value is stale.
	MessageOverReleases = promauto.NewGaugeFunc(prometheus.GaugeOpts{
		Name: "hermod_message_over_releases_total",
		Help: "Messages released after their reference count already reached zero (always a bug)",
	}, func() float64 { return float64(message.OverReleaseCount()) })

	// WorkerAdmissionRejected counts workflows a worker declined to start
	// because the host was above its resource threshold. This is deliberate load
	// shedding, but it is indistinguishable from "the platform is fine" unless
	// it is measured: the workflow simply never starts and only a warning is
	// logged. Alert when it is sustained.
	WorkerAdmissionRejected = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "hermod_worker_admission_rejected_total",
		Help: "Workflows not started because the worker was over its CPU/memory admission threshold",
	}, []string{"worker_id", "reason"})

	// SubSourceBackoff counts times an individual source inside a multi-source
	// workflow was held back after a read error. A source stuck in backoff is
	// delivering nothing while its workflow still reports healthy, because its
	// siblings are fine.
	SubSourceBackoff = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "hermod_engine_sub_source_backoff_total",
		Help: "Times a single source within a multi-source workflow was backed off after a read error",
	}, []string{"workflow_id", "source_id"})

	// TxGroupInDoubt reports transactions a transactional sink group has
	// prepared but not resolved.
	//
	// This is the most expensive state the system can be in. On PostgreSQL a
	// prepared transaction holds locks and blocks VACUUM cluster-wide — not
	// only for the table involved, and not only for Hermod — for as long as it
	// lasts. The documented backstop was a human remembering to run
	// `SELECT * FROM pg_prepared_xacts` against the destination, which is a
	// check nobody runs on a good day and therefore is not running on the bad
	// one.
	//
	// A gauge rather than a counter, and republished on every reaper sweep
	// including the sweeps that find nothing: a value only written when
	// something is wrong never comes back down, and the alert it drives cannot
	// clear.
	TxGroupInDoubt = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "hermod_txgroup_in_doubt",
		Help: "Transactions prepared by a transactional sink group and not yet resolved",
	}, []string{"workflow_id"})

	// TxGroupReaped counts transactions the reaper rolled back after they
	// passed their deadline. Non-zero means something failed to resolve its own
	// transaction and the backstop caught it — working as designed, and worth
	// knowing about.
	TxGroupReaped = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "hermod_txgroup_reaped_total",
		Help: "Transactions rolled back by the reaper after being left in doubt past their deadline",
	}, []string{"workflow_id"})

	// SinkUnmappedField counts fields a message carried that the sink's column
	// mappings do not cover, and which were therefore not written.
	//
	// This is how a source growing a column becomes visible. A mapped sink
	// writes only the columns it was told about, so a new field upstream is
	// read by nothing — the destination quietly stops matching the source while
	// every status stays green. Counted once per field per sink rather than per
	// message: a schema change is a standing condition, not an event per row.
	SinkUnmappedField = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "hermod_sink_unmapped_field_total",
		Help: "Message fields with no column mapping, which are not written to the destination",
	}, []string{"table", "field"})

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

	DeadLetterErrors = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "hermod_engine_dead_letter_errors_total",
		Help: "The total number of errors when writing to Dead Letter Sink",
	}, []string{"workflow_id", "sink_id"})
)
