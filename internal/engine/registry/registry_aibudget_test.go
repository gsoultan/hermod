package registry

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gsoultan/hermod/internal/storage"
	"github.com/gsoultan/hermod/pkg/comm/message"
	"github.com/gsoultan/hermod/pkg/comm/transformer/genai"
	"github.com/gsoultan/hermod/pkg/llm"
)

// AI budgets end to end, from where an operator sets them: a budget saved in
// the registry's store governs an ai_prompt node run through the engine's
// node path, with the registry's budget installed the way cmd/hermod installs
// it. A cap is counted from the tokens the provider reported and refuses the
// next call; the kill switch refuses the call before the provider sees it.
func TestAIBudgetGovernsAnAINodeFromStorage(t *testing.T) {
	reg := newSimRegistry(t)
	genai.SetBudget(reg.AIBudget())
	t.Cleanup(func() { genai.SetBudget(nil) })

	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"model":   "served-model",
			"choices": []any{map[string]any{"message": map[string]any{"content": "ok"}, "finish_reason": "stop"}},
			"usage":   map[string]any{"prompt_tokens": 10, "completion_tokens": 5},
		})
	}))
	t.Cleanup(srv.Close)

	budgets, ok := reg.store().(storage.AIBudgetStore)
	if !ok {
		t.Fatal("the registry's store holds no AI budgets")
	}
	node := &storage.WorkflowNode{ID: "ai-1", Type: "transformation", Config: map[string]any{
		"transType": "ai_prompt", "provider": "openai_compatible", "baseUrl": srv.URL, "model": "m", "prompt": "hi",
	}}
	runNode := func() error {
		msg := message.AcquireMessage()
		defer message.ReleaseMessage(msg)
		msg.SetVHost("tenant-a")
		out, _, err := reg.RunWorkflowNodeContext(t.Context(), "wf-1", node, msg)
		for _, m := range out {
			m.Release()
		}
		return err
	}

	if err := reg.AIBudget().Save(t.Context(), storage.AIBudget{VHost: "tenant-a",
		Workflows: []storage.AIWorkflowCap{{WorkflowID: "wf-1", MonthlyTokens: 15}}}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := runNode(); err != nil {
		t.Fatalf("first call under the cap: %v", err)
	}
	err := runNode()
	var be *llm.BudgetError
	if !errors.As(err, &be) || be.Limit != llm.LimitWorkflowTokens || be.WorkflowID != "wf-1" {
		t.Fatalf("second call: err = %v, want wf-1's token cap", err)
	}
	if n := calls.Load(); n != 1 {
		t.Fatalf("provider calls = %d, want 1", n)
	}
	u, err := budgets.GetAIUsage(t.Context(), "tenant-a", storage.AIUsagePeriod(time.Now()), "wf-1")
	if err != nil || u.Tokens() != 15 || u.Calls != 1 {
		t.Fatalf("stored usage = %+v, %v; want the 15 tokens the provider reported", u, err)
	}

	if err := reg.AIBudget().Save(t.Context(), storage.AIBudget{VHost: "tenant-a", Disabled: true}); err != nil {
		t.Fatal(err)
	}
	if err := runNode(); !errors.As(err, &be) || be.Limit != llm.LimitDisabled {
		t.Fatalf("with the kill switch on: err = %v", err)
	}
	if n := calls.Load(); n != 1 {
		t.Fatalf("the kill switch let a call through: provider calls = %d", n)
	}
}
