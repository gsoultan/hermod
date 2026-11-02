// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package sql

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/gsoultan/hermod/internal/storage"
)

func TestDeadLetterGroupFieldsRoundTrip(t *testing.T) {
	st := deadLetterStore(t)
	ctx := t.Context()
	first := time.Date(2026, 10, 30, 17, 41, 34, 0, time.UTC)
	last := time.Date(2026, 10, 30, 19, 22, 48, 0, time.UTC)
	d := sampleDeadLetter("g", "", "wf-1", last)
	d.RecordKey, d.FailureCount, d.FirstFailedAt = "payload:abc", 263, first
	if err := st.PutDeadLetter(ctx, d); err != nil {
		t.Fatal(err)
	}
	got, err := st.GetDeadLetter(ctx, "g")
	if err != nil {
		t.Fatal(err)
	}
	if got.RecordKey != "payload:abc" || got.FailureCount != 263 || !got.FirstFailedAt.Equal(first) {
		t.Fatalf("got key %q, %d attempts from %s", got.RecordKey, got.FailureCount, got.FirstFailedAt)
	}
	page, _, err := st.ListDeadLetters(ctx, storage.DeadLetterFilter{})
	if err != nil || len(page) != 1 {
		t.Fatalf("list: %v, %d rows", err, len(page))
	}
	if page[0].RecordKey != "payload:abc" || page[0].FailureCount != 263 || !page[0].FirstFailedAt.Equal(first) {
		t.Fatalf("a list row lost the group fields: %+v", page[0])
	}
}

func TestFindDeadLetterGroupMatchesOnlyTheWaitingRowOfThatRecordAndPlace(t *testing.T) {
	st := deadLetterStore(t)
	ctx := t.Context()
	now := time.Now().UTC().Truncate(time.Second)
	put := func(id, wf, node, sink, key string, status storage.DeadLetterStatus) {
		d := sampleDeadLetter(id, "", wf, now)
		d.NodeID, d.SinkID, d.RecordKey, d.Status = node, sink, key, status
		if err := st.PutDeadLetter(ctx, d); err != nil {
			t.Fatal(err)
		}
	}
	put("settled", "wf-1", "", "panmail", "k", storage.DeadLetterReplayed)
	put("other-key", "wf-1", "", "panmail", "k2", storage.DeadLetterQueued)
	put("other-sink", "wf-1", "", "smtp", "k", storage.DeadLetterQueued)
	put("other-node", "wf-1", "normalize", "", "k", storage.DeadLetterQueued)
	put("other-wf", "wf-2", "", "panmail", "k", storage.DeadLetterQueued)
	key := storage.DeadLetterGroupKey{WorkflowID: "wf-1", SinkID: "panmail", RecordKey: "k"}
	if _, err := st.FindDeadLetterGroup(ctx, key); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("with no waiting row of the group: err = %v, want ErrNotFound", err)
	}
	put("it", "wf-1", "", "panmail", "k", storage.DeadLetterQueued)
	got, err := st.FindDeadLetterGroup(ctx, key)
	if err != nil || got.ID != "it" || string(got.Payload) == "" {
		t.Fatalf("found %q (payload %d bytes), err %v; want the whole row it", got.ID, len(got.Payload), err)
	}
}

func TestUpdateDeadLetterOnlyAppliesToTheCountItRead(t *testing.T) {
	st := deadLetterStore(t)
	ctx := t.Context()
	now := time.Now().UTC().Truncate(time.Second)
	if err := st.PutDeadLetter(ctx, sampleDeadLetter("g", "", "wf-1", now)); err != nil {
		t.Fatal(err)
	}
	g, _ := st.GetDeadLetter(ctx, "g")
	next := g
	next.RecordKey, next.FailureCount, next.MessageID, next.FailedAt = "k", 2, "msg-2", now.Add(time.Minute)
	next.Payload = []byte(`{"id":"ord-1","total":"13,00"}`)
	if err := st.UpdateDeadLetter(ctx, next, 1); err != nil {
		t.Fatalf("first update: %v", err)
	}
	if err := st.UpdateDeadLetter(ctx, next, 1); !errors.Is(err, storage.ErrDeadLetterConflict) {
		t.Fatalf("an update from a stale read: err = %v, want ErrDeadLetterConflict", err)
	}
	got, _ := st.GetDeadLetter(ctx, "g")
	if got.FailureCount != 2 || got.RecordKey != "k" || got.MessageID != "msg-2" || !got.FailedAt.Equal(now.Add(time.Minute)) ||
		string(got.Payload) != `{"id":"ord-1","total":"13,00"}` || !got.FirstFailedAt.Equal(now) {
		t.Fatalf("after the update: %+v", got)
	}
	if err := st.SettleDeadLetter(ctx, "g", storage.DeadLetterDiscarded, storage.NewDeadLetterEvent("discarded", "", "")); err != nil {
		t.Fatal(err)
	}
	next.FailureCount = 3
	if err := st.UpdateDeadLetter(ctx, next, 2); !errors.Is(err, storage.ErrDeadLetterConflict) {
		t.Fatalf("updating a settled row: err = %v, want ErrDeadLetterConflict", err)
	}
}

