package storage

import (
	"context"
	"errors"
	"github.com/user/hermod"
	"time"
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
	Since time.Time
	Until time.Time
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
	Config      map[string]string `json:"config"`
	Sample      string            `json:"sample,omitempty"`
	State       map[string]string `json:"state,omitempty"`
}

type Sink struct {
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	Type        string            `json:"type"`
	VHost       string            `json:"vhost"`
	Active      bool              `json:"active"`
	Status      string            `json:"status,omitempty"`
	WorkerID    string            `json:"worker_id"`
	WorkspaceID string            `json:"workspace_id,omitempty"`
	Config      map[string]string `json:"config"`
}

type Transformation struct {
	Type   string            `json:"type"`
	Config map[string]string `json:"config"`
}

type WorkflowNode struct {
	ID        string                 `json:"id"`
	Type      string                 `json:"type"`             // source, sink, transformer, condition, etc.
	RefID     string                 `json:"ref_id,omitempty"` // ID of the source, sink, or transformation
	Config    map[string]interface{} `json:"config,omitempty"`
	X         float64                `json:"x"`
	Y         float64                `json:"y"`
	UnitTests []UnitTest             `json:"unit_tests,omitempty"`
}

type UnitTest struct {
	Name           string                 `json:"name"`
	Input          map[string]interface{} `json:"input"`
	ExpectedOutput map[string]interface{} `json:"expected_output"`
	Description    string                 `json:"description,omitempty"`
}

type WorkflowEdge struct {
	ID       string                 `json:"id"`
	SourceID string                 `json:"source_id"`
	TargetID string                 `json:"target_id"`
	Config   map[string]interface{} `json:"config,omitempty"`
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
	LeaseUntil        *time.Time     `json:"lease_until,omitempty"`
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
	Tags              []string `json:"tags,omitempty"`
	TraceRetention    string   `json:"trace_retention,omitempty"` // e.g. "7d", "30d"
	AuditRetention    string   `json:"audit_retention,omitempty"` // e.g. "30d", "365d"
	WorkspaceID       string   `json:"workspace_id,omitempty"`
	CPURequest        float64  `json:"cpu_request,omitempty"`
	MemoryRequest     float64  `json:"memory_request,omitempty"`
	ThroughputRequest int      `json:"throughput_request,omitempty"`
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
	LastSeen    *time.Time `json:"last_seen,omitempty"`
	CPUUsage    float64    `json:"cpu_usage,omitempty"`
	MemoryUsage float64    `json:"memory_usage,omitempty"`
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
}

type VHost struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
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
	From       *time.Time `json:"from"`
	To         *time.Time `json:"to"`
}

type WebhookRequest struct {
	ID        string            `json:"id"`
	Timestamp time.Time         `json:"timestamp"`
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
	Timestamp time.Time `json:"timestamp"`
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
	CreatedAt time.Time `json:"created_at"`
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
	InstalledAt *time.Time `json:"installed_at,omitempty"`
}

type TraceStep = hermod.TraceStep

type MessageTrace struct {
	ID         string             `json:"id"`
	MessageID  string             `json:"message_id"`
	WorkflowID string             `json:"workflow_id"`
	Steps      []hermod.TraceStep `json:"steps"`
	CreatedAt  time.Time          `json:"created_at"`
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

type Storage interface {
	// Init performs storage initialization/migrations and is safe to call multiple times.
	Init(ctx context.Context) error

	ListSources(ctx context.Context, filter CommonFilter) ([]Source, int, error)
	CreateSource(ctx context.Context, src Source) error
	UpdateSource(ctx context.Context, src Source) error
	UpdateSourceStatus(ctx context.Context, id string, status string) error
	UpdateSourceState(ctx context.Context, id string, state map[string]string) error
	DeleteSource(ctx context.Context, id string) error
	GetSource(ctx context.Context, id string) (Source, error)

	ListSinks(ctx context.Context, filter CommonFilter) ([]Sink, int, error)
	CreateSink(ctx context.Context, snk Sink) error
	UpdateSink(ctx context.Context, snk Sink) error
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
	DeleteVHost(ctx context.Context, id string) error
	GetVHost(ctx context.Context, id string) (VHost, error)

	ListWorkflows(ctx context.Context, filter CommonFilter) ([]Workflow, int, error)
	ListWorkspaces(ctx context.Context) ([]Workspace, error)
	CreateWorkspace(ctx context.Context, ws Workspace) error
	GetWorkspace(ctx context.Context, id string) (Workspace, error)
	DeleteWorkspace(ctx context.Context, id string) error
	CreateWorkflow(ctx context.Context, wf Workflow) error
	UpdateWorkflow(ctx context.Context, wf Workflow) error
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
	DeleteLogs(ctx context.Context, filter LogFilter) error

	ListAuditLogs(ctx context.Context, filter AuditFilter) ([]AuditLog, int, error)
	CreateAuditLog(ctx context.Context, log AuditLog) error
	PurgeAuditLogs(ctx context.Context, before time.Time) error
	PurgeMessageTraces(ctx context.Context, before time.Time) error

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
	UpdateNodeState(ctx context.Context, workflowID, nodeID string, state interface{}) error
	GetNodeStates(ctx context.Context, workflowID string) (map[string]interface{}, error)

	// Schema Registry
	ListSchemas(ctx context.Context, name string) ([]Schema, error)
	ListAllSchemas(ctx context.Context) ([]Schema, error)
	GetSchema(ctx context.Context, name string, version int) (Schema, error)
	GetLatestSchema(ctx context.Context, name string) (Schema, error)
	CreateSchema(ctx context.Context, schema Schema) error

	// Message Tracing
	RecordTraceStep(ctx context.Context, workflowID, messageID string, step hermod.TraceStep) error
	GetMessageTrace(ctx context.Context, workflowID, messageID string) (MessageTrace, error)
	ListMessageTraces(ctx context.Context, workflowID string, limit int) ([]MessageTrace, error)

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
}
