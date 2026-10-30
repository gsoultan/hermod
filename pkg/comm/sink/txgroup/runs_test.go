// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package txgroup

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/pkg/engine/twopc"
)

// Each run of a transaction is recorded.
//
// The run view shows, per transaction, how many runs committed, rolled back,
// were left in doubt and were refused, each member's share, and the latest runs
// with the member that failed one. The group records that as it writes and
// keeps it in the state store, so the view reads the same numbers whichever
// worker runs the workflow, and after a restart.

func TestACommittedRunIsRecordedWithEachMembersRows(t *testing.T) {
	g := newGroup(t, Member{ID: "node-a", Sink: &txSink{name: "a"}}, Member{ID: "node-b", Sink: &txSink{name: "b"}})

	if err := g.WriteBatch(t.Context(), []hermod.Message{
		rowFor(t, "r1", "node-a"), rowFor(t, "r2", "node-b"), rowFor(t, "r3", "node-a"),
	}); err != nil {
		t.Fatalf("WriteBatch: %v", err)
	}

	st := g.Stats()
	if st.Committed != 1 || st.RolledBack != 0 {
		t.Errorf("Committed %d, RolledBack %d; want 1, 0", st.Committed, st.RolledBack)
	}
	if len(st.Recent) != 1 {
		t.Fatalf("Recent = %+v, want one run", st.Recent)
	}
	run := st.Recent[0]
	if run.Outcome != OutcomeCommitted || run.At.IsZero() {
		t.Errorf("run = %+v, want committed with a time", run)
	}
	rows := map[string]int{}
	for _, m := range run.Members {
		rows[m.ID] = m.Rows
	}
	if rows["node-a"] != 2 || rows["node-b"] != 1 {
		t.Errorf("rows per member %v, want node-a 2, node-b 1", rows)
	}
	if a := memberStats(st, "node-a"); a.Committed != 1 {
		t.Errorf("node-a stats %+v, want one commit", a)
	}
}

func TestARunTouchingOneMemberIsACommitOfThatMemberAlone(t *testing.T) {
	g := newGroup(t, Member{ID: "node-a", Sink: &txSink{name: "a"}}, Member{ID: "node-b", Sink: &txSink{name: "b"}})
	if err := g.WriteBatch(t.Context(), []hermod.Message{rowFor(t, "r1", "node-a")}); err != nil {
		t.Fatalf("WriteBatch: %v", err)
	}
	st := g.Stats()
	if st.Committed != 1 || len(st.Recent) != 1 || len(st.Recent[0].Members) != 1 {
		t.Errorf("stats %+v, want one commit naming only node-a", st)
	}
	if b := memberStats(st, "node-b"); b.Committed != 0 {
		t.Errorf("node-b took no part and was counted: %+v", b)
	}
}

func TestARolledBackRunNamesTheMemberThatRefused(t *testing.T) {
	a := &txSink{name: "a"}
	b := &txSink{name: "b", prepareErr: errors.New("max_prepared_transactions is 0")}
	g := newGroup(t, Member{ID: "node-a", Sink: a}, Member{ID: "node-b", Sink: b})

	if err := g.WriteBatch(t.Context(), []hermod.Message{rowFor(t, "r1", "node-a"), rowFor(t, "r2", "node-b")}); err == nil {
		t.Fatal("WriteBatch succeeded although b could not prepare")
	}

	st := g.Stats()
	if st.RolledBack != 1 || st.Committed != 0 {
		t.Errorf("RolledBack %d, Committed %d; want 1, 0", st.RolledBack, st.Committed)
	}
	run := st.Recent[0]
	if run.Outcome != OutcomeRolledBack || run.Error == "" {
		t.Errorf("run = %+v, want rolled back with the reason", run)
	}
	for _, m := range run.Members {
		if m.Failed != (m.ID == "node-b") {
			t.Errorf("member %s Failed = %v; only node-b refused", m.ID, m.Failed)
		}
	}
	bs := memberStats(st, "node-b")
	if bs.RolledBack != 1 || bs.LastError == "" || bs.LastErrorAt.IsZero() {
		t.Errorf("node-b stats %+v, want a rollback with its error", bs)
	}
	if as := memberStats(st, "node-a"); as.RolledBack != 1 || as.LastError != "" {
		t.Errorf("node-a stats %+v, want rolled back with no error of its own", as)
	}
}

func TestAWriteFailureNamesTheMemberToo(t *testing.T) {
	a := &txSink{name: "a", writeErr: errors.New("duplicate key")}
	g := newGroup(t, Member{ID: "node-a", Sink: a}, Member{ID: "node-b", Sink: &txSink{name: "b"}})
	_ = g.WriteBatch(t.Context(), []hermod.Message{rowFor(t, "r1", "node-a"), rowFor(t, "r2", "node-b")})
	for _, m := range g.Stats().Recent[0].Members {
		if m.Failed != (m.ID == "node-a") {
			t.Errorf("member %s Failed = %v; only node-a failed", m.ID, m.Failed)
		}
	}
}

