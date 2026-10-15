// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package sink

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/gsoultan/hermod"
)

// ErrCircuitOpen is returned when the circuit breaker is open and rejecting requests.
var ErrCircuitOpen = errors.New("circuit breaker is open")

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
		return ErrCircuitOpen
	}

	err := s.Sink.Write(ctx, msg)
	s.recordResult(err)
	return err
}

// WriteBatch routes batch writes through the circuit breaker. Without this
// method the embedded BatchSink's WriteBatch would be promoted directly,
// bypassing the breaker entirely.
func (s *CircuitBreakerSink) WriteBatch(ctx context.Context, msgs []hermod.Message) error {
	if !s.allowRequest() {
		return ErrCircuitOpen
	}

	bs, ok := s.Sink.(hermod.BatchSink)
	if !ok {
		for _, m := range msgs {
			if err := s.Write(ctx, m); err != nil {
				return err
			}
		}
		return nil
	}

	err := bs.WriteBatch(ctx, msgs)
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
