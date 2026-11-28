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
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gsoultan/hermod/internal/advisor/queryplan"
	"github.com/gsoultan/hermod/internal/api/handlers"
	"github.com/gsoultan/hermod/internal/engine/registry"
	"github.com/gsoultan/hermod/internal/storage"
)

func speedMux(t *testing.T, plan func(ctx context.Context, src storage.Source, sql string) (queryplan.Check, error)) *http.ServeMux {
	t.Helper()
	store := storeWith(t)
	reg := registry.NewRegistry(store)
	t.Cleanup(reg.Close)
	h := NewSpeedCheckHandler(&handlers.Handler{Storage: store, LogStorage: store, Registry: reg})
	h.plan = plan
	mux := http.NewServeMux()
	h.RegisterSpeedCheckRoutes(mux)
	return mux
}

func postSpeed(t *testing.T, mux *http.ServeMux, body any, role storage.Role, vhosts ...string) *httptest.ResponseRecorder {
	t.Helper()
	b, _ := json.Marshal(body)
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/sources/query/speed-check", bytes.NewReader(b))
	req = req.WithContext(context.WithValue(req.Context(), handlers.UserContextKey,
		&storage.User{ID: "u-1", Username: "tester", Role: role, VHosts: vhosts}))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func pgBody(query string) map[string]any {
	return map[string]any{
		"config": map[string]any{"type": "postgres", "vhost": "team-a",
			"config": map[string]string{"connection_string": "postgres://u:secret@db:6432/athena"}},
		"query": query,
	}
}

// The builder holds the statement as a template. What is planned is what the
// engine sends: each {{.path}} a bound parameter, never a value spliced in.
func TestSpeedCheckPlansTheTemplateAsTheEngineSendsIt(t *testing.T) {
	var planned string
	mux := speedMux(t, func(_ context.Context, _ storage.Source, sql string) (queryplan.Check, error) {
		planned = sql
		return queryplan.Check{Findings: []queryplan.Finding{{Rule: queryplan.RuleWrappedColumn, Table: "public.sales_line"}}}, nil
	})
	rec := postSpeed(t, mux, pgBody("SELECT * FROM sales_line WHERE id = {{.after.id}} AND code = {{.code}}"), storage.RoleEditor, "team-a")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	if planned != "SELECT * FROM sales_line WHERE id = $1 AND code = $2" {
		t.Errorf("planned %q", planned)
	}
	var got speedCheckView
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got.SQL != planned || len(got.Findings) != 1 {
		t.Errorf("view = %+v", got)
	}
}

// It connects to the database, like Run Query, so it takes an editor of the
// connector's vhost.
func TestSpeedCheckIsForEditorsOfTheVHost(t *testing.T) {
	ran := false
	mux := speedMux(t, func(context.Context, storage.Source, string) (queryplan.Check, error) {
		ran = true
		return queryplan.Check{}, nil
	})
	if rec := postSpeed(t, mux, pgBody("SELECT 1"), storage.RoleViewer, "team-a"); rec.Code != http.StatusForbidden {
		t.Errorf("viewer got %d", rec.Code)
	}
	if rec := postSpeed(t, mux, pgBody("SELECT 1"), storage.RoleEditor, "team-b"); rec.Code != http.StatusForbidden {
		t.Errorf("editor of another vhost got %d", rec.Code)
	}
	if ran {
		t.Error("a refused caller had the query planned")
	}
}

// Only PostgreSQL can be planned so far, and the answer says so rather than
// failing on the database.
func TestSpeedCheckSaysWhichDatabasesItReads(t *testing.T) {
	mux := speedMux(t, func(context.Context, storage.Source, string) (queryplan.Check, error) {
		t.Error("a MySQL source was planned")
		return queryplan.Check{}, nil
	})
	body := pgBody("SELECT 1")
	body["config"].(map[string]any)["type"] = "mysql"
	rec := postSpeed(t, mux, body, storage.RoleEditor, "team-a")
	if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), "PostgreSQL") {
		t.Errorf("status %d: %s", rec.Code, rec.Body)
	}
}

