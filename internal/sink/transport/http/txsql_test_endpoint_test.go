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
	"path/filepath"
	"strings"
	"testing"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/internal/api/handlers"
	"github.com/gsoultan/hermod/internal/engine/registry"
	"github.com/gsoultan/hermod/internal/factory"
	"github.com/gsoultan/hermod/internal/storage"
	_ "modernc.org/sqlite"
)

func txsqlTestMux(t *testing.T) *http.ServeMux {
	t.Helper()
	store := vhostSecretStorage{secrets: map[string]string{}}
	reg := registry.NewRegistry(store)
	reg.SetFactories(nil, factory.CreateSink)
	mux := http.NewServeMux()
	(&SinkHandler{Handler: &handlers.Handler{Storage: store, Registry: reg}}).RegisterSinkRoutes(mux)
	return mux
}

func post(t *testing.T, mux *http.ServeMux, path string, body any, user *storage.User) *httptest.ResponseRecorder {
	t.Helper()
	b, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, path, strings.NewReader(string(b)))
	req = req.WithContext(context.WithValue(req.Context(), handlers.UserContextKey, user))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

var admin = &storage.User{Role: storage.RoleAdministrator}

func sqliteOrders(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "orders.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if _, err := db.ExecContext(t.Context(), `CREATE TABLE orders (id INTEGER PRIMARY KEY AUTOINCREMENT, external_id TEXT UNIQUE, total INTEGER)`); err != nil {
		t.Fatal(err)
	}
	return path
}

func countRows(t *testing.T, path string) int {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	var n int
	if err := db.QueryRowContext(t.Context(), "SELECT count(*) FROM orders").Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// The form's Test button runs the statements against the database with the
// sample message and shows what each did, and nothing it did is kept.
func TestTheTestButtonRunsTheStatementsAndKeepsNothing(t *testing.T) {
	path := sqliteOrders(t)
	mux := txsqlTestMux(t)
	rec := post(t, mux, "/api/sinks/txsql/test", map[string]any{
		"vhost": "/",
		"config": map[string]string{
			"driver": "sqlite", "path": path,
			"statements": `[{"name":"order","sql":"INSERT INTO orders (external_id, total) VALUES ({{id}}, {{total}}) RETURNING id"}]`,
		},
		"sample": map[string]any{"id": "E-1", "total": 5},
	}, admin)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	var got struct {
		Simulation hermod.SinkSimulation `json:"simulation"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Simulation.Error != "" || len(got.Simulation.Steps) != 1 || got.Simulation.Steps[0].RowCount != 1 {
		t.Fatalf("simulation = %+v", got.Simulation)
	}
	if n := countRows(t, path); n != 0 {
		t.Fatalf("the test left %d rows behind", n)
	}
}

func TestTheTestButtonExplainsAConfigThatCannotRun(t *testing.T) {
	mux := txsqlTestMux(t)
	rec := post(t, mux, "/api/sinks/txsql/test", map[string]any{
		"config": map[string]string{"driver": "sqlite", "path": "x.db", "statements": `[{"sql":"DROP TABLE orders"}]`},
	}, admin)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "schema changes") {
		t.Fatalf("status %d body %s", rec.Code, rec.Body)
	}
}

// Nobody can run statements against a vhost they cannot reach. (Viewers are
// kept out by EditorOnly, as on every route that touches a database; this
// harness runs in first-run mode, where RBAC is bypassed, so it is not
// asserted here.)
func TestTheTestButtonIsForTheVHostsOwnEditors(t *testing.T) {
	mux := txsqlTestMux(t)
	body := map[string]any{"vhost": "sales", "config": map[string]string{"driver": "sqlite", "path": "x.db", "statements": `[{"sql":"SELECT 1"}]`}}
	if rec := post(t, mux, "/api/sinks/txsql/test", body, &storage.User{Role: storage.RoleEditor, VHosts: []string{"other"}}); rec.Code != http.StatusForbidden {
		t.Fatalf("editor of another vhost: %d", rec.Code)
	}
}

// Show SQL and Convert to SQL mode ask the server for the statements a Guided
// step list compiles to, so the form never has its own copy of the compiler.
func TestCompileShowsTheSQLAGuidedStepListBecomes(t *testing.T) {
	mux := txsqlTestMux(t)
	rec := post(t, mux, "/api/sinks/txsql/compile", map[string]any{"config": map[string]string{
		"driver": "postgres", "mode": "guided",
		"steps": `[{"name":"order","table":"orders","action":"upsert","match":["external_id"],"columns":[{"column":"external_id","value":"{{id}}"},{"column":"total","value":"{{total}}"}],"returning":["id"]}]`,
	}}, admin)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	var got struct {
		Statements []map[string]any `json:"statements"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Statements) != 1 || !strings.Contains(got.Statements[0]["sql"].(string), `ON CONFLICT ("external_id")`) || got.Statements[0]["name"] != "order" {
		t.Fatalf("statements = %+v", got.Statements)
	}

	rec = post(t, mux, "/api/sinks/txsql/compile", map[string]any{"config": map[string]string{
		"driver": "postgres", "mode": "guided", "steps": `[{"table":"orders; DROP TABLE x","action":"insert","columns":[{"column":"a","value":"{{a}}"}]}]`,
	}}, admin)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("a bad step compiled: %d %s", rec.Code, rec.Body)
	}
}
