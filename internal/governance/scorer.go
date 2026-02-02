package governance

import (
	"context"
	"fmt"
	"math"
	"sync"
	"time"

	"github.com/user/hermod"
)

// DQMetricType defines the type of data quality check.
type DQMetricType string

const (
	DQCompleteness DQMetricType = "completeness"
	DQAccuracy     DQMetricType = "accuracy"
	DQConsistency  DQMetricType = "consistency"
	DQValidity     DQMetricType = "validity"
)

// DQResult represents the result of a data quality check.
type DQResult struct {
	Score         float64            `json:"score"` // 0.0 to 1.0
	CheckTime     time.Time          `json:"check_time"`
	Metrics       map[string]float64 `json:"metrics"`
	DriftDetected bool               `json:"drift_detected"`
}

// Scorer calculates data quality scores for messages.
type Scorer struct {
	mu           sync.RWMutex
	history      map[string][]float64
	historyLimit int
}

func NewScorer() *Scorer {
	return &Scorer{
		history:      make(map[string][]float64),
		historyLimit: 100,
	}
}

// Score calculates a DQ score for a message based on its payload and schema adherence.
func (s *Scorer) Score(ctx context.Context, workflowID string, msg hermod.Message) DQResult {
	metrics := make(map[string]float64)

	// 1. Completeness: % of non-null fields in Data()
	data := msg.Data()
	if len(data) > 0 {
		nonNull := 0
		for _, v := range data {
			if v != nil {
				if str, ok := v.(string); ok && str == "" {
					continue
				}
				nonNull++
			}
		}
		metrics[string(DQCompleteness)] = float64(nonNull) / float64(len(data))
	} else {
		metrics[string(DQCompleteness)] = 1.0
	}

	// 2. Validity: Check for schema-related metadata
	if msg.Metadata()["schema_validated"] == "true" {
		metrics[string(DQValidity)] = 1.0
	} else if msg.Metadata()["schema_validation_error"] != "" {
		metrics[string(DQValidity)] = 0.0
	} else {
		metrics[string(DQValidity)] = 1.0 // Assume valid if no error and no explicit validation (for backward compatibility)
	}

	// 3. Consistency/Accuracy (Placeholder - could be expanded)
	if msg.Metadata()["anomaly"] == "true" {
		metrics[string(DQAccuracy)] = 0.5
	} else {
		metrics[string(DQAccuracy)] = 1.0
	}

	// Calculate overall score (average of metrics)
	var total float64
	for _, v := range metrics {
		total += v
	}
	overallScore := total / float64(len(metrics))

	// 4. Drift Detection (simple moving average comparison)
	drift := s.updateHistoryAndCheckDrift(workflowID, overallScore)

	// Attach to message
	msg.SetMetadata("dq_score", fmt.Sprintf("%.2f", overallScore*100))
	msg.SetData("_dq_score", overallScore*100)
	if drift {
		msg.SetMetadata("dq_drift", "true")
	}

	return DQResult{
		Score:         overallScore,
		CheckTime:     time.Now(),
		Metrics:       metrics,
		DriftDetected: drift,
	}
}

func (s *Scorer) updateHistoryAndCheckDrift(workflowID string, score float64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	h := s.history[workflowID]
	if len(h) < 10 {
		s.history[workflowID] = append(h, score)
		return false
	}

	// Calculate average of history
	var sum float64
	for _, v := range h {
		sum += v
	}
	avg := sum / float64(len(h))

	// If current score is more than 2 standard deviations or significant % away
	// Using a simple 30% threshold for now
	drift := math.Abs(score-avg) > 0.3

	// Update history (FIFO)
	if len(h) >= s.historyLimit {
		h = h[1:]
	}
	s.history[workflowID] = append(h, score)

	return drift
}

func (s *Scorer) GetAverageScore(workflowID string) float64 {
	s.mu.RLock()
	defer s.mu.RUnlock()

	h, ok := s.history[workflowID]
	if !ok || len(h) == 0 {
		return 0
	}

	var sum float64
	for _, v := range h {
		sum += v
	}
	return sum / float64(len(h))
}
