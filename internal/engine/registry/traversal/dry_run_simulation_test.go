// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package traversal_test

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/internal/engine/registry/traversal"
	"github.com/gsoultan/hermod/internal/storage"
	"github.com/gsoultan/hermod/pkg/comm/message"
	pkgengine "github.com/gsoultan/hermod/pkg/engine"
	"github.com/gsoultan/hermod/pkg/engine/config"
)

type simulatingSink struct {
	calls atomic.Int32
	rep   hermod.SinkSimulation
	err   error
	panic bool
}

func (s *simulatingSink) Write(context.Context, hermod.Message) error { return nil }
func (s *simulatingSink) Ping(context.Context) error                  { return nil }
func (s *simulatingSink) Close() error                                { return nil }
func (s *simulatingSink) Simulate(context.Context, hermod.Message) (hermod.SinkSimulation, error) {
	s.calls.Add(1)
	if s.panic {
		panic("boom")
	}
	return s.rep, s.err
}

func walkToSink(t *testing.T, snk hermod.Sink, dryRun bool) (*mockRegistry, *traversal.WorkflowTraversal) {
	t.Helper()
	reg := &mockRegistry{}
	eng := pkgengine.NewEngine(nil, []hermod.Sink{snk}, nil)
	eng.SetConfig(config.Config{DryRun: dryRun})
	nodeMap := map[string]*storage.WorkflowNode{
		"S": {ID: "S", Type: "source"},
		"K": {ID: "K", Type: "sink"},
	}
	srcMsg := message.AcquireMessage()
	srcMsg.SetID("m1")
	nodeIndex := map[string]int{"S": 0, "K": 1}
	tr := traversal.Acquire(reg, eng, "wf-sim", nodeMap, map[string][]string{"S": {"K"}}, nodeIndex, nil, nil,
		map[string]int{"K": 1}, map[string]int{"K": 0})
	srcMsg.Retain()
	tr.CurrentMessages[nodeIndex["S"]] = srcMsg
	tr.Traverse(t.Context(), "S")
	t.Cleanup(func() {
		for _, rm := range tr.Routed {
			rm.Message.Release()
		}
		srcMsg.Release()
	})
	return reg, tr
}

func (m *mockRegistry) loggedData(part string) bool {
	m.logsMu.Lock()
	defer m.logsMu.Unlock()
	for _, d := range m.Data {
		if strings.Contains(d, part) {
			return true
		}
	}
	return false
}

func logged(reg *mockRegistry, part string) bool {
	reg.logsMu.Lock()
	defer reg.logsMu.Unlock()
	for _, l := range reg.Logs {
		if strings.Contains(l, part) {
			return true
		}
	}
	return false
}

// A dry run writes to no sink, so a sink that can show what it would have
// written is asked to, and what it says reaches the run's log.
func TestADryRunAsksASimulatingSinkWhatItWouldWrite(t *testing.T) {
	snk := &simulatingSink{rep: hermod.SinkSimulation{RolledBack: true, Steps: []hermod.SimulatedStep{
		{Name: "order", RowCount: 1, Affected: -1, Args: []any{"secret-card-number"}, Rows: []map[string]any{{"id": 7}}},
		{Name: "audit", Affected: 1},
	}}}
	reg, tr := walkToSink(t, snk, true)
	if snk.calls.Load() != 1 {
		t.Fatalf("Simulate called %d times, want 1", snk.calls.Load())
	}
	if !logged(reg, "[DRY-RUN] Sink node K would run 2 statements") || !logged(reg, "order") {
		t.Fatalf("logs = %v, want the simulation report", reg.Logs)
	}
	if reg.loggedData("secret-card-number") {
		t.Fatal("a bound value reached the stored log")
	}
	if len(tr.Routed) != 1 {
		t.Fatalf("routed = %d; the dry-run writer still decides what is skipped", len(tr.Routed))
	}
}

func TestALiveRunNeverSimulates(t *testing.T) {
	snk := &simulatingSink{}
	walkToSink(t, snk, false)
	if snk.calls.Load() != 0 {
		t.Fatal("a live run asked the sink to simulate")
	}
}

// A simulation that fails, or panics, is reported and the walk goes on: a dry
// run is for finding exactly this kind of problem.
func TestADryRunReportsAFailedSimulationWithoutFailingTheWalk(t *testing.T) {
	snk := &simulatingSink{rep: hermod.SinkSimulation{RolledBack: true, Error: `statement "audit": expected at least 1 row`}}
	reg, tr := walkToSink(t, snk, true)
	if !logged(reg, `would fail: statement "audit"`) {
		t.Fatalf("logs = %v", reg.Logs)
	}
	if tr.Unaccounted.Load() {
		t.Fatal("a failed simulation marked the walk as failed")
	}

	reg, _ = walkToSink(t, &simulatingSink{err: errors.New("no connection")}, true)
	if !logged(reg, "could not be simulated: no connection") {
		t.Fatalf("logs = %v", reg.Logs)
	}

	reg, _ = walkToSink(t, &simulatingSink{panic: true}, true)
	if !logged(reg, "could not be simulated") {
		t.Fatalf("a panicking simulation was not reported: %v", reg.Logs)
	}
}
