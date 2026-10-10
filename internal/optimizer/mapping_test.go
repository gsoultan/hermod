package optimizer_test

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/gsoultan/hermod/internal/optimizer"
	"github.com/gsoultan/hermod/pkg/llm"
)

// fakeModel answers every chat and keeps what it was sent.
type fakeModel struct {
	mu   sync.Mutex
	reqs []llm.ChatRequest
}

func (*fakeModel) Name() string { return "fake" }

func (f *fakeModel) Chat(_ context.Context, req llm.ChatRequest) (llm.ChatResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reqs = append(f.reqs, req)
	return llm.ChatResponse{Text: "map email to contact_email", StopReason: llm.StopEnd}, nil
}

func (f *fakeModel) sent() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var b strings.Builder
	for _, r := range f.reqs {
		b.WriteString(r.System)
		for _, m := range r.Messages {
			b.WriteString(m.Text)
		}
	}
	return b.String()
}

func TestLLMMappingAdvisorMasksPIIBeforeTheSampleLeaves(t *testing.T) {
	model := &fakeModel{}
	var asked string
	adv := optimizer.NewLLMMappingAdvisor(func(_ context.Context, workflowID string) (llm.Provider, string, error) {
		asked = workflowID
		return model, "m", nil
	})

	got, err := adv.SuggestMapping(t.Context(), "wf", "validate-1", map[string]any{
		"email":   "jane.doe@example.com",
		"contact": map[string]any{"ssn": "123-45-6789", "ip": "10.1.2.3"},
		"tags":    []any{"call 555-123-4567"},
		"amount":  42,
	})
	if err != nil {
		t.Fatal(err)
	}
	if asked != "wf" || got != "map email to contact_email" {
		t.Fatalf("asked for %q, answer %q", asked, got)
	}
	sent := model.sent()
	for _, secret := range []string{"jane.doe@example.com", "123-45-6789", "10.1.2.3", "555-123-4567"} {
		if strings.Contains(sent, secret) {
			t.Errorf("%q reached the model: %s", secret, sent)
		}
	}
	for _, kept := range []string{"email", "ssn", "amount", "42"} {
		if !strings.Contains(sent, kept) {
			t.Errorf("%q, which is not PII, was lost: %s", kept, sent)
		}
	}
}

func TestLLMMappingAdvisorReportsAnUnconfiguredConnection(t *testing.T) {
	adv := optimizer.NewLLMMappingAdvisor(func(context.Context, string) (llm.Provider, string, error) {
		return nil, "", optimizer.ErrAdvisorNotConfigured
	})
	if _, err := adv.SuggestMapping(t.Context(), "wf", "n", map[string]any{"a": 1}); err == nil {
		t.Fatal("no error without a connection")
	}
}
