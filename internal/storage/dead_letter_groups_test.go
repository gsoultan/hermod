// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package storage

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

// groupStore is a DeadLetterStore kept in memory, enough to drive the
// grouping logic the backends share.
type groupStore struct {
	mu      sync.Mutex
	letters map[string]DeadLetter
	// conflicts makes the next n UpdateDeadLetter calls lose a race.
	conflicts int
}

func newGroupStore() *groupStore { return &groupStore{letters: map[string]DeadLetter{}} }

func (s *groupStore) PutDeadLetter(_ context.Context, d DeadLetter) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	d = CapDeadLetter(d)
	if err := ValidateDeadLetter(d); err != nil {
		return err
	}
	s.letters[d.ID] = d
	return nil
}

func (s *groupStore) GetDeadLetter(_ context.Context, id string) (DeadLetter, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	d, ok := s.letters[id]
	if !ok {
		return DeadLetter{}, ErrNotFound
	}
	return d, nil
}

func (s *groupStore) FindDeadLetterGroup(_ context.Context, g DeadLetterGroupKey) (DeadLetter, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, d := range s.letters {
		if d.Status == DeadLetterQueued && d.GroupKey() == g {
			return d, nil
		}
	}
	return DeadLetter{}, ErrNotFound
}

func (s *groupStore) UpdateDeadLetter(_ context.Context, d DeadLetter, prevCount int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.conflicts > 0 {
		s.conflicts--
		cur := s.letters[d.ID]
		cur.FailureCount++ // someone else folded a failure in first
		s.letters[d.ID] = cur
		return ErrDeadLetterConflict
	}
	cur, ok := s.letters[d.ID]
	if !ok {
		return ErrNotFound
	}
	if cur.Status != DeadLetterQueued || cur.Attempts() != prevCount {
		return ErrDeadLetterConflict
	}
	d = CapDeadLetter(d)
	d.Status = cur.Status
	s.letters[d.ID] = d
	return nil
}

func (s *groupStore) ListDeadLetters(_ context.Context, f DeadLetterFilter) ([]DeadLetter, int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []DeadLetter
	for _, d := range s.letters {
		if f.Status != "" && d.Status != f.Status {
			continue
		}
		if f.Ungrouped && d.RecordKey != "" {
			continue
		}
		if f.WorkflowID != "" && d.WorkflowID != f.WorkflowID {
			continue
		}
		out = append(out, d)
	}
	slices.SortFunc(out, func(a, b DeadLetter) int {
		if f.OldestFirst {
			return a.FailedAt.Compare(b.FailedAt)
		}
		return b.FailedAt.Compare(a.FailedAt)
	})
	total := len(out)
	if f.Offset < len(out) {
		out = out[f.Offset:]
	} else {
		out = nil
	}
	if f.Limit > 0 && len(out) > f.Limit {
		out = out[:f.Limit]
	}
	return out, total, nil
}

func (s *groupStore) SettleDeadLetter(_ context.Context, id string, status DeadLetterStatus, ev DeadLetterEvent) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	d, ok := s.letters[id]
	if !ok {
		return ErrNotFound
	}
	if d.Status != DeadLetterQueued {
		return ErrDeadLetterSettled
	}
	d.Status = status
	d.History = AppendDeadLetterEvent(d.History, ev)
	s.letters[id] = d
	return nil
}

func (s *groupStore) AddDeadLetterEvent(context.Context, string, DeadLetterEvent) error { return nil }
func (s *groupStore) DeadLetterStats(context.Context, []string, time.Time) (DeadLetterStats, error) {
	return DeadLetterStats{}, nil
}
func (s *groupStore) PurgeDeadLetters(context.Context, time.Time) (int64, error) { return 0, nil }

func failure(id string, at time.Time, payload string) DeadLetter {
	return DeadLetter{
		ID: id, VHost: "", WorkflowID: "wf-1", MessageID: "msg-" + id, SinkID: "panmail",
		Error: "panmail: invalid credentials", FailedAt: at, Payload: []byte(payload),
		Origin:  DeadLetterOrigin{Operation: "create"},
		History: []DeadLetterEvent{{At: at, Action: "failed", Detail: "panmail"}},
	}
}

