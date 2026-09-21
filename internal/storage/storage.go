package storage

import (
	"context"
	"errors"
	"time"

	"github.com/gsoultan/hermod"
)

var ErrNotFound = errors.New("not found")

type Log struct {
	ID         string    `json:"id"`
	Timestamp  time.Time `json:"timestamp"`
	Level      string    `json:"level"`
	Message    string    `json:"message"`
	Action     string    `json:"action,omitempty"`
	SourceID   string    `json:"source_id,omitempty"`
	SinkID     string    `json:"sink_id,omitempty"`
	WorkflowID string    `json:"workflow_id,omitempty"`
	UserID     string    `json:"user_id,omitempty"`
	Username   string    `json:"username,omitempty"`
	Data       string    `json:"data,omitempty"`
}

type CommonFilter struct {
	Page        int
	Limit       int
	Search      string
	VHost       string
	WorkspaceID string
	// Since and Until bound time-based queries (e.g., logs). Zero value means not set.
	Since time.Time `json:"since" omitzero:"true"`
	Until time.Time `json:"until" omitzero:"true"`

	// Workflow filters
	WorkerID string `json:"worker_id,omitempty"`
	OwnerID  string `json:"owner_id,omitempty"`
	Active   *bool  `json:"active,omitempty"`
}

type LogFilter struct {
	CommonFilter
	SourceID   string
	SinkID     string
	WorkflowID string
	Level      string
	Action     string
	// WithoutWorkflow limits matches to logs without a workflow_id (NULL).
	WithoutWorkflow bool
}

type Source struct {
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	Type        string            `json:"type"`
	VHost       string            `json:"vhost"`
	Active      bool              `json:"active"`
	Status      string            `json:"status,omitempty"`
	WorkerID    string            `json:"worker_id"`
	WorkspaceID string            `json:"workspace_id,omitempty"`
	Config      hermod.StringMap  `json:"config"`
	Sample      string            `json:"sample,omitempty"`
	State       map[string]string `json:"state" omitzero:"true"`
	// CreatedAt is the sort key the source list pages on. Storage stamps it
	// when a caller leaves it zero and never rewrites it on update, so it stays
	// the creation time rather than the last-touched time. See Workflow.CreatedAt.
	CreatedAt time.Time `json:"created_at" bson:"created_at"`
}

type Sink struct {
	ID          string           `json:"id"`
	Name        string           `json:"name"`
	Type        string           `json:"type"`
	VHost       string           `json:"vhost"`
	Active      bool             `json:"active"`
	Status      string           `json:"status,omitempty"`
	WorkerID    string           `json:"worker_id"`
	WorkspaceID string           `json:"workspace_id,omitempty"`
	Config      hermod.StringMap `json:"config"`
	// CreatedAt is the sort key the sink list pages on. Storage stamps it
	// when a caller leaves it zero and never rewrites it on update, so it stays
	// the creation time rather than the last-touched time. See Workflow.CreatedAt.
	CreatedAt time.Time `json:"created_at" bson:"created_at"`
}

type Transformation struct {
	Type   string         `json:"type"`
	Config map[string]any `json:"config"`
}

type WorkflowNode struct {
	ID        string         `json:"id"`
	Type      string         `json:"type"`             // source, sink, transformer, condition, etc.
	RefID     string         `json:"ref_id,omitempty"` // ID of the source, sink, or transformation
	Config    map[string]any `json:"config" omitzero:"true"`
	X         float64        `json:"x"`
	Y         float64        `json:"y"`
	UnitTests []UnitTest     `json:"unit_tests" omitzero:"true"`
}

type UnitTest struct {
	Name           string         `json:"name"`
	Input          map[string]any `json:"input"`
	ExpectedOutput map[string]any `json:"expected_output"`
	Description    string         `json:"description,omitempty"`
}

type WorkflowEdge struct {
	ID           string         `json:"id"`
	SourceID     string         `json:"source_id"`
	TargetID     string         `json:"target_id"`
	SourceHandle string         `json:"source_handle,omitempty"`
	TargetHandle string         `json:"target_handle,omitempty"`
	Config       map[string]any `json:"config" omitzero:"true"`
}

