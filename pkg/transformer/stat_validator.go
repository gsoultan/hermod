package transformer

import (
	"context"
	"fmt"
	"math"
	"sync"

	"github.com/user/hermod"
)

func init() {
	Register("stat_validator", &StatValidatorTransformer{
		stats: make(map[string]*rollingStats),
	})
}

type rollingStats struct {
	count float64
	mean  float64
	m2    float64
	mu    sync.RWMutex
}

// Update adds a new value to the rolling statistics using Welford's online algorithm.
func (s *rollingStats) Update(x float64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.count++
	delta := x - s.mean
	s.mean += delta / s.count
	delta2 := x - s.mean
	s.m2 += delta * delta2
}

func (s *rollingStats) Variance() float64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.count < 2 {
		return 0
	}
	return s.m2 / (s.count - 1)
}

func (s *rollingStats) StdDev() float64 {
	return math.Sqrt(s.Variance())
}

func (s *rollingStats) Mean() float64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.mean
}

type StatValidatorTransformer struct {
	stats map[string]*rollingStats
	mu    sync.RWMutex
}

func (t *StatValidatorTransformer) Transform(ctx context.Context, msg hermod.Message, config map[string]any) (hermod.Message, error) {
	field, _ := config["field"].(string)
	if field == "" {
		return msg, nil
	}

	threshold, _ := config["threshold"].(float64)
	if threshold == 0 {
		threshold = 3.0 // Default to 3 sigma
	}

	minSamples, _ := config["min_samples"].(float64)
	if minSamples == 0 {
		minSamples = 10
	}

	data := msg.Data()
	if data == nil {
		return msg, nil
	}

	val, ok := data[field]
	if !ok {
		return msg, nil
	}

	var x float64
	switch v := val.(type) {
	case float64:
		x = v
	case int:
		x = float64(v)
	case int64:
		x = float64(v)
	default:
		return msg, nil
	}

	t.mu.Lock()
	stat, exists := t.stats[field]
	if !exists {
		stat = &rollingStats{}
		t.stats[field] = stat
	}
	t.mu.Unlock()

	mean := stat.Mean()
	stdDev := stat.StdDev()
	count := 0.0
	stat.mu.RLock()
	count = stat.count
	stat.mu.RUnlock()

	// Update stats after reading for next message, or before?
	// Usually we update before checking if we want to include current message in baseline.
	// But for anomaly detection, we check against current baseline.
	if count >= minSamples {
		zScore := 0.0
		if stdDev > 0 {
			zScore = math.Abs(x-mean) / stdDev
		}

		if zScore > threshold {
			msg.SetMetadata("anomaly", "true")
			msg.SetMetadata("z_score", fmt.Sprintf("%.2f", zScore))

			action, _ := config["action"].(string)
			if action == "drop" {
				return nil, fmt.Errorf("stat_validator: anomaly detected (z-score: %.2f) on field %s", zScore, field)
			}
		}
	}

	stat.Update(x)

	return msg, nil
}
