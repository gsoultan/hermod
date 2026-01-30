package storage

import (
	"context"
	"errors"
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
	Page   int
	Limit  int
	Search string
	VHost  string
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
	ID       string            `json:"id"`
	Name     string            `json:"name"`
	Type     string            `json:"type"`
	VHost    string            `json:"vhost"`
	Active   bool              `json:"active"`
	Status   string            `json:"status,omitempty"`
	WorkerID string            `json:"worker_id"`
	Config   map[string]string `json:"config"`
	Sample   string            `json:"sample,omitempty"`
	State    map[string]string `json:"state,omitempty"`
}

type Sink struct {
	ID       string            `json:"id"`
	Name     string            `json:"name"`
	Type     string            `json:"type"`
	VHost    string            `json:"vhost"`
	Active   bool              `json:"active"`
	Status   string            `json:"status,omitempty"`
	WorkerID string            `json:"worker_id"`
	Config   map[string]string `json:"config"`
}

type Transformation struct {
	Type   string            `json:"type"`
	Config map[string]string `json:"config"`
}

type WorkflowNode struct {
	ID     string                 `json:"id"`
	Type   string                 `json:"type"`             // source, sink, transformer, condition, etc.
	RefID  string                 `json:"ref_id,omitempty"` // ID of the source, sink, or transformation
	Config map[string]interface{} `json:"config,omitempty"`
	X      float64                `json:"x"`
	Y      float64                `json:"y"`
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
	RetentionDays *int   `json:"retention_days,omitempty"`
	SchemaType    string `json:"schema_type,omitempty"`
	Schema        string `json:"schema,omitempty"`
	Cron          string `json:"cron,omitempty"`
	DLQThreshold  int    `json:"dlq_threshold,omitempty"`
}

type Worker struct {
	ID          string     `json:"id"`
	Name        string     `json:"name"`
	Host        string     `json:"host"`
	Port        int        `json:"port"`
	Description string     `json:"description"`
	Token       string     `json:"token"`
	LastSeen    *time.Time `json:"last_seen,omitempty"`
}

type Role string

const (
	RoleAdministrator Role = "Administrator"
	RoleEditor        Role = "Editor"
	RoleViewer        Role = "Viewer"
)

type User struct {
	ID       string   `json:"id"`
	Username string   `json:"username"`
	Password string   `json:"password,omitempty"`
	FullName string   `json:"full_name"`
	Email    string   `json:"email"`
	Role     Role     `json:"role"`
	VHosts   []string `json:"vhosts"`
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

type Storage interface {
	// Init performs storage initialization/migrations and is safe to call multiple times.
	Init(ctx context.Context) error

	ListSources(ctx context.Context, filter CommonFilter) ([]Source, int, error)
	CreateSource(ctx context.Context, src Source) error
	UpdateSource(ctx context.Context, src Source) error
	UpdateSourceState(ctx context.Context, id string, state map[string]string) error
	DeleteSource(ctx context.Context, id string) error
	GetSource(ctx context.Context, id string) (Source, error)

	ListSinks(ctx context.Context, filter CommonFilter) ([]Sink, int, error)
	CreateSink(ctx context.Context, snk Sink) error
	UpdateSink(ctx context.Context, snk Sink) error
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
	UpdateWorkerHeartbeat(ctx context.Context, id string) error
	DeleteWorker(ctx context.Context, id string) error
	GetWorker(ctx context.Context, id string) (Worker, error)

	ListLogs(ctx context.Context, filter LogFilter) ([]Log, int, error)
	CreateLog(ctx context.Context, log Log) error
	DeleteLogs(ctx context.Context, filter LogFilter) error

	ListAuditLogs(ctx context.Context, filter AuditFilter) ([]AuditLog, int, error)
	CreateAuditLog(ctx context.Context, log AuditLog) error

	ListWebhookRequests(ctx context.Context, filter WebhookRequestFilter) ([]WebhookRequest, int, error)
	CreateWebhookRequest(ctx context.Context, req WebhookRequest) error
	GetWebhookRequest(ctx context.Context, id string) (WebhookRequest, error)
	DeleteWebhookRequests(ctx context.Context, filter WebhookRequestFilter) error

	GetSetting(ctx context.Context, key string) (string, error)
	SaveSetting(ctx context.Context, key string, value string) error

	// Node State Management
	UpdateNodeState(ctx context.Context, workflowID, nodeID string, state interface{}) error
	GetNodeStates(ctx context.Context, workflowID string) (map[string]interface{}, error)
}