type WorkflowTier string

const (
	WorkflowTierHot  WorkflowTier = "Hot"
	WorkflowTierCold WorkflowTier = "Cold"
)

type Workflow struct {
	ID                string         `json:"id"`
	Name              string         `json:"name"`
	VHost             string         `json:"vhost"`
	Active            bool           `json:"active"`
	Status            string         `json:"status,omitempty"`
	WorkerID          string         `json:"worker_id"`
	OwnerID           string         `json:"owner_id,omitempty"`
	LeaseUntil        *time.Time     `json:"lease_until" omitzero:"true"`
	Nodes             []WorkflowNode `json:"nodes"`
	Edges             []WorkflowEdge `json:"edges"`
	DeadLetterSinkID  string         `json:"dead_letter_sink_id,omitempty"`
	PrioritizeDLQ     bool           `json:"prioritize_dlq,omitempty"`
	MaxRetries        int            `json:"max_retries,omitempty"`
	RetryInterval     string         `json:"retry_interval,omitempty"`
	ReconnectInterval string         `json:"reconnect_interval,omitempty"`
	DryRun            bool           `json:"dry_run,omitempty"`
	IdleTimeout       string         `json:"idle_timeout,omitempty"` // e.g. "5m"
	Tier              WorkflowTier   `json:"tier,omitempty"`
	// RetentionDays optionally overrides global log retention for this workflow.
	// nil means inherit the global setting; 0 means keep forever; >0 means days to retain.
	RetentionDays     *int     `json:"retention_days,omitempty"`
	SchemaType        string   `json:"schema_type,omitempty"`
	Schema            string   `json:"schema,omitempty"`
	Cron              string   `json:"cron,omitempty"`
	DLQThreshold      int      `json:"dlq_threshold,omitempty"`
	TraceSampleRate   float64  `json:"trace_sample_rate,omitempty"`
	Tags              []string `json:"tags" omitzero:"true"`
	TraceRetention    string   `json:"trace_retention,omitempty"` // e.g. "7d", "30d"
	AuditRetention    string   `json:"audit_retention,omitempty"` // e.g. "30d", "365d"
	WorkspaceID       string   `json:"workspace_id,omitempty"`
	CPURequest        float64  `json:"cpu_request,omitempty"`
	MemoryRequest     float64  `json:"memory_request,omitempty"`
	ThroughputRequest int      `json:"throughput_request,omitempty"`
	TotalProcessed    uint64   `json:"total_processed,omitempty"`
	TotalErrors       uint64   `json:"total_errors,omitempty"`
	TotalLag          uint64   `json:"total_lag,omitempty"`
	// CreatedAt is the sort key the workflow list pages on. Storage stamps it
	// when a caller leaves it zero and never rewrites it on update, so it stays
	// the creation time rather than the last-touched time. The bson tag is here
	// because the mongo backend writes snake_case keys explicitly.
	CreatedAt time.Time `json:"created_at" bson:"created_at"`
}

type WorkflowHealth struct {
	WorkflowID string        `json:"workflow_id"`
	Status     string        `json:"status"` // "healthy", "degraded", "error"
	Issues     []string      `json:"issues" omitzero:"true"`
	Uptime     time.Duration `json:"uptime"`
	Processed  uint64        `json:"processed"`
	Errors     uint64        `json:"errors"`
	Lag        uint64        `json:"lag"`
	MPS        float64       `json:"mps"`
	Latency    time.Duration `json:"latency"`
}

type Workspace struct {
	ID            string    `json:"id"`
	Name          string    `json:"name"`
	Description   string    `json:"description"`
	MaxWorkflows  int       `json:"max_workflows"`
	MaxCPU        float64   `json:"max_cpu"`
	MaxMemory     float64   `json:"max_memory"`
	MaxThroughput int       `json:"max_throughput"` // messages per second
	CreatedAt     time.Time `json:"created_at"`
}

