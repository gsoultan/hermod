package optimizer

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/user/hermod"
	"github.com/user/hermod/pkg/engine"
)

// CorrectionAction defines the type of fix to apply.
type CorrectionAction string

const (
	ActionScaleBatch     CorrectionAction = "scale_batch"
	ActionAdjustTimeout  CorrectionAction = "adjust_timeout"
	ActionIncreaseRetry  CorrectionAction = "increase_retry"
	ActionNotifyOperator CorrectionAction = "notify_operator"
)

// FailurePattern represents a detected issue in the pipeline.
type FailurePattern struct {
	ID          string
	Description string
	Threshold   float64
	Window      time.Duration
}

// SelfCorrectionGate handles high-confidence failure patterns.
type SelfCorrectionGate struct {
	mu       sync.RWMutex
	patterns []FailurePattern
	history  map[string][]time.Time
	logger   hermod.Logger
	notifier func(workflowID, title, message string)
}

func NewSelfCorrectionGate(logger hermod.Logger, notifier func(workflowID, title, message string)) *SelfCorrectionGate {
	return &SelfCorrectionGate{
		patterns: []FailurePattern{
			{
				ID:          "high_error_rate",
				Description: "Node reporting high error rate (>20%)",
				Threshold:   0.20,
				Window:      5 * time.Minute,
			},
			{
				ID:          "buffer_saturation",
				Description: "Sink buffer constantly above 90%",
				Threshold:   0.90,
				Window:      2 * time.Minute,
			},
		},
		history:  make(map[string][]time.Time),
		logger:   logger,
		notifier: notifier,
	}
}

func (g *SelfCorrectionGate) Analyze(id string, e *engine.Engine, status engine.StatusUpdate) {
	// 1. Check for High Error Rates on Nodes
	for nodeID, count := range status.NodeMetrics {
		if count > 100 {
			errCount := status.NodeErrorMetrics[nodeID]
			errRate := float64(errCount) / float64(count)

			if errRate > 0.20 {
				g.logger.Warn("Self-Correction: High error rate detected",
					"workflow_id", id, "node_id", nodeID, "rate", fmt.Sprintf("%.2f", errRate))

				g.applyFix(id, nodeID, ActionIncreaseRetry, e)
			}
		}
	}

	// 2. Check for Schema Drift / Validation Failures
	for nodeID, errCount := range status.NodeErrorMetrics {
		if strings.Contains(strings.ToLower(nodeID), "validate") && errCount > 50 {
			g.logger.Error("Self-Correction: Frequent validation failures",
				"workflow_id", id, "node_id", nodeID)

			g.applyFix(id, nodeID, ActionNotifyOperator, e)
		}
	}

	// 3. Pattern: Cascading Failures (multiple sinks reporting issues)
	sinksInError := 0
	for _, st := range status.SinkStatuses {
		if st == "error" || st == "failed" {
			sinksInError++
		}
	}
	if sinksInError > 1 {
		g.logger.Warn("Self-Correction: Cascading failure pattern detected", "workflow_id", id, "sinks_in_error", sinksInError)
		// Trigger circuit breaker for the whole workflow if possible, or notify
		g.applyFix(id, "workflow", ActionNotifyOperator, e)
	}

	// 4. Pattern: Latency Spikes with High Memory
	// If AvgLatency > 1s and ProcessedCount is high
	if status.AvgLatency > 1*time.Second && status.ProcessedCount > 500 {
		g.logger.Warn("Self-Correction: Significant latency spike detected", "workflow_id", id, "latency", status.AvgLatency.String())
		// Suggest scaling batch size down to reduce per-request overhead if needed
		g.applyFix(id, "global", ActionScaleBatch, e)
	}
}

func (g *SelfCorrectionGate) applyFix(workflowID, nodeID string, action CorrectionAction, e *engine.Engine) {
	key := fmt.Sprintf("%s:%s:%s", workflowID, nodeID, action)

	g.mu.Lock()
	defer g.mu.Unlock()

	// Rate limit corrections (once per 10 minutes per node/action)
	lastFixes := g.history[key]
	now := time.Now()
	if len(lastFixes) > 0 && time.Since(lastFixes[len(lastFixes)-1]) < 10*time.Minute {
		return
	}

	g.logger.Info("Applying self-correction fix", "workflow_id", workflowID, "node_id", nodeID, "action", action)
	g.history[key] = append(g.history[key], now)

	switch action {
	case ActionIncreaseRetry:
		e.UpdateSinkConfig(nodeID, func(cfg *engine.SinkConfig) {
			cfg.MaxRetries++
			if cfg.MaxRetries > 10 {
				cfg.MaxRetries = 10
			}
			cfg.RetryInterval = cfg.RetryInterval * 2
			if cfg.RetryInterval > 10*time.Second {
				cfg.RetryInterval = 10 * time.Second
			}
		})
	case ActionScaleBatch:
		e.UpdateSinkConfig(nodeID, func(cfg *engine.SinkConfig) {
			cfg.BatchSize = int(float64(cfg.BatchSize) * 0.7)
			if cfg.BatchSize < 1 {
				cfg.BatchSize = 1
			}
		})
	case ActionNotifyOperator:
		if g.notifier != nil {
			g.notifier(workflowID, "Self-Correction Alert", fmt.Sprintf("Action '%s' triggered for node '%s'", action, nodeID))
		}
		g.logger.Info("Correction: Operator notification triggered", "workflow_id", workflowID, "node_id", nodeID)
	}
}
