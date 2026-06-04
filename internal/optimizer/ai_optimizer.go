package optimizer

import (
	"context"
	"fmt"

	"github.com/user/hermod"
	"github.com/user/hermod/internal/ai"
	"github.com/user/hermod/pkg/engine"
	"github.com/user/hermod/pkg/engine/telemetry"
)

// DefaultAIOptimizer implements AIOptimizer using the SelfHealingService.
type DefaultAIOptimizer struct {
	aiSvc  *ai.SelfHealingService
	logger hermod.Logger
}

// NewAIOptimizer creates a new DefaultAIOptimizer.
func NewAIOptimizer(aiSvc *ai.SelfHealingService, logger hermod.Logger) *DefaultAIOptimizer {
	return &DefaultAIOptimizer{
		aiSvc:  aiSvc,
		logger: logger,
	}
}

// Optimize analyzes engine metrics and applies AI-driven tunings.
func (a *DefaultAIOptimizer) Optimize(ctx context.Context, id string, e *engine.Engine, status telemetry.StatusUpdate) error {
	a.logger.Debug("Running AI optimization", "workflow_id", id, "throughput", status.Throughput)

	if status.Throughput < 10 && status.ErrorRate > 0.05 {
		return a.handleHighErrorRate(ctx, id, e, status)
	}

	if status.Throughput > 1000 && status.Backpressure > 0.8 {
		return a.handleHighBackpressure(ctx, id, e, status)
	}

	return nil
}

func (a *DefaultAIOptimizer) handleHighErrorRate(ctx context.Context, id string, e *engine.Engine, status telemetry.StatusUpdate) error {
	a.logger.Warn("AI: High error rate detected, seeking AI advice", "workflow_id", id)

	suggestion, err := a.aiSvc.AnalyzeError(ctx, id, "global", "high error rate", status)
	if err != nil {
		return fmt.Errorf("AI error analysis failed: %w", err)
	}

	if suggestion != nil && suggestion.AutoFixable {
		a.logger.Info("AI: Applying auto-fix", "workflow_id", id, "suggestion", suggestion.Description)
		// Logic to apply fix would go here
	}
	return nil
}

func (a *DefaultAIOptimizer) handleHighBackpressure(ctx context.Context, id string, e *engine.Engine, status telemetry.StatusUpdate) error {
	a.logger.Info("AI: High backpressure detected, scaling workers", "workflow_id", id)
	e.UpdateConcurrency(e.GetConcurrency() + 1)
	return nil
}