type Worker struct {
	ID          string     `json:"id"`
	Name        string     `json:"name"`
	Host        string     `json:"host"`
	Port        int        `json:"port"`
	Description string     `json:"description"`
	Token       string     `json:"token"`
	LastSeen    *time.Time `json:"last_seen" omitzero:"true"`
	CPUUsage    float64    `json:"cpu_usage,omitempty"`
	MemoryUsage float64    `json:"memory_usage,omitempty"`
	// Draining is a transient flag (never persisted) used to signal a running
	// worker that the platform has requested a graceful shutdown. It is set on
	// API responses by the platform when an administrator triggers a shutdown.
	Draining bool `json:"draining,omitempty"`
	// CreatedAt is the sort key the worker list pages on. Storage stamps it when a
	// caller leaves it zero and never rewrites it on update. See Workflow.CreatedAt.
	CreatedAt time.Time `json:"created_at" bson:"created_at"`
}

type Role string

const (
	RoleAdministrator Role = "Administrator"
	RoleEditor        Role = "Editor"
	RoleViewer        Role = "Viewer"
)

type User struct {
	ID               string   `json:"id"`
	Username         string   `json:"username"`
	Password         string   `json:"password,omitempty"`
	FullName         string   `json:"full_name"`
	Email            string   `json:"email"`
	Role             Role     `json:"role"`
	VHosts           []string `json:"vhosts"`
	TwoFactorEnabled bool     `json:"two_factor_enabled"`
	TwoFactorSecret  string   `json:"two_factor_secret,omitempty"`
	// CreatedAt is the sort key the user list pages on. Storage stamps it when a
	// caller leaves it zero and never rewrites it on update. See Workflow.CreatedAt.
	CreatedAt time.Time `json:"created_at" bson:"created_at"`
}

type VHost struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	// CreatedAt is the sort key the vhost list pages on. Storage stamps it when a
	// caller leaves it zero and never rewrites it on update. See Workflow.CreatedAt.
	CreatedAt time.Time `json:"created_at" bson:"created_at"`
}

type AuditLog struct {
	ID         string    `json:"id"`
	Timestamp  time.Time `json:"timestamp"`
	UserID     string    `json:"user_id"`
	Username   string    `json:"username"`
	Action     string    `json:"action"`      // e.g., "CREATE", "UPDATE", "DELETE", "START", "STOP"
	EntityType string    `json:"entity_type"` // e.g., "workflow", "source", "sink", "user"
	EntityID   string    `json:"entity_id"`
	Payload    string    `json:"payload,omitempty"` // Details about the change (JSON)
	IP         string    `json:"ip,omitempty"`
}

type AuditFilter struct {
	CommonFilter
	UserID     string     `json:"user_id"`
	EntityType string     `json:"entity_type"`
	EntityID   string     `json:"entity_id"`
	Action     string     `json:"action"`
	From       *time.Time `json:"from" omitzero:"true"`
	To         *time.Time `json:"to" omitzero:"true"`
}

type WebhookRequest struct {
	ID        string            `json:"id"`
	Timestamp time.Time         `json:"timestamp" omitzero:"true"`
	Path      string            `json:"path"`
	Method    string            `json:"method"`
	Headers   map[string]string `json:"headers"`
	Body      []byte            `json:"body"`
}

type WebhookRequestFilter struct {
	CommonFilter
	Path string `json:"path"`
}

type FormSubmission struct {
	ID        string    `json:"id"`
	Timestamp time.Time `json:"timestamp" omitzero:"true"`
	Path      string    `json:"path"`
	Data      []byte    `json:"data"`
	Status    string    `json:"status"` // pending, processing, completed, failed
}

type FormSubmissionFilter struct {
	CommonFilter
	Path   string `json:"path"`
	Status string `json:"status"`
}

type Schema struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Version   int       `json:"version"`
	Type      string    `json:"type"`
	Content   string    `json:"content"`
	CreatedAt time.Time `json:"created_at" omitzero:"true"`
}

