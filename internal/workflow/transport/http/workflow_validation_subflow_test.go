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
	"strings"
	"testing"

	"github.com/gsoultan/hermod/internal/api/handlers"
	"github.com/gsoultan/hermod/internal/engine/registry"
	"github.com/gsoultan/hermod/internal/engine/registry/subflow"
	"github.com/gsoultan/hermod/internal/storage"
)

func errorsOf(issues []ValidationIssue) []string {
	var out []string
	for _, i := range issues {
		if i.Severity == "error" {
			out = append(out, i.Message)
		}
	}
	return out
}

func addressSubflow() storage.Workflow {
	return storage.Workflow{ID: "address", Name: "Clean address", Nodes: []storage.WorkflowNode{
		{ID: "in", Type: subflow.TypeInput, Config: map[string]any{"fields": []any{map[string]any{"name": "zip", "required": true}}}},
		{ID: "t", Type: "transformation", Config: map[string]any{"transType": "set"}},
		{ID: "ret", Type: subflow.TypeReturn, Config: map[string]any{"name": "valid"}},
	}, Edges: []storage.WorkflowEdge{
		{ID: "e1", SourceID: "in", TargetID: "t"},
		{ID: "e2", SourceID: "t", TargetID: "ret"},
	}}
}

func TestValidateWorkflowSavesASubflowWithoutASource(t *testing.T) {
	h := &WorkflowHandler{Handler: &handlers.Handler{}}
	issues := h.ValidateWorkflow(context.Background(), addressSubflow())
	if errs := errorsOf(issues); len(errs) > 0 {
		t.Fatalf("a sub-flow was refused: %v", errs)
	}
	for _, i := range issues {
		if i.NodeID == "in" || i.NodeID == "ret" {
			t.Fatalf("the sub-flow's own input or return was flagged: %s", i.Message)
		}
	}
}

func TestValidateWorkflowRefusesABrokenSubflow(t *testing.T) {
	h := &WorkflowHandler{Handler: &handlers.Handler{}}
	noReturn := addressSubflow()
	noReturn.Nodes = noReturn.Nodes[:2]
	noReturn.Edges = noReturn.Edges[:1]

	withSource := addressSubflow()
	withSource.Nodes = append(withSource.Nodes, storage.WorkflowNode{ID: "s", Type: "source", RefID: "x"})

	twoInputs := addressSubflow()
	twoInputs.Nodes = append(twoInputs.Nodes, storage.WorkflowNode{ID: "in2", Type: subflow.TypeInput})

	for name, tc := range map[string]struct {
		wf   storage.Workflow
		want string
	}{
		"no return":  {noReturn, "Return"},
		"a source":   {withSource, "source"},
		"two inputs": {twoInputs, "one Sub-flow input"},
	} {
		t.Run(name, func(t *testing.T) {
			errs := errorsOf(h.ValidateWorkflow(context.Background(), tc.wf))
			if !strings.Contains(strings.Join(errs, "\n"), tc.want) {
				t.Fatalf("errors %v, want one mentioning %s", errs, tc.want)
			}
		})
	}
}

func TestValidateWorkflowRefusesACallThatCannotRun(t *testing.T) {
	store := newSQLiteStore(t, "subflow-calls")
	ctx := t.Context()
	sub := addressSubflow()
	if err := store.CreateWorkflow(ctx, sub); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateWorkflowVersion(ctx, storage.WorkflowVersion{
		ID: "address-v1", WorkflowID: "address", Version: 1, Nodes: sub.Nodes, Edges: sub.Edges,
	}); err != nil {
		t.Fatal(err)
	}
	h := &WorkflowHandler{Handler: &handlers.Handler{Storage: store, Registry: registry.NewRegistry(store)}}

	caller := func(cfg map[string]any) storage.Workflow {
		c := map[string]any{"subflow_id": "address", "version": float64(1),
			"inputs": []any{map[string]any{"from": "zip", "to": "zip"}}}
		for k, v := range cfg {
			c[k] = v
		}
		return storage.Workflow{ID: "orders", Name: "orders", Nodes: []storage.WorkflowNode{
			{ID: "src", Type: "source", RefID: "s"},
			{ID: "call", Type: subflow.TypeCall, Config: c},
			{ID: "out", Type: "sink", RefID: "k"},
		}, Edges: []storage.WorkflowEdge{
			{ID: "e1", SourceID: "src", TargetID: "call"},
			{ID: "e2", SourceID: "call", TargetID: "out"},
		}}
	}

	for name, tc := range map[string]struct {
		cfg  map[string]any
		want string
	}{
		"missing version": {map[string]any{"version": float64(9)}, "version 9"},
		"unmapped input":  {map[string]any{"inputs": nil}, `"zip"`},
		"no pin":          {map[string]any{"version": nil}, "pin"},
	} {
		t.Run(name, func(t *testing.T) {
			var found *ValidationIssue
			for _, i := range h.ValidateWorkflow(ctx, caller(tc.cfg)) {
				if i.Severity == "error" && strings.Contains(i.Message, tc.want) {
					found = &i
				}
			}
			if found == nil || found.NodeID != "call" {
				t.Fatalf("no error on the call step mentioning %s: %+v", tc.want, h.ValidateWorkflow(ctx, caller(tc.cfg)))
			}
		})
	}

	for _, i := range h.ValidateWorkflow(ctx, caller(nil)) {
		if strings.Contains(i.Message, "sub-flow") && i.Severity == "error" {
			t.Fatalf("a call that can run was refused: %s", i.Message)
		}
	}
}

