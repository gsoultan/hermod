// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package traversal

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/pkg/infra/sqlutil"
)

// simulateSink asks the sink behind a sink node what it would have written,
// on a dry run, and logs the answer. A dry run writes to no sink, so without
// this a preview of a workflow ending in a Transactional SQL sink showed
// nothing about the statements that are the point of it.
//
// Only a dry run calls this. The simulation runs in the message's sandbox
// (ctx carries it), so it sees what the walk's own nodes wrote and is rolled
// back with them. Whatever it does -- fail, or panic -- is reported and the
// walk goes on: the routing below still decides what the dry-run writer skips.
func (t *WorkflowTraversal) simulateSink(ctx context.Context, nodeID string, msgs []hermod.Message) {
	idx, ok := t.SinkNodeToIndex[nodeID]
	if !ok {
		return
	}
	sinks := t.Eng.GetSinks()
	if idx < 0 || idx >= len(sinks) {
		return
	}
	sim, ok := hermod.SimulatorOf(sinks[idx])
	if !ok {
		return
	}
	for _, m := range msgs {
		t.simulateOne(ctx, nodeID, sim, m)
	}
}

func (t *WorkflowTraversal) simulateOne(ctx context.Context, nodeID string, sim hermod.SimulatingSink, msg hermod.Message) {
	defer func() {
		if rec := recover(); rec != nil {
			t.Registry.BroadcastLog(t.WorkflowID, "WARN", fmt.Sprintf(
				"[DRY-RUN] Sink node %s could not be simulated: it panicked: %v", nodeID, rec), msg.ID())
		}
	}()
	// The sandbox has one connection per database; taking the node's turn
	// keeps a parallel branch off it while this runs.
	nctx, leave := sqlutil.EnterNode(ctx)
	defer leave()

	rep, err := sim.Simulate(nctx, msg)
	if err != nil {
		t.Registry.BroadcastLog(t.WorkflowID, "WARN", fmt.Sprintf(
			"[DRY-RUN] Sink node %s could not be simulated: %v", nodeID, err), msg.ID())
		return
	}
	level, line := "INFO", fmt.Sprintf("[DRY-RUN] Sink node %s would run %d statements: %s",
		nodeID, len(rep.Steps), summarize(rep.Steps))
	if rep.Error != "" {
		level, line = "WARN", fmt.Sprintf("[DRY-RUN] Sink node %s would fail: %s", nodeID, rep.Error)
	}
	details, _ := json.Marshal(forLog(rep))
	t.Registry.BroadcastLog(t.WorkflowID, level, line, string(details))
}

// forLog drops the bound values and returned rows from a report. Logs are
// stored and kept; values are message data and rows are the database's, and
// neither belongs there. The Test button, which nothing stores, shows both.
func forLog(rep hermod.SinkSimulation) hermod.SinkSimulation {
	steps := make([]hermod.SimulatedStep, len(rep.Steps))
	for i, s := range rep.Steps {
		s.Args, s.Rows = nil, nil
		steps[i] = s
	}
	rep.Steps = steps
	return rep
}

// summarize is one clause per step: what it returned or changed.
func summarize(steps []hermod.SimulatedStep) string {
	const most = 10
	parts := make([]string, 0, min(len(steps), most)+1)
	for i, s := range steps {
		if i == most {
			parts = append(parts, fmt.Sprintf("and %d more", len(steps)-most))
			break
		}
		name := s.Name
		if s.Item > 0 {
			name = fmt.Sprintf("%s #%d", s.Name, s.Item)
		}
		switch {
		case s.Error != "":
			parts = append(parts, name+" failed")
		case s.Affected >= 0:
			parts = append(parts, fmt.Sprintf("%s changed %d", name, s.Affected))
		default:
			parts = append(parts, fmt.Sprintf("%s returned %d", name, s.RowCount))
		}
	}
	return strings.Join(parts, ", ")
}