type Plugin struct {
	ID          string     `json:"id"`
	Name        string     `json:"name"`
	Description string     `json:"description"`
	Author      string     `json:"author"`
	Stars       int        `json:"stars"`
	Category    string     `json:"category"`
	Certified   bool       `json:"certified"`
	Type        string     `json:"type"` // WASM, Connector, Transformer
	WasmURL     string     `json:"wasm_url,omitempty"`
	Installed   bool       `json:"installed"`
	InstalledAt *time.Time `json:"installed_at" omitzero:"true"`
	// CreatedAt is the sort key the plugin list pages on. Storage stamps it when a
	// caller leaves it zero and never rewrites it on update. See Workflow.CreatedAt.
	CreatedAt time.Time `json:"created_at" bson:"created_at"`
}

type TraceStep = hermod.TraceStep

type MessageTrace struct {
	ID         string             `json:"id"`
	MessageID  string             `json:"message_id"`
	WorkflowID string             `json:"workflow_id"`
	Steps      []hermod.TraceStep `json:"steps"`
	CreatedAt  time.Time          `json:"created_at"`

	// Summary fields, carried on the parent row so the trace list never has to
	// touch the step table. Listing used to aggregate every step a workflow had
	// ever produced — measured on PostgreSQL 17, a sequential scan of 250k rows
	// and ~390 MB of I/O to return 25 of them — because "when did this message
	// start" was only derivable as MIN(timestamp) over its steps. It is now
	// written once, when the steps are.
	StepCount  int   `json:"step_count"`
	ErrorCount int   `json:"error_count"`
	DurationMs int64 `json:"duration_ms"`
}

// TraceFilter selects a page of message traces.
//
// Before is a keyset cursor: pass the CreatedAt of the last row you saw and the
// next page is an index seek, not a scan-and-discard. Offset remains for
// callers that page by number, and is cheap now that it walks one row per
// message rather than one per step — but it still reads and throws away
// everything it skips, so Before is the one to reach for.
type TraceFilter struct {
	Before time.Time
	Limit  int
	Offset int
}

type WorkflowVersion struct {
	ID             string         `json:"id"`
	WorkflowID     string         `json:"workflow_id"`
	Version        int            `json:"version"`
	Nodes          []WorkflowNode `json:"nodes"`
	Edges          []WorkflowEdge `json:"edges"`
	TraceRetention string         `json:"trace_retention,omitempty"`
	AuditRetention string         `json:"audit_retention,omitempty"`
	Config         string         `json:"config"` // JSON of other workflow settings
	CreatedAt      time.Time      `json:"created_at"`
	CreatedBy      string         `json:"created_by"`
	Message        string         `json:"message"` // commit message for the version
}

type OutboxItem = hermod.OutboxItem

type LineageEdge struct {
	SourceID     string `json:"source_id"`
	SourceName   string `json:"source_name"`
	SourceType   string `json:"source_type"`
	SinkID       string `json:"sink_id"`
	SinkName     string `json:"sink_name"`
	SinkType     string `json:"sink_type"`
	WorkflowID   string `json:"workflow_id"`
	WorkflowName string `json:"workflow_name"`
}

type Approval struct {
	ID             string            `json:"id"`
	WorkflowID     string            `json:"workflow_id"`
	NodeID         string            `json:"node_id"`
	MessageID      string            `json:"message_id"`
	Payload        []byte            `json:"payload"`
	Metadata       map[string]string `json:"metadata"`
	Data           map[string]any    `json:"data"`
	FormDefinition map[string]any    `json:"form_definition,omitempty"`
	FormData       map[string]any    `json:"form_data,omitempty"`
	Status         string            `json:"status"` // pending, approved, rejected
	CreatedAt      time.Time         `json:"created_at"`
	ProcessedAt    *time.Time        `json:"processed_at,omitempty"`
	ProcessedBy    string            `json:"processed_by,omitempty"`
	Notes          string            `json:"notes,omitempty"`
}

type ApprovalFilter struct {
	CommonFilter
	WorkflowID string
	Status     string
}

