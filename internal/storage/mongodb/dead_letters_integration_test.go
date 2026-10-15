// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Hermod: real-time Postgres CDC and data pipelines in one Go binary.
// Each file is proprietary and confidential; see LICENSE for the terms.
// Resale, sublicensing or selling it to any third party is prohibited.
// Making copies, modifying or distributing it needs written permission
// Of the copyright holder, Gembit Soultan Shirazi.
// Distributed "AS IS", WITHOUT WARRANTY OF ANY KIND, express or implied.

//go:build integration
// +build integration

package mongodb

import (
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/gsoultan/hermod/internal/storage"
)

// The same contract the SQL store keeps (internal/storage/sql/dead_letters_test.go),
// against a live MongoDB.
func TestMongoDeadLetters(t *testing.T) {
	s, _ := newTraceMongo(t)
	st, ok := s.(storage.DeadLetterStore)
	if !ok {
		t.Fatal("the MongoDB store does not implement storage.DeadLetterStore")
	}
	ctx := t.Context()
	now := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	put := func(id, vhost, wf, node, errText string, at time.Time) {
		t.Helper()
		err := st.PutDeadLetter(ctx, storage.DeadLetter{
			ID: id, VHost: vhost, WorkflowID: wf, MessageID: "msg-" + id, NodeID: node, Error: errText, FailedAt: at,
			Payload: []byte(`{"total":"12,50"}`), Metadata: map[string]string{"table": "orders"},
		})
		if err != nil {
			t.Fatalf("PutDeadLetter %s: %v", id, err)
		}
	}
	for i := range 3 {
		put(fmt.Sprintf("a%d", i), "tenant-a", "wf-orders", "normalize", "timeout", now.Add(-time.Duration(i+1)*time.Hour))
	}
	put("a3", "tenant-a", "wf-orders", "normalize", "bad json", now.Add(-72*time.Hour))
	put("b0", "tenant-b", "wf-billing", "enrich", "bad json", now.Add(-time.Hour))

	if _, err := st.GetDeadLetter(ctx, "missing"); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("missing: err = %v, want ErrNotFound", err)
	}
	got, err := st.GetDeadLetter(ctx, "a0")
	if err != nil || got.Metadata["table"] != "orders" || string(got.Payload) != `{"total":"12,50"}` || got.Status != storage.DeadLetterQueued {
		t.Fatalf("round trip: %+v, %v", got, err)
	}

	page, total, err := st.ListDeadLetters(ctx, storage.DeadLetterFilter{VHosts: []string{"tenant-a"}, Limit: 2, Offset: 1})
	if err != nil || total != 4 || len(page) != 2 || page[0].ID != "a1" || page[0].Payload != nil {
		t.Fatalf("page: %+v total %d err %v", page, total, err)
	}
	if _, total, _ := st.ListDeadLetters(ctx, storage.DeadLetterFilter{Search: "TIME."}); total != 0 {
		t.Fatalf("a search is text, not a pattern: %d matched", total)
	}
	if _, total, _ := st.ListDeadLetters(ctx, storage.DeadLetterFilter{Where: "enrich"}); total != 1 {
		t.Fatalf("by node: %d", total)
	}

	var wg sync.WaitGroup
	errs := make([]error, 8)
	for i := range errs {
		wg.Go(func() {
			errs[i] = st.SettleDeadLetter(ctx, "a3", storage.DeadLetterReplayed, storage.DeadLetterEvent{At: now.Add(-time.Hour), Action: "replayed", By: "ada"})
		})
	}
	wg.Wait()
	won := 0
	for _, err := range errs {
		if err == nil {
			won++
		} else if !errors.Is(err, storage.ErrDeadLetterSettled) {
			t.Fatalf("losing settle: %v", err)
		}
	}
	if won != 1 {
		t.Fatalf("%d settles won, want 1", won)
	}

	stats, err := st.DeadLetterStats(ctx, []string{"tenant-a"}, now)
	if err != nil {
		t.Fatal(err)
	}
	want := storage.DeadLetterStats{Queued: 3, Last24h: 3, TopError: "timeout", TopErrorCount: 3, ReplayedThisWeek: 1}
	if stats != want {
		t.Fatalf("stats = %+v, want %+v", stats, want)
	}

	n, err := st.PurgeDeadLetters(ctx, now.Add(-48*time.Hour))
	if err != nil || n != 1 {
		t.Fatalf("purge: %d, %v", n, err)
	}
}
