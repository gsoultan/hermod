// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

//go:build integration
// +build integration

package mongodb

import (
	"fmt"
	"testing"
	"time"

	"github.com/gsoultan/hermod/internal/storage"
)

// The grouping contract the SQL store keeps
// (internal/storage/sql/dead_letter_groups_test.go), against a live MongoDB.
func TestMongoDeadLetterGroups(t *testing.T) {
	s, _ := newTraceMongo(t)
	st := s.(storage.DeadLetterStore)
	ctx := t.Context()
	t0 := time.Date(2026, 10, 30, 17, 41, 34, 0, time.UTC)

	for i := range 40 {
		doc := "A"
		if i%2 == 1 {
			doc = "B"
		}
		d := storage.DeadLetter{
			ID: fmt.Sprintf("m%d", i), WorkflowID: "wf-1", MessageID: fmt.Sprintf("msg-%d", i), SinkID: "panmail",
			Error: "invalid credentials", FailedAt: t0.Add(time.Duration(i) * 10 * time.Second), Payload: []byte(`{"doc":"` + doc + `"}`),
		}
		if _, err := storage.KeepDeadLetter(ctx, st, d, nil); err != nil {
			t.Fatalf("keeping %d: %v", i, err)
		}
	}
	page, total, err := st.ListDeadLetters(ctx, storage.DeadLetterFilter{Status: storage.DeadLetterQueued})
	if err != nil {
		t.Fatal(err)
	}
	if total != 2 || page[0].Attempts()+page[1].Attempts() != 40 {
		t.Fatalf("%d documents, want 2 holding 40 attempts", total)
	}
	stats, err := st.DeadLetterStats(ctx, nil, t0.Add(time.Hour))
	if err != nil || stats.Queued != 2 || stats.QueuedAttempts != 40 {
		t.Fatalf("stats = %+v, err %v; want 2 records, 40 attempts", stats, err)
	}

	// Copies an earlier release kept one per attempt.
	for i := range 3 {
		err := st.PutDeadLetter(ctx, storage.DeadLetter{
			ID: fmt.Sprintf("old%d", i), WorkflowID: "wf-2", MessageID: fmt.Sprintf("old-%d", i), NodeID: "normalize",
			Error: "bad json", FailedAt: t0.Add(time.Duration(i) * time.Minute), Payload: []byte(`{"id":1}`),
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	merged, err := storage.GroupUngroupedDeadLetters(ctx, st)
	if err != nil || merged != 2 {
		t.Fatalf("merged %d, err %v; want 2", merged, err)
	}
	_, all, _ := st.ListDeadLetters(ctx, storage.DeadLetterFilter{WorkflowID: "wf-2"})
	if all != 1 {
		t.Fatalf("wf-2 lists %d documents, want 1 (merged ones hidden)", all)
	}
	g, err := st.GetDeadLetter(ctx, "old0")
	if err != nil || g.Attempts() != 3 || g.MessageID != "old-2" || !g.FirstFailedAt.Equal(t0) {
		t.Fatalf("group = %+v, err %v; want 3 attempts from %s with message old-2", g, err, t0)
	}
}