type SuspendedMessage struct {
	ID         string            `json:"id"`
	WorkflowID string            `json:"workflow_id"`
	NodeID     string            `json:"node_id"`
	Payload    []byte            `json:"payload"`
	Metadata   map[string]string `json:"metadata"`
	Data       map[string]any    `json:"data"`
	ResumeAt   time.Time         `json:"resume_at"`
	CreatedAt  time.Time         `json:"created_at"`
}

type DashboardStats struct {
	ActiveSources   int     `json:"active_sources"`
	ActiveSinks     int     `json:"active_sinks"`
	ActiveWorkflows int     `json:"active_workflows"`
	TotalProcessed  uint64  `json:"total_processed"`
	TotalErrors     uint64  `json:"total_errors"`
	TotalLag        uint64  `json:"total_lag"`
	FailedWorkflows int     `json:"failed_workflows"`
	Uptime          int64   `json:"uptime"`
	ActiveWorkers   int     `json:"active_workers"`
	TotalWorkflows  int     `json:"total_workflows"`
	TotalSources    int     `json:"total_sources"`
	TotalSinks      int     `json:"total_sinks"`
	Throughput      float64 `json:"throughput"` // Messages per second

	// Health signals. These answer "is the data moving well?", which the
	// counters above cannot: a pipeline with a rising TotalProcessed and a
	// wedged sink looks identical to a healthy one until someone reads the
	// logs.
	//
	// There is deliberately no DeadLetterCount here. TotalErrors is already
	// exactly that — flushStatsToStorage persists baseErrors +
	// StatusUpdate.DeadLetterCount into total_errors — and showing the same
	// number twice under two names is how a dashboard loses the reader's
	// trust.
	//
	// ErrorRate is derived from the persisted counters and so is cumulative
	// over the workflow's life. The other three are point-in-time readings of
	// engines running on this node, so they reset when an engine restarts and
	// read zero when nothing is running.
	ErrorRate           float64 `json:"error_rate"`     // Dead-lettered share of attempted messages, 0..1
	AvgLatencyMs        float64 `json:"avg_latency_ms"` // Mean end-to-end latency across running engines
	Backpressure        float64 `json:"backpressure"`   // Worst sink buffer fill, 0..1
	CircuitBreakersOpen int     `json:"circuit_breakers_open"`

	// A pending-approvals count is deliberately absent. The approvals table
	// has no vhost column and ListApprovals filters only on workflow and
	// status, so the only count available here is the global one — and vhost
	// is an authorization boundary (users carry a vhosts list), so showing it
	// on a single-tenant dashboard would tell one tenant about another's
	// queue. Adding it means a vhost-aware count on every backend.
}

// DashboardSample is one point of dashboard history.
//
// The throughput chart used to be built entirely in the browser: the page held
// the last thirty WebSocket readings in React state and threw them away on
// reload. That makes a reload indistinguishable from an outage — the chart
// restarts at zero either way — and means nobody can answer "was it like this
// an hour ago?". Persisting a sample per interval is what turns that sliver
// into a series the chart can be rebuilt from.
//
// It is deliberately a narrow projection of DashboardStats rather than the
// whole struct: configuration counts (TotalSources, TotalWorkflows) describe
// what exists rather than what happened, and storing them once per interval
// would be a lot of rows to say nothing. Only the fields that move over time
// are kept, and none of them are message payloads, so no customer data enters
// this table.
type DashboardSample struct {
	Timestamp       time.Time `json:"timestamp"`
	VHost           string    `json:"vhost"`
	Throughput      float64   `json:"throughput"`
	TotalProcessed  uint64    `json:"total_processed"`
	TotalErrors     uint64    `json:"total_errors"`
	TotalLag        uint64    `json:"total_lag"`
	ErrorRate       float64   `json:"error_rate"`
	AvgLatencyMs    float64   `json:"avg_latency_ms"`
	ActiveWorkflows int       `json:"active_workflows"`
	ActiveWorkers   int       `json:"active_workers"`
}

// NormalizeVHost maps the two spellings of "no vhost filter" onto one.
//
// The UI sends vhost=all for the unfiltered view while GetDashboardStats
// treats an empty string the same way. Storing history under both spellings
// would split one series into two, so every read and write of a sample goes
// through here.
func NormalizeVHost(vhost string) string {
	if vhost == "all" {
		return ""
	}
	return vhost
}

