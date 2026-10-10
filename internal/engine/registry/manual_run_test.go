package registry

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/internal/factory"
	"github.com/gsoultan/hermod/internal/storage"
	"github.com/gsoultan/hermod/pkg/comm/message"
)

// manualRunRegistry is a registry over a real SQL store (so traces are
// written and read back the way the API reads them) whose sink snk-1 is the
// given fixture.
func manualRunRegistry(t *testing.T, out *pipeSink) *Registry {
	t.Helper()
	reg := newSimRegistry(t)
	t.Cleanup(reg.Close)
	reg.SetFactories(nil, func(cfg factory.SinkConfig) (hermod.Sink, error) {
		if cfg.ID == "snk-1" {
			return out, nil
		}
		return nil, fmt.Errorf("no sink fixture for %q", cfg.ID)
	})
	return reg
}

func manualWorkflow(middle storage.WorkflowNode) storage.Workflow {
	return storage.Workflow{
		ID:   "wf-manual",
		Name: "wf-manual",
		Nodes: []storage.WorkflowNode{
			{ID: "src", Type: "source", RefID: "src-1"},
			middle,
			{ID: "out", Type: "sink", RefID: "snk-1"},
		},
		Edges: []storage.WorkflowEdge{
			{ID: "e1", SourceID: "src", TargetID: middle.ID},
			{ID: "e2", SourceID: middle.ID, TargetID: "out"},
		},
	}
}

func inputMessage(data map[string]any) hermod.Message {
	m := message.AcquireMessage()
	for k, v := range data {
		m.SetData(k, v)
	}
	return m
}

// Run with input sends the user's payload through the workflow once,
// delivers it, and leaves a trace under the returned run id with a step for
// every node, whatever the workflow's trace sample rate.
func TestRunWorkflowOnceDeliversAndTraces(t *testing.T) {
	out := &pipeSink{name: "out"}
	reg := manualRunRegistry(t, out)
	wf := manualWorkflow(storage.WorkflowNode{ID: "greet", Type: "transformation", Config: map[string]any{
		"transType": "set", "column.greeting": "hello",
	}})

	in := inputMessage(map[string]any{"name": "Ada"})
	defer in.Release()
	res, err := reg.RunWorkflowOnce(t.Context(), wf, in, "")
	if err != nil {
		t.Fatal(err)
	}
	if res.RunID == "" || res.Status != RunCompleted {
		t.Fatalf("result = %+v, want a run id and status %q", res, RunCompleted)
	}
	got := out.received()
	if len(got) != 1 || got[0]["greeting"] != "hello" || got[0]["name"] != "Ada" {
		t.Fatalf("sink received %v", got)
	}

	trace, err := reg.GetStorage().GetMessageTrace(t.Context(), wf.ID, res.RunID)
	if err != nil {
		t.Fatalf("no trace under the run id: %v", err)
	}
	seen := map[string]bool{}
	for _, s := range trace.Steps {
		seen[s.NodeID] = true
	}
	for _, id := range []string{"src", "greet", "out"} {
		if !seen[id] {
			t.Errorf("trace has no step for node %s; steps = %+v", id, trace.Steps)
		}
	}
}

func TestRunWorkflowOnceReportsAFailedNode(t *testing.T) {
	out := &pipeSink{name: "out"}
	reg := manualRunRegistry(t, out)
	wf := manualWorkflow(storage.WorkflowNode{ID: "broken", Type: "transformation", Config: map[string]any{
		"transType": "no_such_transformation",
	}})

	in := inputMessage(map[string]any{"x": 1})
	defer in.Release()
	res, err := reg.RunWorkflowOnce(t.Context(), wf, in, "")
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != RunFailed {
		t.Fatalf("status = %q, want %q", res.Status, RunFailed)
	}
	if out.count() != 0 {
		t.Fatal("a message whose node failed reached the sink")
	}
	var stepErr string
	for _, s := range res.Steps {
		if s.NodeID == "broken" {
			stepErr = s.Error
		}
	}
	if stepErr == "" {
		t.Fatalf("the failing node's step carries no error: %+v", res.Steps)
	}
}

func TestRunWorkflowOnceStopsAtAnApproval(t *testing.T) {
	out := &pipeSink{name: "out"}
	reg := manualRunRegistry(t, out)
	wf := manualWorkflow(storage.WorkflowNode{ID: "approve", Type: "approval", Config: map[string]any{}})

	in := inputMessage(map[string]any{"amount": 10})
	defer in.Release()
	res, err := reg.RunWorkflowOnce(t.Context(), wf, in, "")
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != RunWaiting {
		t.Fatalf("status = %q, want %q", res.Status, RunWaiting)
	}
	if out.count() != 0 {
		t.Fatal("the message went past an approval nobody gave")
	}
}

func TestRunWorkflowOnceRejectsAnUnknownSourceNode(t *testing.T) {
	reg := manualRunRegistry(t, &pipeSink{name: "out"})
	wf := manualWorkflow(storage.WorkflowNode{ID: "greet", Type: "transformation", Config: map[string]any{"transType": "set"}})
	in := inputMessage(nil)
	defer in.Release()
	if _, err := reg.RunWorkflowOnce(t.Context(), wf, in, "greet"); err == nil {
		t.Fatal("a run must start at a source node of the workflow")
	}
}

// The approval path resumed a workflow with every node on
// context.Background(): ResumeApproval took a context and ignored it, so a
// hung lookup after an approval could not be cancelled.
func TestResumeApprovalHonoursItsContext(t *testing.T) {
	ep := newHangingEndpoint(t)
	out := &pipeSink{name: "out"}
	reg := manualRunRegistry(t, out)

	wf := storage.Workflow{
		ID: "wf-resume-ctx", Name: "wf-resume-ctx",
		Nodes: []storage.WorkflowNode{
			{ID: "src", Type: "source", RefID: "src-1"},
			{ID: "approve", Type: "approval"},
			apiLookupNode(ep.srv.URL),
			{ID: "out", Type: "sink", RefID: "snk-1"},
		},
		Edges: []storage.WorkflowEdge{
			{ID: "e1", SourceID: "src", TargetID: "approve"},
			{ID: "e2", SourceID: "approve", TargetID: "enrich"},
			{ID: "e3", SourceID: "enrich", TargetID: "out"},
		},
	}
	if err := reg.GetStorage().CreateWorkflow(t.Context(), wf); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() {
		done <- reg.ResumeApproval(ctx, storage.Approval{
			WorkflowID: wf.ID, NodeID: "approve", MessageID: "m-resume",
			Data: map[string]any{"customer_code": "C-1"},
		}, "approved")
	}()

	select {
	case <-ep.arrived:
	case <-time.After(10 * time.Second):
		t.Fatal("the resumed workflow never reached its lookup")
	}
	cancel()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("ResumeApproval was still running 5s after its context was cancelled; " +
			"the resumed nodes do not run on the caller's context")
	}
}
