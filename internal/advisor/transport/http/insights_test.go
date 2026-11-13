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
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/gsoultan/hermod/internal/advisor"
	"github.com/gsoultan/hermod/internal/api/handlers"
	"github.com/gsoultan/hermod/internal/engine/registry"
	"github.com/gsoultan/hermod/internal/storage"
	sqlstorage "github.com/gsoultan/hermod/internal/storage/sql"
	"github.com/gsoultan/hermod/pkg/llm"
	_ "modernc.org/sqlite"
)

// dbSeq keeps the in-memory databases of one test apart.
var dbSeq atomic.Int64

func storeWith(t *testing.T) storage.Storage {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "db_config.yaml"), []byte("type: sqlite\nconn: \"file::memory:\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HERMOD_CONFIG_DIR", dir)
	db, err := sql.Open("sqlite", fmt.Sprintf("file:adv_%s_%d?mode=memory&cache=shared", strings.ReplaceAll(t.Name(), "/", "_"), dbSeq.Add(1)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	store := sqlstorage.NewSQLStorage(db, "sqlite")
	if err := store.Init(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateWorkflow(t.Context(), storage.Workflow{ID: "wf-1", Name: "wf-1", VHost: "team-a"}); err != nil {
		t.Fatal(err)
	}
	// One user exists, so the role guards are not in first-run bypass.
	if err := store.CreateUser(t.Context(), storage.User{ID: "u-0", Username: "owner", Password: "x", Role: storage.RoleAdministrator}); err != nil {
		t.Fatal(err)
	}
	return store
}

func newMux(t *testing.T) *http.ServeMux {
	t.Helper()
	store := storeWith(t)
	reg := registry.NewRegistry(store)
	t.Cleanup(reg.Close)
	mux := http.NewServeMux()
	NewInsightsHandler(&handlers.Handler{Storage: store, Registry: reg}).RegisterInsightsRoutes(mux)
	return mux
}

func get(t *testing.T, mux *http.ServeMux, path string, role storage.Role, vhosts ...string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, path, nil)
	req = req.WithContext(context.WithValue(req.Context(), handlers.UserContextKey,
		&storage.User{ID: "u-1", Username: "tester", Role: role, VHosts: vhosts}))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func TestInsightsForAStoppedFlowSaysItIsNotRunning(t *testing.T) {
	rec := get(t, newMux(t), "/api/workflows/wf-1/insights", storage.RoleViewer, "team-a")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body)
	}
	var rep advisor.Report
	if err := json.Unmarshal(rec.Body.Bytes(), &rep); err != nil {
		t.Fatal(err)
	}
	if rep.WorkflowID != "wf-1" || rep.State != advisor.StateNotRunning {
		t.Fatalf("report = %+v", rep)
	}
	if rep.Findings == nil {
		t.Fatal("findings must be an empty list, not null, so the page can map over it")
	}
}

func TestInsightsRefusesAnotherVHost(t *testing.T) {
	rec := get(t, newMux(t), "/api/workflows/wf-1/insights", storage.RoleViewer, "team-b")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
}

