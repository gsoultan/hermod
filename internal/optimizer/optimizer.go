// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package optimizer

import (
	"context"
	"maps"
	"sync"
	"time"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/pkg/engine"
	"github.com/gsoultan/hermod/pkg/engine/telemetry"
)

type OptimizerConfig struct {
	OptimizationInterval time.Duration
	EnableAI             bool
}

// Optimizer watches running engines for failure patterns and proposes
// fixes. It never changes an engine or a stored workflow itself.
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
			// Off. Its triggers read the status frame's error rate and
			// backpressure, which were always 0 until the frame started
			// filling them in, so it never ran. Left on, a failing flow would
			// send its status (payload samples included) to an LLM every cycle
			// and discard the reply, and a busy one would have its concurrency
			// raised with no ceiling.
			EnableAI: false,
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
	maps.Copy(engines, o.registry)
	o.mu.Unlock()

	for id, e := range engines {
		o.optimizeEngine(id, e)
	}
}

func (o *Optimizer) optimizeEngine(id string, e *engine.Engine) {
	status := e.GetStatus()

	// Sink sizing is not done here. This loop used to rewrite a running
	// sink's batch size on its own; that is now a recommendation of
	// internal/advisor, for a person to act on.

	// Self-Correction Gates
	if o.gate != nil {
		o.gate.Analyze(id, e, status)
	}

	// AI-Driven Pattern Recognition (Future)
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
