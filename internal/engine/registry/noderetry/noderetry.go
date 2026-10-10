// Package noderetry is the per-step retry policy any workflow node can carry.
//
// A node opts in with a "retry" object in its config:
//
//	"retry": {"maxAttempts": 3, "backoff": "1s", "maxBackoff": "30s", "on": "timeout|429"}
//
// maxAttempts counts the first try and is capped at MaxAttemptsLimit. The wait
// before retry n is backoff·2^(n-1), capped at maxBackoff (itself capped at
// MaxBackoffLimit). "on", when set, is a regular expression an error's text
// must match to be retried; anything else fails at once. The graph runner
// applies it around a node's execution; once the last attempt fails the
// failure is handled exactly as before, so a dead-letter sink still receives
// the message.
package noderetry

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const (
	// ConfigKey is the node config key holding the policy.
	ConfigKey = "retry"

	// MaxAttemptsLimit bounds attempts. A retrying node holds its message,
	// and its slot in the workflow, for the whole sequence.
	MaxAttemptsLimit = 10
	// DefaultBackoff is the first wait when the policy names none.
	DefaultBackoff = time.Second
	// DefaultMaxBackoff caps a wait when the policy names no cap.
	DefaultMaxBackoff = 30 * time.Second
	// MaxBackoffLimit caps any single wait whatever the policy asks.
	MaxBackoffLimit = 5 * time.Minute
)

// Policy is a parsed retry policy. The zero value and MaxAttempts 1 both mean
// "run once".
type Policy struct {
	MaxAttempts int
	Backoff     time.Duration
	MaxBackoff  time.Duration
	// On restricts retries to errors whose text matches. Nil retries any
	// error other than the context's own.
	On *regexp.Regexp
}

// Configured reports whether a node's config carries a retry policy at all.
// It is a map lookup, so the success path pays nothing for the feature.
func Configured(config map[string]any) bool {
	_, ok := config[ConfigKey]
	return ok
}

// Parse reads a node's retry policy. A node without one gets MaxAttempts 1.
func Parse(config map[string]any) (Policy, error) {
	raw, ok := config[ConfigKey]
	if !ok || raw == nil {
		return Policy{MaxAttempts: 1}, nil
	}
	m, ok := raw.(map[string]any)
	if !ok {
		return Policy{}, fmt.Errorf("retry must be an object, got %T", raw)
	}

	p := Policy{MaxAttempts: 1, Backoff: DefaultBackoff, MaxBackoff: DefaultMaxBackoff}
	if v, ok := m["maxAttempts"]; ok && v != nil {
		n, err := toInt(v)
		if err != nil {
			return Policy{}, fmt.Errorf("retry.maxAttempts: %w", err)
		}
		p.MaxAttempts = min(max(n, 1), MaxAttemptsLimit)
	}
	var err error
	if p.Backoff, err = duration(m, "backoff", DefaultBackoff); err != nil {
		return Policy{}, err
	}
	if p.MaxBackoff, err = duration(m, "maxBackoff", DefaultMaxBackoff); err != nil {
		return Policy{}, err
	}
	p.MaxBackoff = min(p.MaxBackoff, MaxBackoffLimit)
	if s, _ := m["on"].(string); strings.TrimSpace(s) != "" {
		re, err := regexp.Compile(s)
		if err != nil {
			return Policy{}, fmt.Errorf("retry.on: %w", err)
		}
		p.On = re
	}
	return p, nil
}

func toInt(v any) (int, error) {
	switch n := v.(type) {
	case float64:
		return int(n), nil
	case int:
		return n, nil
	case int64:
		return int(n), nil
	case string:
		i, err := strconv.Atoi(strings.TrimSpace(n))
		if err != nil {
			return 0, fmt.Errorf("%q is not a whole number", n)
		}
		return i, nil
	default:
		return 0, fmt.Errorf("%v is not a whole number", v)
	}
}

func duration(m map[string]any, key string, def time.Duration) (time.Duration, error) {
	v, ok := m[key]
	if !ok || v == nil || v == "" {
		return def, nil
	}
	s, ok := v.(string)
	if !ok {
		return 0, fmt.Errorf("retry.%s must be a duration such as \"500ms\" or \"2s\"", key)
	}
	d, err := time.ParseDuration(strings.TrimSpace(s))
	if err != nil || d < 0 {
		return 0, fmt.Errorf("retry.%s: %q is not a duration such as \"500ms\" or \"2s\"", key, s)
	}
	return d, nil
}

// Delay is the wait before retry n (n starts at 1).
func (p Policy) Delay(retry int) time.Duration {
	d := p.Backoff
	for i := 1; i < retry && d < p.MaxBackoff; i++ {
		d *= 2
	}
	return min(d, p.MaxBackoff)
}

// retryable reports whether err may be retried under p. A context error is
// never retried: the workflow is stopping, or the call's own deadline is the
// caller's decision.
func (p Policy) retryable(err error) bool {
	if err == nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	return p.On == nil || p.On.MatchString(err.Error())
}

// Run calls call until it succeeds, its error is not retryable, or the policy
// runs out of attempts, and returns the last result. A failed attempt's result
// is handed to discard before the next one starts; onRetry, when not nil, is
// told about each retry before its wait. Waiting ends early when ctx does, and
// then Run returns the zero result with the last error.
func Run[T any](ctx context.Context, p Policy, call func() (T, error), discard func(T), onRetry func(attempt int, wait time.Duration, err error)) (T, error) {
	for attempt := 1; ; attempt++ {
		res, err := call()
		if err == nil || attempt >= p.MaxAttempts || !p.retryable(err) || ctx.Err() != nil {
			return res, err
		}
		wait := p.Delay(attempt)
		if onRetry != nil {
			onRetry(attempt, wait, err)
		}
		discard(res)
		if !sleep(ctx, wait) {
			var zero T
			return zero, err
		}
	}
}

func sleep(ctx context.Context, d time.Duration) bool {
	if d <= 0 {
		return ctx.Err() == nil
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return true
	case <-ctx.Done():
		return false
	}
}
