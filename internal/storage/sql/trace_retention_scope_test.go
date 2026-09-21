package sql

// Retention is a per-workflow setting. The purge that enforced it was not.
//
// `purgeRetention` loops over workflows and passed each one's cutoff to
// `DELETE FROM message_trace_steps WHERE timestamp < ?` — a statement with no
// workflow predicate. The shortest window in the deployment therefore decided
// what every workflow kept, and the same full-table delete ran once per
// workflow per hour.

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/internal/storage"
	_ "modernc.org/sqlite"
)

func seedTrace(t *testing.T, s storage.Storage, wf, msg string, at time.Time) {
	t.Helper()
	if err := s.RecordTraceStep(t.Context(), wf, msg, hermod.TraceStep{
		NodeID:    "n1",
		Timestamp: at,
		After:     map[string]any{"msg": msg},
	}); err != nil {
		t.Fatalf("seeding %s/%s: %v", wf, msg, err)
	}
}

func stepCount(t *testing.T, db *sql.DB, wf string) int {
	t.Helper()
	var n int
	if err := db.QueryRowContext(t.Context(),
		"SELECT COUNT(*) FROM message_trace_steps WHERE workflow_id = ?", wf).Scan(&n); err != nil {
		t.Fatalf("counting %s: %v", wf, err)
	}
	return n
}

func parentCount(t *testing.T, db *sql.DB, wf string) int {
	t.Helper()
	var n int
	if err := db.QueryRowContext(t.Context(),
		"SELECT COUNT(*) FROM message_traces WHERE workflow_id = ?", wf).Scan(&n); err != nil {
		t.Fatalf("counting parents for %s: %v", wf, err)
	}
	return n
}

// The headline defect: a short window on one workflow deleted another's traces.
func TestPurgeMessageTracesLeavesOtherWorkflowsAlone(t *testing.T) {
	s, db := newTraceStorage(t)
	now := time.Now().UTC()
	old := now.Add(-30 * 24 * time.Hour)

	seedTrace(t, s, "wf_short", "m_old", old)
	seedTrace(t, s, "wf_short", "m_new", now)
	seedTrace(t, s, "wf_long", "m_old", old) // 365d window: must survive
	seedTrace(t, s, "wf_long", "m_new", now)

	err := s.PurgeMessageTraces(t.Context(), storage.TraceRetention{
		Keep: map[string]time.Time{
			"wf_short": now.Add(-7 * 24 * time.Hour),
			"wf_long":  now.Add(-365 * 24 * time.Hour),
		},
		Live:           map[string]struct{}{"wf_short": {}, "wf_long": {}},
		LiveIsComplete: true,
	})
	if err != nil {
		t.Fatalf("PurgeMessageTraces: %v", err)
	}

	if got := stepCount(t, db, "wf_short"); got != 1 {
		t.Errorf("wf_short has %d steps, want 1 (the 30-day-old one expired)", got)
	}
	if got := stepCount(t, db, "wf_long"); got != 2 {
		t.Errorf("wf_long has %d steps, want 2 — a 7d window on another workflow "+
			"deleted traces a 365d window still covers", got)
	}
	// The parent rows have to move with the steps, or the list advertises
	// traces whose steps are gone.
	if got := parentCount(t, db, "wf_long"); got != 2 {
		t.Errorf("wf_long has %d parent rows, want 2", got)
	}
	if got := parentCount(t, db, "wf_short"); got != 1 {
		t.Errorf("wf_short has %d parent rows, want 1", got)
	}
}

// A workflow with no window keeps everything. There is no safe default: a short
// one deletes traces nobody asked to lose, and none at all is the growth bug.
func TestPurgeMessageTracesKeepsWorkflowsWithNoWindow(t *testing.T) {
	s, db := newTraceStorage(t)
	now := time.Now().UTC()

	seedTrace(t, s, "wf_unset", "m_ancient", now.Add(-400*24*time.Hour))

	if err := s.PurgeMessageTraces(t.Context(), storage.TraceRetention{
		Keep:           map[string]time.Time{}, // no entry for wf_unset
		Live:           map[string]struct{}{"wf_unset": {}},
		LiveIsComplete: true,
	}); err != nil {
		t.Fatalf("PurgeMessageTraces: %v", err)
	}

	if got := stepCount(t, db, "wf_unset"); got != 1 {
		t.Errorf("a workflow with no configured window lost %d of its traces", 1-got)
	}
}

