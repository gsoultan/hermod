// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package registry

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/gsoultan/hermod/internal/engine/registry/subflow"
	"github.com/gsoultan/hermod/internal/storage"
	"github.com/gsoultan/hermod/pkg/comm/message"
)

// subflowStore serves workflows and their saved versions on top of the Kafka
// fixtures, so a sub-flow can hold a sink the exactly-once guard must see.
type subflowStore struct {
	kafkaEOSStore
	workflows map[string]storage.Workflow
	versions  map[string]storage.WorkflowVersion // "id@n"
}

func (s *subflowStore) GetWorkflow(_ context.Context, id string) (storage.Workflow, error) {
	wf, ok := s.workflows[id]
	if !ok {
		return storage.Workflow{}, storage.ErrNotFound
	}
	return wf, nil
}

func (s *subflowStore) GetWorkflowVersion(_ context.Context, id string, version int) (storage.WorkflowVersion, error) {
	v, ok := s.versions[fmt.Sprintf("%s@%d", id, version)]
	if !ok {
		return storage.WorkflowVersion{}, storage.ErrNotFound
	}
	return v, nil
}

// auditSubflow takes a zip, writes it to the sink named by ref and returns it.
func auditSubflow(vhost, ref string) (storage.Workflow, storage.WorkflowVersion) {
	nodes := []storage.WorkflowNode{
		{ID: "in", Type: subflow.TypeInput, Config: map[string]any{"fields": []any{map[string]any{"name": "zip", "required": true}}}},
		sinkNode("audit", ref, ""),
		{ID: "ret", Type: subflow.TypeReturn},
	}
	edgs := append(edges("in", "audit"), edges("in", "ret")...)
	wf := storage.Workflow{ID: "audit", VHost: vhost, Nodes: nodes, Edges: edgs}
	return wf, storage.WorkflowVersion{WorkflowID: "audit", Version: 2, Nodes: nodes, Edges: edgs}
}

func subflowRegistry(vhost string) *Registry {
	r := kafkaEOSRegistry()
	sub, v2 := auditSubflow(vhost, "pg")
	r.storage = &subflowStore{
		kafkaEOSStore: *r.storage.(*kafkaEOSStore),
		workflows:     map[string]storage.Workflow{"audit": sub},
		versions:      map[string]storage.WorkflowVersion{"audit@2": v2},
	}
	return r
}

// callerWorkflow reads source, calls the audit sub-flow at version 2, and
// writes what comes back to sink.
func callerWorkflow(source, sink string) storage.Workflow {
	return storage.Workflow{ID: "orders", Nodes: []storage.WorkflowNode{
		{ID: "S", Type: "source", RefID: source},
		{ID: "C", Type: subflow.TypeCall, Config: map[string]any{
			"subflow_id": "audit", "version": float64(2),
			"inputs":  []any{map[string]any{"from": "customer.zip", "to": "zip"}},
			"outputs": []any{map[string]any{"from": "zip", "to": "checked_zip"}},
		}},
		sinkNode("K", sink, ""),
	}, Edges: append(edges("S", "C"), edges("C", "K")...)}
}

func TestKafkaExactlyOnceSeesASinkInsideASubflow(t *testing.T) {
	r := subflowRegistry("")
	wf := callerWorkflow("kin", "eos")
	for name, check := range map[string]func(context.Context, storage.Workflow) error{
		"save":  r.CheckKafkaExactlyOnce,
		"start": r.ValidateWorkflow,
	} {
		err := check(context.Background(), wf)
		if err == nil || !strings.Contains(err.Error(), "only") {
			t.Fatalf("%s: err = %v; a Postgres sink inside a sub-flow must count as a second sink", name, err)
		}
	}
}

func TestASubflowIsNotStartedByItself(t *testing.T) {
	r := subflowRegistry("")
	sub, _ := auditSubflow("", "pg")
	err := r.ValidateWorkflow(context.Background(), sub)
	if err == nil || !strings.Contains(err.Error(), "runs inside") {
		t.Fatalf("err = %v, want a sub-flow refused", err)
	}
}

func TestASubflowInAnotherVHostIsNotCalled(t *testing.T) {
	r := subflowRegistry("finance")
	wf := callerWorkflow("cdc", "pg")
	wf.VHost = "sales"
	_, err := r.ExpandSubflows(context.Background(), wf)
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("err = %v; a sub-flow from another vhost must look missing", err)
	}
}

