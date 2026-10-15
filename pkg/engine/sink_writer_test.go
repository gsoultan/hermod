// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package engine

import (
	"testing"
	"time"

	"github.com/gsoultan/hermod/pkg/engine/config"
)

func TestCircuitBreakerTransitions(t *testing.T) {
	e := NewEngine(nil, nil, nil) // &Engine{config: config.DefaultConfig()}
	sw := &sinkWriter{
		engine: e,
		sinkID: "s1",
		config: config.SinkConfig{
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
