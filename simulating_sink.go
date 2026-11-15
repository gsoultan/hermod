// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package hermod

import "context"

// MaxSimulatedRows bounds the rows one simulated step reports. The count is
// always reported in full; only the rows themselves are cut.
const MaxSimulatedRows = 20

// SimulatingSink is an optional interface for a sink that can show what it
// would write without writing it.
//
// Simulate runs the sink's real work for msg and then undoes it. When ctx
// carries a preview sandbox (sqlutil.WithSandbox), the work runs inside the
// sandbox's transaction, so it sees what earlier nodes of the same run wrote
// and the run's rollback undoes it with everything else. Without one the
// sink rolls back its own transaction before returning.
//
// A failure of the work itself is reported in the simulation, not as the
// error: the error is for a simulation that could not be run at all.
type SimulatingSink interface {
	Sink
	Simulate(ctx context.Context, msg Message) (SinkSimulation, error)
}

// SinkSimulation is what a simulated write did.
type SinkSimulation struct {
	// Steps are the units of work in the order they ran. A step that failed
	// is the last one: nothing after it ran.
	Steps []SimulatedStep `json:"steps"`
	// Error is why the write would have failed, or empty.
	Error string `json:"error,omitempty"`
	// RolledBack says the work was undone. It is false only when the undo
	// itself failed, which Error then explains.
	RolledBack bool `json:"rolled_back"`
	// Notes are caveats the reader should know once, such as ids a database
	// consumed even though nothing was committed.
	Notes []string `json:"notes,omitempty"`
}

// SimulatedStep is one statement, request or call a simulated write made.
type SimulatedStep struct {
	Name string `json:"name"`
	// Item is the 1-based element of a repeated step, or 0.
	Item int `json:"item,omitempty"`
	// SQL is the statement as sent, with placeholders.
	SQL  string `json:"sql,omitempty"`
	Args []any  `json:"args,omitempty"`
	// Rows are the first MaxSimulatedRows rows the step returned; RowCount
	// is how many it returned in all.
	Rows     []map[string]any `json:"rows,omitempty"`
	RowCount int64            `json:"row_count"`
	// Affected is how many rows a step that returns none changed, or -1 when
	// the driver cannot say.
	Affected   int64  `json:"affected"`
	DurationMs int64  `json:"duration_ms"`
	Error      string `json:"error,omitempty"`
}

// SimulatorOf finds the SimulatingSink behind s, looking through decorators
// that expose the sink they wrap with Unwrap.
func SimulatorOf(s Sink) (SimulatingSink, bool) {
	for depth := 0; s != nil && depth < 8; depth++ {
		if sim, ok := s.(SimulatingSink); ok {
			return sim, true
		}
		u, ok := s.(interface{ Unwrap() Sink })
		if !ok {
			return nil, false
		}
		s = u.Unwrap()
	}
	return nil, false
}
