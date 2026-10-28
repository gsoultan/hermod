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
	"testing"
	"time"

	"github.com/gsoultan/hermod/pkg/engine/telemetry"
	"github.com/gsoultan/hermod/pkg/infra/state"
)

// runTelemetryWorkflow drives `messages` rows through the real
// source -> condition -> mapping -> sink workflow and returns its status once
// every row has reached the sink.
func runTelemetryWorkflow(t *testing.T, reg *Registry, wfID string, messages int) telemetry.StatusUpdate {
	t.Helper()
	snk := &pipeSink{name: "out", countOnly: true}
	src := newWideSource(int64(messages), 8)
	wf := benchWorkflow(wfID)

	stop := startBenchPipeline(t, reg, wf, src, snk)
	defer stop()

	deadline := time.Now().Add(30 * time.Second)
	for snk.count() < messages {
		if time.Now().After(deadline) {
			t.Fatalf("only %d of %d messages reached the sink", snk.count(), messages)
		}
		time.Sleep(time.Millisecond)
	}
	eng, ok := reg.GetEngine(wfID)
	if !ok {
		t.Fatal("engine not found")
	}
	return eng.GetStatus()
}

// A running workflow reports, per node, how many messages ran through it and
// how long it takes, and per edge how many messages travelled it. Before this
// the router produced none of it: node_metrics, edge_metrics and node_samples
// were empty for every workflow.
func TestRunningWorkflowReportsPerNodeAndPerEdgeTelemetry(t *testing.T) {
	const messages = 200
	reg := NewRegistry(newPipeStorage())
	reg.SetStateStore(state.NewMemoryStore())

	st := runTelemetryWorkflow(t, reg, "wf-node-telemetry", messages)

	for _, node := range []string{"src", "cond", "map", "out"} {
		if got := st.NodeMetrics[node]; got != messages {
			t.Errorf("NodeMetrics[%s] = %d, want %d", node, got, messages)
		}
	}
	for _, edge := range []string{"src->cond", "cond->map", "map->out"} {
		if got := st.EdgeMetrics[edge]; got != messages {
			t.Errorf("EdgeMetrics[%s] = %d, want %d", edge, got, messages)
		}
	}
	// The editor draws each canvas edge from its own count, keyed by its ID.
	for _, edge := range []string{"e1", "e2", "e3"} {
		if got := st.EdgeCounts[edge]; got != messages {
			t.Errorf("EdgeCounts[%s] = %d, want %d", edge, got, messages)
		}
	}
	if st.SampledAt == 0 {
		t.Error("the status does not say when its edge counts were read")
	}
	for _, node := range []string{"cond", "map"} {
		if got := st.NodeLatencies[node]; got <= 0 {
			t.Errorf("NodeLatencies[%s] = %v, want a measured run time", node, got)
		}
	}
	if len(st.NodeSamples) != 0 {
		t.Errorf("payload samples were taken with nobody watching the workflow: %v", keys(st.NodeSamples))
	}
}

// With the workflow open in the editor (a per-workflow status subscriber), each
// node's latest payload is sampled for the Sample Inspector.
func TestWatchedWorkflowSamplesNodePayloads(t *testing.T) {
	reg := NewRegistry(newPipeStorage())
	reg.SetStateStore(state.NewMemoryStore())

	const wfID = "wf-node-samples"
	ch := reg.SubscribeWorkflowStatus(wfID)
	go func() {
		for u := range ch {
			_ = u // drained so broadcasts never back up
		}
	}()
	defer reg.UnsubscribeStatus(ch)

	st := runTelemetryWorkflow(t, reg, wfID, 50)

	sample, ok := st.NodeSamples["map"].(map[string]any)
	if !ok {
		t.Fatalf("no payload sample for node map while watched; samples: %v", keys(st.NodeSamples))
	}
	if _, ok := sample["band"]; !ok {
		t.Errorf("the map node's sample is not its output (no \"band\" field): %v", sample)
	}
}

func keys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// While someone has a workflow open, its status is pushed on a steady beat so
// the editor's edge rates stay current. The engine only pushes on its own
// events and its health tick, every few seconds and unevenly. Nobody watching
// costs nothing: no frame is built.
func TestWatchedWorkflowStatusIsPushedOnABeat(t *testing.T) {
	reg := NewRegistry(newPipeStorage())
	reg.SetStateStore(state.NewMemoryStore())
	const wfID = "wf-status-beat"

	runTelemetryWorkflowAndKeepRunning := func() func() {
		snk := &pipeSink{name: "out", countOnly: true}
		return startBenchPipeline(t, reg, benchWorkflow(wfID), newWideSource(10, 8), snk)
	}
	stop := runTelemetryWorkflowAndKeepRunning()
	defer stop()

	if pushed := reg.pushWatchedStatuses(); pushed != 0 {
		t.Fatalf("pushed %d frames with nobody watching, want 0", pushed)
	}

	ch := reg.SubscribeWorkflowStatus(wfID)
	defer reg.UnsubscribeStatus(ch)
	for len(ch) > 0 {
		<-ch
	}

	if pushed := reg.pushWatchedStatuses(); pushed != 1 {
		t.Fatalf("pushed %d frames with the workflow open, want 1", pushed)
	}
	select {
	case u := <-ch:
		if u.WorkflowID != wfID || u.SampledAt == 0 {
			t.Errorf("pushed frame = workflow %q, sampled_at %d; want %q and a read time", u.WorkflowID, u.SampledAt, wfID)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the watched workflow's subscriber got no frame")
	}
}
