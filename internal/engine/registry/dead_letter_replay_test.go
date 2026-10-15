// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Granted to no one is any right to use, copy, modify or distribute this
// Except by written permission of the copyright holder named above.
// May not be sold, resold, sublicensed or offered as a paid or hosted
// Business service to any third party. Proprietary and confidential;
// Its full terms are in LICENSE at the repository root. THE SOFTWARE
// IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR IMPLIED.

package registry

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"github.com/gsoultan/hermod/internal/storage"
)

// replayWorkflow is src → stamp → fix → out: stamp marks every message that
// passed it, so a test can tell whether a replay ran the nodes before the one
// that failed.
func replayWorkflow() storage.Workflow {
	return storage.Workflow{
		ID:   "wf-replay",
		Name: "wf-replay",
		Nodes: []storage.WorkflowNode{
			{ID: "src", Type: "source", RefID: "src-1"},
			{ID: "stamp", Type: "transformation", Config: map[string]any{"transType": "set", "column.stamped": "yes"}},
			{ID: "fix", Type: "transformation", Config: map[string]any{"transType": "set", "column.fixed": "yes"}},
			{ID: "out", Type: "sink", RefID: "snk-1"},
		},
		Edges: []storage.WorkflowEdge{
			{ID: "e1", SourceID: "src", TargetID: "stamp"},
			{ID: "e2", SourceID: "stamp", TargetID: "fix"},
			{ID: "e3", SourceID: "fix", TargetID: "out"},
		},
	}
}

// fieldsOf is the row a delivered message carries. A change message (one with
// an operation) reports it as its "after" image.
func fieldsOf(t *testing.T, m map[string]any) map[string]any {
	t.Helper()
	after, ok := m["after"]
	if !ok {
		return m
	}
	var row map[string]any
	if err := json.Unmarshal([]byte(fmt.Sprint(after)), &row); err != nil {
		t.Fatalf("after is not JSON: %v", err)
	}
	return row
}

func failedAtFix() storage.DeadLetter {
	return storage.DeadLetter{
		ID: "dl-1", WorkflowID: "wf-replay", MessageID: "ord-88412", NodeID: "fix",
		Error:   `field total is not a number: got "12,50"`,
		Payload: []byte(`{"id":"ord-88412","total":"12,50"}`),
		Metadata: map[string]string{
			"table": "orders", "_hermod_failed_node": "fix", "_hermod_last_error": "boom",
			"_hermod_failed_at": "2026-10-14T04:52:55Z", "_hermod_dead_lettered": "true",
		},
		Origin: storage.DeadLetterOrigin{Operation: "update", Table: "orders"},
		Status: storage.DeadLetterQueued,
	}
}