func TestTheRecordKeyOfAPayloadIgnoresKeyOrderAndWhitespace(t *testing.T) {
	a := DeadLetterRecordKey([]byte(`{"b":1,"a":{"y":2,"x":"z"}}`), DeadLetterOrigin{Operation: "create"}, nil)
	b := DeadLetterRecordKey([]byte(" {\"a\": {\"x\": \"z\", \"y\": 2},\n \"b\": 1}"), DeadLetterOrigin{Operation: "create"}, nil)
	if a == "" || a != b {
		t.Fatalf("the same JSON written two ways got keys %q and %q", a, b)
	}
	if !strings.HasPrefix(a, "payload:") {
		t.Fatalf("a payload fingerprint should say so: %q", a)
	}
}

func TestTheRecordKeyTellsDifferentPayloadsAndOperationsApart(t *testing.T) {
	base := DeadLetterRecordKey([]byte(`{"id":1}`), DeadLetterOrigin{Operation: "create"}, nil)
	for name, other := range map[string]string{
		"another payload":    DeadLetterRecordKey([]byte(`{"id":2}`), DeadLetterOrigin{Operation: "create"}, nil),
		"another operation":  DeadLetterRecordKey([]byte(`{"id":1}`), DeadLetterOrigin{Operation: "update"}, nil),
		"another table":      DeadLetterRecordKey([]byte(`{"id":1}`), DeadLetterOrigin{Operation: "create", Table: "t"}, nil),
		"a non-JSON payload": DeadLetterRecordKey([]byte(`id=1`), DeadLetterOrigin{Operation: "create"}, nil),
	} {
		if other == base {
			t.Errorf("%s got the same key as the base record", name)
		}
	}
}

func TestTheRecordKeyUsesTheNamedFieldsAndIgnoresTheRest(t *testing.T) {
	fields := []string{"Application.application_id", "payload.DocumentTypeIds"}
	a := DeadLetterRecordKey([]byte(`{"Application":{"application_id":"app-1"},"payload":{"DocumentTypeIds":"x,y"},"URL":"signed-1"}`), DeadLetterOrigin{}, fields)
	b := DeadLetterRecordKey([]byte(`{"URL":"signed-2","payload":{"DocumentTypeIds":"x,y"},"Application":{"application_id":"app-1"}}`), DeadLetterOrigin{}, fields)
	c := DeadLetterRecordKey([]byte(`{"Application":{"application_id":"app-1"},"payload":{"DocumentTypeIds":"z"}}`), DeadLetterOrigin{}, fields)
	if a != b {
		t.Fatalf("a field outside the key changed the key: %q vs %q", a, b)
	}
	if a == c {
		t.Fatal("a field inside the key did not change the key")
	}
	if !strings.HasPrefix(a, "fields:") {
		t.Fatalf("a key from named fields should say so: %q", a)
	}
}

func TestTheRecordKeyFallsBackToThePayloadWhenNoNamedFieldIsThere(t *testing.T) {
	got := DeadLetterRecordKey([]byte(`{"id":1}`), DeadLetterOrigin{}, []string{"order.number"})
	want := DeadLetterRecordKey([]byte(`{"id":1}`), DeadLetterOrigin{}, nil)
	if got != want {
		t.Fatalf("with none of the fields present the key is %q, want the payload fingerprint %q", got, want)
	}
}

func TestKeepingTheSameRecordTwiceCountsIntoOneGroup(t *testing.T) {
	st := newGroupStore()
	ctx := t.Context()
	t0 := time.Date(2026, 10, 30, 17, 41, 34, 0, time.UTC)
	payload := `{"ApplicationId":"app-1"}`

	var ids []string
	for i := range 263 {
		id, err := KeepDeadLetter(ctx, st, failure(fmt.Sprintf("a%d", i), t0.Add(time.Duration(i)*20*time.Second), payload), nil)
		if err != nil {
			t.Fatalf("keeping failure %d: %v", i, err)
		}
		ids = append(ids, id)
	}
	if len(st.letters) != 1 {
		t.Fatalf("263 failures of one record made %d rows, want 1", len(st.letters))
	}
	g := st.letters[ids[0]]
	for _, id := range ids {
		if id != g.ID {
			t.Fatalf("a later failure reported group %s, want %s", id, g.ID)
		}
	}
	last := t0.Add(262 * 20 * time.Second)
	if g.Attempts() != 263 || !g.FirstFailedAt.Equal(t0) || !g.FailedAt.Equal(last) || g.MessageID != "msg-a262" {
		t.Fatalf("group = %d attempts, first %s, last %s, message %s; want 263, %s, %s, msg-a262",
			g.Attempts(), g.FirstFailedAt, g.FailedAt, g.MessageID, t0, last)
	}
	if len(g.History) != 2 || g.History[0].Action != "failed" || g.History[1].Action != "failed_again" || g.History[1].Count != 262 {
		t.Fatalf("identical failures should fold into one history line, got %+v", g.History)
	}
	if g.History[1].Until == nil || !g.History[1].Until.Equal(last) {
		t.Fatalf("the folded line should end at the last failure, got %+v", g.History[1])
	}
}