func TestInsightsForAMissingFlow(t *testing.T) {
	rec := get(t, newMux(t), "/api/workflows/nope/insights", storage.RoleAdministrator)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

type fakeModel struct {
	calls int
	err   error
}

func (*fakeModel) Name() string { return "fake" }

func (f *fakeModel) Chat(context.Context, llm.ChatRequest) (llm.ChatResponse, error) {
	f.calls++
	return llm.ChatResponse{Text: "Cache the lookup first.", StopReason: llm.StopEnd}, f.err
}

// explainMux serves a flow whose report has one finding, explained by m; a
// nil m leaves AI explanations off.
func explainMux(t *testing.T, m *fakeModel, findings ...advisor.Finding) *http.ServeMux {
	t.Helper()
	mux := http.NewServeMux()
	store := storeWith(t)
	reg := registry.NewRegistry(store)
	t.Cleanup(reg.Close)
	h := NewInsightsHandler(&handlers.Handler{Storage: store, LogStorage: store, Registry: reg})
	h.report = func(context.Context, string) advisor.Report {
		return advisor.Report{WorkflowID: "wf-1", State: advisor.StateReady, Findings: findings}
	}
	h.explainer = func() *advisor.Explainer {
		if m == nil {
			return nil
		}
		return advisor.NewExplainer(func(context.Context, string) (llm.Provider, string, error) { return m, "model-x", nil })
	}
	h.RegisterInsightsRoutes(mux)
	return mux
}

var slowStep = advisor.Finding{Rule: advisor.RuleBottleneck, Severity: advisor.SeverityHigh, Title: "Find customer is the slowest step"}

func post(t *testing.T, mux *http.ServeMux, role storage.Role, vhosts ...string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/workflows/wf-1/insights/explain", nil)
	req = req.WithContext(context.WithValue(req.Context(), handlers.UserContextKey,
		&storage.User{ID: "u-1", Username: "tester", Role: role, VHosts: vhosts}))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func TestInsightsSayWhetherAnExplanationCanBeAsked(t *testing.T) {
	for name, tc := range map[string]struct {
		m    *fakeModel
		want bool
	}{"configured": {&fakeModel{}, true}, "off": {nil, false}} {
		rec := get(t, explainMux(t, tc.m, slowStep), "/api/workflows/wf-1/insights", storage.RoleViewer, "team-a")
		var body struct {
			CanExplain bool   `json:"can_explain"`
			State      string `json:"state"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if body.CanExplain != tc.want || body.State != advisor.StateReady {
			t.Errorf("%s: body = %+v (%s)", name, body, rec.Body)
		}
	}
}

func TestExplainReturnsTheModelsWords(t *testing.T) {
	m := &fakeModel{}
	rec := post(t, explainMux(t, m, slowStep), storage.RoleEditor, "team-a")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body)
	}
	var got advisor.Explanation
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Text != "Cache the lookup first." || got.Model != "model-x" || m.calls != 1 {
		t.Fatalf("explanation = %+v after %d calls", got, m.calls)
	}
}

// An explanation spends the vhost's AI budget, so reading the page is not
// enough to ask for one, and neither is editing somewhere else.
func TestExplainIsForEditorsOfTheFlowsVHost(t *testing.T) {
	m := &fakeModel{}
	mux := explainMux(t, m, slowStep)
	if rec := post(t, mux, storage.RoleViewer, "team-a"); rec.Code != http.StatusForbidden {
		t.Errorf("viewer: status = %d, want 403", rec.Code)
	}
	if rec := post(t, mux, storage.RoleEditor, "team-b"); rec.Code != http.StatusForbidden {
		t.Errorf("other vhost: status = %d, want 403", rec.Code)
	}
	if m.calls != 0 {
		t.Errorf("the model was called %d times for refused requests", m.calls)
	}
}

func TestExplainSaysWhyItCannot(t *testing.T) {
	budget := &llm.BudgetError{Limit: llm.LimitVHostTokens, VHost: "team-a", Used: 10, Max: 10}
	for name, tc := range map[string]struct {
		mux  *http.ServeMux
		want int
	}{
		"not set up":   {explainMux(t, nil, slowStep), http.StatusConflict},
		"no findings":  {explainMux(t, &fakeModel{}), http.StatusConflict},
		"budget spent": {explainMux(t, &fakeModel{err: budget}, slowStep), http.StatusTooManyRequests},
		"model failed": {explainMux(t, &fakeModel{err: errors.New("boom")}, slowStep), http.StatusBadGateway},
	} {
		if rec := post(t, tc.mux, storage.RoleEditor, "team-a"); rec.Code != tc.want {
			t.Errorf("%s: status = %d, want %d: %s", name, rec.Code, tc.want, rec.Body)
		}
	}
}
