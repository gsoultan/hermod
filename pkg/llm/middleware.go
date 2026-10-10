package llm

import (
	"context"
	"errors"
	"math/rand/v2"
	"time"
)

// Middleware decorates a Provider.
type Middleware func(Provider) Provider

// Chain applies mws to p so that the first middleware is the outermost.
func Chain(p Provider, mws ...Middleware) Provider {
	for i := len(mws) - 1; i >= 0; i-- {
		p = mws[i](p)
	}
	return p
}

// chatFunc turns a function into a Provider that keeps the inner provider's
// name and forwards Embed to it.
type chatFunc struct {
	inner Provider
	chat  func(context.Context, ChatRequest) (ChatResponse, error)
}

func (c *chatFunc) Name() string { return c.inner.Name() }

func (c *chatFunc) Chat(ctx context.Context, req ChatRequest) (ChatResponse, error) {
	return c.chat(ctx, req)
}

func (c *chatFunc) Embed(ctx context.Context, req EmbedRequest) (EmbedResponse, error) {
	if e, ok := c.inner.(Embedder); ok {
		return e.Embed(ctx, req)
	}
	return EmbedResponse{}, ErrUnsupported
}

// RetryPolicy bounds WithRetry.
type RetryPolicy struct {
	// MaxAttempts counts the first try. Values below 1 mean 1.
	MaxAttempts int
	BaseDelay   time.Duration
	MaxDelay    time.Duration
}

// DefaultRetryPolicy is three attempts with jittered exponential backoff.
var DefaultRetryPolicy = RetryPolicy{MaxAttempts: 3, BaseDelay: 500 * time.Millisecond, MaxDelay: 20 * time.Second}

// WithRetry retries retryable failures with jittered exponential backoff,
// waiting at least as long as a provider's Retry-After asks.
func WithRetry(policy RetryPolicy) Middleware {
	attempts := max(policy.MaxAttempts, 1)
	return func(p Provider) Provider {
		return &chatFunc{inner: p, chat: func(ctx context.Context, req ChatRequest) (ChatResponse, error) {
			var lastErr error
			for attempt := range attempts {
				resp, err := p.Chat(ctx, req)
				if err == nil || !IsRetryable(err) || attempt == attempts-1 {
					return resp, err
				}
				lastErr = err
				if waitErr := sleep(ctx, backoff(policy, attempt, err)); waitErr != nil {
					return ChatResponse{}, waitErr
				}
			}
			return ChatResponse{}, lastErr
		}}
	}
}

func backoff(policy RetryPolicy, attempt int, err error) time.Duration {
	d := policy.BaseDelay << attempt
	if d <= 0 || (policy.MaxDelay > 0 && d > policy.MaxDelay) {
		d = policy.MaxDelay
	}
	if d > 0 {
		d = d/2 + rand.N(d/2+1) //nolint:gosec // backoff jitter, not a secret
	}
	var apiErr *APIError
	if errors.As(err, &apiErr) && apiErr.RetryAfter > d {
		d = apiErr.RetryAfter
	}
	return d
}

