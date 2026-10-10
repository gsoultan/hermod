package llm

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type fakeProvider struct {
	name  string
	mu    sync.Mutex
	calls int
	reply func(call int) (ChatResponse, error)
	block chan struct{}
	inUse atomic.Int32
	peak  atomic.Int32
}

func (f *fakeProvider) Name() string { return f.name }

func (f *fakeProvider) Chat(ctx context.Context, _ ChatRequest) (ChatResponse, error) {
	n := f.inUse.Add(1)
	defer f.inUse.Add(-1)
	for {
		p := f.peak.Load()
		if n <= p || f.peak.CompareAndSwap(p, n) {
			break
		}
	}
	if f.block != nil {
		select {
		case <-f.block:
		case <-ctx.Done():
			return ChatResponse{}, ctx.Err()
		}
	}
	f.mu.Lock()
	f.calls++
	call := f.calls
	f.mu.Unlock()
	return f.reply(call)
}

func (f *fakeProvider) Calls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

var (
	errRetryable = &APIError{Provider: "fake", Status: 503, Message: "busy", Retryable: true}
	errFatal     = &APIError{Provider: "fake", Status: 400, Message: "bad"}
	okResp       = ChatResponse{Text: "ok", StopReason: StopEnd, Provider: "fake", Usage: Usage{InputTokens: 3, OutputTokens: 2}}
	req          = ChatRequest{Messages: []Message{{Role: RoleUser, Text: "q"}}}
)

func fastRetry() RetryPolicy {
	return RetryPolicy{MaxAttempts: 3, BaseDelay: time.Millisecond, MaxDelay: time.Millisecond}
}

func TestRetry_RetriesRetryableThenSucceeds(t *testing.T) {
	f := &fakeProvider{name: "fake", reply: func(call int) (ChatResponse, error) {
		if call < 3 {
			return ChatResponse{}, errRetryable
		}
		return okResp, nil
	}}
	resp, err := Chain(f, WithRetry(fastRetry())).Chat(t.Context(), req)
	if err != nil || resp.Text != "ok" || f.Calls() != 3 {
		t.Fatalf("resp=%+v err=%v calls=%d", resp, err, f.Calls())
	}
}

func TestRetry_StopsOnFatalAndAtMaxAttempts(t *testing.T) {
	f := &fakeProvider{name: "fake", reply: func(int) (ChatResponse, error) { return ChatResponse{}, errFatal }}
	if _, err := Chain(f, WithRetry(fastRetry())).Chat(t.Context(), req); !errors.Is(err, errFatal) || f.Calls() != 1 {
		t.Fatalf("err=%v calls=%d", err, f.Calls())
	}
	g := &fakeProvider{name: "fake", reply: func(int) (ChatResponse, error) { return ChatResponse{}, errRetryable }}
	if _, err := Chain(g, WithRetry(fastRetry())).Chat(t.Context(), req); !errors.Is(err, errRetryable) || g.Calls() != 3 {
		t.Fatalf("err=%v calls=%d", err, g.Calls())
	}
}

func TestRetry_HonoursRetryAfterAndContext(t *testing.T) {
	f := &fakeProvider{name: "fake", reply: func(int) (ChatResponse, error) {
		return ChatResponse{}, &APIError{Provider: "fake", Status: 429, Retryable: true, RetryAfter: time.Hour}
	}}
	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := Chain(f, WithRetry(RetryPolicy{MaxAttempts: 5, BaseDelay: time.Millisecond, MaxDelay: 2 * time.Hour})).Chat(ctx, req)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want deadline exceeded while waiting out Retry-After", err)
	}
	if time.Since(start) > 2*time.Second {
		t.Fatal("retry wait ignored the context")
	}
}

