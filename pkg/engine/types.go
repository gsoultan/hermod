package engine

import (
	"context"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/pkg/engine/config"
	"github.com/gsoultan/hermod/pkg/engine/telemetry"
)

// Re-export common types from sub-packages for backward compatibility
type Config = config.Config
type SinkConfig = config.SinkConfig
type BackpressureStrategy = config.BackpressureStrategy
type StatusUpdate = telemetry.StatusUpdate
type SourceConfig = config.SourceConfig

// DefaultConfig returns the default configuration for the Engine.
func DefaultConfig() Config {
	return config.DefaultConfig()
}

// NewDefaultLogger creates a DefaultLogger with stderr output and timestamps.
func NewDefaultLogger() hermod.Logger {
	return telemetry.NewDefaultLogger()
}

// RoutedMessage represents a message and its target sink index.
type RoutedMessage struct {
	SinkIndex int
	Message   hermod.Message
}

// MetaDeliveredInline marks a message that a sink node already wrote itself.
//
// A sink node with `sequential: true` writes inline and deliberately routes
// nothing, so the router hands the engine an empty target list. That is
// indistinguishable, at the engine, from a workflow that resolved none of its
// sinks — which is data loss and is handled by refusing to acknowledge. Applied
// to a message that was in fact delivered, that refusal pins the source: a
// PostgreSQL replication slot stops advancing and retains WAL for the life of
// the workflow, while every message is re-read on the next start.
//
// The marker is what tells those two cases apart. It is set only after the
// inline write succeeded, so a failed write still takes the un-acknowledged
// path and is redelivered.
const MetaDeliveredInline = "_hermod_delivered_inline"

// MetaDeadLettered marks a message the workflow already parked in the
// dead-letter sink.
//
// A node that fails is dead-lettered where it failed, and the traversal then
// resolves no sink for that message. It reaches the engine as an empty target
// list — the same shape as a message nothing ever handled — so the engine parked
// it a second time. One failed node produced two identical rows in the queue,
// differing only in which metadata the second pass overwrote, and whoever
// drained the queue had to work out they were the same event.
//
// The marker says the message is already preserved: acknowledge the source and
// do not park it again. It is set only after a park that succeeded, so a refused
// park still takes the un-acknowledged path and is redelivered.
const MetaDeadLettered = "_hermod_dead_lettered"

// RouterFunc is a function that routes a message to one or more sinks.
type RouterFunc func(ctx context.Context, msg hermod.Message) ([]RoutedMessage, error)