func sleep(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// WithConcurrencyLimit allows at most n calls in flight; others wait until a
// slot frees or their context ends.
func WithConcurrencyLimit(n int) Middleware {
	return func(p Provider) Provider {
		if n <= 0 {
			return p
		}
		slots := make(chan struct{}, n)
		return &chatFunc{inner: p, chat: func(ctx context.Context, req ChatRequest) (ChatResponse, error) {
			select {
			case slots <- struct{}{}:
			case <-ctx.Done():
				return ChatResponse{}, ctx.Err()
			}
			defer func() { <-slots }()
			return p.Chat(ctx, req)
		}}
	}
}

// Fallback tries providers in order. It moves to the next one when a
// provider fails in a way that is not the request's fault (a retryable
// error) or declines to answer (a refusal). A non-retryable error, such as a
// malformed request, is returned at once: the next provider would reject it
// too.
func Fallback(primary Provider, others ...Provider) Provider {
	chain := append([]Provider{primary}, others...)
	return &chatFunc{inner: primary, chat: func(ctx context.Context, req ChatRequest) (ChatResponse, error) {
		var (
			resp ChatResponse
			err  error
		)
		for _, p := range chain {
			resp, err = p.Chat(ctx, req)
			if ctxErr := ctx.Err(); ctxErr != nil {
				return resp, ctxErr
			}
			switch {
			case err == nil && resp.StopReason != StopRefusal:
				return resp, nil
			case err != nil && !IsRetryable(err):
				return resp, err
			}
		}
		return resp, err
	}}
}

// CallRecord describes one provider call, for metrics, audit and cost.
type CallRecord struct {
	Provider   string
	Model      string
	Usage      Usage
	Latency    time.Duration
	StopReason StopReason
	Err        error
}

// Observer receives a CallRecord after every call. It must not block.
type Observer func(context.Context, CallRecord)

// WithObserver reports every call to obs. Placed inside WithRetry it sees
// each attempt; outside, each logical call.
func WithObserver(obs Observer) Middleware {
	return func(p Provider) Provider {
		return &chatFunc{inner: p, chat: func(ctx context.Context, req ChatRequest) (ChatResponse, error) {
			start := time.Now()
			resp, err := p.Chat(ctx, req)
			rec := CallRecord{
				Provider:   p.Name(),
				Model:      req.Model,
				Usage:      resp.Usage,
				Latency:    time.Since(start),
				StopReason: resp.StopReason,
				Err:        err,
			}
			if resp.Model != "" {
				rec.Model = resp.Model
			}
			obs(ctx, rec)
			return resp, err
		}}
	}
}

// ErrBudgetExceeded is returned when a Budget refuses a call.
var ErrBudgetExceeded = errors.New("llm: token budget exceeded")

// Budget gates calls on spend so far.
type Budget interface {
	// Allow returns an error (normally wrapping ErrBudgetExceeded) when no
	// more calls may be made.
	Allow(ctx context.Context) error
	// Record adds a finished call's usage. The record names the provider and
	// the model that answered, so a budget can price what it is told.
	Record(ctx context.Context, rec CallRecord)
}

// WithBudget refuses calls once b says the budget is spent, and records the
// usage of every call that ran. Embeddings are gated and recorded the same
// way: they are paid for too.
//
// The check comes before the call and the usage is only known after it, so a
// call that starts under the limit can finish over it: a budget is overshot by
// at most the calls already in flight when it ran out.
func WithBudget(b Budget) Middleware {
	return func(p Provider) Provider {
		return &budgeted{inner: p, budget: b}
	}
}

type budgeted struct {
	inner  Provider
	budget Budget
}

func (b *budgeted) Name() string { return b.inner.Name() }

func (b *budgeted) Chat(ctx context.Context, req ChatRequest) (ChatResponse, error) {
	if err := b.budget.Allow(ctx); err != nil {
		return ChatResponse{}, err
	}
	resp, err := b.inner.Chat(ctx, req)
	if resp.Usage != (Usage{}) {
		rec := CallRecord{Provider: b.inner.Name(), Model: req.Model, Usage: resp.Usage, StopReason: resp.StopReason, Err: err}
		if resp.Provider != "" {
			rec.Provider = resp.Provider
		}
		if resp.Model != "" {
			rec.Model = resp.Model
		}
		b.budget.Record(ctx, rec)
	}
	return resp, err
}

func (b *budgeted) Embed(ctx context.Context, req EmbedRequest) (EmbedResponse, error) {
	e, ok := b.inner.(Embedder)
	if !ok {
		return EmbedResponse{}, ErrUnsupported
	}
	if err := b.budget.Allow(ctx); err != nil {
		return EmbedResponse{}, err
	}
	resp, err := e.Embed(ctx, req)
	if resp.Usage != (Usage{}) {
		rec := CallRecord{Provider: b.inner.Name(), Model: req.Model, Usage: resp.Usage, Err: err}
		if resp.Model != "" {
			rec.Model = resp.Model
		}
		b.budget.Record(ctx, rec)
	}
	return resp, err
}
