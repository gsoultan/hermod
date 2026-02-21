package hermod

import (
	"context"
	"time"
)

// Operation defines the type of CDC operation.
type Operation string

const (
	OpCreate   Operation = "create"
	OpUpdate   Operation = "update"
	OpDelete   Operation = "delete"
	OpSnapshot Operation = "snapshot"
)

// SinkOperationMode defines how a sink should treat incoming messages.
type SinkOperationMode string

const (
	SinkOpModeAuto   SinkOperationMode = "auto"   // Follow message operation
	SinkOpModeInsert SinkOperationMode = "insert" // Always insert
	SinkOpModeUpsert SinkOperationMode = "upsert" // Always upsert/merge
	SinkOpModeUpdate SinkOperationMode = "update" // Always update
	SinkOpModeDelete SinkOperationMode = "delete" // Always delete
)

// Message represents a generic message structure.
// Using interface to allow different message implementations.
type Message interface {
	ID() string
	Operation() Operation
	Table() string
	Schema() string
	Before() []byte
	After() []byte
	Payload() []byte // Primary data payload
	Metadata() map[string]string
	Data() map[string]any
	SetMetadata(key, value string)
	SetData(key string, value any)
	Clone() Message
	ClearPayloads()
}

// Producer defines the interface for sending messages.
type Producer interface {
	Produce(ctx context.Context, msg Message) error
	Close() error
}

// Consumer defines the interface for receiving messages.
type Consumer interface {
	Consume(ctx context.Context, handler Handler) error
	Close() error
}

// Source defines the interface for reading data from a CDC source.
type Source interface {
	Read(ctx context.Context) (Message, error)
	Ack(ctx context.Context, msg Message) error
	Ping(ctx context.Context) error
	Close() error
}

// ReadyChecker is an optional interface for sources that support deep readiness checks.
type ReadyChecker interface {
	IsReady(ctx context.Context) error
}

// Discoverer defines an optional interface for discovering databases and tables.
type Discoverer interface {
	DiscoverDatabases(ctx context.Context) ([]string, error)
	DiscoverTables(ctx context.Context) ([]string, error)
}

// ColumnDiscoverer defines an optional interface for discovering columns of a table.
type ColumnDiscoverer interface {
	DiscoverColumns(ctx context.Context, table string) ([]ColumnInfo, error)
}

type ColumnInfo struct {
	Name       string `json:"name"`
	Type       string `json:"type"`
	IsNullable bool   `json:"is_nullable"`
	IsPK       bool   `json:"is_pk"`
	IsIdentity bool   `json:"is_identity"`
	Default    string `json:"default"`
}

// Stateful defines an optional interface for sources and sinks that have persistent state.
type Stateful interface {
	GetState() map[string]string
	SetState(state map[string]string)
}

// Sampler defines an optional interface for fetching a sample record from a table.
type Sampler interface {
	Sample(ctx context.Context, table string) (Message, error)
}

// Browser defines an optional interface for browsing multiple records from a table.
type Browser interface {
	Browse(ctx context.Context, table string, limit int) ([]Message, error)
}

// Snapshottable defines an optional interface for sources that support manual snapshots.
type Snapshottable interface {
	Snapshot(ctx context.Context, tables ...string) error
}

// SQLExecutor is an interface for sources and sinks that can execute arbitrary SQL queries.
type SQLExecutor interface {
	ExecuteSQL(ctx context.Context, query string) ([]map[string]any, error)
}

type Sink interface {
	Write(ctx context.Context, msg Message) error
	Ping(ctx context.Context) error
	Close() error
}

// BatchSink is an optional interface for sinks that support batch writes.
type BatchSink interface {
	Sink
	WriteBatch(ctx context.Context, msgs []Message) error
}

// IdempotencyReporter is an optional interface for sinks to report whether the
// last successful Write/WriteBatch resulted in a deduplicated (skipped) write
// and/or a payload conflict. Engines can use this to emit standardized metrics.
type IdempotencyReporter interface {
	// LastWriteIdempotent returns true when the latest write was treated as a
	// duplicate (no-op), and true for conflict when a key collision with a
	// differing payload was detected (when supported by the sink).
	LastWriteIdempotent() (dedup bool, conflict bool)
}

// ValidatingSink is an optional interface for sinks that support pre-write validation.
type ValidatingSink interface {
	Sink
	Validate(ctx context.Context, msg Message) error
}

// Transactional is an optional interface for sources and sinks that support atomic transactions.
type Transactional interface {
	Begin(ctx context.Context) error
	Commit(ctx context.Context) error
	Rollback(ctx context.Context) error
}

// TwoPhaseCommit is an optional interface for sinks that support 2PC.
type TwoPhaseCommit interface {
	Transactional
	Prepare(ctx context.Context) (string, error) // Returns transaction ID
	CommitPrepared(ctx context.Context, txID string) error
	RollbackPrepared(ctx context.Context, txID string) error
}

// Formatter defines the interface for formatting messages before they are written to a sink.
type Formatter interface {
	Format(msg Message) ([]byte, error)
}

// Logger defines the interface for logging in Hermod.
type Logger interface {
	Debug(msg string, keysAndValues ...any)
	Info(msg string, keysAndValues ...any)
	Warn(msg string, keysAndValues ...any)
	Error(msg string, keysAndValues ...any)
}

// Loggable defines an optional interface for things that support structured logging.
type Loggable interface {
	SetLogger(Logger)
}

// StateStore defines an interface for persistent key-value storage used by transformers.
type StateStore interface {
	Get(ctx context.Context, key string) ([]byte, error)
	Set(ctx context.Context, key string, value []byte) error
	Delete(ctx context.Context, key string) error
}

// TraceStep represents a single step in a message's journey.
type TraceStep struct {
	NodeID    string         `json:"node_id"`
	Timestamp time.Time      `json:"timestamp" omitzero:"true"`
	Duration  time.Duration  `json:"duration" omitzero:"true"`
	Data      map[string]any `json:"data" omitzero:"true"`
	Error     string         `json:"error,omitempty"`
}

// TraceRecorder defines the interface for recording message traces.
type TraceRecorder interface {
	RecordStep(ctx context.Context, workflowID, messageID string, step TraceStep)
}

// OutboxItem represents a message persisted for reliable delivery.
type OutboxItem struct {
	ID         string            `json:"id"`
	WorkflowID string            `json:"workflow_id"`
	SinkID     string            `json:"sink_id"`
	Payload    []byte            `json:"payload" omitzero:"true"`
	Metadata   map[string]string `json:"metadata" omitzero:"true"`
	CreatedAt  time.Time         `json:"created_at" omitzero:"true"`
	Attempts   int               `json:"attempts"`
	LastError  string            `json:"last_error,omitempty"`
	Status     string            `json:"status"` // pending, processing, failed
}

// OutboxStorage defines the interface for persisting and retrieving outbox items.
type OutboxStorage interface {
	CreateOutboxItem(ctx context.Context, item OutboxItem) error
	ListOutboxItems(ctx context.Context, status string, limit int) ([]OutboxItem, error)
	DeleteOutboxItem(ctx context.Context, id string) error
	UpdateOutboxItem(ctx context.Context, item OutboxItem) error
}

type contextKey string

const (
	StateStoreKey contextKey = "stateStore"
)

// Handler is a function type for processing received messages.
type Handler func(ctx context.Context, msg Message) error
