package hermod

import "context"

// Operation defines the type of CDC operation.
type Operation string

const (
	OpCreate   Operation = "create"
	OpUpdate   Operation = "update"
	OpDelete   Operation = "delete"
	OpSnapshot Operation = "snapshot"
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
	Data() map[string]interface{}
	SetMetadata(key, value string)
	SetData(key string, value interface{})
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

// Sink defines the interface for writing data to a message stream provider.
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

// Formatter defines the interface for formatting messages before they are written to a sink.
type Formatter interface {
	Format(msg Message) ([]byte, error)
}

// Logger defines the interface for logging in Hermod.
type Logger interface {
	Debug(msg string, keysAndValues ...interface{})
	Info(msg string, keysAndValues ...interface{})
	Warn(msg string, keysAndValues ...interface{})
	Error(msg string, keysAndValues ...interface{})
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

type contextKey string

const (
	StateStoreKey contextKey = "stateStore"
)

// Handler is a function type for processing received messages.
type Handler func(ctx context.Context, msg Message) error
