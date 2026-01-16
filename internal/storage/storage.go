package storage

import (
	"context"
	"errors"
	"time"
)

var ErrNotFound = errors.New("not found")

type Log struct {
	ID           string    `json:"id"`
	Timestamp    time.Time `json:"timestamp"`
	Level        string    `json:"level"`
	Message      string    `json:"message"`
	Action       string    `json:"action,omitempty"`
	SourceID     string    `json:"source_id,omitempty"`
	SinkID       string    `json:"sink_id,omitempty"`
	ConnectionID string    `json:"connection_id,omitempty"`
	Data         string    `json:"data,omitempty"`
}

type CommonFilter struct {
	Page   int
	Limit  int
	Search string
	VHost  string
}

type LogFilter struct {
	CommonFilter
	SourceID     string
	SinkID       string
	ConnectionID string
	Level        string
	Action       string
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
	ID        string            `json:"id"`
	Name      string            `json:"name"`
	Type      string            `json:"type"`
	Steps     []Transformation  `json:"steps,omitempty"`
	Config    map[string]string `json:"config"`
	OnFailure string            `json:"on_failure,omitempty"`
	ExecuteIf string            `json:"execute_if,omitempty"`
}

type Connection struct {
	ID                   string                `json:"id"`
	Name                 string                `json:"name"`
	VHost                string                `json:"vhost"`
	SourceID             string                `json:"source_id"`
	SinkIDs              []string              `json:"sink_ids"`
	Active               bool                  `json:"active"`
	Status               string                `json:"status,omitempty"`
	WorkerID             string                `json:"worker_id"`
	TransformationIDs    []string              `json:"transformation_ids"`
	Transformations      []Transformation      `json:"transformations"`
	TransformationGroups []TransformationGroup `json:"transformation_groups"`
}

type TransformationGroup struct {
	Name              string           `json:"name,omitempty"`
	SinkIDs           []string         `json:"sink_ids"`
	TransformationIDs []string         `json:"transformation_ids"`
	Transformations   []Transformation `json:"transformations"`
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
	DeleteSource(ctx context.Context, id string) error
	GetSource(ctx context.Context, id string) (Source, error)

	ListSinks(ctx context.Context, filter CommonFilter) ([]Sink, int, error)
	CreateSink(ctx context.Context, snk Sink) error
	UpdateSink(ctx context.Context, snk Sink) error
	DeleteSink(ctx context.Context, id string) error
	GetSink(ctx context.Context, id string) (Sink, error)

	ListConnections(ctx context.Context, filter CommonFilter) ([]Connection, int, error)
	CreateConnection(ctx context.Context, conn Connection) error
	UpdateConnection(ctx context.Context, conn Connection) error
	DeleteConnection(ctx context.Context, id string) error
	GetConnection(ctx context.Context, id string) (Connection, error)

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

	ListTransformations(ctx context.Context, filter CommonFilter) ([]Transformation, int, error)
	CreateTransformation(ctx context.Context, trans Transformation) error
	UpdateTransformation(ctx context.Context, trans Transformation) error
	DeleteTransformation(ctx context.Context, id string) error
	GetTransformation(ctx context.Context, id string) (Transformation, error)

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
