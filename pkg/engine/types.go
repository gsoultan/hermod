package engine

import (
	"context"

	"github.com/user/hermod"
	"github.com/user/hermod/pkg/engine/config"
	"github.com/user/hermod/pkg/engine/telemetry"
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

// RouterFunc is a function that routes a message to one or more sinks.
type RouterFunc func(ctx context.Context, msg hermod.Message) ([]RoutedMessage, error)