func TestReplayStartsAtTheNodeThatFailed(t *testing.T) {
	out := &pipeSink{name: "out"}
	reg := manualRunRegistry(t, out)

	res, err := reg.ReplayDeadLetter(t.Context(), replayWorkflow(), failedAtFix(), DeadLetterReplay{
		Payload: []byte(`{"id":"ord-88412","total":"12.50"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != RunCompleted {
		t.Fatalf("status = %q, want %q; steps %+v", res.Status, RunCompleted, res.Steps)
	}
	got := out.received()
	if len(got) != 1 {
		t.Fatalf("sink received %d messages, want 1", len(got))
	}
	row := fieldsOf(t, got[0])
	if row["total"] != "12.50" || row["fixed"] != "yes" {
		t.Fatalf("sink received %v, want the edited payload through fix", got[0])
	}
	if _, ran := row["stamped"]; ran {
		t.Fatal("the replay ran stamp, which the message had already passed")
	}
}

func TestReplayFromTheStartRunsEveryNode(t *testing.T) {
	out := &pipeSink{name: "out"}
	reg := manualRunRegistry(t, out)

	res, err := reg.ReplayDeadLetter(t.Context(), replayWorkflow(), failedAtFix(), DeadLetterReplay{FromStart: true})
	if err != nil || res.Status != RunCompleted {
		t.Fatalf("result %+v, err %v", res, err)
	}
	got := out.received()
	if len(got) != 1 || fieldsOf(t, got[0])["stamped"] != "yes" || fieldsOf(t, got[0])["fixed"] != "yes" || fieldsOf(t, got[0])["total"] != "12,50" {
		t.Fatalf("sink received %v", got)
	}
}

// A sink that refused the message gets it again, without the nodes before it
// running a second time.
func TestReplayOfARefusedWriteGoesStraightToTheSink(t *testing.T) {
	out := &pipeSink{name: "out"}
	reg := manualRunRegistry(t, out)
	dl := failedAtFix()
	dl.NodeID, dl.SinkID = "", "snk-1"

	res, err := reg.ReplayDeadLetter(t.Context(), replayWorkflow(), dl, DeadLetterReplay{})
	if err != nil || res.Status != RunCompleted {
		t.Fatalf("result %+v, err %v", res, err)
	}
	got := out.received()
	if len(got) != 1 || fieldsOf(t, got[0])["id"] != "ord-88412" {
		t.Fatalf("sink received %v", got)
	}
	if _, ran := fieldsOf(t, got[0])["fixed"]; ran {
		t.Fatal("the replay ran fix again before the sink")
	}
}

// The failure marks would tell every node downstream that the message had
// already been dead-lettered.
func TestReplayClearsTheFailureMarks(t *testing.T) {
	msg, err := deadLetterMessage(failedAtFix(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer msg.Release()
	meta := msg.Metadata()
	for _, k := range []string{"_hermod_failed_node", "_hermod_last_error", "_hermod_failed_at", "_hermod_dead_lettered"} {
		if _, ok := meta[k]; ok {
			t.Errorf("the replayed message still carries %s", k)
		}
	}
	if meta["table"] != "orders" || msg.ID() != "ord-88412" || msg.Table() != "orders" || string(msg.Operation()) != "update" {
		t.Fatalf("the replayed message lost what it was: id %q meta %v", msg.ID(), meta)
	}
}

func TestReplayRefusesWhatItCannotReplay(t *testing.T) {
	reg := manualRunRegistry(t, &pipeSink{name: "out"})
	ctx := context.Background()

	cut := failedAtFix()
	cut.Truncated = true
	if _, err := reg.ReplayDeadLetter(ctx, replayWorkflow(), cut, DeadLetterReplay{}); !errors.Is(err, ErrDeadLetterTruncated) {
		t.Errorf("a truncated payload: err = %v, want ErrDeadLetterTruncated", err)
	}

	gone := failedAtFix()
	gone.NodeID = "deleted-node"
	if _, err := reg.ReplayDeadLetter(ctx, replayWorkflow(), gone, DeadLetterReplay{}); !errors.Is(err, ErrDeadLetterNodeGone) {
		t.Errorf("a node no longer in the workflow: err = %v, want ErrDeadLetterNodeGone", err)
	}

	bad := failedAtFix()
	if _, err := reg.ReplayDeadLetter(ctx, replayWorkflow(), bad, DeadLetterReplay{Payload: []byte(`{not json`)}); err == nil {
		t.Error("an edited payload that is not JSON was replayed")
	}
}

// Test first shows what each node would produce from the failed node on, and
// writes nothing.
func TestReplayPreviewWritesNothing(t *testing.T) {
	out := &pipeSink{name: "out"}
	reg := manualRunRegistry(t, out)

	steps, err := reg.PreviewDeadLetterReplay(t.Context(), replayWorkflow(), failedAtFix(), DeadLetterReplay{
		Payload: []byte(`{"id":"ord-88412","total":"12.50"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if out.count() != 0 {
		t.Fatal("the preview wrote to the sink")
	}
	var fix *WorkflowStepResult
	for i := range steps {
		if steps[i].NodeID == "stamp" && !steps[i].Skipped {
			t.Fatalf("the preview ran stamp, before the failed node: %+v", steps[i])
		}
		if steps[i].NodeID == "fix" {
			fix = &steps[i]
		}
	}
	if fix == nil || fieldsOf(t, fix.Payload)["fixed"] != "yes" || fieldsOf(t, fix.Payload)["total"] != "12.50" {
		t.Fatalf("fix step = %+v; all %+v", fix, steps)
	}
}
