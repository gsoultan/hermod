// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Granted to no one is any right to use, copy, modify or distribute this
// Except by written permission of the copyright holder named above.
// May not be sold, resold, sublicensed or offered as a paid or hosted
// Business service to any third party. Proprietary and confidential;
// Its full terms are in LICENSE at the repository root. THE SOFTWARE
// IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR IMPLIED.

package http

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gsoultan/hermod/internal/api/handlers"
	"github.com/gsoultan/hermod/internal/storage"
	"github.com/gsoultan/hermod/internal/testutil"
)

// TestMain makes the install look set up, so the role guards apply rather
// than the first-run bypass.
func TestMain(m *testing.M) {
	_ = os.Setenv("HERMOD_DB_TYPE", "sqlite")
	_ = os.Setenv("HERMOD_DB_CONN", "file::memory:")
	os.Exit(m.Run())
}

// store holds dead letters of two vhosts, a and b, in memory.
type store struct {
	testutil.BaseMockStorage
	mu         sync.Mutex
	letters    map[string]storage.DeadLetter
	listFilter storage.DeadLetterFilter
	statsFor   []string
}

func newStore() *store {
	at := time.Date(2026, 10, 14, 4, 52, 55, 0, time.UTC)
	return &store{letters: map[string]storage.DeadLetter{
		"dl-a": {ID: "dl-a", VHost: "a", WorkflowID: "wf-a", MessageID: "ord-1", NodeID: "normalize",
			Error: `field total is not a number: got "12,50"`, FailedAt: at, Status: storage.DeadLetterQueued,
			Payload: []byte(`{"total":"12,50"}`)},
		"dl-b": {ID: "dl-b", VHost: "b", WorkflowID: "wf-b", MessageID: "inv-9", SinkID: "warehouse",
			Error: "connection refused", FailedAt: at, Status: storage.DeadLetterQueued, Payload: []byte(`{}`)},
		"dl-cut": {ID: "dl-cut", VHost: "a", WorkflowID: "wf-a", MessageID: "big", NodeID: "normalize",
			FailedAt: at, Status: storage.DeadLetterQueued, Truncated: true},
	}}
}

func (s *store) GetWorkflow(_ context.Context, id string) (storage.Workflow, error) {
	switch id {
	case "wf-a":
		return storage.Workflow{ID: "wf-a", Name: "Orders sync", VHost: "a"}, nil
	case "wf-b":
		return storage.Workflow{ID: "wf-b", Name: "Billing", VHost: "b"}, nil
	}
	return storage.Workflow{}, storage.ErrNotFound
}

func (s *store) ListUsers(context.Context, storage.CommonFilter) ([]storage.User, int, error) {
	return []storage.User{{Username: "root"}}, 1, nil
}

func (s *store) CreateLog(context.Context, storage.Log) error           { return nil }
func (s *store) CreateAuditLog(context.Context, storage.AuditLog) error { return nil }

func (s *store) PutDeadLetter(context.Context, storage.DeadLetter) error { return nil }

func (s *store) GetDeadLetter(_ context.Context, id string) (storage.DeadLetter, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	d, ok := s.letters[id]
	if !ok {
		return d, storage.ErrNotFound
	}
	return d, nil
}

func (s *store) ListDeadLetters(_ context.Context, f storage.DeadLetterFilter) ([]storage.DeadLetter, int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.listFilter = f
	var out []storage.DeadLetter
	for _, d := range s.letters {
		if f.VHosts == nil || containsString(storage.WithSharedVHosts(f.VHosts), d.VHost) {
			d.Payload = nil
			out = append(out, d)
		}
	}
	return out, len(out), nil
}

func containsString(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}

func (s *store) SettleDeadLetter(_ context.Context, id string, status storage.DeadLetterStatus, ev storage.DeadLetterEvent) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	d, ok := s.letters[id]
	if !ok {
		return storage.ErrNotFound
	}
	if d.Status != storage.DeadLetterQueued {
		return storage.ErrDeadLetterSettled
	}
	d.Status, d.SettledBy = status, ev.By
	d.History = append(d.History, ev)
	s.letters[id] = d
	return nil
}

