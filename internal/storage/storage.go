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
	Data       string    `json:"data,omitempty"`
}

type CommonFilter struct {
	Page   int
	Limit  int
	Search string
	VHost  string
}

type LogFilter struct {
	CommonFilter
	SourceID   string
	SinkID     string
	WorkflowID string
	Level      string
	Action     string
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

type Workflow struct {
	ID       string         `json:"id"`
	Name     string         `json:"name"`
	VHost    string         `json:"vhost"`
	Active   bool           `json:"active"`
	Status   string         `json:"status,omitempty"`
	WorkerID string         `json:"worker_id"`
	Nodes    []WorkflowNode `json:"nodes"`
	Edges    []WorkflowEdge `json:"edges"`
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

type Storage interface {
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

	ListVHosts(ctx context.Context, filter CommonFilter) ([]VHost, int, error)
	CreateVHost(ctx context.Context, vhost VHost) error
	DeleteVHost(ctx context.Context, id string) error
	GetVHost(ctx context.Context, id string) (VHost, error)

	ListWorkflows(ctx context.Context, filter CommonFilter) ([]Workflow, int, error)
	CreateWorkflow(ctx context.Context, wf Workflow) error
	UpdateWorkflow(ctx context.Context, wf Workflow) error
	DeleteWorkflow(ctx context.Context, id string) error
	GetWorkflow(ctx context.Context, id string) (Workflow, error)

	ListWorkers(ctx context.Context, filter CommonFilter) ([]Worker, int, error)
	CreateWorker(ctx context.Context, worker Worker) error
	UpdateWorker(ctx context.Context, worker Worker) error
	UpdateWorkerHeartbeat(ctx context.Context, id string) error
	DeleteWorker(ctx context.Context, id string) error
	GetWorker(ctx context.Context, id string) (Worker, error)

	ListLogs(ctx context.Context, filter LogFilter) ([]Log, int, error)
	CreateLog(ctx context.Context, log Log) error
	DeleteLogs(ctx context.Context, filter LogFilter) error

	GetSetting(ctx context.Context, key string) (string, error)
	SaveSetting(ctx context.Context, key string, value string) error
}
