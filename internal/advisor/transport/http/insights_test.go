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
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gsoultan/hermod/internal/advisor"
	"github.com/gsoultan/hermod/internal/api/handlers"
	"github.com/gsoultan/hermod/internal/engine/registry"
	"github.com/gsoultan/hermod/internal/storage"
	sqlstorage "github.com/gsoultan/hermod/internal/storage/sql"
	_ "modernc.org/sqlite"
)

func newMux(t *testing.T) *http.ServeMux {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "db_config.yaml"), []byte("type: sqlite\nconn: \"file::memory:\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HERMOD_CONFIG_DIR", dir)
	db, err := sql.Open("sqlite", "file:adv_"+strings.ReplaceAll(t.Name(), "/", "_")+"?mode=memory&cache=shared")
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