func TestConcurrencyLimit(t *testing.T) {
	f := &fakeProvider{name: "fake", block: make(chan struct{}), reply: func(int) (ChatResponse, error) { return okResp, nil }}
	p := Chain(f, WithConcurrencyLimit(2))
	var wg sync.WaitGroup
	for range 6 {
		wg.Go(func() { _, _ = p.Chat(t.Context(), req) })
	}
	time.Sleep(50 * time.Millisecond)
	close(f.block)
	wg.Wait()
	if f.peak.Load() > 2 {
		t.Fatalf("peak concurrency %d exceeds limit 2", f.peak.Load())
	}
	if f.Calls() != 6 {
		t.Fatalf("calls = %d", f.Calls())
	}
}

func TestConcurrencyLimit_WaitRespectsContext(t *testing.T) {
	f := &fakeProvider{name: "fake", block: make(chan struct{}), reply: func(int) (ChatResponse, error) { return okResp, nil }}
	defer close(f.block)
	p := Chain(f, WithConcurrencyLimit(1))
	go func() { _, _ = p.Chat(t.Context(), req) }()
	time.Sleep(20 * time.Millisecond)
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	if _, err := p.Chat(ctx, req); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v", err)
	}
}

func TestFallback(t *testing.T) {
	down := &fakeProvider{name: "a", reply: func(int) (ChatResponse, error) { return ChatResponse{}, errRetryable }}
	refuses := &fakeProvider{name: "b", reply: func(int) (ChatResponse, error) { return ChatResponse{StopReason: StopRefusal}, nil }}
	up := &fakeProvider{name: "c", reply: func(int) (ChatResponse, error) { return okResp, nil }}
	resp, err := Fallback(down, refuses, up).Chat(t.Context(), req)
	if err != nil || resp.Text != "ok" || down.Calls() != 1 || refuses.Calls() != 1 || up.Calls() != 1 {
		t.Fatalf("resp=%+v err=%v", resp, err)
	}

	bad := &fakeProvider{name: "a", reply: func(int) (ChatResponse, error) { return ChatResponse{}, errFatal }}
	spare := &fakeProvider{name: "b", reply: func(int) (ChatResponse, error) { return okResp, nil }}
	if _, err := Fallback(bad, spare).Chat(t.Context(), req); !errors.Is(err, errFatal) || spare.Calls() != 0 {
		t.Fatalf("a non-retryable request error must not fall through: err=%v spare=%d", err, spare.Calls())
	}
}

func TestObserverSeesEveryCall(t *testing.T) {
	var got []CallRecord
	var mu sync.Mutex
	obs := func(_ context.Context, r CallRecord) { mu.Lock(); got = append(got, r); mu.Unlock() }
	f := &fakeProvider{name: "fake", reply: func(call int) (ChatResponse, error) {
		if call == 1 {
			return ChatResponse{}, errRetryable
		}
		return okResp, nil
	}}
	// Observer inside retry: one record per attempt.
	if _, err := Chain(f, WithRetry(fastRetry()), WithObserver(obs)).Chat(t.Context(), ChatRequest{Model: "m", Messages: req.Messages}); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Err == nil || got[1].Err != nil || got[1].Usage.OutputTokens != 2 || got[1].Model != "m" || got[1].Provider != "fake" {
		t.Fatalf("records = %+v", got)
	}
}

type countingBudget struct {
	mu      sync.Mutex
	spent   Usage
	limit   int64
	records []CallRecord
}

func (b *countingBudget) Allow(context.Context) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.spent.InputTokens+b.spent.OutputTokens >= b.limit {
		return ErrBudgetExceeded
	}
	return nil
}

func (b *countingBudget) Record(_ context.Context, rec CallRecord) {
	b.mu.Lock()
	b.spent = b.spent.Add(rec.Usage)
	b.records = append(b.records, rec)
	b.mu.Unlock()
}

func TestBudget_BlocksOnceSpent(t *testing.T) {
	f := &fakeProvider{name: "fake", reply: func(int) (ChatResponse, error) { return okResp, nil }}
	b := &countingBudget{limit: 6}
	p := Chain(f, WithBudget(b))
	for i := range 2 {
		if _, err := p.Chat(t.Context(), req); err != nil {
			t.Fatalf("call %d: %v", i, err)
		}
	}
	if _, err := p.Chat(t.Context(), req); !errors.Is(err, ErrBudgetExceeded) {
		t.Fatalf("err = %v", err)
	}
	if f.Calls() != 2 {
		t.Fatalf("calls = %d", f.Calls())
	}
}

