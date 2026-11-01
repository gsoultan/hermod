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