// stuckSink prepares and then cannot commit: the decision stands and recovery
// owes the commit.
type stuckSink struct{ txSink }

func (s *stuckSink) CommitPrepared(context.Context, string) error {
	return errors.New("connection lost")
}

func TestACommitRecoveryMustFinishIsRecordedInDoubt(t *testing.T) {
	g := newGroup(t, Member{ID: "node-a", Sink: &txSink{name: "a"}}, Member{ID: "node-b", Sink: &stuckSink{txSink{name: "b"}}})
	_ = g.WriteBatch(t.Context(), []hermod.Message{rowFor(t, "r1", "node-a"), rowFor(t, "r2", "node-b")})

	st := g.Stats()
	if st.InDoubt != 1 || st.RolledBack != 0 || st.Committed != 0 {
		t.Errorf("InDoubt %d, RolledBack %d, Committed %d; want 1, 0, 0", st.InDoubt, st.RolledBack, st.Committed)
	}
	if st.Recent[0].Outcome != OutcomeInDoubt {
		t.Errorf("outcome %q, want in doubt", st.Recent[0].Outcome)
	}
}

func TestRecentRunsAreBoundedNewestFirst(t *testing.T) {
	g := newGroup(t, Member{ID: "node-a", Sink: &txSink{name: "a"}}, Member{ID: "node-b", Sink: &txSink{name: "b"}})
	for range MaxRecentRuns + 5 {
		if err := g.WriteBatch(t.Context(), []hermod.Message{rowFor(t, "r", "node-a"), rowFor(t, "r", "node-b")}); err != nil {
			t.Fatal(err)
		}
	}
	st := g.Stats()
	if st.Committed != uint64(MaxRecentRuns+5) {
		t.Errorf("Committed %d, want every run counted", st.Committed)
	}
	if len(st.Recent) != MaxRecentRuns {
		t.Errorf("kept %d runs, want %d", len(st.Recent), MaxRecentRuns)
	}
	for i := 1; i < len(st.Recent); i++ {
		if st.Recent[i].At.After(st.Recent[i-1].At) {
			t.Fatalf("runs are not newest first at %d", i)
		}
	}
}

func TestRunsAreKeptInTheStoreAndPickedUpByTheNextGroup(t *testing.T) {
	store := newMemStore()
	g := newGroup(t, Member{ID: "node-a", Sink: &txSink{name: "a"}}, Member{ID: "node-b", Sink: &txSink{name: "b"}})
	g.KeepRunsIn(t.Context(), store, "twopc-runs/wf-1/order")
	if err := g.WriteBatch(t.Context(), []hermod.Message{rowFor(t, "r1", "node-a"), rowFor(t, "r2", "node-b")}); err != nil {
		t.Fatal(err)
	}

	raw, err := store.Get(t.Context(), "twopc-runs/wf-1/order")
	if err != nil {
		t.Fatalf("the runs were not stored: %v", err)
	}
	var stored Stats
	if err := json.Unmarshal(raw, &stored); err != nil || stored.Committed != 1 {
		t.Fatalf("stored %s (%v), want one commit", raw, err)
	}

	// A restart: a new group over the same store carries on counting.
	next := newGroup(t, Member{ID: "node-a", Sink: &txSink{name: "a"}}, Member{ID: "node-b", Sink: &txSink{name: "b"}})
	next.KeepRunsIn(t.Context(), store, "twopc-runs/wf-1/order")
	if err := next.WriteBatch(t.Context(), []hermod.Message{rowFor(t, "r3", "node-a"), rowFor(t, "r4", "node-b")}); err != nil {
		t.Fatal(err)
	}
	if st := next.Stats(); st.Committed != 2 || len(st.Recent) != 2 {
		t.Errorf("after a restart Committed %d with %d runs, want 2 and 2", st.Committed, len(st.Recent))
	}
	got, err := ReadRuns(t.Context(), store, "twopc-runs/wf-1/order")
	if err != nil || got.Committed != 2 {
		t.Errorf("ReadRuns = %+v, %v; want two commits", got, err)
	}
}

func TestReadRunsOfATransactionThatNeverRanIsEmpty(t *testing.T) {
	got, err := ReadRuns(t.Context(), newMemStore(), "twopc-runs/none")
	if err != nil || got.Committed != 0 || len(got.Recent) != 0 {
		t.Errorf("ReadRuns = %+v, %v; want empty and no error", got, err)
	}
}

var _ = twopc.ErrNotFound