func TestEmbedPassesThroughDecorators(t *testing.T) {
	f := &fakeProvider{name: "fake"}
	if _, err := Chain(f, WithRetry(fastRetry())).(Embedder).Embed(t.Context(), EmbedRequest{}); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("err = %v", err)
	}
}

// A budget that prices usage has to know which model answered: a fallback
// can serve a call with a different model from the one asked for.
func TestBudget_RecordsWhoAnswered(t *testing.T) {
	f := &fakeProvider{name: "fake", reply: func(int) (ChatResponse, error) {
		r := okResp
		r.Model = "served-model"
		return r, nil
	}}
	b := &countingBudget{limit: 100}
	if _, err := Chain(f, WithBudget(b)).Chat(t.Context(), ChatRequest{Model: "asked-model"}); err != nil {
		t.Fatal(err)
	}
	if len(b.records) != 1 {
		t.Fatalf("records = %d, want 1", len(b.records))
	}
	got := b.records[0]
	if got.Provider != "fake" || got.Model != "served-model" || got.Usage != okResp.Usage {
		t.Fatalf("record = %+v", got)
	}
}

type fakeEmbedder struct {
	fakeProvider
	embeds atomic.Int32
}

func (f *fakeEmbedder) Embed(context.Context, EmbedRequest) (EmbedResponse, error) {
	f.embeds.Add(1)
	return EmbedResponse{Vectors: [][]float32{{1}}, Usage: Usage{InputTokens: 4}, Model: "embed-model"}, nil
}

// Embeddings are paid for like chat, so a budget gates them too.
func TestBudget_GatesEmbed(t *testing.T) {
	f := &fakeEmbedder{fakeProvider: fakeProvider{name: "fake"}}
	b := &countingBudget{limit: 6}
	e := Chain(f, WithBudget(b)).(Embedder)
	for i := range 2 {
		if _, err := e.Embed(t.Context(), EmbedRequest{Inputs: []string{"x"}}); err != nil {
			t.Fatalf("embed %d: %v", i, err)
		}
	}
	if _, err := e.Embed(t.Context(), EmbedRequest{Inputs: []string{"x"}}); !errors.Is(err, ErrBudgetExceeded) {
		t.Fatalf("err = %v, want ErrBudgetExceeded", err)
	}
	if n := f.embeds.Load(); n != 2 {
		t.Fatalf("embeds = %d, want 2", n)
	}
	if got := b.records[0]; got.Model != "embed-model" || got.Usage.InputTokens != 4 {
		t.Fatalf("record = %+v", got)
	}
}

func TestScopeRoundTrip(t *testing.T) {
	if got := ScopeFrom(t.Context()); got != (Scope{}) {
		t.Fatalf("empty context scope = %+v", got)
	}
	ctx := WithScope(t.Context(), Scope{VHost: "tenant-a", WorkflowID: "wf-1"})
	if got := ScopeFrom(ctx); got.VHost != "tenant-a" || got.WorkflowID != "wf-1" {
		t.Fatalf("scope = %+v", got)
	}
}

func TestBudgetErrorIsBudgetExceeded(t *testing.T) {
	err := fmt.Errorf("node: %w", &BudgetError{Limit: LimitVHostTokens, VHost: "tenant-a", Used: 120, Max: 100})
	if !errors.Is(err, ErrBudgetExceeded) {
		t.Fatalf("errors.Is(%v, ErrBudgetExceeded) = false", err)
	}
	var be *BudgetError
	if !errors.As(err, &be) || be.Limit != LimitVHostTokens {
		t.Fatalf("errors.As = %v, %+v", err, be)
	}
	if !strings.Contains(err.Error(), "tenant-a") {
		t.Fatalf("message %q does not name the vhost", err)
	}
}