func TestKeepingFiveHundredCopiesInSQLLeavesTwoRows(t *testing.T) {
	st := deadLetterStore(t)
	ctx := t.Context()
	t0 := time.Date(2026, 10, 30, 17, 41, 34, 0, time.UTC)
	for i := range 500 {
		doc := "A"
		if i%2 == 1 {
			doc = "B"
		}
		d := sampleDeadLetter(fmt.Sprintf("m%d", i), "", "wf-1", t0.Add(time.Duration(i)*10*time.Second))
		d.NodeID, d.SinkID, d.Payload = "", "panmail", []byte(`{"doc":"`+doc+`"}`)
		if _, err := storage.KeepDeadLetter(ctx, st, d, nil); err != nil {
			t.Fatalf("keeping %d: %v", i, err)
		}
	}
	page, total, err := st.ListDeadLetters(ctx, storage.DeadLetterFilter{Status: storage.DeadLetterQueued})
	if err != nil {
		t.Fatal(err)
	}
	if total != 2 || page[0].Attempts()+page[1].Attempts() != 500 {
		t.Fatalf("%d rows holding %d+%d attempts, want 2 rows holding 500", total, page[0].Attempts(), page[1].Attempts())
	}
	stats, err := st.DeadLetterStats(ctx, nil, t0.Add(2*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if stats.Queued != 2 || stats.QueuedAttempts != 500 || stats.TopErrorCount != 2 {
		t.Fatalf("stats = %+v, want 2 records, 500 attempts, top error on 2 records", stats)
	}
}

func TestGroupingOldSQLRowsAndHidingTheMergedOnes(t *testing.T) {
	st := deadLetterStore(t)
	ctx := t.Context()
	t0 := time.Date(2026, 10, 30, 17, 41, 34, 0, time.UTC)
	for i := range 4 {
		if err := st.PutDeadLetter(ctx, sampleDeadLetter(fmt.Sprintf("old%d", i), "", "wf-1", t0.Add(time.Duration(i)*time.Minute))); err != nil {
			t.Fatal(err)
		}
	}
	page, _, _ := st.ListDeadLetters(ctx, storage.DeadLetterFilter{Ungrouped: true, OldestFirst: true})
	if len(page) != 4 || page[0].ID != "old0" {
		t.Fatalf("ungrouped, oldest first = %v, want old0 first of 4", ids(page))
	}
	merged, err := storage.GroupUngroupedDeadLetters(ctx, st)
	if err != nil || merged != 3 {
		t.Fatalf("merged %d, err %v; want 3", merged, err)
	}
	_, all, _ := st.ListDeadLetters(ctx, storage.DeadLetterFilter{})
	if all != 1 {
		t.Fatalf("the page lists %d rows with no status filter, want 1 (merged rows hidden)", all)
	}
	_, hidden, _ := st.ListDeadLetters(ctx, storage.DeadLetterFilter{Status: storage.DeadLetterMerged})
	if hidden != 3 {
		t.Fatalf("%d merged rows, want 3", hidden)
	}
	g, _ := st.GetDeadLetter(ctx, "old0")
	if g.Attempts() != 4 || g.MessageID != "msg-old3" {
		t.Fatalf("group = %d attempts, message %s; want 4, msg-old3", g.Attempts(), g.MessageID)
	}
}
