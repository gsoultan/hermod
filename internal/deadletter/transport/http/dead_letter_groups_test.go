// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package http

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/internal/api/handlers"
	"github.com/gsoultan/hermod/internal/engine/registry"
	"github.com/gsoultan/hermod/internal/factory"
	"github.com/gsoultan/hermod/internal/storage"
)

// groupedStore is the handler's store with a record that failed 263 times,
// and a sink to check.
type groupedStore struct {
	*store
}

func newGroupedStore() groupedStore {
	st := newStore()
	first := time.Date(2026, 10, 30, 17, 41, 34, 0, time.UTC)
	st.letters["dl-many"] = storage.DeadLetter{ID: "dl-many", VHost: "a", WorkflowID: "wf-a", MessageID: "read-263",
		SinkID: "snk-panmail", Error: "panmail: invalid credentials", FailedAt: first.Add(101 * time.Minute),
		FirstFailedAt: first, FailureCount: 263, RecordKey: "payload:abc", Status: storage.DeadLetterQueued,
		Payload: []byte(`{"doc":"A"}`)}
	return groupedStore{st}
}

func (s groupedStore) GetSink(_ context.Context, id string) (storage.Sink, error) {
	if id == "snk-panmail" {
		return storage.Sink{ID: id, Name: "Panmail", Type: "panmail", VHost: "a", Config: map[string]string{"base_url": "https://panmail.example"}}, nil
	}
	return storage.Sink{}, storage.ErrNotFound
}

type pingSink struct{ err error }

func (s pingSink) Write(context.Context, hermod.Message) error { return nil }
func (s pingSink) Ping(context.Context) error                  { return s.err }
func (s pingSink) Close() error                                { return nil }

// serveWithRegistry POSTs path, the only method the check route accepts.
func serveWithRegistry(t *testing.T, st storage.Storage, ping error, user *storage.User, path string) *httptest.ResponseRecorder {
	t.Helper()
	reg := registry.NewRegistry(st)
	t.Cleanup(reg.Close)
	reg.SetSinkFactory(func(factory.SinkConfig) (hermod.Sink, error) { return pingSink{err: ping}, nil })
	h := NewHandler(&handlers.Handler{Storage: st, LogStorage: st, Registry: reg})
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, path, nil)
	req = req.WithContext(context.WithValue(req.Context(), handlers.UserContextKey, user))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func TestTheListAcceptsTheDeliveredStatus(t *testing.T) {
	st := newStore()
	rec := serve(st, viewerA, http.MethodGet, "/api/dead-letters?status=delivered", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	if st.listFilter.Status != storage.DeadLetterDelivered {
		t.Fatalf("filter status = %q, want delivered", st.listFilter.Status)
	}
}

func TestTheListCarriesTheFailureCount(t *testing.T) {
	st := newGroupedStore()
	rec := serve(st, viewerA, http.MethodGet, "/api/dead-letters", "")
	var body struct {
		Data []struct {
			ID            string    `json:"id"`
			FailureCount  int       `json:"failure_count"`
			FirstFailedAt time.Time `json:"first_failed_at"`
			RecordKey     string    `json:"record_key"`
		} `json:"data"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	for _, d := range body.Data {
		if d.ID == "dl-many" {
			if d.FailureCount != 263 || d.FirstFailedAt.IsZero() || d.RecordKey == "" {
				t.Fatalf("row = %+v, want 263 failures with a first failure and a key", d)
			}
			return
		}
	}
	t.Fatal("the grouped row is not listed")
}

func TestExportCarriesTheFailureCount(t *testing.T) {
	rec := serve(newGroupedStore(), editorA, http.MethodGet, "/api/dead-letters/export?ids=dl-many", "")
	rows, err := csv.NewReader(rec.Body).ReadAll()
	if err != nil || len(rows) != 2 {
		t.Fatalf("rows = %v, err %v", rows, err)
	}
	col := map[string]int{}
	for i, name := range rows[0] {
		col[name] = i
	}
	for _, name := range []string{"failure_count", "first_failed_at", "record_key"} {
		if _, ok := col[name]; !ok {
			t.Fatalf("header %v lacks %s", rows[0], name)
		}
	}
	if rows[1][col["failure_count"]] != "263" || rows[1][col["first_failed_at"]] != "2026-10-30T17:41:34Z" {
		t.Fatalf("row = %v", rows[1])
	}
}

// Before replaying, the page asks whether the sink the record failed at
// answers now.
func TestCheckSaysWhetherTheFailedSinkIsReachable(t *testing.T) {
	rec := serveWithRegistry(t, newGroupedStore(), nil, editorA, "/api/dead-letters/dl-many/check")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	var ok checkResult
	_ = json.NewDecoder(rec.Body).Decode(&ok)
	if !ok.Checked || !ok.Reachable || ok.SinkName != "Panmail" {
		t.Fatalf("check = %+v, want a reachable Panmail", ok)
	}

	rec = serveWithRegistry(t, newGroupedStore(), errors.New("dial tcp: connection refused"), editorA, "/api/dead-letters/dl-many/check")
	var down checkResult
	_ = json.NewDecoder(rec.Body).Decode(&down)
	if !down.Checked || down.Reachable || !strings.Contains(down.Error, "connection refused") {
		t.Fatalf("check = %+v, want an unreachable sink and why", down)
	}
}

// A record that failed at a node has no destination to check.
func TestCheckOfANodeFailureChecksNothing(t *testing.T) {
	rec := serveWithRegistry(t, newGroupedStore(), nil, editorA, "/api/dead-letters/dl-a/check")
	var got checkResult
	_ = json.NewDecoder(rec.Body).Decode(&got)
	if rec.Code != http.StatusOK || got.Checked {
		t.Fatalf("status %d, check = %+v; want 200 and nothing checked", rec.Code, got)
	}
}

func TestViewersCannotCheck(t *testing.T) {
	rec := serveWithRegistry(t, newGroupedStore(), nil, viewerA, "/api/dead-letters/dl-many/check")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
}

// An export of a filter takes the newest 500 and says how many matched, so a
// cut export is not mistaken for the whole queue.
func TestExportOfAFilterSaysHowManyMatched(t *testing.T) {
	rec := serve(newGroupedStore(), editorA, http.MethodGet, "/api/dead-letters/export?status=queued", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if got := rec.Header().Get("X-Dead-Letters-Total"); got != "3" {
		t.Fatalf("X-Dead-Letters-Total = %q, want 3 (the editor sees vhost a only)", got)
	}
}
