package sink

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/user/hermod"
)

type state int

const (
	stateClosed state = iota
	stateOpen
	stateHalfOpen
)

type CircuitBreakerSink struct {
	hermod.Sink
	mu           sync.RWMutex
	state        state
	failureCount int
	threshold    int
	timeout      time.Duration
	lastFailure  time.Time
}

func NewCircuitBreakerSink(s hermod.Sink, threshold int, timeout time.Duration) *CircuitBreakerSink {
	return &CircuitBreakerSink{
		Sink:      s,
		threshold: threshold,
		timeout:   timeout,
	}
}

func (s *CircuitBreakerSink) Write(ctx context.Context, msg hermod.Message) error {
	if !s.allowRequest() {
		return errors.New("circuit breaker is open")
	}

	err := s.Sink.Write(ctx, msg)
	s.recordResult(err)
	return err
}

func (s *CircuitBreakerSink) allowRequest() bool {
	s.mu.RLock()
	st := s.state
	last := s.lastFailure
	s.mu.RUnlock()

	if st == stateClosed {
		return true
	}
	if st == stateOpen {
		if time.Since(last) > s.timeout {
			s.mu.Lock()
			s.state = stateHalfOpen
			s.mu.Unlock()
			return true
		}
		return false
	}
	return true
}

func (s *CircuitBreakerSink) recordResult(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err == nil {
		s.handleSuccess()
	} else {
		s.handleFailure()
	}
}

func (s *CircuitBreakerSink) handleSuccess() {
	s.failureCount = 0
	if s.state == stateHalfOpen {
		s.state = stateClosed
	}
}

func (s *CircuitBreakerSink) handleFailure() {
	s.failureCount++
	s.lastFailure = time.Now()
	if s.failureCount >= s.threshold {
		s.state = stateOpen
	}
}