func TestADifferentRecordOrPlaceGetsItsOwnGroup(t *testing.T) {
	st := newGroupStore()
	ctx := t.Context()
	now := time.Now().UTC()
	a := failure("a", now, `{"id":1}`)
	b := failure("b", now, `{"id":2}`)
	c := failure("c", now, `{"id":1}`)
	c.SinkID = "smtp"
	d := failure("d", now, `{"id":1}`)
	d.WorkflowID = "wf-2"
	for _, x := range []DeadLetter{a, b, c, d} {
		if _, err := KeepDeadLetter(ctx, st, x, nil); err != nil {
			t.Fatal(err)
		}
	}
	if len(st.letters) != 4 {
		t.Fatalf("four different records or places made %d rows, want 4", len(st.letters))
	}
}

func TestAFailureAfterTheGroupWasSettledOpensANewGroup(t *testing.T) {
	st := newGroupStore()
	ctx := t.Context()
	now := time.Now().UTC()
	first, err := KeepDeadLetter(ctx, st, failure("a", now, `{"id":1}`), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SettleDeadLetter(ctx, first, DeadLetterReplayed, NewDeadLetterEvent("replayed", "ops", "")); err != nil {
		t.Fatal(err)
	}
	second, err := KeepDeadLetter(ctx, st, failure("b", now.Add(time.Hour), `{"id":1}`), nil)
	if err != nil {
		t.Fatal(err)
	}
	if second == first || len(st.letters) != 2 || st.letters[first].Attempts() != 1 {
		t.Fatalf("a failure after a replay should start again, got groups %s and %s, %d rows", first, second, len(st.letters))
	}
}

func TestKeepingRetriesWhenAnotherWriterFoldedFirst(t *testing.T) {
	st := newGroupStore()
	ctx := t.Context()
	now := time.Now().UTC()
	if _, err := KeepDeadLetter(ctx, st, failure("a", now, `{"id":1}`), nil); err != nil {
		t.Fatal(err)
	}
	st.conflicts = 2
	id, err := KeepDeadLetter(ctx, st, failure("b", now.Add(time.Second), `{"id":1}`), nil)
	if err != nil {
		t.Fatalf("two lost races should still keep the failure: %v", err)
	}
	// One failure from the first keep, two from the racing writers, one from this.
	if got := st.letters[id].Attempts(); got != 4 {
		t.Fatalf("attempts = %d, want 4", got)
	}
}

func TestKeepingGivesUpOnAGroupThatKeepsChangingAndKeepsTheFailureAlone(t *testing.T) {
	st := newGroupStore()
	ctx := t.Context()
	now := time.Now().UTC()
	if _, err := KeepDeadLetter(ctx, st, failure("a", now, `{"id":1}`), nil); err != nil {
		t.Fatal(err)
	}
	st.conflicts = 100
	id, err := KeepDeadLetter(ctx, st, failure("b", now.Add(time.Second), `{"id":1}`), nil)
	if err != nil {
		t.Fatalf("a busy group must not lose the failure: %v", err)
	}
	if id != "b" || len(st.letters) != 2 {
		t.Fatalf("the failure should be kept as a row of its own, got id %s and %d rows", id, len(st.letters))
	}
}

func TestKeepingRefusesADeadLetterWithoutAWorkflow(t *testing.T) {
	d := failure("a", time.Now(), `{}`)
	d.WorkflowID = ""
	if _, err := KeepDeadLetter(t.Context(), newGroupStore(), d, nil); err == nil {
		t.Fatal("a dead letter with no workflow was kept")
	}
}

func TestGroupingOldRowsFoldsCopiesAndMarksThemMerged(t *testing.T) {
	st := newGroupStore()
	ctx := t.Context()
	t0 := time.Date(2026, 10, 30, 17, 41, 34, 0, time.UTC)
	// What an earlier release left: one row per attempt and no record key.
	for i := range 5 {
		_ = st.PutDeadLetter(ctx, failure(fmt.Sprintf("a%d", i), t0.Add(time.Duration(i)*20*time.Second), `{"doc":"A"}`))
	}
	for i := range 3 {
		_ = st.PutDeadLetter(ctx, failure(fmt.Sprintf("b%d", i), t0.Add(time.Duration(i)*20*time.Second+10*time.Second), `{"doc":"B"}`))
	}
	replayed := failure("old", t0.Add(-time.Hour), `{"doc":"A"}`)
	replayed.Status = DeadLetterReplayed
	_ = st.PutDeadLetter(ctx, replayed)

	merged, err := GroupUngroupedDeadLetters(ctx, st)
	if err != nil {
		t.Fatal(err)
	}
	if merged != 6 {
		t.Fatalf("merged %d copies, want 6 (4 of A and 2 of B)", merged)
	}
	waiting, _, _ := st.ListDeadLetters(ctx, DeadLetterFilter{Status: DeadLetterQueued})
	if len(waiting) != 2 {
		t.Fatalf("%d groups waiting, want 2", len(waiting))
	}
	counts := map[string]int{}
	for _, g := range waiting {
		counts[string(g.Payload)] = g.Attempts()
		if g.RecordKey == "" {
			t.Fatalf("group %s has no record key", g.ID)
		}
	}
	if counts[`{"doc":"A"}`] != 5 || counts[`{"doc":"B"}`] != 3 {
		t.Fatalf("attempts per group = %v, want A 5 and B 3", counts)
	}
	a := waiting[0]
	if string(a.Payload) != `{"doc":"A"}` {
		a = waiting[1]
	}
	if !a.FirstFailedAt.Equal(t0) || !a.FailedAt.Equal(t0.Add(80*time.Second)) || a.MessageID != "msg-a4" {
		t.Fatalf("group A spans %s to %s with message %s, want %s to %s with msg-a4", a.FirstFailedAt, a.FailedAt, a.MessageID, t0, t0.Add(80*time.Second))
	}
	if st.letters["old"].Status != DeadLetterReplayed {
		t.Fatal("a settled row was touched")
	}
	if n := countStatus(st, DeadLetterMerged); n != 6 {
		t.Fatalf("%d rows marked merged, want 6", n)
	}

	again, err := GroupUngroupedDeadLetters(ctx, st)
	if err != nil || again != 0 {
		t.Fatalf("a second run merged %d (err %v), want 0", again, err)
	}
}

func TestGroupingAnOldRowFoldsItIntoAGroupThatAlreadyExists(t *testing.T) {
	st := newGroupStore()
	ctx := t.Context()
	now := time.Now().UTC()
	id, err := KeepDeadLetter(ctx, st, failure("new", now, `{"id":1}`), nil)
	if err != nil {
		t.Fatal(err)
	}
	// An older release, during a rolling upgrade, parked one more copy.
	_ = st.PutDeadLetter(ctx, failure("legacy", now.Add(-time.Minute), `{"id":1}`))

	if _, err := GroupUngroupedDeadLetters(ctx, st); err != nil {
		t.Fatal(err)
	}
	g := st.letters[id]
	if g.Attempts() != 2 || !g.FirstFailedAt.Equal(now.Add(-time.Minute)) || g.MessageID != "msg-new" {
		t.Fatalf("group = %d attempts from %s with message %s; want 2 from a minute earlier, msg-new", g.Attempts(), g.FirstFailedAt, g.MessageID)
	}
	if st.letters["legacy"].Status != DeadLetterMerged {
		t.Fatalf("the legacy copy is %s, want merged", st.letters["legacy"].Status)
	}
}

func TestDeliveredAndMergedAreSettledStatuses(t *testing.T) {
	for _, s := range []DeadLetterStatus{DeadLetterReplayed, DeadLetterDiscarded, DeadLetterDelivered, DeadLetterMerged} {
		if !ValidSettledStatus(s) {
			t.Errorf("%s should be a settled status", s)
		}
		if err := ValidateDeadLetter(DeadLetter{ID: "x", WorkflowID: "w", Status: s}); err != nil {
			t.Errorf("%s: %v", s, err)
		}
	}
	if ValidSettledStatus(DeadLetterQueued) {
		t.Error("queued is not a settled status")
	}
}

func TestAttemptsIsAtLeastOne(t *testing.T) {
	if got := (DeadLetter{}).Attempts(); got != 1 {
		t.Fatalf("a row from before counting has %d attempts, want 1", got)
	}
}

func TestErrDeadLetterConflictIsDistinct(t *testing.T) {
	if errors.Is(ErrDeadLetterConflict, ErrDeadLetterSettled) {
		t.Fatal("a lost race is not the same as a settled dead letter")
	}
}

func countStatus(s *groupStore, status DeadLetterStatus) int {
	n := 0
	for _, d := range s.letters {
		if d.Status == status {
			n++
		}
	}
	return n
}