// usedAddress saves the address sub-flow and two callers: "orders" on the
// latest save and "returns" pinned to version 1.
func usedAddress(t *testing.T) *WorkflowHandler {
	t.Helper()
	store := newSQLiteStore(t, "subflow-used")
	ctx := t.Context()
	sub := addressSubflow()
	if err := store.CreateWorkflow(ctx, sub); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateWorkflowVersion(ctx, storage.WorkflowVersion{
		ID: "address-v1", WorkflowID: "address", Version: 1, Nodes: sub.Nodes, Edges: sub.Edges,
	}); err != nil {
		t.Fatal(err)
	}
	for id, version := range map[string]any{"orders": "latest", "returns": float64(1)} {
		if err := store.CreateWorkflow(ctx, storage.Workflow{ID: id, Name: id, Nodes: []storage.WorkflowNode{
			{ID: "src", Type: "source", RefID: "s"},
			{ID: "call", Type: subflow.TypeCall, Config: map[string]any{
				"label": "Clean address", "subflow_id": "address", "version": version,
				"inputs": []any{map[string]any{"from": "zip", "to": "zip"}}}},
		}, Edges: []storage.WorkflowEdge{{ID: "e1", SourceID: "src", TargetID: "call"}}}); err != nil {
			t.Fatal(err)
		}
	}
	return &WorkflowHandler{Handler: &handlers.Handler{Storage: store, Registry: registry.NewRegistry(store)}}
}

func TestValidateWorkflowRefusesASubflowChangeThatBreaksALatestCaller(t *testing.T) {
	h := usedAddress(t)
	changed := addressSubflow()
	changed.Nodes[0].Config = map[string]any{"fields": []any{
		map[string]any{"name": "zip", "required": true},
		map[string]any{"name": "country", "required": true},
	}}
	errs := strings.Join(errorsOf(h.ValidateWorkflow(t.Context(), changed)), "\n")
	if !strings.Contains(errs, `"orders"`) || !strings.Contains(errs, `"country"`) {
		t.Fatalf("errors %q, want the latest caller orders named for the unmapped country", errs)
	}
	if strings.Contains(errs, `"returns"`) {
		t.Fatalf("errors %q name returns, which pins version 1", errs)
	}

	if errs := errorsOf(h.ValidateWorkflow(t.Context(), addressSubflow())); len(errs) > 0 {
		t.Fatalf("an unchanged sub-flow was refused: %v", errs)
	}
}

func TestCallersEndpointListsWhereTheSubflowIsUsed(t *testing.T) {
	h := usedAddress(t)
	mux := http.NewServeMux()
	h.RegisterWorkflowRoutes(mux)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/workflows/address/callers", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	var body struct {
		Data []subflow.Caller `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	got := map[string]subflow.Caller{}
	for _, c := range body.Data {
		got[c.WorkflowID] = c
	}
	if len(got) != 2 || !got["orders"].Latest || got["returns"].Version != 1 || got["orders"].Step != "Clean address" {
		t.Fatalf("callers = %+v", body.Data)
	}

	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/workflows/nope/callers", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("missing workflow: status %d, want 404", rec.Code)
	}
}

// A caller can only pin a saved version, so a new sub-flow is saved as version
// 1 when it is created rather than only on its first update.
func TestCreatingASubflowSavesVersionOne(t *testing.T) {
	store := newSQLiteStore(t, "subflow-create")
	h := &WorkflowHandler{Handler: &handlers.Handler{Storage: store, LogStorage: store, Registry: registry.NewRegistry(store)}}
	mux := http.NewServeMux()
	h.RegisterWorkflowRoutes(mux)

	body, _ := json.Marshal(addressSubflow())
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/workflows", strings.NewReader(string(body))))
	if rec.Code != http.StatusCreated {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	v, err := store.GetWorkflowVersion(t.Context(), "address", 1)
	if err != nil {
		t.Fatalf("no version 1 after creating a sub-flow: %v", err)
	}
	if len(v.Nodes) != 3 {
		t.Fatalf("version 1 has %d nodes, want the 3 created", len(v.Nodes))
	}

	// Its first update is version 2.
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodPut, "/api/workflows/address", strings.NewReader(string(body))))
	if rec.Code != http.StatusOK {
		t.Fatalf("update status %d: %s", rec.Code, rec.Body)
	}
	if _, err := store.GetWorkflowVersion(t.Context(), "address", 2); err != nil {
		t.Fatalf("no version 2 after the first update: %v", err)
	}
}
