// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package pebble

// Log and audit rows are keyed newest first so that a list reads the latest
// page without sorting and a cleanup removes one contiguous run of old keys.
// These tests hold the embedded database to both, and to carrying forward rows
// written under the earlier key that sorted the other way.

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/cockroachdb/pebble"
	"github.com/gsoultan/hermod/internal/storage"
)

func logIDs(t *testing.T, s storage.Storage) []string {
	t.Helper()
	rows, _, err := s.ListLogs(context.Background(), storage.LogFilter{})
	if err != nil {
		t.Fatalf("listing logs: %v", err)
	}
	ids := make([]string, 0, len(rows))
	for _, r := range rows {
		ids = append(ids, r.ID)
	}
	return ids
}

func auditIDs(t *testing.T, s storage.Storage) []string {
	t.Helper()
	rows, _, err := s.ListAuditLogs(context.Background(), storage.AuditFilter{})
	if err != nil {
		t.Fatalf("listing audit logs: %v", err)
	}
	ids := make([]string, 0, len(rows))
	for _, r := range rows {
		ids = append(ids, r.ID)
	}
	return ids
}

func sameIDs(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

func TestPurgeLogsRemovesOnlyRowsOlderThanTheCutoff(t *testing.T) {
	s := newTracePebble(t)
	ctx := context.Background()
	now := time.Now().UTC()
	for id, age := range map[string]time.Duration{"old": 48 * time.Hour, "edge": 25 * time.Hour, "new": time.Hour} {
		if err := s.CreateLog(ctx, storage.Log{ID: id, Timestamp: now.Add(-age), Message: id}); err != nil {
			t.Fatalf("creating %s: %v", id, err)
		}
	}
	if err := s.CreateLogs(ctx, []storage.Log{{ID: "batch-old", Timestamp: now.Add(-72 * time.Hour)}, {ID: "batch-new", Timestamp: now}}); err != nil {
		t.Fatalf("creating a batch: %v", err)
	}

	if err := s.PurgeLogs(ctx, now.Add(-24*time.Hour)); err != nil {
		t.Fatalf("purging: %v", err)
	}

	if got, want := logIDs(t, s), []string{"batch-new", "new"}; !sameIDs(got, want) {
		t.Fatalf("after purging rows older than a day got %v, want %v", got, want)
	}
}

func TestPurgeAuditLogsRemovesOnlyRowsOlderThanTheCutoff(t *testing.T) {
	s := newTracePebble(t)
	ctx := context.Background()
	now := time.Now().UTC()
	for id, age := range map[string]time.Duration{"old": 48 * time.Hour, "edge": 25 * time.Hour, "new": time.Hour} {
		if err := s.CreateAuditLog(ctx, storage.AuditLog{ID: id, Timestamp: now.Add(-age), Action: id}); err != nil {
			t.Fatalf("creating %s: %v", id, err)
		}
	}

	if err := s.PurgeAuditLogs(ctx, now.Add(-24*time.Hour)); err != nil {
		t.Fatalf("purging: %v", err)
	}

	if got, want := auditIDs(t, s), []string{"new"}; !sameIDs(got, want) {
		t.Fatalf("after purging rows older than a day got %v, want %v", got, want)
	}
}

func TestLogsAndAuditLogsListNewestFirst(t *testing.T) {
	s := newTracePebble(t)
	ctx := context.Background()
	now := time.Now().UTC()
	for i, id := range []string{"third", "first", "second"} {
		age := []time.Duration{3 * time.Hour, time.Hour, 2 * time.Hour}[i]
		if err := s.CreateLog(ctx, storage.Log{ID: id, Timestamp: now.Add(-age)}); err != nil {
			t.Fatalf("creating log %s: %v", id, err)
		}
		if err := s.CreateAuditLog(ctx, storage.AuditLog{ID: id, Timestamp: now.Add(-age)}); err != nil {
			t.Fatalf("creating audit log %s: %v", id, err)
		}
	}
	want := []string{"first", "second", "third"}
	if got := logIDs(t, s); !sameIDs(got, want) {
		t.Errorf("logs listed as %v, want %v", got, want)
	}
	if got := auditIDs(t, s); !sameIDs(got, want) {
		t.Errorf("audit logs listed as %v, want %v", got, want)
	}
}

// legacyTimeKey is the key every release before this one wrote. The constant is
// smaller than any nanosecond timestamp since 2001, so the number is negative
// and a newer row sorts after an older one.
func legacyTimeKey(prefix string, ts time.Time, id string) []byte {
	return []byte(fmt.Sprintf("%s:%020d:%s", prefix, 999999999999999999-ts.UnixNano(), id))
}

func TestRowsUnderTheEarlierKeyAreRekeyedOnOpen(t *testing.T) {
	dir := t.TempDir()
	now := time.Now().UTC()
	ages := map[string]time.Duration{"old": 48 * time.Hour, "mid": 30 * time.Hour, "new": time.Hour}

	db, err := pebble.Open(dir, &pebble.Options{})
	if err != nil {
		t.Fatalf("opening the raw database: %v", err)
	}
	for id, age := range ages {
		ts := now.Add(-age)
		l, _ := json.Marshal(storage.Log{ID: id, Timestamp: ts})
		a, _ := json.Marshal(storage.AuditLog{ID: id, Timestamp: ts})
		if err := db.Set(legacyTimeKey("l", ts, id), l, pebble.Sync); err != nil {
			t.Fatalf("writing a legacy log: %v", err)
		}
		if err := db.Set(legacyTimeKey("a", ts, id), a, pebble.Sync); err != nil {
			t.Fatalf("writing a legacy audit log: %v", err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatalf("closing the raw database: %v", err)
	}

	s, err := NewPebbleStorage(dir)
	if err != nil {
		t.Fatalf("opening pebble: %v", err)
	}
	t.Cleanup(func() { _ = s.(*pebbleStorage).Close() })
	ctx := context.Background()
	if err := s.CreateLog(ctx, storage.Log{ID: "latest", Timestamp: now}); err != nil {
		t.Fatalf("creating a log: %v", err)
	}
	if err := s.CreateAuditLog(ctx, storage.AuditLog{ID: "latest", Timestamp: now}); err != nil {
		t.Fatalf("creating an audit log: %v", err)
	}

	want := []string{"latest", "new", "mid", "old"}
	if got := logIDs(t, s); !sameIDs(got, want) {
		t.Errorf("logs listed as %v, want %v", got, want)
	}
	if got := auditIDs(t, s); !sameIDs(got, want) {
		t.Errorf("audit logs listed as %v, want %v", got, want)
	}

	if err := s.PurgeLogs(ctx, now.Add(-24*time.Hour)); err != nil {
		t.Fatalf("purging logs: %v", err)
	}
	if err := s.PurgeAuditLogs(ctx, now.Add(-24*time.Hour)); err != nil {
		t.Fatalf("purging audit logs: %v", err)
	}
	want = []string{"latest", "new"}
	if got := logIDs(t, s); !sameIDs(got, want) {
		t.Errorf("after the purge logs are %v, want %v", got, want)
	}
	if got := auditIDs(t, s); !sameIDs(got, want) {
		t.Errorf("after the purge audit logs are %v, want %v", got, want)
	}
}
