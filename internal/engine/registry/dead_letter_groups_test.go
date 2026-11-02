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
	"testing"
	"time"

	"github.com/gsoultan/hermod/internal/storage"
	"github.com/gsoultan/hermod/pkg/comm/message"
)

// sinkFailure is a message the engine parked after a sink refused it.
func sinkFailure(t *testing.T, id, payload, at string) *message.DefaultMessage {
	t.Helper()
	msg := message.AcquireMessage()
	t.Cleanup(msg.Release)
	msg.SetID(id)
	msg.SetOperation("create")
	msg.SetPayload([]byte(payload))
	msg.SetMetadata("_hermod_failed_sink", "panmail")
	msg.SetMetadata("_hermod_last_error", "panmail: invalid credentials")
	msg.SetMetadata("_hermod_failed_at", at)
	return msg
}

func delivery(t *testing.T, id, payload string) *message.DefaultMessage {
	t.Helper()
	msg := message.AcquireMessage()
	t.Cleanup(msg.Release)
	msg.SetID(id)
	msg.SetOperation("create")
	msg.SetPayload([]byte(payload))
	return msg
}

// The source re-sent one record every 20 seconds and the sink refused every
// copy. The page gets one row that counts them, not one row per copy.
func TestTheDeadLetterQueueCountsTheSameRecordIntoOneRow(t *testing.T) {
	reg, st := newDeadLetterRegistry(t)
	wf := storage.Workflow{ID: "wf-1", DeadLetters: true}
	dlq := reg.deadLetterQueue(wf)
	for i, at := range []string{"2026-10-30T17:41:34Z", "2026-10-30T17:41:54Z", "2026-10-30T17:42:14Z"} {
		if err := dlq.Write(t.Context(), sinkFailure(t, string(rune('a'+i)), `{"doc":"A"}`, at)); err != nil {
			t.Fatal(err)
		}
	}
	kept := st.all()
	if len(kept) != 1 {
		t.Fatalf("three copies of one record made %d rows, want 1", len(kept))
	}
	if kept[0].Attempts() != 3 || kept[0].MessageID != "c" || kept[0].RecordKey == "" {
		t.Fatalf("row = %d attempts, message %s, key %q; want 3, c, a key", kept[0].Attempts(), kept[0].MessageID, kept[0].RecordKey)
	}
}

// With key fields set, copies that differ only outside them are one record.
func TestTheDeadLetterQueueUsesTheWorkflowsKeyFields(t *testing.T) {
	reg, st := newDeadLetterRegistry(t)
	wf := storage.Workflow{ID: "wf-1", DeadLetters: true, DeadLetterKeyFields: []string{"application_id"}}
	dlq := reg.deadLetterQueue(wf)
	_ = dlq.Write(t.Context(), sinkFailure(t, "a", `{"application_id":"app-1","url":"signed-1"}`, "2026-10-30T17:41:34Z"))
	_ = dlq.Write(t.Context(), sinkFailure(t, "b", `{"application_id":"app-1","url":"signed-2"}`, "2026-10-30T17:41:54Z"))
	if kept := st.all(); len(kept) != 1 || kept[0].Attempts() != 2 {
		t.Fatalf("two copies of app-1 made %d rows, want 1 with 2 attempts", len(kept))
	}
}

func waitForStatus(t *testing.T, st *deadLetterStorage, id string, want storage.DeadLetterStatus) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if st.status(id) == want {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("dead letter %s is %q, want %q", id, st.status(id), want)
}

// Once the cause is fixed, the source's next copy of the record goes through.
// The waiting row is then closed as delivered, so nobody replays it and sends
// the record a second time.
func TestADeliveryOfAWaitingRecordClosesItsDeadLetter(t *testing.T) {
	reg, st := newDeadLetterRegistry(t)
	wf := storage.Workflow{ID: "wf-1", DeadLetters: true}
	if err := reg.deadLetterQueue(wf).Write(t.Context(), sinkFailure(t, "a", `{"doc":"A"}`, "2026-10-30T17:41:34Z")); err != nil {
		t.Fatal(err)
	}
	id := st.all()[0].ID
	observe := reg.deliveredObserver(wf)

	observe("smtp", delivery(t, "x", `{"doc":"A"}`))
	observe("panmail", delivery(t, "y", `{"doc":"B"}`))
	time.Sleep(50 * time.Millisecond)
	if got := st.status(id); got != storage.DeadLetterQueued {
		t.Fatalf("a delivery of another record or to another sink closed the row: %s", got)
	}

	observe("panmail", delivery(t, "z", ` {"doc": "A"} `))
	waitForStatus(t, st, id, storage.DeadLetterDelivered)
}

// A workflow that starts with records already waiting closes them too.
func TestWaitingRecordsAreKnownWhenTheWorkflowStarts(t *testing.T) {
	reg, st := newDeadLetterRegistry(t)
	wf := storage.Workflow{ID: "wf-1", DeadLetters: true}
	key := storage.DeadLetterRecordKey([]byte(`{"doc":"A"}`), storage.DeadLetterOrigin{Operation: "create"}, nil)
	_ = st.PutDeadLetter(t.Context(), storage.DeadLetter{ID: "waiting", WorkflowID: "wf-1", SinkID: "panmail",
		RecordKey: key, FailedAt: time.Now(), Payload: []byte(`{"doc":"A"}`), Origin: storage.DeadLetterOrigin{Operation: "create"}})

	reg.loadOpenDeadLetters(t.Context(), wf)
	reg.deliveredObserver(wf)("panmail", delivery(t, "z", `{"doc":"A"}`))
	waitForStatus(t, st, "waiting", storage.DeadLetterDelivered)
}

// With nothing waiting, a delivery is not even fingerprinted: the observer
// sits on every message a workflow delivers.
func TestADeliveryWithNothingWaitingTouchesNothing(t *testing.T) {
	reg, st := newDeadLetterRegistry(t)
	wf := storage.Workflow{ID: "wf-1", DeadLetters: true}
	if reg.openDLs.has(wf.ID) {
		t.Fatal("a workflow with nothing parked has waiting records")
	}
	reg.deliveredObserver(wf)("panmail", delivery(t, "z", `{"doc":"A"}`))
	if st.finds != 0 || len(st.all()) != 0 {
		t.Fatal("a delivery with nothing waiting reached the store")
	}
}

// The hourly sweep also folds rows an earlier release kept one per attempt.
func TestTheRetentionSweepGroupsOldRows(t *testing.T) {
	reg, st := newDeadLetterRegistry(t)
	for _, id := range []string{"old1", "old2"} {
		_ = st.PutDeadLetter(context.Background(), storage.DeadLetter{ID: id, WorkflowID: "wf-1", SinkID: "panmail",
			FailedAt: time.Now(), Payload: []byte(`{"doc":"A"}`)})
	}
	reg.purgeDeadLetters(context.Background(), st)
	if st.status("old2") != storage.DeadLetterMerged && st.status("old1") != storage.DeadLetterMerged {
		t.Fatalf("old rows were not grouped: old1 %s, old2 %s", st.status("old1"), st.status("old2"))
	}
}