// TraceRetention describes one sweep of the message trace tables.
//
// A cutoff *per workflow*, where this used to be a single time.Time. Retention
// is a per-workflow setting, but the purge it drove was
// `DELETE FROM message_trace_steps WHERE timestamp < ?` with no workflow
// predicate, called from inside a loop over workflows. So the shortest window
// in the deployment decided what every workflow kept: one set to 7d deleted
// the traces of one set to 365d, and on PostgreSQL it dropped whole day
// partitions out from under it, which no VACUUM undoes. That same full-table
// delete also ran once per workflow per hour.
type TraceRetention struct {
	// Keep maps a workflow id to the oldest trace step it keeps.
	//
	// A workflow with no entry here keeps everything it has. Either no window
	// is configured or the configured one could not be parsed, and neither is
	// a licence to delete: an operator-supplied window has no safe default,
	// because a short one deletes traces nobody asked to lose and none at all
	// restores the growth bug this mechanism exists to prevent.
	Keep map[string]time.Time

	// Live is every workflow that exists, including those absent from Keep.
	//
	// Traces of a workflow that is not here are unreachable — the viewer
	// reaches a trace through its workflow, and ListMessageTraces takes a
	// workflow id — so they are swept regardless of age. Nothing else removes
	// them: DeleteWorkflow deletes one row.
	Live map[string]struct{}

	// LiveIsComplete reports whether Live really is every workflow there is.
	//
	// Not inferred from Live being non-empty, and not a detail. The caller
	// reads workflows through a paged list; on a deployment with more
	// workflows than it asked for, every workflow past the first page would
	// look like a deleted one and have its traces swept. When this is false
	// the orphan sweep is skipped and everything else still runs.
	LiveIsComplete bool
}

// PartitionFloor is the oldest moment no live workflow needs any more, and
// whether one exists at all.
//
// Only whole days below this may be reclaimed by dropping a partition, because
// a drop takes every workflow's rows for that day at once. A single live
// workflow that keeps everything therefore blocks partition dropping for the
// whole deployment — correctly: the alternative is destroying its traces to
// save space on someone else's.
func (r TraceRetention) PartitionFloor() (time.Time, bool) {
	if !r.LiveIsComplete {
		return time.Time{}, false
	}
	var floor time.Time
	for id := range r.Live {
		cutoff, ok := r.Keep[id]
		if !ok {
			return time.Time{}, false
		}
		if floor.IsZero() || cutoff.Before(floor) {
			floor = cutoff
		}
	}
	return floor, !floor.IsZero()
}

// Orphans returns the workflow ids in present that no longer exist, and
// whether the caller is entitled to act on the answer.
func (r TraceRetention) Orphans(present []string) ([]string, bool) {
	if !r.LiveIsComplete {
		return nil, false
	}
	var gone []string
	for _, id := range present {
		if _, alive := r.Live[id]; !alive {
			gone = append(gone, id)
		}
	}
	return gone, true
}