func TestTestWorkflowRunsTheSubflowAndMapsItsResult(t *testing.T) {
	r := subflowRegistry("")
	msg := message.AcquireMessage()
	defer msg.Release()
	msg.SetID("m1")
	msg.SetData("customer", map[string]any{"zip": "10115", "email": "a@b.c"})

	steps, err := r.TestWorkflow(context.Background(), callerWorkflow("cdc", "pg"), msg)
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]WorkflowStepResult{}
	for _, s := range steps {
		byID[s.NodeID] = s
	}
	audit, ok := byID["C"+subflow.Separator+"audit"]
	if !ok {
		t.Fatalf("no step for the sink inside the sub-flow: %v", steps)
	}
	if audit.Payload["zip"] != "10115" || audit.Payload["customer"] != nil {
		t.Fatalf("sink inside the sub-flow got %v, want only its input", audit.Payload)
	}
	if got := audit.Metadata["idempotency_key"]; got != "m1:C" {
		t.Fatalf("sink inside the sub-flow keys on %q, want m1:C", got)
	}
	out := byID["K"].Payload
	cust, _ := out["customer"].(map[string]any)
	if out["checked_zip"] != "10115" || cust["email"] != "a@b.c" {
		t.Fatalf("caller's sink got %v", out)
	}
}

type subflowPipeStorage struct {
	*pipeStorage
	sub storage.Workflow
}

func (s *subflowPipeStorage) GetWorkflow(ctx context.Context, id string) (storage.Workflow, error) {
	if id == s.sub.ID {
		return s.sub, nil
	}
	return s.pipeStorage.GetWorkflow(ctx, id)
}

// A running workflow writes through a sub-flow: the sink inside it gets the
// sub-flow's inputs, the caller's sink gets the caller's row back with the
// output mapped in, and the worker still sees the workflow as it was saved —
// comparing it against the spliced graph would restart it on every sync.
func TestRunningWorkflowWritesThroughASubflow(t *testing.T) {
	const orders = 4
	sub := storage.Workflow{ID: "audit", Nodes: []storage.WorkflowNode{
		{ID: "in", Type: subflow.TypeInput, Config: map[string]any{"fields": []any{map[string]any{"name": "order", "required": true}}}},
		{ID: "log", Type: "sink", RefID: "snk-audit"},
		{ID: "ret", Type: subflow.TypeReturn},
	}, Edges: append(edges("in", "log"), edges("in", "ret")...)}
	store := &subflowPipeStorage{pipeStorage: newPipeStorage(), sub: sub}
	reg := NewRegistry(store)
	sinks := map[string]*pipeSink{"snk-audit": {name: "audit"}, "snk-out": {name: "out"}}

	wf := storage.Workflow{
		ID: "wf-subflow", Name: "wf-subflow",
		Nodes: []storage.WorkflowNode{
			{ID: "src", Type: "source", RefID: "s-orders"},
			{ID: "call", Type: subflow.TypeCall, Config: map[string]any{
				"subflow_id": "audit", "version": "latest",
				"inputs":  []any{map[string]any{"from": "order_id", "to": "order"}},
				"outputs": []any{map[string]any{"from": "order", "to": "audited"}},
			}},
			{ID: "out", Type: "sink", RefID: "snk-out"},
		},
		Edges:      append(edges("src", "call"), edges("call", "out")...),
		MaxRetries: 5, RetryInterval: "10ms",
	}

	message.ResetOverReleaseCount()
	stop := startForeachPipeline(t, reg, wf, &arraySource{count: orders}, sinks)
	defer stop()

	if !waitUntil(t, 30*time.Second, "both sinks written", func() bool {
		return sinks["snk-audit"].count() >= orders && sinks["snk-out"].count() >= orders
	}) {
		t.Fatalf("audit got %d, out got %d; want %d each", sinks["snk-audit"].count(), sinks["snk-out"].count(), orders)
	}
	for _, row := range sinks["snk-audit"].received() {
		if row["order"] == nil || row["lines"] != nil {
			t.Fatalf("sink inside the sub-flow got %v, want only its input", row)
		}
	}
	for _, row := range sinks["snk-out"].received() {
		if row["audited"] != row["order_id"] || row["lines"] == nil {
			t.Fatalf("caller's sink got %v", row)
		}
	}
	saved, ok := reg.GetWorkflowConfig(wf.ID)
	if !ok || len(saved.Nodes) != len(wf.Nodes) || saved.Nodes[1].Type != subflow.TypeCall {
		t.Fatalf("GetWorkflowConfig = %+v, want the workflow as saved", saved.Nodes)
	}
	if n := message.OverReleaseCount(); n != 0 {
		t.Fatalf("over-released %d message reference(s)", n)
	}
}
