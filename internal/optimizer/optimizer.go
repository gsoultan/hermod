package optimizer

import (
	"context"
	"fmt"
	"math/rand"
	"sync"
	"time"

	"github.com/user/hermod"
	"github.com/user/hermod/pkg/engine"
	"github.com/user/hermod/pkg/engine/config"
	"github.com/user/hermod/pkg/engine/telemetry"
)

type OptimizerConfig struct {
	OptimizationInterval time.Duration
	EnableAI             bool
}

// Optimizer tracks engine metrics and suggests/applies runtime tunings.
type Optimizer struct {
	mu          sync.Mutex
	registry    map[string]*engine.Engine
	logger      hermod.Logger
	config      OptimizerConfig
	gate        *SelfCorrectionGate
	aiOptimizer AIOptimizer
}

func NewOptimizer(logger hermod.Logger, aiOpt AIOptimizer, notifier func(workflowID, title, message string)) *Optimizer {
	return &Optimizer{
		registry: make(map[string]*engine.Engine),
		logger:   logger,
		config: OptimizerConfig{
			OptimizationInterval: 30 * time.Second,
			EnableAI:             true,
		},
		gate:        NewSelfCorrectionGate(logger, notifier),
		aiOptimizer: aiOpt,
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

			e.UpdateSinkConfig(sinkID, func(cfg *config.SinkConfig) {
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
			e.UpdateSinkConfig(sinkID, func(cfg *config.SinkConfig) {
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

func (o *Optimizer) runAIOptimization(id string, e *engine.Engine, status telemetry.StatusUpdate) {
	if o.aiOptimizer == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := o.aiOptimizer.Optimize(ctx, id, e, status); err != nil {
		o.logger.Error("AIOptimization failed", "error", err, "workflow_id", id)
	}
}