type Storage interface {
	// Init performs storage initialization/migrations and is safe to call multiple times.
	Init(ctx context.Context) error

	// Ping checks the connection to the storage.
	Ping(ctx context.Context) error

	ListSources(ctx context.Context, filter CommonFilter) ([]Source, int, error)
	CreateSource(ctx context.Context, src Source) error
	UpdateSource(ctx context.Context, src Source) error
	UpdateSourceStatus(ctx context.Context, id string, status string) error
	UpdateSourceState(ctx context.Context, id string, state map[string]string) error
	// UpdateSourceSample writes only the stored preview payload. Storing a
	// sample through UpdateSource carried every other column along with it,
	// from whatever copy of the source the caller happened to hold.
	UpdateSourceSample(ctx context.Context, id string, sample string) error
	DeleteSource(ctx context.Context, id string) error
	GetSource(ctx context.Context, id string) (Source, error)

	ListSinks(ctx context.Context, filter CommonFilter) ([]Sink, int, error)
	CreateSink(ctx context.Context, snk Sink) error
	UpdateSink(ctx context.Context, snk Sink) error

	// ReEncryptSecrets rewrites every stored credential under newKey. Key
	// rotation must call this *before* installing the new key: rotating
	// without it leaves every password and API key encrypted under a key the
	// process no longer has.
	ReEncryptSecrets(ctx context.Context, newKey string) error
	UpdateSinkStatus(ctx context.Context, id string, status string) error
	DeleteSink(ctx context.Context, id string) error
	GetSink(ctx context.Context, id string) (Sink, error)

	ListUsers(ctx context.Context, filter CommonFilter) ([]User, int, error)
	CreateUser(ctx context.Context, user User) error
	UpdateUser(ctx context.Context, user User) error
	DeleteUser(ctx context.Context, id string) error
	GetUser(ctx context.Context, id string) (User, error)
	GetUserByUsername(ctx context.Context, username string) (User, error)
	GetUserByEmail(ctx context.Context, email string) (User, error)

	ListVHosts(ctx context.Context, filter CommonFilter) ([]VHost, int, error)
	CreateVHost(ctx context.Context, vhost VHost) error
	UpdateVHost(ctx context.Context, vhost VHost) error
	DeleteVHost(ctx context.Context, id string) error
	GetVHost(ctx context.Context, id string) (VHost, error)

	ListWorkflows(ctx context.Context, filter CommonFilter) ([]Workflow, int, error)
	ListWorkspaces(ctx context.Context) ([]Workspace, error)
	CreateWorkspace(ctx context.Context, ws Workspace) error
	GetWorkspace(ctx context.Context, id string) (Workspace, error)
	DeleteWorkspace(ctx context.Context, id string) error
	CreateWorkflow(ctx context.Context, wf Workflow) error
	UpdateWorkflow(ctx context.Context, wf Workflow) error
	UpdateWorkflowStatus(ctx context.Context, id string, status string) error
	UpdateWorkflowStats(ctx context.Context, id string, processed, errors, lag uint64) error
	DeleteWorkflow(ctx context.Context, id string) error
	GetWorkflow(ctx context.Context, id string) (Workflow, error)

	// Lease-based ownership for workflows
	// AcquireWorkflowLease attempts to set owner_id and extend lease_until atomically with TTL seconds.
	// Returns true if the lease was acquired.
	AcquireWorkflowLease(ctx context.Context, workflowID, ownerID string, ttlSeconds int) (bool, error)
	// RenewWorkflowLease extends lease_until for an existing owner if not expired yet. Returns true if renewed.
	RenewWorkflowLease(ctx context.Context, workflowID, ownerID string, ttlSeconds int) (bool, error)
	// ReleaseWorkflowLease clears ownership if owned by ownerID.
	ReleaseWorkflowLease(ctx context.Context, workflowID, ownerID string) error

	ListWorkers(ctx context.Context, filter CommonFilter) ([]Worker, int, error)
	CreateWorker(ctx context.Context, worker Worker) error
	UpdateWorker(ctx context.Context, worker Worker) error
	UpdateWorkerHeartbeat(ctx context.Context, id string, cpu, mem float64) error
	DeleteWorker(ctx context.Context, id string) error
	GetWorker(ctx context.Context, id string) (Worker, error)

	ListLogs(ctx context.Context, filter LogFilter) ([]Log, int, error)
	CreateLog(ctx context.Context, log Log) error
	CreateLogs(ctx context.Context, logs []Log) error
	DeleteLogs(ctx context.Context, filter LogFilter) error
	PurgeLogs(ctx context.Context, before time.Time) error

	ListAuditLogs(ctx context.Context, filter AuditFilter) ([]AuditLog, int, error)
	CreateAuditLog(ctx context.Context, log AuditLog) error
	PurgeAuditLogs(ctx context.Context, before time.Time) error
	PurgeMessageTraces(ctx context.Context, retention TraceRetention) error
	DeleteWorkflowMessageTraces(ctx context.Context, workflowID string) error

	ListWebhookRequests(ctx context.Context, filter WebhookRequestFilter) ([]WebhookRequest, int, error)
	CreateWebhookRequest(ctx context.Context, req WebhookRequest) error
	GetWebhookRequest(ctx context.Context, id string) (WebhookRequest, error)
	DeleteWebhookRequests(ctx context.Context, filter WebhookRequestFilter) error

	CreateFormSubmission(ctx context.Context, sub FormSubmission) error
	ListFormSubmissions(ctx context.Context, filter FormSubmissionFilter) ([]FormSubmission, int, error)
	GetFormSubmission(ctx context.Context, id string) (FormSubmission, error)
	UpdateFormSubmissionStatus(ctx context.Context, id string, status string) error
	DeleteFormSubmissions(ctx context.Context, filter FormSubmissionFilter) error

	GetSetting(ctx context.Context, key string) (string, error)
	SaveSetting(ctx context.Context, key string, value string) error

	// Node State Management
	UpdateNodeState(ctx context.Context, workflowID, nodeID string, state any) error
	GetNodeStates(ctx context.Context, workflowID string) (map[string]any, error)

	// Schema Registry
	ListSchemas(ctx context.Context, name string) ([]Schema, error)
	ListAllSchemas(ctx context.Context) ([]Schema, error)
	GetSchema(ctx context.Context, name string, version int) (Schema, error)
	GetLatestSchema(ctx context.Context, name string) (Schema, error)
	CreateSchema(ctx context.Context, schema Schema) error

	// Message Tracing
	RecordTraceStep(ctx context.Context, workflowID, messageID string, step hermod.TraceStep) error
	GetMessageTrace(ctx context.Context, workflowID, messageID string) (MessageTrace, error)
	ListMessageTraces(ctx context.Context, workflowID string, filter TraceFilter) ([]MessageTrace, error)

	// Workflow Versioning
	CreateWorkflowVersion(ctx context.Context, version WorkflowVersion) error
	ListWorkflowVersions(ctx context.Context, workflowID string) ([]WorkflowVersion, error)
	GetWorkflowVersion(ctx context.Context, workflowID string, version int) (WorkflowVersion, error)

	// Transactional Outbox
	CreateOutboxItem(ctx context.Context, item OutboxItem) error
	ListOutboxItems(ctx context.Context, status string, limit int) ([]OutboxItem, error)
	DeleteOutboxItem(ctx context.Context, id string) error
	UpdateOutboxItem(ctx context.Context, item OutboxItem) error

	// Lineage
	GetLineage(ctx context.Context) ([]LineageEdge, error)

	// Marketplace
	ListPlugins(ctx context.Context) ([]Plugin, error)
	GetPlugin(ctx context.Context, id string) (Plugin, error)
	InstallPlugin(ctx context.Context, id string) error
	UninstallPlugin(ctx context.Context, id string) error

	// Approvals
	ListApprovals(ctx context.Context, filter ApprovalFilter) ([]Approval, int, error)
	CreateApproval(ctx context.Context, app Approval) error
	GetApproval(ctx context.Context, id string) (Approval, error)
	UpdateApprovalStatus(ctx context.Context, id string, status string, processedBy string, notes string, formData map[string]any) error
	DeleteApproval(ctx context.Context, id string) error

	// Suspended Messages
	CreateSuspendedMessage(ctx context.Context, m SuspendedMessage) error
	ListSuspendedMessages(ctx context.Context, workflowID string, before time.Time) ([]SuspendedMessage, error)
	DeleteSuspendedMessage(ctx context.Context, id string) error

	// Aggregated Dashboard Stats
	GetDashboardStats(ctx context.Context, vhost string) (DashboardStats, error)

	// Dashboard history. Samples are appended on an interval so the dashboard
	// chart survives a page reload; PurgeDashboardHistory bounds the table.
	RecordDashboardSample(ctx context.Context, sample DashboardSample) error
	GetDashboardHistory(ctx context.Context, vhost string, since time.Time, limit int) ([]DashboardSample, error)
	PurgeDashboardHistory(ctx context.Context, before time.Time) error
}
