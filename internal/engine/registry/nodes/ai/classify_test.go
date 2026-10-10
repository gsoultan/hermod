package ai

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gsoultan/hermod/internal/engine/registry/interfaces"
	"github.com/gsoultan/hermod/internal/storage"
	"github.com/gsoultan/hermod/pkg/comm/message"
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
