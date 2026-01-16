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

// Discoverer defines an optional interface for discovering databases and tables.
type Discoverer interface {
	DiscoverDatabases(ctx context.Context) ([]string, error)
	DiscoverTables(ctx context.Context) ([]string, error)
}

// Sampler defines an optional interface for fetching a sample record from a table.
type Sampler interface {
	Sample(ctx context.Context, table string) (Message, error)
}

// Sink defines the interface for writing data to a message stream provider.
type Sink interface {
	Write(ctx context.Context, msg Message) error
	Ping(ctx context.Context) error
	Close() error
}

// Formatter defines the interface for formatting messages before they are written to a sink.
type Formatter interface {
	Format(msg Message) ([]byte, error)
}

// Transformer defines the interface for transforming messages.
type Transformer interface {
	Transform(ctx context.Context, msg Message) (Message, error)
	Close() error
}

// Logger defines the interface for logging in Hermod.
type Logger interface {
	Debug(msg string, keysAndValues ...interface{})
	Info(msg string, keysAndValues ...interface{})
	Warn(msg string, keysAndValues ...interface{})
	Error(msg string, keysAndValues ...interface{})
}

// Handler is a function type for processing received messages.
type Handler func(ctx context.Context, msg Message) error
