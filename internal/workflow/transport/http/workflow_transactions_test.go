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
	"net/http/httptest"
	"testing"

	"github.com/gsoultan/hermod/internal/api/handlers"
	"github.com/gsoultan/hermod/internal/engine/registry"
	"github.com/gsoultan/hermod/internal/storage"
)

// GET /api/workflows/{id}/transactions is what the run view reads.

func transactionsWorkflowStorage() *MockWorkflowStorage {
	return &MockWorkflowStorage{
		workflows: map[string]storage.Workflow{
			"wf": {
				ID:    "wf",
				VHost: "tenant-a",
				Nodes: []storage.WorkflowNode{
					{ID: "src", Type: "source", RefID: "src-1"},
					{ID: "pg", Type: "sink", RefID: "pg-1", Config: map[string]any{"label": "Orders", "transaction": "order"}},
					{ID: "my", Type: "sink", RefID: "my-1", Config: map[string]any{"label": "Invoices", "transaction": "order"}},
				},
				Edges: []storage.WorkflowEdge{
					{ID: "e1", SourceID: "src", TargetID: "pg"},
					{ID: "e2", SourceID: "src", TargetID: "my"},
				},
			},
		},
		sinks: map[string]storage.Sink{
			"pg-1": {ID: "pg-1", Type: "postgres"},
			"my-1": {ID: "my-1", Type: "mysql"},
		},
	}
}

func getTransactions(t *testing.T, h *WorkflowHandler, id string, user *storage.User) *httptest.ResponseRecorder {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/workflows/{id}/transactions", h.GetWorkflowTransactions)
	ctx := t.Context()
	if user != nil {
		ctx = context.WithValue(ctx, handlers.UserContextKey, user)
	}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequestWithContext(ctx, http.MethodGet, "/api/workflows/"+id+"/transactions", nil))
	return rec
}

func TestGetWorkflowTransactionsListsTheTransactions(t *testing.T) {
	store := transactionsWorkflowStorage()
	h := &WorkflowHandler{Handler: &handlers.Handler{Storage: store, Registry: registry.NewRegistry(store)}}

	rec := getTransactions(t, h, "wf", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	var body struct {
		Transactions []registry.TransactionRuns `json:"transactions"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.Transactions) != 1 || body.Transactions[0].Name != "order" || len(body.Transactions[0].Members) != 2 {
		t.Errorf("transactions = %+v, want order with its two members", body.Transactions)
	}
}

func TestGetWorkflowTransactionsOfAnotherTenantIsForbidden(t *testing.T) {
	store := transactionsWorkflowStorage()
	h := &WorkflowHandler{Handler: &handlers.Handler{Storage: store, Registry: registry.NewRegistry(store)}}
	rec := getTransactions(t, h, "wf", &storage.User{Role: storage.RoleViewer, VHosts: []string{"tenant-b"}})
	if rec.Code != http.StatusForbidden {
		t.Errorf("status %d, want 403 for a user without the workflow's vhost", rec.Code)
	}
}

func TestGetWorkflowTransactionsOfAMissingWorkflowIsNotFound(t *testing.T) {
	store := transactionsWorkflowStorage()
	h := &WorkflowHandler{Handler: &handlers.Handler{Storage: store, Registry: registry.NewRegistry(store)}}
	if rec := getTransactions(t, h, "nope", nil); rec.Code != http.StatusNotFound {
		t.Errorf("status %d, want 404", rec.Code)
	}
}
