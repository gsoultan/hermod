// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package registry

import (
	"context"
	"time"

	"github.com/gsoultan/hermod/internal/advisor"
	"github.com/gsoultan/hermod/internal/storage"
)

// runAdvisorSampler reads every running workflow into the advisor once per
// advisor.SampleInterval until the registry closes.
func (r *Registry) runAdvisorSampler(every time.Duration) {
	defer func() {
		if p := recover(); p != nil {
			r.logger.Error("Registry: advisor sampler panicked", "panic", p)
		}
	}()
	ticker := time.NewTicker(every)
	defer ticker.Stop()
	for {
		select {
		case <-r.ctx.Done():
			return
		case at := <-ticker.C:
			r.sampleAdvisor(at)
		}
	}
}

// sampleAdvisor takes one reading of every running workflow and drops the
// windows of workflows that have stopped.
func (r *Registry) sampleAdvisor(at time.Time) {
	if r.advisor == nil {
		return
	}
	r.mu.RLock()
	type running struct {
		id string
		ae *activeEngine
	}
	engines := make([]running, 0, len(r.engines))
	for id, ae := range r.engines {
		if ae != nil && ae.isWorkflow && ae.engine != nil {
			engines = append(engines, running{id, ae})
		}
	}
	r.mu.RUnlock()

	ids := make(map[string]bool, len(engines))
	for _, e := range engines {
		ids[e.id] = true
		r.advisor.Observe(e.id, advisor.SampleFrom(at, e.ae.engine.GetStatus()))
	}
	r.advisor.Retain(ids)
}

// WorkflowInsights is the advisor's reading of a workflow running here. It
// describes the workflow as the engine runs it (sub-flows spliced in, the
// sink settings it started with), not as it is stored now.
func (r *Registry) WorkflowInsights(ctx context.Context, id string) advisor.Report {
	r.mu.RLock()
	ae, ok := r.engines[id]
	var wf storage.Workflow
	var sinks map[string]storage.Sink
	if ok && ae != nil && ae.isWorkflow {
		wf = ae.workflow
		sinks = make(map[string]storage.Sink, len(ae.snkConfigs))
		for _, c := range ae.snkConfigs {
			sinks[c.ID] = storage.Sink{ID: c.ID, Name: c.ID, Type: c.Type, Config: c.Config}
		}
	}
	r.mu.RUnlock()

	if r.advisor == nil || wf.ID == "" {
		return advisor.Analyze(nil, advisor.Flow{ID: id})
	}
	// Names only make the text readable; a sink that cannot be read keeps its ID.
	if store := r.GetStorage(); store != nil {
		for sid, s := range sinks {
			if stored, err := store.GetSink(ctx, sid); err == nil && stored.Name != "" {
				s.Name = stored.Name
				sinks[sid] = s
			}
		}
	}
	return r.advisor.Report(advisor.FlowFrom(wf, sinks))
}
