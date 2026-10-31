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
	"database/sql"
	"errors"
	"strings"
	"testing"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/internal/engine/registry/interfaces"
	"github.com/gsoultan/hermod/internal/storage"
	"github.com/gsoultan/hermod/pkg/comm/message"
)

// failButEmit fails and still returns the message it was given.
type failButEmit struct{}

func (failButEmit) Execute(_ context.Context, _ interfaces.NodeContext, _ string, _ *storage.WorkflowNode, msg hermod.Message) ([]hermod.Message, string, error) {
	msg.Retain()
	return []hermod.Message{msg}, "", errors.New("this node always fails")
}

func init() {
	interfaces.RegisterNodeExecutor("test_fail_but_emit", failButEmit{})
}

// ---------------------------------------------------------------------------
// A preview that fails after it wrote.
//
// Simulate and Run Preview run an Execute SQL node's write in a transaction
// that is rolled back when the run ends. The run does not always end well: a
// node after the write can fail. Then the write must still be rolled back, the
// connection the sandbox held must go back to the pool, the failure must be
// reported on the node that failed, and nothing after it may run as if it had
// not. A second preview straight after must be able to make the same write.
// ---------------------------------------------------------------------------

// insertThenFailThenLookup writes a row, then runs a statement that cannot
// succeed, then reads the row back.
func insertThenFailThenLookup() []storage.Transformation {
	return []storage.Transformation{
		{Type: "execute_sql", Config: map[string]any{
			"sourceId":      "orders-db",
			"queryTemplate": `INSERT INTO orders (code, status) VALUES ({{.code}}, 'new') RETURNING id, code, status`,
			"resultField":   "inserted",
		}},
		{Type: "execute_sql", Config: map[string]any{
			"sourceId":      "orders-db",
			"queryTemplate": `INSERT INTO no_such_table (code) VALUES ({{.code}})`,
		}},
		{Type: "db_lookup", Config: map[string]any{
			"sourceId":      "orders-db",
			"mode":          "query",
			"queryTemplate": `SELECT status FROM orders WHERE code = {{.code}}`,
			"valueColumn":   "status",
			"targetField":   "status_after_insert",
		}},
	}
}

func chainWorkflow(id string, trans []storage.Transformation) storage.Workflow {
	wf := storage.Workflow{ID: id, Nodes: []storage.WorkflowNode{{ID: "src", Type: "source", RefID: "src-1"}}}
	prev := "src"
	for i, tr := range trans {
		cfg := map[string]any{"transType": tr.Type}
		for k, v := range tr.Config {
			cfg[k] = v
		}
		nodeID := "t" + string(rune('1'+i))
		wf.Nodes = append(wf.Nodes, storage.WorkflowNode{ID: nodeID, Type: "transformation", Config: cfg})
		wf.Edges = append(wf.Edges, storage.WorkflowEdge{ID: "e" + nodeID, SourceID: prev, TargetID: nodeID})
		prev = nodeID
	}
	return wf
}

func assertNoConnectionHeld(t *testing.T, db *sql.DB) {
	t.Helper()
	if st := db.Stats(); st.InUse != 0 {
		t.Errorf("the preview still holds %d connection(s) after it ended; the sandbox must return its connection", st.InUse)
	}
}

func stepByNode(steps []WorkflowStepResult, nodeID string) []WorkflowStepResult {
	var out []WorkflowStepResult
	for _, s := range steps {
		if s.NodeID == nodeID {
			out = append(out, s)
		}
	}
	return out
}

