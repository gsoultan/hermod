package ai

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gsoultan/hermod/internal/engine/registry/interfaces"
	"github.com/gsoultan/hermod/internal/storage"
	"github.com/gsoultan/hermod/pkg/comm/message"
	"github.com/gsoultan/hermod/pkg/comm/transformer/genai"
	"github.com/gsoultan/hermod/pkg/llm"
)

// nullCtx satisfies NodeContext; the classify node uses none of it.
type nullCtx struct{ interfaces.NodeContext }

func llmServer(t *testing.T, content string) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []any{map[string]any{"message": map[string]any{"content": content}, "finish_reason": "stop"}},
		})
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

func TestClassifyNode_RoutesOnLabel(t *testing.T) {
	exec, ok := interfaces.GetNodeExecutor("ai_classify")
	if !ok {
		t.Fatal("ai_classify is not registered")
	}
	node := &storage.WorkflowNode{ID: "n1", Type: "ai_classify", Config: map[string]any{
		"provider": "openai_compatible", "baseUrl": llmServer(t, `{"label":"refund","confidence":0.9,"reason":"r"}`), "model": "m",
		"labels": "refund,question",
	}}
	msg := message.AcquireMessage()
	msg.SetData("text", "I want my money back")
	out, branch, err := exec.Execute(t.Context(), nullCtx{}, "wf", node, msg)
	if err != nil {
		t.Fatal(err)
	}
	if branch != "refund" || len(out) != 1 || out[0].Data()["ai_label"] != "refund" {
		t.Fatalf("branch = %q out = %v", branch, out)
	}
}

func TestClassifyNode_ErrorKeepsMessageOnErrorBranch(t *testing.T) {
	exec, _ := interfaces.GetNodeExecutor("ai_classify")
	node := &storage.WorkflowNode{ID: "n1", Type: "ai_classify", Config: map[string]any{"labels": "a"}}
	msg := message.AcquireMessage()
	if _, _, err := exec.Execute(t.Context(), nullCtx{}, "wf", node, msg); err == nil {
		t.Fatal("a node with no provider must fail")
	}
}

// refusingBudget refuses every call and remembers the scope it was asked in.
type refusingBudget struct{ scopes []llm.Scope }

func (b *refusingBudget) Allow(ctx context.Context) error {
	s := llm.ScopeFrom(ctx)
	b.scopes = append(b.scopes, s)
	return &llm.BudgetError{Limit: llm.LimitWorkflowTokens, VHost: s.VHost, WorkflowID: s.WorkflowID, Used: 5, Max: 5}
}

func (b *refusingBudget) Record(context.Context, llm.CallRecord) {}

// A classify node over its workflow's cap fails with the budget error, so the
// message takes the node's error branch, and the cap it was checked against
// is its own workflow's in its own vhost.
func TestClassifyNode_BudgetIsCheckedInTheWorkflowScope(t *testing.T) {
	b := &refusingBudget{}
	genai.SetBudget(b)
	t.Cleanup(func() { genai.SetBudget(nil) })

	exec, _ := interfaces.GetNodeExecutor("ai_classify")
	node := &storage.WorkflowNode{ID: "n1", Type: "ai_classify", Config: map[string]any{
		"provider": "openai_compatible", "baseUrl": llmServer(t, `{"label":"a","confidence":1}`), "model": "m", "labels": "a,b",
	}}
	msg := message.AcquireMessage()
	msg.SetVHost("tenant-a")
	out, _, err := exec.Execute(t.Context(), nullCtx{}, "wf-c", node, msg)
	if !errors.Is(err, llm.ErrBudgetExceeded) {
		t.Fatalf("err = %v, want the budget error", err)
	}
	if len(out) != 1 {
		t.Fatalf("the refused message must stay on the error path, got %v", out)
	}
	if len(b.scopes) != 1 || b.scopes[0] != (llm.Scope{VHost: "tenant-a", WorkflowID: "wf-c"}) {
		t.Fatalf("scopes = %+v", b.scopes)
	}
}
