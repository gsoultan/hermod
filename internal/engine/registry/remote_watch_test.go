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
	"slices"
	"testing"

	"github.com/gsoultan/hermod/pkg/infra/state"
)

// drainStatus keeps a status subscription from backing up for the life of a test.
func drainStatus(t *testing.T, reg *Registry, workflowID string) {
	t.Helper()
	ch := reg.SubscribeWorkflowStatus(workflowID)
	go func() {
		for range ch {
		}
	}()
	t.Cleanup(func() { reg.UnsubscribeStatus(ch) })
}

// A remote worker has no editor of its own: the platform tells it which of its
// workflows someone has open. Those count as watched here, exactly as a local
// status subscriber would, and stop counting once the platform stops saying so.
func TestRemotelyWatchedWorkflowCountsAsWatched(t *testing.T) {
	reg := NewRegistry(newPipeStorage())

	if reg.IsWorkflowWatched("wf-a") {
		t.Fatal("a workflow nobody has open reads as watched")
	}
	reg.SetRemotelyWatched([]string{"wf-a"})
	if !reg.IsWorkflowWatched("wf-a") {
		t.Fatal("a workflow the platform says is open does not read as watched")
	}
	// Told twice is still one watcher, so one "no longer" ends it.
	reg.SetRemotelyWatched([]string{"wf-a"})
	reg.SetRemotelyWatched(nil)
	if reg.IsWorkflowWatched("wf-a") {
		t.Fatal("still watched after the platform stopped saying so")
	}
}

// The platform's word about remote watchers adds to local subscribers and never
// takes one away.
func TestRemoteWatchLeavesLocalSubscribersAlone(t *testing.T) {
	reg := NewRegistry(newPipeStorage())
	drainStatus(t, reg, "wf-b")

	reg.SetRemotelyWatched([]string{"wf-b"})
	reg.SetRemotelyWatched(nil)
	if !reg.IsWorkflowWatched("wf-b") {
		t.Fatal("clearing remote watchers dropped a local subscriber")
	}
}

// WatchedAmong answers a worker's "which of these are open?" with just those.
func TestWatchedAmongReportsOnlyWatchedWorkflows(t *testing.T) {
	reg := NewRegistry(newPipeStorage())
	drainStatus(t, reg, "wf-open")

	got := reg.WatchedAmong([]string{"wf-closed", "wf-open", "wf-other"})
	if !slices.Equal(got, []string{"wf-open"}) {
		t.Fatalf("WatchedAmong = %v, want [wf-open]", got)
	}
}

// A workflow open on the platform but running on a worker samples its node
// payloads on the worker, so the Sample Inspector and edge previews have
// something to show.
func TestRemotelyWatchedWorkflowSamplesNodePayloads(t *testing.T) {
	reg := NewRegistry(newPipeStorage())
	reg.SetStateStore(state.NewMemoryStore())

	const wfID = "wf-remote-samples"
	reg.SetRemotelyWatched([]string{wfID})

	st := runTelemetryWorkflow(t, reg, wfID, 50)
	if _, ok := st.NodeSamples["map"].(map[string]any); !ok {
		t.Fatalf("no payload sample for node map while watched remotely; samples: %v", keys(st.NodeSamples))
	}
}

// RunningWorkflowIDs lists the workflows this registry is running, which is what
// a worker reports to the platform.
func TestRunningWorkflowIDsListsRunningWorkflows(t *testing.T) {
	reg := NewRegistry(newPipeStorage())
	reg.SetStateStore(state.NewMemoryStore())

	snk := &pipeSink{name: "out", countOnly: true}
	stop := startBenchPipeline(t, reg, benchWorkflow("wf-running"), newWideSource(1, 1), snk)
	defer stop()

	if got := reg.RunningWorkflowIDs(); !slices.Contains(got, "wf-running") {
		t.Fatalf("RunningWorkflowIDs = %v, want it to include wf-running", got)
	}
}
