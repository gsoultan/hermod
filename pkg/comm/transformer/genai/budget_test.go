package genai

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/gsoultan/hermod/pkg/comm/message"
	"github.com/gsoultan/hermod/pkg/comm/transformer"
	"github.com/gsoultan/hermod/pkg/llm"
)

// fakeBudget refuses when refuse is set and remembers the scope of every
// check and every record.
type fakeBudget struct {
	mu      sync.Mutex
	refuse  error
	checked []llm.Scope
	scopes  []llm.Scope
	records []llm.CallRecord
}

func (b *fakeBudget) Allow(ctx context.Context) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.checked = append(b.checked, llm.ScopeFrom(ctx))
	return b.refuse
}

func (b *fakeBudget) Record(ctx context.Context, rec llm.CallRecord) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.scopes = append(b.scopes, llm.ScopeFrom(ctx))
	b.records = append(b.records, rec)
}

func useBudget(t *testing.T, b llm.Budget) {
	t.Helper()
	SetBudget(b)
	t.Cleanup(func() { SetBudget(nil) })
}

// A refused call never reaches the provider, and the node fails with an error
// that is still a budget error, so it takes the node's error branch.
func TestBudget_RefusalFailsTheNodeWithoutCallingTheModel(t *testing.T) {
	useBudget(t, &fakeBudget{refuse: &llm.BudgetError{Limit: llm.LimitDisabled, VHost: "tenant-a"}})
	f := newFakeLLM(t, "never")

	_, err := run(t, "ai_prompt", f.config(map[string]any{"prompt": "x"}), nil)
	if !errors.Is(err, llm.ErrBudgetExceeded) {
		t.Fatalf("err = %v, want a budget error", err)
	}
	var be *llm.BudgetError
	if !errors.As(err, &be) || be.Limit != llm.LimitDisabled {
		t.Fatalf("err = %v, want the typed BudgetError", err)
	}
	if f.calls() != 0 {
		t.Fatalf("the provider was called %d times after the budget refused", f.calls())
	}
}

func TestBudget_RefusalStopsEmbeddings(t *testing.T) {
	useBudget(t, &fakeBudget{refuse: &llm.BudgetError{Limit: llm.LimitVHostTokens, VHost: "tenant-a", Used: 10, Max: 10}})
	f := newFakeLLM(t, `{"data":[{"embedding":[0.1]}]}`)

	_, err := run(t, "ai_embed", f.config(map[string]any{"text": "hello"}), nil)
	if !errors.Is(err, llm.ErrBudgetExceeded) {
		t.Fatalf("err = %v, want a budget error", err)
	}
	if f.calls() != 0 {
		t.Fatalf("the provider was called %d times after the budget refused", f.calls())
	}
}

// The budget is told whose call it was: the vhost the engine stamped on the
// message, and the workflow the node runs in. And it is told what the call
// cost, by the model that actually answered.
func TestBudget_SeesScopeAndUsage(t *testing.T) {
	b := &fakeBudget{}
	useBudget(t, b)
	f := newFakeLLM(t, "ok")
	tf, _ := transformer.Get("ai_prompt")
	msg := message.AcquireMessage()
	defer message.ReleaseMessage(msg)
	msg.SetVHost("tenant-a")
	// A planted metadata value must not move the cost elsewhere.
	msg.SetMetadata("vhost", "tenant-b")

	ctx := llm.WithScope(t.Context(), llm.Scope{WorkflowID: "wf-1"})
	if _, err := tf.Transform(ctx, msg, f.config(map[string]any{"prompt": "x"})); err != nil {
		t.Fatal(err)
	}

	b.mu.Lock()
	defer b.mu.Unlock()
	want := llm.Scope{VHost: "tenant-a", WorkflowID: "wf-1"}
	if len(b.checked) != 1 || b.checked[0] != want {
		t.Fatalf("checked scopes = %+v, want [%+v]", b.checked, want)
	}
	if len(b.records) != 1 || b.scopes[0] != want {
		t.Fatalf("recorded scopes = %+v, want [%+v]", b.scopes, want)
	}
	if r := b.records[0]; r.Model != "served-model" || r.Usage.InputTokens != 10 || r.Usage.OutputTokens != 5 {
		t.Fatalf("record = %+v", r)
	}
}

// The budget sits inside the observer, so a refusal is counted in the AI
// call metrics under its own outcome.
func TestBudget_RefusalIsObserved(t *testing.T) {
	useBudget(t, &fakeBudget{refuse: &llm.BudgetError{Limit: llm.LimitDisabled, VHost: "tenant-a"}})
	var mu sync.Mutex
	var got []llm.CallRecord
	SetObserver(func(_ context.Context, r llm.CallRecord) { mu.Lock(); got = append(got, r); mu.Unlock() })
	t.Cleanup(func() { SetObserver(nil) })

	f := newFakeLLM(t, "never")
	_, _ = run(t, "ai_prompt", f.config(map[string]any{"prompt": "x", "model": "observed-refusal"}), nil)

	mu.Lock()
	defer mu.Unlock()
	if len(got) != 1 || !errors.Is(got[0].Err, llm.ErrBudgetExceeded) {
		t.Fatalf("records = %+v, want one budget refusal", got)
	}
}