func (s *store) AddDeadLetterEvent(context.Context, string, storage.DeadLetterEvent) error {
	return nil
}

func (s *store) DeadLetterStats(_ context.Context, vhosts []string, _ time.Time) (storage.DeadLetterStats, error) {
	s.statsFor = vhosts
	return storage.DeadLetterStats{Queued: 3, TopError: "connection refused", TopErrorCount: 1}, nil
}

func (s *store) PurgeDeadLetters(context.Context, time.Time) (int64, error) { return 0, nil }

func serve(st storage.Storage, user *storage.User, method, path, body string) *httptest.ResponseRecorder {
	h := NewHandler(&handlers.Handler{Storage: st, LogStorage: st})
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)
	req := httptest.NewRequestWithContext(context.Background(), method, path, strings.NewReader(body))
	req = req.WithContext(context.WithValue(req.Context(), handlers.UserContextKey, user))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

var (
	editorA = &storage.User{Username: "ada", Role: storage.RoleEditor, VHosts: []string{"a"}}
	viewerA = &storage.User{Username: "vic", Role: storage.RoleViewer, VHosts: []string{"a"}}
	admin   = &storage.User{Username: "root", Role: storage.RoleAdministrator}
)

func TestTheListIsLimitedToTheUsersVHostsAndNamesTheWorkflow(t *testing.T) {
	st := newStore()
	rec := serve(st, viewerA, http.MethodGet, "/api/dead-letters?workflow_id=wf-a&where=normalize&status=queued&search=total&since=2026-10-01T00:00:00Z&limit=20&offset=40", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	f := st.listFilter
	if len(f.VHosts) != 1 || f.VHosts[0] != "a" || f.WorkflowID != "wf-a" || f.Where != "normalize" ||
		f.Status != storage.DeadLetterQueued || f.Search != "total" || f.Since.IsZero() || f.Limit != 20 || f.Offset != 40 {
		t.Fatalf("filter = %+v", f)
	}
	var body struct {
		Data []struct {
			ID           string `json:"id"`
			WorkflowName string `json:"workflow_name"`
		} `json:"data"`
		Total int `json:"total"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Total != 2 {
		t.Fatalf("total = %d, want vhost a's 2", body.Total)
	}
	for _, d := range body.Data {
		if d.WorkflowName != "Orders sync" {
			t.Errorf("%s: workflow_name = %q", d.ID, d.WorkflowName)
		}
	}

	serve(st, admin, http.MethodGet, "/api/dead-letters", "")
	if st.listFilter.VHosts != nil {
		t.Fatalf("an administrator's list was limited to %v", st.listFilter.VHosts)
	}
}

// The vhost picked in the header narrows the list to it, and only to a vhost
// the caller may see.
func TestTheListNarrowsToTheSelectedVHost(t *testing.T) {
	st := newStore()
	serve(st, admin, http.MethodGet, "/api/dead-letters?vhost=b", "")
	if len(st.listFilter.VHosts) != 1 || st.listFilter.VHosts[0] != "b" {
		t.Fatalf("vhosts = %v, want [b]", st.listFilter.VHosts)
	}
	serve(st, viewerA, http.MethodGet, "/api/dead-letters?vhost=all", "")
	if len(st.listFilter.VHosts) != 1 || st.listFilter.VHosts[0] != "a" {
		t.Fatalf("all: vhosts = %v, want the user's [a]", st.listFilter.VHosts)
	}
	if rec := serve(st, viewerA, http.MethodGet, "/api/dead-letters?vhost=b", ""); rec.Code != http.StatusForbidden {
		t.Fatalf("another vhost: status = %d, want 403", rec.Code)
	}
	if rec := serve(st, viewerA, http.MethodGet, "/api/dead-letters/stats?vhost=b", ""); rec.Code != http.StatusForbidden {
		t.Fatalf("stats of another vhost: status = %d, want 403", rec.Code)
	}
}

func TestTheListRefusesALimitPastTheCap(t *testing.T) {
	st := newStore()
	serve(st, admin, http.MethodGet, "/api/dead-letters?limit=100000", "")
	if st.listFilter.Limit != maxPage {
		t.Fatalf("limit = %d, want the cap %d", st.listFilter.Limit, maxPage)
	}
}

// A dead letter carries the message itself; a user of another vhost must not
// read it, replay it or discard it, nor learn that it exists.
func TestAnotherVHostsDeadLetterIsNotFound(t *testing.T) {
	for _, tc := range []struct{ method, path, body string }{
		{http.MethodGet, "/api/dead-letters/dl-b", ""},
		{http.MethodPost, "/api/dead-letters/dl-b/replay", `{}`},
		{http.MethodPost, "/api/dead-letters/discard", `{"ids":["dl-b"]}`},
		{http.MethodGet, "/api/dead-letters/export?ids=dl-b", ""},
	} {
		st := newStore()
		rec := serve(st, editorA, tc.method, tc.path, tc.body)
		if tc.method == http.MethodGet && strings.HasSuffix(tc.path, "export?ids=dl-b") {
			if strings.Contains(rec.Body.String(), "inv-9") {
				t.Errorf("export leaked another vhost's dead letter: %s", rec.Body.String())
			}
			continue
		}
		if strings.HasPrefix(tc.path, "/api/dead-letters/discard") {
			if !strings.Contains(rec.Body.String(), `"not_found"`) {
				t.Errorf("discard: %s", rec.Body.String())
			}
		} else if rec.Code != http.StatusNotFound {
			t.Errorf("%s %s: status = %d, want 404", tc.method, tc.path, rec.Code)
		}
		if st.letters["dl-b"].Status != storage.DeadLetterQueued {
			t.Errorf("%s %s: another vhost's dead letter was settled", tc.method, tc.path)
		}
	}
}

func TestGetReturnsTheMessageAndItsWorkflow(t *testing.T) {
	rec := serve(newStore(), viewerA, http.MethodGet, "/api/dead-letters/dl-a", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var d struct {
		storage.DeadLetter
		WorkflowName string `json:"workflow_name"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &d); err != nil {
		t.Fatal(err)
	}
	if d.WorkflowName != "Orders sync" || string(d.Payload) != `{"total":"12,50"}` {
		t.Fatalf("got %+v", d)
	}
}

func TestViewersCannotReplayOrDiscard(t *testing.T) {
	for _, path := range []string{"/api/dead-letters/dl-a/replay", "/api/dead-letters/replay", "/api/dead-letters/discard"} {
		st := newStore()
		rec := serve(st, viewerA, http.MethodPost, path, `{"ids":["dl-a"]}`)
		if rec.Code != http.StatusForbidden {
			t.Errorf("%s: status = %d, want 403", path, rec.Code)
		}
	}
}

func TestDiscardSettlesEachAndReportsWhatItCouldNot(t *testing.T) {
	st := newStore()
	rec := serve(st, editorA, http.MethodPost, "/api/dead-letters/discard", `{"ids":["dl-a","dl-a","missing"]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Results []struct {
			ID     string `json:"id"`
			Status string `json:"status"`
		} `json:"results"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	got := map[string]string{}
	for _, r := range body.Results {
		got[r.ID] = r.Status
	}
	if got["dl-a"] != "discarded" || got["missing"] != "not_found" || len(body.Results) != 2 {
		t.Fatalf("results = %+v", body.Results)
	}
	if d := st.letters["dl-a"]; d.Status != storage.DeadLetterDiscarded || d.SettledBy != "ada" {
		t.Fatalf("dl-a = %+v", d)
	}
	rec = serve(st, editorA, http.MethodPost, "/api/dead-letters/discard", `{"ids":["dl-a"]}`)
	if !strings.Contains(rec.Body.String(), `"already_settled"`) {
		t.Fatalf("a second discard: %s", rec.Body.String())
	}
}

func TestBulkActionsAreBounded(t *testing.T) {
	ids := make([]string, maxBulk+1)
	for i := range ids {
		ids[i] = fmt.Sprint("dl-", i)
	}
	raw, _ := json.Marshal(map[string]any{"ids": ids})
	rec := serve(newStore(), editorA, http.MethodPost, "/api/dead-letters/discard", string(raw))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 for %d ids", rec.Code, len(ids))
	}
	raw, _ = json.Marshal(map[string]any{"ids": ids[:maxBulkReplay+1]})
	if rec := serve(newStore(), editorA, http.MethodPost, "/api/dead-letters/replay", string(raw)); rec.Code != http.StatusBadRequest {
		t.Fatalf("replay: status = %d, want 400 for %d ids", rec.Code, maxBulkReplay+1)
	}
}

func TestReplayRefusesWhatCannotBeReplayed(t *testing.T) {
	st := newStore()
	// A truncated payload is not the message that failed.
	rec := serve(st, editorA, http.MethodPost, "/api/dead-letters/dl-cut/replay", `{}`)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("truncated: status = %d, want 422: %s", rec.Code, rec.Body.String())
	}
	rec = serve(st, editorA, http.MethodPost, "/api/dead-letters/dl-a/replay", `{"payload":"{not json"}`)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("invalid JSON: status = %d, want 400", rec.Code)
	}
	st.letters["dl-a"] = func() storage.DeadLetter { d := st.letters["dl-a"]; d.Status = storage.DeadLetterDiscarded; return d }()
	rec = serve(st, editorA, http.MethodPost, "/api/dead-letters/dl-a/replay", `{}`)
	if rec.Code != http.StatusConflict {
		t.Errorf("discarded: status = %d, want 409", rec.Code)
	}
}

