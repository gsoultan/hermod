package optimizer

import (
	"context"
	"fmt"
	"math/rand"
	"sync"
	"time"

	"github.com/user/hermod"
	"github.com/user/hermod/pkg/engine"
)

type OptimizerConfig struct {
	OptimizationInterval time.Duration
	EnableAI             bool
}

// Optimizer tracks engine metrics and suggests/applies runtime tunings.
type Optimizer struct {
	mu       sync.Mutex
	registry map[string]*engine.Engine
	logger   hermod.Logger
	config   OptimizerConfig
	gate     *SelfCorrectionGate
}

func NewOptimizer(logger hermod.Logger, notifier func(workflowID, title, message string)) *Optimizer {
	return &Optimizer{
		registry: make(map[string]*engine.Engine),
		logger:   logger,
		config: OptimizerConfig{
			OptimizationInterval: 30 * time.Second,
			EnableAI:             false,
		},
		gate: NewSelfCorrectionGate(logger, notifier),
	}
}

func (o *Optimizer) Start(ctx context.Context) {
	ticker := time.NewTicker(o.config.OptimizationInterval)
	defer ticker.Stop()

	o.logger.Info("Autonomous Pipeline Optimizer started", "interval", o.config.OptimizationInterval)

	for {
		select {
		case <-ticker.C:
			o.optimizeAll()
		case <-ctx.Done():
			o.logger.Info("Autonomous Pipeline Optimizer stopping")
			return
		}
	}
}

func (o *Optimizer) Register(id string, e *engine.Engine) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.registry[id] = e
}

func (o *Optimizer) Unregister(id string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	delete(o.registry, id)
}

func (o *Optimizer) optimizeAll() {
	o.mu.Lock()
	engines := make(map[string]*engine.Engine)
	for id, e := range o.registry {
		engines[id] = e
	}
	o.mu.Unlock()

	for id, e := range engines {
		o.optimizeEngine(id, e)
	}

	// 4. Chaos Monkey (Resilience Testing)
	o.runChaosMonkey()
}

func (o *Optimizer) runChaosMonkey() {
	o.mu.Lock()
	defer o.mu.Unlock()

	// Only run if there are active engines and we want to test resilience
	if len(o.registry) == 0 {
		return
	}

	// Very low probability of failure simulation (e.g. 0.1% per optimization cycle)
	if rand.Float64() > 0.001 {
		return
	}

	// Pick a random engine to "perturb"
	ids := make([]string, 0, len(o.registry))
	for id := range o.registry {
		ids = append(ids, id)
	}
	targetID := ids[rand.Intn(len(ids))]
	_ = o.registry[targetID] // Ensure engine exists

	o.logger.Warn("Chaos Monkey: Simulating node transient failure", "workflow_id", targetID)

	// Simulate failure by temporarily pausing processing or injecting error metadata
	if eng, ok := o.registry[targetID]; ok {
		eng.SimulateFailure(10 * time.Second)
	}
}

func (o *Optimizer) optimizeEngine(id string, e *engine.Engine) {
	status := e.GetStatus()

	// 1. Adaptive Batching & Concurrency Tuning
	for sinkID, fill := range status.SinkBufferFill {
		if fill > 0.8 {
			o.logger.Info("Optimizer: high buffer pressure detected, increasing batch size and concurrency",
				"workflow_id", id, "sink_id", sinkID, "fill", fmt.Sprintf("%.2f", fill))

			e.UpdateSinkConfig(sinkID, func(cfg *engine.SinkConfig) {
				// Increase batch size to handle more volume per request
				cfg.BatchSize = int(float64(cfg.BatchSize) * 1.2)
				if cfg.BatchSize > 10000 {
					cfg.BatchSize = 10000
				}
				// Slightly increase timeout to allow filling larger batches
				cfg.BatchTimeout = time.Duration(float64(cfg.BatchTimeout) * 1.1)
				if cfg.BatchTimeout > 2*time.Second {
					cfg.BatchTimeout = 2 * time.Second
				}
			})
		} else if fill < 0.1 && status.ProcessedCount > 1000 {
			// If buffer is very empty and we have processed enough messages (not just starting),
			// maybe we can reduce batch size to improve latency.
			e.UpdateSinkConfig(sinkID, func(cfg *engine.SinkConfig) {
				if cfg.BatchSize > 10 {
					cfg.BatchSize = int(float64(cfg.BatchSize) * 0.8)
					if cfg.BatchSize < 10 {
						cfg.BatchSize = 10
					}
				}
			})
		}
	}

	// 2. Self-Correction Gates
	if o.gate != nil {
		o.gate.Analyze(id, e, status)
	}

	// 3. AI-Driven Pattern Recognition (Future)
	if o.config.EnableAI {
		o.runAIOptimization(id, e, status)
	}
}

func (o *Optimizer) runAIOptimization(id string, e *engine.Engine, status engine.StatusUpdate) {
	// AI-Driven Pattern Recognition
	// In a real implementation, this would call an LLM with historical metrics.
	// Here we implement advanced heuristic logic that simulates AI insights.

	o.logger.Debug("Running AI optimization analysis", "workflow_id", id)

	// Pattern 1: High Latency with Low Throughput (Congestion AI)
	if status.ProcessedCount > 0 && status.AvgLatency > 500*time.Millisecond {
		o.logger.Info("AI Insight: Pipeline congestion detected. Increasing parallelism and adjusting backoff.", "workflow_id", id)
		for sinkID := range status.SinkBufferFill {
			e.UpdateSinkConfig(sinkID, func(cfg *engine.SinkConfig) {
				cfg.Concurrency++
				if cfg.Concurrency > 20 {
					cfg.Concurrency = 20
				}
				// Reduce retry interval slightly to recover faster if destination is just slow but up
				cfg.RetryInterval = time.Duration(float64(cfg.RetryInterval) * 0.9)
			})
		}
	}

	// Pattern 2: Success Rate Decay (Stability AI)
	successRate := 1.0
	totalNodes := 0
	for nodeID, count := range status.NodeMetrics {
		if count > 0 {
			totalNodes++
			errCount := status.NodeErrorMetrics[nodeID]
			nodeSR := 1.0 - (float64(errCount) / float64(count))
			if nodeSR < successRate {
				successRate = nodeSR
			}
		}
	}

	if totalNodes > 0 && successRate < 0.95 {
		o.logger.Warn("AI Insight: Stability decay detected. Enabling stricter circuit breaking.", "workflow_id", id)
		for sinkID := range status.SinkStatuses {
			e.UpdateSinkConfig(sinkID, func(cfg *engine.SinkConfig) {
				if cfg.CircuitBreakerThreshold > 5 {
					cfg.CircuitBreakerThreshold = 5 // Tighten threshold
				}
				cfg.CircuitBreakerCoolDown *= 2 // Increase cooldown
			})
		}
	}

	// Pattern 3: High Error Rate + High Throughput (Burn AI)
	if totalNodes > 0 && successRate < 0.7 && status.ProcessedCount > 5000 {
		o.logger.Error("AI Insight: High-volume failure pattern (Burn). Enabling emergency throttling.", "workflow_id", id)
		for sinkID := range status.SinkStatuses {
			e.UpdateSinkConfig(sinkID, func(cfg *engine.SinkConfig) {
				cfg.Concurrency = 1
				cfg.BatchSize = 1
				cfg.RetryInterval = 5 * time.Second // Slow down retries significantly
			})
		}
	}
}
