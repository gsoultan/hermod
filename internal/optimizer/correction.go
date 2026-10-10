package optimizer

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/pkg/engine"
	"github.com/gsoultan/hermod/pkg/engine/telemetry"
)

// CorrectionAction defines the type of fix to apply.
type CorrectionAction string

const (
	ActionScaleBatch     CorrectionAction = "scale_batch"
	ActionAdjustTimeout  CorrectionAction = "adjust_timeout"
	ActionIncreaseRetry  CorrectionAction = "increase_retry"
	ActionNotifyOperator CorrectionAction = "notify_operator"
	ActionSafeMode       CorrectionAction = "safe_mode"
	ActionSuggestMapping CorrectionAction = "suggest_mapping"
)

// correctionCooldown is how long the gate stays quiet about the same
// node and action after speaking up.
const correctionCooldown = 10 * time.Minute

// FailurePattern represents a detected issue in the pipeline.
type FailurePattern struct {
	ID          string
	Description string
	Threshold   float64
	Window      time.Duration
}

// SelfCorrectionGate watches a workflow's telemetry for failure patterns.
//
// It never changes a running engine or a stored workflow. A fix becomes a
// Suggestion handed to the Proposer, which keeps it for a person to approve;
// everything else is advice to the operator.
type SelfCorrectionGate struct {
	mu       sync.RWMutex
	patterns []FailurePattern
	history  map[string][]time.Time
	logger   hermod.Logger
	notifier func(workflowID, title, message string)
	proposer Proposer
	advisor  MappingAdvisor
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

func (g *SelfCorrectionGate) Analyze(id string, _ *engine.Engine, status telemetry.StatusUpdate) {
	// 1. Check for High Error Rates on Nodes
	for nodeID, count := range status.NodeMetrics {
		if count > 100 {
			errCount := status.NodeErrorMetrics[nodeID]
			errRate := float64(errCount) / float64(count)

			if errRate > 0.50 {
				g.logger.Error("Self-Correction: CRITICAL error rate detected. Recommending Safe Mode.",
					"workflow_id", id, "node_id", nodeID, "rate", fmt.Sprintf("%.2f", errRate))
				g.applyFix(id, nodeID, ActionSafeMode, status)
			} else if errRate > 0.20 {
				g.logger.Warn("Self-Correction: High error rate detected",
					"workflow_id", id, "node_id", nodeID, "rate", fmt.Sprintf("%.2f", errRate))
				g.applyFix(id, nodeID, ActionIncreaseRetry, status)
			}
		}
	}

	// 2. Check for Schema Drift / Validation Failures
	for nodeID, errCount := range status.NodeErrorMetrics {
		if strings.Contains(strings.ToLower(nodeID), "validate") && errCount > 50 {
			g.logger.Error("Self-Correction: Frequent validation failures",
				"workflow_id", id, "node_id", nodeID)
			g.applyFix(id, nodeID, ActionSuggestMapping, status)
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
		g.applyFix(id, "workflow", ActionNotifyOperator, status)
	}

	// 4. Pattern: Latency Spikes
	if status.AvgLatency > 1*time.Second && status.ProcessedCount > 500 {
		g.logger.Warn("Self-Correction: Significant latency spike detected", "workflow_id", id, "latency", status.AvgLatency.String())
		g.applyFix(id, "global", ActionScaleBatch, status)
	}
}

// due reports whether the gate may act on key now, and records that it did.
func (g *SelfCorrectionGate) due(key string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	last := g.history[key]
	if len(last) > 0 && time.Since(last[len(last)-1]) < correctionCooldown {
		return false
	}
	g.history[key] = append(last, time.Now())
	return true
}

func (g *SelfCorrectionGate) applyFix(workflowID, nodeID string, action CorrectionAction, status telemetry.StatusUpdate) {
	if !g.due(fmt.Sprintf("%s:%s:%s", workflowID, nodeID, action)) {
		return
	}
	g.logger.Info("Self-correction triggered", "workflow_id", workflowID, "node_id", nodeID, "action", action)

	switch action {
	case ActionIncreaseRetry:
		g.propose(Suggestion{
			WorkflowID: workflowID, NodeID: nodeID, Action: action,
			Reason: fmt.Sprintf("Node '%s' is failing more than a fifth of its messages; more retries with a longer interval may ride out transient errors.", nodeID),
		})
	case ActionScaleBatch:
		// Batch size is a sink setting, not a workflow one, so there is no
		// workflow-level change to propose.
		g.notify(workflowID, "Self-healing advice: latency spike",
			fmt.Sprintf("Workflow '%s' is averaging more than a second per message. Consider a smaller batch size on its slowest sink.", workflowID))
	case ActionSafeMode:
		// Advisory only. The node counters are lifetime totals, so acting on
		// them would pin the whole workflow to the dead-letter sink long after
		// the failures stopped; an operator decides.
		g.notify(workflowID, "CRITICAL: Safe Mode recommended", fmt.Sprintf("Node '%s' of workflow '%s' is failing more than half of its messages. Consider enabling Safe Mode to divert traffic to the dead-letter sink while you investigate.", nodeID, workflowID))
	case ActionSuggestMapping:
		g.suggestMapping(workflowID, nodeID, status.NodeSamples)
	case ActionNotifyOperator:
		g.notify(workflowID, "Self-Correction Alert", fmt.Sprintf("Action '%s' triggered for node '%s'", action, nodeID))
	}
}

func (g *SelfCorrectionGate) notify(workflowID, title, body string) {
	if g.notifier != nil {
		g.notifier(workflowID, title, body)
	}
}

// propose hands a suggestion to the proposer and tells the operator it is
// waiting. Without a proposer the suggestion is only announced.
func (g *SelfCorrectionGate) propose(s Suggestion) {
	g.mu.RLock()
	p := g.proposer
	g.mu.RUnlock()
	if p == nil {
		g.notify(s.WorkflowID, "Self-healing suggestion", s.Reason)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := p.Propose(ctx, s); err != nil {
		g.logger.Error("Self-correction: storing the proposal failed", "workflow_id", s.WorkflowID, "error", err)
		g.notify(s.WorkflowID, "Self-healing suggestion", s.Reason)
		return
	}
	g.notify(s.WorkflowID, "Self-healing fix awaiting approval",
		s.Reason+" A change to the workflow's retry settings is waiting for approval.")
}

// suggestMapping tells the operator a node keeps failing validation and,
// when an advisor is configured, asks it for a mapping from the node's last
// sample. Without one no sample leaves Hermod.
func (g *SelfCorrectionGate) suggestMapping(workflowID, nodeID string, samples map[string]any) {
	g.mu.RLock()
	adv := g.advisor
	g.mu.RUnlock()
	title := "Validation failures"
	body := fmt.Sprintf("Node '%s' is failing validation repeatedly. Check that the incoming fields match its schema.", nodeID)
	sample, _ := samples[nodeID].(map[string]any)
	if adv == nil || len(sample) == 0 {
		g.notify(workflowID, title, body)
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		suggestion, err := adv.SuggestMapping(ctx, workflowID, nodeID, sample)
		if err != nil {
			g.logger.Error("Self-correction: mapping suggestion failed", "workflow_id", workflowID, "error", err)
			g.notify(workflowID, title, body)
			return
		}
		g.notify(workflowID, "AI Mapping Suggestion", fmt.Sprintf("Node '%s' is failing validation. AI Suggestion:\n%s", nodeID, suggestion))
	}()
}