// Traces of a deleted workflow are unreachable — the viewer reaches a trace
// through its workflow — and nothing else removes them, so the sweep does,
// regardless of age.
func TestPurgeMessageTracesRemovesTracesOfDeletedWorkflows(t *testing.T) {
	s, db := newTraceStorage(t)
	now := time.Now().UTC()

	seedTrace(t, s, "wf_live", "m1", now)
	seedTrace(t, s, "wf_gone", "m1", now) // brand new, but its workflow is gone

	if err := s.PurgeMessageTraces(t.Context(), storage.TraceRetention{
		Keep:           map[string]time.Time{"wf_live": now.Add(-7 * 24 * time.Hour)},
		Live:           map[string]struct{}{"wf_live": {}},
		LiveIsComplete: true,
	}); err != nil {
		t.Fatalf("PurgeMessageTraces: %v", err)
	}

	if got := stepCount(t, db, "wf_gone"); got != 0 {
		t.Errorf("a deleted workflow still has %d trace steps, and nothing else will ever remove them", got)
	}
	if got := parentCount(t, db, "wf_gone"); got != 0 {
		t.Errorf("a deleted workflow still has %d parent rows", got)
	}
	if got := stepCount(t, db, "wf_live"); got != 1 {
		t.Errorf("the live workflow lost traces: %d left, want 1", got)
	}
}

// The orphan sweep is only safe when the caller can prove it enumerated every
// workflow. It reads a paged list; on a deployment with more workflows than one
// page, everything past the first page looks deleted.
func TestPurgeMessageTracesSkipsOrphansWhenTheWorkflowListIsIncomplete(t *testing.T) {
	s, db := newTraceStorage(t)
	now := time.Now().UTC()

	seedTrace(t, s, "wf_page_two", "m1", now)

	if err := s.PurgeMessageTraces(t.Context(), storage.TraceRetention{
		Keep:           map[string]time.Time{},
		Live:           map[string]struct{}{"wf_page_one": {}},
		LiveIsComplete: false, // the caller could not see every workflow
	}); err != nil {
		t.Fatalf("PurgeMessageTraces: %v", err)
	}

	if got := stepCount(t, db, "wf_page_two"); got != 1 {
		t.Errorf("a workflow the caller simply could not see had its traces swept as an orphan")
	}
}

// Everything for one workflow goes, regardless of age. This is what a deleted
// workflow leaves behind, and nothing else ever reclaims it: DeleteWorkflow
// removes one row, and a trace is only reachable through its workflow.
//
// Not a cascade inside DeleteWorkflow, because SetLogStorage may point traces
// at a different database from the one holding workflows — the Registry drives
// it, and internal/engine/registry covers that assembly.
func TestDeleteWorkflowMessageTracesRemovesOnlyThatWorkflow(t *testing.T) {
	s, db := newTraceStorage(t)
	now := time.Now().UTC()

	seedTrace(t, s, "wf_doomed", "m1", now)
	seedTrace(t, s, "wf_doomed", "m2", now.Add(-time.Hour))
	seedTrace(t, s, "wf_keeper", "m1", now)

	if err := s.DeleteWorkflowMessageTraces(t.Context(), "wf_doomed"); err != nil {
		t.Fatalf("DeleteWorkflowMessageTraces: %v", err)
	}

	if got := stepCount(t, db, "wf_doomed"); got != 0 {
		t.Errorf("left %d trace steps behind", got)
	}
	if got := parentCount(t, db, "wf_doomed"); got != 0 {
		t.Errorf("left %d parent rows behind", got)
	}
	if got := stepCount(t, db, "wf_keeper"); got != 1 {
		t.Errorf("took another workflow's traces: %d left, want 1", got)
	}
}

// A day partition holds every workflow's rows for that day, so it can only be
// dropped once no live workflow still wants any of it.
func TestPartitionFloorIsBlockedByAWorkflowThatKeepsEverything(t *testing.T) {
	now := time.Now().UTC()

	both := storage.TraceRetention{
		Keep: map[string]time.Time{
			"a": now.Add(-7 * 24 * time.Hour),
			"b": now.Add(-365 * 24 * time.Hour),
		},
		Live:           map[string]struct{}{"a": {}, "b": {}},
		LiveIsComplete: true,
	}
	floor, ok := both.PartitionFloor()
	if !ok {
		t.Fatal("two configured workflows should yield a floor")
	}
	if !floor.Equal(now.Add(-365 * 24 * time.Hour)) {
		t.Errorf("floor is %v, want the most conservative window (365d)", floor)
	}

	// One workflow with no window keeps everything, so no whole day is expired.
	withUnset := both
	withUnset.Live = map[string]struct{}{"a": {}, "b": {}, "c": {}}
	if _, ok := withUnset.PartitionFloor(); ok {
		t.Error("a live workflow that keeps everything must block partition drops; " +
			"dropping one destroys its traces to reclaim space for another workflow")
	}

	// And an incomplete list tells us nothing about what may be dropped.
	incomplete := both
	incomplete.LiveIsComplete = false
	if _, ok := incomplete.PartitionFloor(); ok {
		t.Error("an incomplete workflow list must block partition drops")
	}
}

var _ = fmt.Sprint
var _ = context.Background