func TestSpeedCheckNeedsAStatement(t *testing.T) {
	mux := speedMux(t, func(context.Context, storage.Source, string) (queryplan.Check, error) { return queryplan.Check{}, nil })
	if rec := postSpeed(t, mux, pgBody("  "), storage.RoleEditor, "team-a"); rec.Code != http.StatusBadRequest {
		t.Errorf("status %d", rec.Code)
	}
}

// When the plan shows a truncated column defeating its index, the answer
// carries the whole query rewritten, in the template's own terms, for the
// builder to put back in the editor.
func TestSpeedCheckReturnsTheQueryRewritten(t *testing.T) {
	mux := speedMux(t, func(context.Context, storage.Source, string) (queryplan.Check, error) {
		return queryplan.Check{Findings: []queryplan.Finding{{Rule: queryplan.RuleWrappedColumn, Rewrite: "x"}}, Tables: slTable}, nil
	})
	rec := postSpeed(t, mux, pgBody("SELECT * FROM sl WHERE date_trunc('day', created_at) = {{.day}}"), storage.RoleEditor, "team-a")
	var got speedCheckView
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	want := "SELECT * FROM sl WHERE (created_at >= ({{.day}}) AND created_at < CAST({{.day}} AS timestamp with time zone) + interval '1 day')"
	if got.Rewritten != want {
		t.Errorf("rewritten = %q", got.Rewritten)
	}
}

// Without such a finding the query is not touched, even if the text could be.
func TestSpeedCheckRewritesOnlyWhatThePlanFaults(t *testing.T) {
	mux := speedMux(t, func(context.Context, storage.Source, string) (queryplan.Check, error) { return queryplan.Check{}, nil })
	rec := postSpeed(t, mux, pgBody("SELECT * FROM sl WHERE date_trunc('day', created_at) = {{.day}}"), storage.RoleEditor, "team-a")
	if strings.Contains(rec.Body.String(), "rewritten") {
		t.Errorf("body = %s", rec.Body)
	}
}

// The rewrite is planned too, so the answer can say how much less work the
// database expects, and a rewrite the database refuses is not offered.
func TestSpeedCheckPlansTheRewriteToo(t *testing.T) {
	plan := func(_ context.Context, _ storage.Source, sql string) (queryplan.Check, error) {
		if strings.Contains(sql, "date_trunc") {
			return queryplan.Check{Cost: 1319, Findings: []queryplan.Finding{{Rule: queryplan.RuleWrappedColumn, Rewrite: "x"}}, Tables: slTable}, nil
		}
		if sql != "SELECT * FROM sl WHERE (created_at >= ($1) AND created_at < CAST($2 AS timestamp with time zone) + interval '1 day')" {
			t.Errorf("rewrite planned as %q", sql)
		}
		return queryplan.Check{Cost: 12.5}, nil
	}
	rec := postSpeed(t, speedMux(t, plan), pgBody("SELECT * FROM sl WHERE date_trunc('day', created_at) = {{.day}}"), storage.RoleEditor, "team-a")
	var got speedCheckView
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got.Cost != 1319 || got.RewrittenCost != 12.5 || got.Rewritten == "" {
		t.Errorf("view = %+v", got)
	}

	refused := func(_ context.Context, _ storage.Source, sql string) (queryplan.Check, error) {
		if strings.Contains(sql, "date_trunc") {
			return queryplan.Check{Findings: []queryplan.Finding{{Rule: queryplan.RuleWrappedColumn, Rewrite: "x"}}, Tables: slTable}, nil
		}
		return queryplan.Check{}, errors.New("operator does not exist")
	}
	rec = postSpeed(t, speedMux(t, refused), pgBody("SELECT * FROM sl WHERE date_trunc('day', created_at) = {{.day}}"), storage.RoleEditor, "team-a")
	if rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), "rewritten") {
		t.Errorf("status %d: %s", rec.Code, rec.Body)
	}
}

var slTable = []queryplan.Table{{Schema: "public", Name: "sl", Columns: map[string]string{"created_at": "timestamp with time zone"}}}
