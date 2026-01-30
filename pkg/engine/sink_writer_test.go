package engine

import (
	"testing"
	"time"
)

func TestCircuitBreakerTransitions(t *testing.T) {
	e := &Engine{config: DefaultConfig()}
	sw := &sinkWriter{
		engine: e,
		sinkID: "s1",
		config: SinkConfig{
			CircuitBreakerThreshold: 2,
			CircuitBreakerInterval:  10 * time.Second,
			CircuitBreakerCoolDown:  100 * time.Millisecond,
		},
	}

	// Initially closed
	if err := sw.checkCircuitBreaker(); err != nil {
		t.Fatalf("unexpected breaker error initially: %v", err)
	}

	// Two failures within interval should open the breaker
	sw.recordFailure()
	if sw.cbStatus != "closed" {
		t.Fatalf("expected closed after 1st failure, got %s", sw.cbStatus)
	}
	sw.recordFailure()
	if sw.cbStatus != "open" {
		t.Fatalf("expected open after 2nd failure, got %s", sw.cbStatus)
	}

	// Move time forward by modifying openUntil to past and check half-open
	sw.cbOpenUntil = time.Now().Add(-1 * time.Millisecond)
	if err := sw.checkCircuitBreaker(); err != nil {
		t.Fatalf("expected half-open transition, got error: %v", err)
	}
	if sw.cbStatus != "half-open" {
		t.Fatalf("expected half-open, got %s", sw.cbStatus)
	}

	// On success from half-open, close
	sw.recordSuccess()
	if sw.cbStatus != "closed" {
		t.Fatalf("expected closed after success, got %s", sw.cbStatus)
	}
}
