package executions

import (
	"testing"
	"time"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/internal/storage"
	"github.com/gsoultan/hermod/pkg/comm/transformer/genai"
)

// supportFlow is src -> classify -> {draft (AI) | archive}; draft -> out.
func supportFlow() storage.Workflow {
	return storage.Workflow{
		ID: "wf",
		Nodes: []storage.WorkflowNode{
			{ID: "src", Type: "source"},
			{ID: "classify", Type: "ai_classify", Config: map[string]any{"label": "Classify"}},
			{ID: "draft", Type: "transformation", Config: map[string]any{"transType": "ai_prompt"}},
			{ID: "approve", Type: "approval"},
			{ID: "out", Type: "sink"},
		},
		Edges: []storage.WorkflowEdge{
			{SourceID: "src", TargetID: "classify"},
			{SourceID: "classify", TargetID: "draft"},
			{SourceID: "draft", TargetID: "approve"},
			{SourceID: "approve", TargetID: "out"},
		},
	}
}

func usageMeta(calls, in, out int) map[string]any {
	return map[string]any{"metadata": map[string]any{
		genai.MetaAICalls:        itoa(calls),
		genai.MetaAIInputTokens:  itoa(in),
		genai.MetaAIOutputTokens: itoa(out),
	}}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	s := ""
	for n > 0 {
		s = string(rune('0'+n%10)) + s
		n /= 10
	}
	return s
}

func TestFromTraceComputesStatusDurationAndTokens(t *testing.T) {
	t0 := time.Date(2026, 10, 10, 9, 0, 0, 0, time.UTC)
	trace := storage.MessageTrace{
		MessageID: "run-1", WorkflowID: "wf",
		Steps: []hermod.TraceStep{
			{NodeID: "src", Timestamp: t0, After: map[string]any{"subject": "refund please"}},
			{NodeID: "classify", Timestamp: t0.Add(2 * time.Second), Duration: 2 * time.Second, After: usageMeta(1, 100, 10)},
			{NodeID: "draft", Timestamp: t0.Add(5 * time.Second), Duration: 3 * time.Second, After: usageMeta(2, 400, 210)},
			{NodeID: "approve", Timestamp: t0.Add(5100 * time.Millisecond), Duration: 100 * time.Millisecond, After: usageMeta(2, 400, 210)},
		},
	}
	ex := FromTrace(supportFlow(), trace, false)

	if ex.RunID != "run-1" || ex.Status != StatusWaiting {
		t.Fatalf("run %q status %q, want run-1 waiting (stopped at an approval)", ex.RunID, ex.Status)
	}
	if !ex.StartedAt.Equal(t0) || ex.DurationMs != 5100 {
		t.Fatalf("started %v duration %dms, want %v and 5100", ex.StartedAt, ex.DurationMs, t0)
	}
	if ex.AI != (genai.Usage{Calls: 2, InputTokens: 400, OutputTokens: 210}) {
		t.Fatalf("run AI usage = %+v", ex.AI)
	}
	if len(ex.Steps) != 4 {
		t.Fatalf("steps = %d", len(ex.Steps))
	}
	if s := ex.Steps[1]; s.NodeType != "ai_classify" || s.Label != "Classify" || s.AI.InputTokens != 100 {
		t.Fatalf("classify step = %+v", s)
	}
	if s := ex.Steps[2]; s.NodeType != "ai_prompt" || s.AI != (genai.Usage{Calls: 1, InputTokens: 300, OutputTokens: 200}) {
		t.Fatalf("draft step = %+v, want only its own call", s)
	}
	if s := ex.Steps[3]; s.AI != (genai.Usage{}) {
		t.Fatalf("approval step = %+v, want no AI usage of its own", s)
	}
	if ex.Steps[0].Output != nil {
		t.Fatal("outputs are left out unless asked for")
	}
}

func TestFromTraceFailedAndSucceeded(t *testing.T) {
	t0 := time.Now()
	failed := FromTrace(supportFlow(), storage.MessageTrace{MessageID: "r", Steps: []hermod.TraceStep{
		{NodeID: "src", Timestamp: t0},
		{NodeID: "classify", Timestamp: t0, Error: "ai_classify: 429"},
	}}, true)
	if failed.Status != StatusFailed || failed.ErrorCount != 1 || failed.Steps[1].Error == "" {
		t.Fatalf("failed run = %+v", failed)
	}

	ok := FromTrace(supportFlow(), storage.MessageTrace{MessageID: "r", Steps: []hermod.TraceStep{
		{NodeID: "src", Timestamp: t0, After: map[string]any{"a": 1}},
		{NodeID: "out", Timestamp: t0},
	}}, true)
	if ok.Status != StatusSucceeded || ok.Steps[0].Output["a"] != 1 {
		t.Fatalf("succeeded run = %+v", ok)
	}
}

// Fan-out: two branches each make a call after a shared one. Each step's
// usage is measured against what its own predecessor carried, so the shared
// call is counted once and both branch calls are counted.
func TestFromTraceTokensAcrossBranches(t *testing.T) {
	wf := storage.Workflow{
		Nodes: []storage.WorkflowNode{{ID: "src", Type: "source"}, {ID: "ai0"}, {ID: "a"}, {ID: "b"}, {ID: "x"}},
		Edges: []storage.WorkflowEdge{
			{SourceID: "src", TargetID: "ai0"}, {SourceID: "ai0", TargetID: "a"}, {SourceID: "ai0", TargetID: "x"}, {SourceID: "x", TargetID: "b"},
		},
	}
	t0 := time.Now()
	ex := FromTrace(wf, storage.MessageTrace{Steps: []hermod.TraceStep{
		{NodeID: "src", Timestamp: t0},
		{NodeID: "ai0", Timestamp: t0.Add(1), After: usageMeta(1, 10, 1)},
		{NodeID: "a", Timestamp: t0.Add(2), After: usageMeta(2, 15, 2)},
		{NodeID: "x", Timestamp: t0.Add(3), After: usageMeta(1, 10, 1)},
		{NodeID: "b", Timestamp: t0.Add(4), After: usageMeta(2, 13, 2)},
	}}, false)
	if ex.AI != (genai.Usage{Calls: 3, InputTokens: 18, OutputTokens: 3}) {
		t.Fatalf("AI = %+v, want 3 calls, 18 in, 3 out", ex.AI)
	}
}

func TestInputIsTheSourceStepWithoutEngineMetadata(t *testing.T) {
	trace := storage.MessageTrace{Steps: []hermod.TraceStep{
		{NodeID: "src", Lineage: "source_ingest", After: map[string]any{
			"subject": "hi",
			"metadata": map[string]any{
				"webhook_path": "/api/webhooks/support", genai.MetaAIInputTokens: "9", "_hermod_reply_id": "r-1",
			},
		}},
		{NodeID: "classify", After: map[string]any{"subject": "hi", "label": "bug"}},
	}}
	in, source, ok := Input(supportFlow(), trace)
	if !ok || source != "src" {
		t.Fatalf("input not found: ok=%v source=%q", ok, source)
	}
	if in["subject"] != "hi" || in["label"] != nil {
		t.Fatalf("input = %v, want the source step's payload", in)
	}
	md, _ := in["metadata"].(map[string]any)
	if md["webhook_path"] == nil || md[genai.MetaAIInputTokens] != nil || md["_hermod_reply_id"] != nil {
		t.Fatalf("metadata = %v: engine bookkeeping must not be replayed, the rest must", md)
	}
}
