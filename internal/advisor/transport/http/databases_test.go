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
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/gsoultan/hermod/internal/advisor"
	"github.com/gsoultan/hermod/internal/api/handlers"
	"github.com/gsoultan/hermod/internal/engine/registry"
	"github.com/gsoultan/hermod/internal/storage"
	"github.com/gsoultan/hermod/pkg/infra/dbactivity"
)

func databasesMux(t *testing.T, snap func(context.Context, storage.Source) (dbactivity.Snapshot, bool)) *http.ServeMux {
	t.Helper()
	store := storeWith(t)
	ctx := t.Context()
	for _, s := range []storage.Source{
		{ID: "src-cdc", Name: "orders_cdc", Type: "postgres", VHost: "team-a"},
		{ID: "src-etl", Name: "athena_etl_db", Type: "postgres", VHost: "team-a"},
	} {
		if err := store.CreateSource(ctx, s); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.CreateWorkflow(ctx, storage.Workflow{ID: "wf-2", Name: "Stock report", VHost: "team-a"}); err != nil {
		t.Fatal(err)
	}
	wf, err := store.GetWorkflow(ctx, "wf-1")
	if err != nil {
		t.Fatal(err)
	}
	wf.Name = "Daily store totals"
	wf.Nodes = []storage.WorkflowNode{
		{ID: "s1", Type: "source", RefID: "src-cdc"},
		{ID: "find", Type: "transformation", Config: map[string]any{"transType": "db_lookup", "sourceId": "src-etl"}},
		{ID: "totals", Type: "transformation", Config: map[string]any{"transType": "execute_sql", "sourceId": "src-etl"}},
	}
	if err := store.UpdateWorkflow(ctx, wf); err != nil {
		t.Fatal(err)
	}
	reg := registry.NewRegistry(store)
	t.Cleanup(reg.Close)
	h := NewInsightsHandler(&handlers.Handler{Storage: store, Registry: reg})
	h.snapshot = snap
	mux := http.NewServeMux()
	h.RegisterInsightsRoutes(mux)
	return mux
}

// Each database the flow reaches is read once, however many steps use it,
// in the flow's order, with other flows named rather than given by ID.
func TestDatabasesReadsEachDatabaseTheFlowReaches(t *testing.T) {
	now := time.Now()
	var asked []string
	mux := databasesMux(t, func(_ context.Context, src storage.Source) (dbactivity.Snapshot, bool) {
		asked = append(asked, src.ID)
		if src.ID == "src-cdc" {
			return dbactivity.Snapshot{}, true
		}
		return dbactivity.Snapshot{Ceiling: 15, Learned: 12, Limit: 9, Queries: []dbactivity.Query{
			{Kind: dbactivity.SQLStep, WorkflowID: "wf-2", NodeID: "report", Started: now.Add(-2 * time.Hour)},
		}}, true
	})
	rec := get(t, mux, "/api/workflows/wf-1/insights/databases", storage.RoleViewer, "team-a")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	var got struct{ Databases []advisor.DatabaseView }
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if len(asked) != 2 || asked[0] != "src-cdc" || asked[1] != "src-etl" {
		t.Errorf("asked %v", asked)
	}
	if len(got.Databases) != 2 || got.Databases[1].Source != "athena_etl_db" || got.Databases[1].Learned != 12 {
		t.Fatalf("databases = %+v", got.Databases)
	}
	var other string
	for _, c := range got.Databases[1].NeedsYou {
		if c.Rule == advisor.RuleOtherFlows {
			other = c.Detail
		}
	}
	if other == "" || !strings.Contains(other, "Stock report") {
		t.Errorf("other flows card = %q", other)
	}
}

// A source Hermod cannot read activity for (not PostgreSQL) is left out
// rather than shown as healthy.
func TestDatabasesLeavesOutWhatItCannotRead(t *testing.T) {
	mux := databasesMux(t, func(_ context.Context, src storage.Source) (dbactivity.Snapshot, bool) {
		return dbactivity.Snapshot{}, src.ID == "src-etl"
	})
	rec := get(t, mux, "/api/workflows/wf-1/insights/databases", storage.RoleViewer, "team-a")
	var got struct{ Databases []advisor.DatabaseView }
	_ = json.NewDecoder(rec.Body).Decode(&got)
	if len(got.Databases) != 1 || got.Databases[0].SourceID != "src-etl" {
		t.Errorf("databases = %+v", got.Databases)
	}
}

func TestDatabasesFollowsTheWorkflowsVHost(t *testing.T) {
	mux := databasesMux(t, func(context.Context, storage.Source) (dbactivity.Snapshot, bool) { return dbactivity.Snapshot{}, true })
	if rec := get(t, mux, "/api/workflows/wf-1/insights/databases", storage.RoleViewer, "team-b"); rec.Code != http.StatusForbidden {
		t.Errorf("status %d", rec.Code)
	}
}
