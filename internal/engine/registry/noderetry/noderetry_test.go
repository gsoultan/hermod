package noderetry

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestParse(t *testing.T) {
	cases := []struct {
		name    string
		config  map[string]any
		want    Policy
		wantErr bool
	}{
		{"absent", map[string]any{}, Policy{MaxAttempts: 1}, false},
		{"defaults", map[string]any{"retry": map[string]any{"maxAttempts": float64(3)}},
			Policy{MaxAttempts: 3, Backoff: DefaultBackoff, MaxBackoff: DefaultMaxBackoff}, false},
		{"explicit", map[string]any{"retry": map[string]any{"maxAttempts": "4", "backoff": "200ms", "maxBackoff": "1s"}},
			Policy{MaxAttempts: 4, Backoff: 200 * time.Millisecond, MaxBackoff: time.Second}, false},
		{"attempts clamped", map[string]any{"retry": map[string]any{"maxAttempts": float64(1000)}},
			Policy{MaxAttempts: MaxAttemptsLimit, Backoff: DefaultBackoff, MaxBackoff: DefaultMaxBackoff}, false},
		{"max backoff clamped", map[string]any{"retry": map[string]any{"maxAttempts": float64(2), "maxBackoff": "10h"}},
			Policy{MaxAttempts: 2, Backoff: DefaultBackoff, MaxBackoff: MaxBackoffLimit}, false},
		{"bad duration", map[string]any{"retry": map[string]any{"maxAttempts": float64(2), "backoff": "soon"}}, Policy{}, true},
		{"bad pattern", map[string]any{"retry": map[string]any{"maxAttempts": float64(2), "on": "("}}, Policy{}, true},
		{"not an object", map[string]any{"retry": "3"}, Policy{}, true},
		{"bad attempts", map[string]any{"retry": map[string]any{"maxAttempts": "many"}}, Policy{}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Parse(tc.config)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tc.wantErr)
			}
			if tc.wantErr {
				return
			}
			got.On = nil
			if got != tc.want {
				t.Fatalf("Parse = %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestDelayIsExponentialAndCapped(t *testing.T) {
	p := Policy{MaxAttempts: 10, Backoff: 100 * time.Millisecond, MaxBackoff: time.Second}
	want := []time.Duration{100 * time.Millisecond, 200 * time.Millisecond, 400 * time.Millisecond, 800 * time.Millisecond, time.Second, time.Second}
	for i, w := range want {
		if got := p.Delay(i + 1); got != w {
			t.Errorf("Delay(%d) = %v, want %v", i+1, got, w)
		}
	}
}

func TestRunRetriesUntilSuccess(t *testing.T) {
	p := Policy{MaxAttempts: 5, Backoff: time.Millisecond, MaxBackoff: time.Millisecond}
	calls, discarded, retries := 0, 0, 0
	got, err := Run(t.Context(), p,
		func() (int, error) {
			calls++
			if calls < 3 {
				return calls, errors.New("transient")
			}
			return calls, nil
		},
		func(int) { discarded++ },
		func(int, time.Duration, error) { retries++ },
	)
	if err != nil || got != 3 || calls != 3 {
		t.Fatalf("got %d, err %v, calls %d", got, err, calls)
	}
	if discarded != 2 || retries != 2 {
		t.Fatalf("discarded %d, retries %d; want 2 and 2", discarded, retries)
	}
}

func TestRunStopsAtMaxAttemptsAndReturnsTheLastResult(t *testing.T) {
	p := Policy{MaxAttempts: 3, Backoff: time.Millisecond, MaxBackoff: time.Millisecond}
	calls := 0
	got, err := Run(t.Context(), p,
		func() (int, error) { calls++; return calls, errors.New("down") },
		func(int) {}, nil)
	if err == nil || calls != 3 || got != 3 {
		t.Fatalf("got %d, err %v, calls %d; want the third attempt's result and error", got, err, calls)
	}
}

func TestRunOnlyRetriesMatchingErrors(t *testing.T) {
	p, err := Parse(map[string]any{"retry": map[string]any{"maxAttempts": float64(5), "backoff": "1ms", "on": "(?i)timeout|429"}})
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	_, err = Run(t.Context(), p, func() (int, error) { calls++; return 0, errors.New("400 bad request") }, func(int) {}, nil)
	if err == nil || calls != 1 {
		t.Fatalf("a non-matching error was retried: calls = %d", calls)
	}
	calls = 0
	_, _ = Run(t.Context(), p, func() (int, error) { calls++; return 0, errors.New("status 429") }, func(int) {}, nil)
	if calls != 5 {
		t.Fatalf("a matching error was not retried: calls = %d", calls)
	}
}

func TestRunStopsWhenTheContextEnds(t *testing.T) {
	p := Policy{MaxAttempts: 5, Backoff: time.Hour, MaxBackoff: time.Hour}
	ctx, cancel := context.WithCancel(t.Context())
	calls := 0
	done := make(chan error, 1)
	go func() {
		_, err := Run(ctx, p, func() (int, error) { calls++; return 0, errors.New("down") }, func(int) {}, nil)
		done <- err
	}()
	time.Sleep(20 * time.Millisecond)
	cancel()
	select {
	case err := <-done:
		if err == nil || calls != 1 {
			t.Fatalf("err = %v, calls = %d", err, calls)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run kept waiting out its backoff after the context was cancelled")
	}
}

func TestRunDoesNotRetryAContextError(t *testing.T) {
	p := Policy{MaxAttempts: 5, Backoff: time.Millisecond, MaxBackoff: time.Millisecond}
	calls := 0
	_, _ = Run(t.Context(), p, func() (int, error) { calls++; return 0, context.Canceled }, func(int) {}, nil)
	if calls != 1 {
		t.Fatalf("a cancelled call was retried %d times", calls-1)
	}
}
