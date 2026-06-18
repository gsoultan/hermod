package sink

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/user/hermod"
)

// fakeBatchSink is a controllable sink that implements both hermod.Sink and
// hermod.BatchSink, recording invocation counts and returning a fixed error.
type fakeBatchSink struct {
	hermod.Sink
	err         error
	writeCalls  int
	batchCalls  int
	failTimes   int // number of initial calls that should fail before succeeding
	currentCall int
}

func (f *fakeBatchSink) Write(_ context.Context, _ hermod.Message) error {
	f.writeCalls++
	return f.next()
}

func (f *fakeBatchSink) WriteBatch(_ context.Context, _ []hermod.Message) error {
	f.batchCalls++
	return f.next()
}

func (f *fakeBatchSink) next() error {
	f.currentCall++
	if f.failTimes > 0 && f.currentCall <= f.failTimes {
		return f.err
	}
	if f.err != nil && f.failTimes == 0 {
		return f.err
	}
	return nil
}

func TestCircuitBreakerSink_WriteBatchOpensAndRejects(t *testing.T) {
	failing := &fakeBatchSink{err: errors.New("boom")}
	cb := NewCircuitBreakerSink(failing, 2, time.Minute)

	// Two failing batch writes should trip the breaker.
	for i := range 2 {
		if err := cb.WriteBatch(t.Context(), nil); err == nil {
			t.Fatalf("call %d: expected error from underlying sink", i)
		}
	}

	// Breaker should now be open and reject without touching the underlying sink.
	gotCalls := failing.batchCalls
	err := cb.WriteBatch(t.Context(), nil)
	if !errors.Is(err, ErrCircuitOpen) {
		t.Fatalf("expected ErrCircuitOpen, got %v", err)
	}
	if failing.batchCalls != gotCalls {
		t.Fatalf("breaker should not call underlying sink when open: before=%d after=%d", gotCalls, failing.batchCalls)
	}
}

func TestRetrySink_WriteSucceedsAfterRetries(t *testing.T) {
	// Fail the first two attempts, succeed on the third.
	s := &fakeBatchSink{err: errors.New("transient"), failTimes: 2}
	rs := NewRetrySink(s, 3, time.Millisecond, nil)

	if err := rs.Write(t.Context(), nil); err != nil {
		t.Fatalf("expected success after retries, got %v", err)
	}
	if s.writeCalls != 3 {
		t.Fatalf("expected 3 write attempts, got %d", s.writeCalls)
	}
}

func TestRetrySink_WriteDoesNotSleepAfterFinalAttempt(t *testing.T) {
	// All attempts fail; with a large interval the call must still return
	// promptly because we must not sleep after the final attempt.
	s := &fakeBatchSink{err: errors.New("perma")}
	rs := NewRetrySink(s, 2, 200*time.Millisecond, nil)

	start := time.Now()
	err := rs.Write(t.Context(), nil)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected failure after exhausting retries")
	}
	if s.writeCalls != 2 {
		t.Fatalf("expected 2 attempts, got %d", s.writeCalls)
	}
	// Only one inter-attempt wait (~200ms) should occur, never two.
	if elapsed >= 400*time.Millisecond {
		t.Fatalf("retry slept after final attempt: elapsed=%v", elapsed)
	}
}
