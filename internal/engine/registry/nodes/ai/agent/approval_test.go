package agent

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/internal/engine/registry/interfaces"
	"github.com/gsoultan/hermod/internal/storage"
	"github.com/gsoultan/hermod/pkg/comm/message"
	"github.com/gsoultan/hermod/pkg/llm"
)

// suspended runs the agent until it asks to approve create_ticket and returns
// the approval it recorded, round-tripped through JSON as storage would.
func suspended(t *testing.T, p *scriptedProvider, nctx *fakeNodeContext, node *storage.WorkflowNode) storage.Approval {
	t.Helper()
	_, branch, err := newNode(p).Execute(t.Context(), nctx, "wf", node, inputMessage(t, map[string]any{"text": "refund please"}))
	if err != nil || branch != "pending" {
		t.Fatalf("branch=%q err=%v", branch, err)
	}
	apps := nctx.store.list()
	if len(apps) != 1 {
		t.Fatalf("approvals = %d", len(apps))
	}
	raw, err := json.Marshal(apps[0])
	if err != nil {
		t.Fatal(err)
	}
	var app storage.Approval
	if err := json.Unmarshal(raw, &app); err != nil {
		t.Fatal(err)
	}
	return app
}

// resumedMessage rebuilds the message from an approval the way
// Registry.ResumeApproval does.
func resumedMessage(t *testing.T, app storage.Approval) hermod.Message {
	t.Helper()
	m := message.AcquireMessage()
	m.SetID(app.MessageID)
	for k, v := range app.Data {
		m.SetData(k, v)
	}
	t.Cleanup(m.Release)
	return m
}

func TestResume_ApprovedWriteRunsOnceAndTheLoopContinues(t *testing.T) {
	p := &scriptedProvider{turns: []func(llm.ChatRequest) llm.ChatResponse{
		call("w1", "create_ticket", `{"subject":"refund"}`),
		say("Ticket opened."),
	}}
	nctx := newFakeNodeContext()
	node := agentNode(nil)
	app := suspended(t, p, nctx, node)

	var exec interfaces.ApprovalResumer = newNode(p)
	msg := resumedMessage(t, app)
	out, branch, err := exec.ResumeApproval(t.Context(), nctx, "wf", node, msg, app, "approved")
	if err != nil {
		t.Fatal(err)
	}
	w := nctx.sinks["ticket-sink"].writes()
	if len(w) != 1 || w[0]["subject"] != "refund" {
		t.Fatalf("writes = %v", w)
	}
	if branch != "" || len(out) != 1 || out[0].Data()[DefaultTargetField] != "Ticket opened." {
		t.Fatalf("branch=%q out=%v", branch, out)
	}
	if _, ok := out[0].Data()[stateField]; ok {
		t.Fatal("the agent's resume state leaked into the output message")
	}
	// The model's second turn saw the whole conversation, ending with the result.
	reqs := p.requests()
	if len(reqs[1].Messages) != 3 {
		t.Fatalf("resumed conversation = %+v", reqs[1].Messages)
	}
	if res := lastToolResults(p); len(res) != 1 || res[0].CallID != "w1" || res[0].IsError {
		t.Fatalf("tool results = %+v", res)
	}
	raw, _ := json.Marshal(out[0].Data()[DefaultTranscriptField])
	for _, want := range []string{`"kind":"approval_requested"`, `"kind":"approval_decision"`, `"text":"approved"`} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("transcript misses %s: %s", want, raw)
		}
	}
}

func TestResume_RejectedWriteDoesNotRunAndTheModelIsTold(t *testing.T) {
	p := &scriptedProvider{turns: []func(llm.ChatRequest) llm.ChatResponse{
		call("w1", "create_ticket", `{"subject":"refund"}`),
		say("I could not open a ticket."),
	}}
	nctx := newFakeNodeContext()
	node := agentNode(nil)
	app := suspended(t, p, nctx, node)
	app.Notes = "not for this customer"

	out, _, err := newNode(p).ResumeApproval(t.Context(), nctx, "wf", node, resumedMessage(t, app), app, "rejected")
	if err != nil {
		t.Fatal(err)
	}
	if w := nctx.sinks["ticket-sink"].writes(); len(w) != 0 {
		t.Fatalf("a rejected call wrote: %v", w)
	}
	res := lastToolResults(p)
	if len(res) != 1 || !res[0].IsError || !strings.Contains(res[0].Content, "rejected") {
		t.Fatalf("tool results = %+v", res)
	}
	if out[0].Data()[DefaultTargetField] != "I could not open a ticket." {
		t.Fatalf("answer = %v", out[0].Data()[DefaultTargetField])
	}
}

func TestResume_ToolNoLongerAllowedIsNotRunEvenWhenApproved(t *testing.T) {
	p := &scriptedProvider{turns: []func(llm.ChatRequest) llm.ChatResponse{
		call("w1", "create_ticket", `{"subject":"refund"}`),
		say("ok"),
	}}
	nctx := newFakeNodeContext()
	app := suspended(t, p, nctx, agentNode(nil))

	// The workflow was edited while the approval waited: create_ticket is gone.
	edited := agentNode(map[string]any{"tools": []any{map[string]any{
		"name": "find_customer", "kind": "db_lookup", "config": map[string]any{"sourceId": "crm"},
	}}})
	if _, _, err := newNode(p).ResumeApproval(t.Context(), nctx, "wf", edited, resumedMessage(t, app), app, "approved"); err != nil {
		t.Fatal(err)
	}
	if w := nctx.sinks["ticket-sink"].writes(); len(w) != 0 {
		t.Fatalf("a tool removed from the allow-list ran: %v", w)
	}
	if res := lastToolResults(p); len(res) != 1 || !res[0].IsError {
		t.Fatalf("tool results = %+v", res)
	}
}

func TestResume_ApprovalWithoutAgentStateIsRefused(t *testing.T) {
	p := &scriptedProvider{}
	nctx := newFakeNodeContext()
	app := storage.Approval{ID: "a", NodeID: "agent-1", MessageID: "m", Data: map[string]any{"x": 1}}
	_, _, err := newNode(p).ResumeApproval(t.Context(), nctx, "wf", agentNode(nil), resumedMessage(t, app), app, "approved")
	if err == nil {
		t.Fatal("want an error")
	}
	if len(p.requests()) != 0 || len(nctx.sinks["ticket-sink"].writes()) != 0 {
		t.Fatal("nothing may run without the agent's own state")
	}
}

func TestResume_StepsAndTokensCarryAcrossTheApproval(t *testing.T) {
	p := &scriptedProvider{turns: []func(llm.ChatRequest) llm.ChatResponse{
		call("w1", "create_ticket", `{"subject":"refund"}`),
		call("w2", "create_ticket", `{"subject":"again"}`),
	}}
	nctx := newFakeNodeContext()
	node := agentNode(map[string]any{"maxSteps": "2"})
	app := suspended(t, p, nctx, node)

	_, _, err := newNode(p).ResumeApproval(t.Context(), nctx, "wf", node, resumedMessage(t, app), app, "approved")
	if err == nil || !strings.Contains(err.Error(), "step") {
		t.Fatalf("err = %v, want the step limit", err)
	}
	if len(p.requests()) != 2 {
		t.Fatalf("the model was called %d times with a two-step limit", len(p.requests()))
	}
	// The approved call ran; the one asked for on the last step did not.
	if w := nctx.sinks["ticket-sink"].writes(); len(w) != 1 || w[0]["subject"] != "refund" {
		t.Fatalf("writes = %v", w)
	}
}
