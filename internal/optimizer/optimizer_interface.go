package optimizer

import (
	"context"

	"github.com/user/hermod/pkg/engine"
	"github.com/user/hermod/pkg/engine/telemetry"
)

// AIOptimizer defines the interface for AI-driven performance optimization.
type AIOptimizer interface {
	// Optimize suggests and applies optimizations for a running engine.
	Optimize(ctx context.Context, id string, e *engine.Engine, status telemetry.StatusUpdate) error
}