func TestSimulationRollsBackSQLWritesWhenALaterNodeFails(t *testing.T) {
	reg, db := newSandboxRegistry(t)
	wf := chainWorkflow("sandbox-fail-wf", insertThenFailThenLookup())

	run := func() []WorkflowStepResult {
		msg := message.AcquireMessage()
		defer msg.Release()
		msg.SetAfter([]byte(`{"code":"C-1"}`))
		steps, err := reg.SimulateWorkflow(t.Context(), wf, SimulationInput{Message: msg, Partial: true})
		if err != nil {
			t.Fatalf("SimulateWorkflow: %v", err)
		}
		return steps
	}

	steps := run()

	// The write before the failure happened inside the run.
	t1 := stepByNode(steps, "t1")
	if len(t1) != 1 || t1[0].Error != "" || t1[0].Payload["inserted"] == nil {
		t.Fatalf("the insert before the failure did not run cleanly: %+v", t1)
	}
	// The failure is reported on the node that failed, with the database's
	// own reason, so the editor can show it on that node.
	t2 := stepByNode(steps, "t2")
	if len(t2) == 0 || t2[0].Error == "" {
		t.Fatalf("the failing node reported no error: %+v", t2)
	}
	if !strings.Contains(t2[0].Error, "no_such_table") {
		t.Errorf("the error does not name what failed: %q", t2[0].Error)
	}
	// Nothing after the failure ran: the lookup is reported skipped, not as a
	// lookup that found (or did not find) the row.
	t3 := stepByNode(steps, "t3")
	if len(t3) != 1 || !t3[0].Skipped {
		t.Errorf("the node after the failure was not reported skipped: %+v", t3)
	}

	assertTableUntouched(t, db)
	assertNoConnectionHeld(t, db)

	// The same preview again: the UNIQUE code must still be free, which it is
	// only if the failed run rolled its insert back.
	again := run()
	if t1 := stepByNode(again, "t1"); len(t1) != 1 || t1[0].Error != "" {
		t.Errorf("the insert failed on the second preview; the first one's write was not rolled back: %+v", t1)
	}
	assertTableUntouched(t, db)
	assertNoConnectionHeld(t, db)
}

func TestRunPreviewRollsBackSQLWritesWhenALaterStepFails(t *testing.T) {
	reg, db := newSandboxRegistry(t)

	run := func() error {
		msg := message.AcquireMessage()
		defer msg.Release()
		msg.SetAfter([]byte(`{"code":"C-1"}`))
		msg.SetPayload([]byte(`{"code":"C-1"}`))
		results, err := reg.TestTransformationPipeline(t.Context(), insertThenFailThenLookup(), msg)
		for _, r := range results {
			if r != nil {
				r.Release()
			}
		}
		return err
	}

	err := run()
	if err == nil {
		t.Fatal("a pipeline whose second step cannot run reported success")
	}
	if !strings.Contains(err.Error(), "no_such_table") {
		t.Errorf("the error does not name what failed: %v", err)
	}
	assertTableUntouched(t, db)
	assertNoConnectionHeld(t, db)

	// And again, on the row the failed run must have rolled back.
	if err := run(); err == nil || !strings.Contains(err.Error(), "no_such_table") {
		t.Errorf("second preview: %v, want the same failure on the second step, not one on the insert", err)
	}
	assertTableUntouched(t, db)
	assertNoConnectionHeld(t, db)
}

// A node that fails and still hands back a message -- some executors return
// the input with the error so a branch has something to carry -- sends
// nothing on. The live traversal prunes every edge out of a failed node
// (handleResults); the simulation forwarded whatever came back, so the
// preview showed the nodes after a failure running on its output.
func TestSimulationSendsNothingPastANodeThatFailed(t *testing.T) {
	reg := newSimRegistry(t)
	wf := storage.Workflow{
		ID: "fail-emit",
		Nodes: []storage.WorkflowNode{
			{ID: "src", Type: "source", RefID: "src-1"},
			{ID: "bad", Type: "test_fail_but_emit"},
			{ID: "after", Type: "transformation", Config: map[string]any{"transType": "set", "fields": `{"ran":"yes"}`}},
		},
		Edges: []storage.WorkflowEdge{
			{ID: "e1", SourceID: "src", TargetID: "bad"},
			{ID: "e2", SourceID: "bad", TargetID: "after"},
		},
	}
	msg := message.AcquireMessage()
	defer msg.Release()
	msg.SetAfter([]byte(`{"id":1}`))

	steps, err := reg.SimulateWorkflow(t.Context(), wf, SimulationInput{Message: msg, Partial: true})
	if err != nil {
		t.Fatalf("SimulateWorkflow: %v", err)
	}
	bad := stepByNode(steps, "bad")
	if len(bad) == 0 || bad[0].Error == "" {
		t.Fatalf("the failing node reported no error: %+v", bad)
	}
	if len(bad[0].TakenEdges) != 0 {
		t.Errorf("a failed node's output travelled along %v; the workflow sends nothing past a failure", bad[0].TakenEdges)
	}
	after := stepByNode(steps, "after")
	if len(after) != 1 || !after[0].Skipped {
		t.Errorf("the node after the failure ran: %+v", after)
	}
}
