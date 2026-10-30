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
	"github.com/gsoultan/hermod/internal/storage"
)

// Saving goes through this validator, so a Kafka-to-Kafka exactly-once
// workflow with a second sink, which the engine refuses to start, is refused
// here first and the editor shows why.
func TestValidateWorkflowRefusesKafkaExactlyOnceWithASecondSink(t *testing.T) {
	store := newSQLiteStore(t, "kafka-eos")
	ctx := t.Context()
	if err := store.CreateSource(ctx, storage.Source{ID: "kin", Name: "orders in", Type: "kafka",
		Config: map[string]string{"brokers": "127.0.0.1:1", "topic": "in", "group_id": "g"}}); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateSink(ctx, storage.Sink{ID: "kout", Name: "orders out", Type: "kafka",
		Config: map[string]string{"brokers": "127.0.0.1:1", "topic": "out", "transactional_id": "hermod-orders"}}); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateSink(ctx, storage.Sink{ID: "pg", Name: "orders db", Type: "postgres",
		Config: map[string]string{"conn": "postgres://u:p@127.0.0.1:1/d", "table": "t"}}); err != nil {
		t.Fatal(err)
	}
	h := &WorkflowHandler{Handler: &handlers.Handler{Storage: store, Registry: registry.NewRegistry(store)}}

	wf := func(sinks ...string) storage.Workflow {
		w := storage.Workflow{Name: "orders", Nodes: []storage.WorkflowNode{{ID: "src", Type: "source", RefID: "kin"}}}
		for _, ref := range sinks {
			w.Nodes = append(w.Nodes, storage.WorkflowNode{ID: ref, Type: "sink", RefID: ref, Config: map[string]any{"label": ref}})
			w.Edges = append(w.Edges, storage.WorkflowEdge{ID: "e-" + ref, SourceID: "src", TargetID: ref})
		}
		return w
	}

	var found *ValidationIssue
	for _, issue := range h.ValidateWorkflow(context.Background(), wf("kout", "pg")) {
		if strings.Contains(issue.Message, "hermod-orders") {
			found = &issue
			break
		}
	}
	if found == nil {
		t.Fatal("a transactional Kafka sink next to a second sink was not reported")
	}
	if found.Severity != "error" {
		t.Errorf("severity = %q, want error: the workflow could not start", found.Severity)
	}
	if found.Recommendation == "" {
		t.Error("the issue gives no recommendation")
	}

	for _, issue := range h.ValidateWorkflow(context.Background(), wf("kout")) {
		if strings.Contains(issue.Message, "hermod-orders") {
			t.Errorf("Kafka as the only sink was reported: %s", issue.Message)
		}
	}
}