func TestExportIsCSVOfTheChosenDeadLetters(t *testing.T) {
	rec := serve(newStore(), editorA, http.MethodGet, "/api/dead-letters/export?ids=dl-a,dl-b", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/csv") {
		t.Fatalf("Content-Type = %q", ct)
	}
	if cd := rec.Header().Get("Content-Disposition"); !strings.Contains(cd, "attachment") {
		t.Fatalf("Content-Disposition = %q", cd)
	}
	rows, err := csv.NewReader(rec.Body).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("rows = %v, want the header and dl-a only", rows)
	}
	header := strings.Join(rows[0], ",")
	for _, col := range []string{"workflow", "message_id", "failed_at", "error", "payload"} {
		if !strings.Contains(header, col) {
			t.Errorf("header %q lacks %s", header, col)
		}
	}
	if !strings.Contains(strings.Join(rows[1], ","), `{"total":"12,50"}`) {
		t.Fatalf("row = %v", rows[1])
	}
}

// A spreadsheet runs a cell that starts with = as a formula; an error message
// is text a stranger's system wrote.
func TestExportDefusesFormulas(t *testing.T) {
	st := newStore()
	d := st.letters["dl-a"]
	d.Error = `=HYPERLINK("http://evil","x")`
	st.letters["dl-a"] = d
	rec := serve(st, editorA, http.MethodGet, "/api/dead-letters/export?ids=dl-a", "")
	rows, _ := csv.NewReader(rec.Body).ReadAll()
	for _, cell := range rows[1] {
		if strings.HasPrefix(cell, "=") {
			t.Fatalf("a cell starts with =: %q", cell)
		}
	}
}

func TestStatsAreForTheUsersVHosts(t *testing.T) {
	st := newStore()
	rec := serve(st, viewerA, http.MethodGet, "/api/dead-letters/stats", "")
	if rec.Code != http.StatusOK || len(st.statsFor) != 1 || st.statsFor[0] != "a" {
		t.Fatalf("status %d, stats for %v", rec.Code, st.statsFor)
	}
}

// A backend that cannot keep dead letters says so rather than failing.
func TestABackendWithoutDeadLettersAnswersNotImplemented(t *testing.T) {
	rec := serve(&testutil.BaseMockStorage{}, admin, http.MethodGet, "/api/dead-letters", "")
	if rec.Code != http.StatusNotImplemented {
		t.Fatalf("status = %d, want 501", rec.Code)
	}
}
