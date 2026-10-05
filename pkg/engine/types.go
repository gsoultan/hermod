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

// MetaFiltered marks a message the workflow chose to deliver nowhere.
//
// A filter that drops a message routes nothing, so the message reaches the
// engine as an empty target list from a workflow that has sinks. That is also
// the shape of a message no sink could be resolved for, which the engine
// refuses to acknowledge: it parks it in the dead-letter sink or leaves it on
// the source. Applied to a filtered message that refusal is a false failure on
// every message the filter drops — a dead-letter queue filling with records
// that were dropped on purpose, or a source never told they were handled.
//
// The marker is what tells the two apart. The workflow's router sets it only
// when every walk that ended without delivering ended on purpose: no node
// failed, no sink node went unresolved, and nothing is holding the message.
// With it the engine acknowledges the source and parks nothing.
const MetaFiltered = "_hermod_filtered"

// RouterFunc is a function that routes a message to one or more sinks.
type RouterFunc func(ctx context.Context, msg hermod.Message) ([]RoutedMessage, error)
