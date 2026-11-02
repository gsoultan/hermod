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
	"context"
	"sync"
	"testing"
	"time"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/pkg/comm/buffer"
	"github.com/gsoultan/hermod/pkg/comm/message"
	"github.com/gsoultan/hermod/pkg/engine/config"
)

// heardDeliveries collects what a DeliveredObserver was told, copying the
// payload during the callback as a real observer must.
type heardDeliveries struct {
	mu     sync.Mutex
	sinks  []string
	bodies []string
	heard  chan struct{}
	once   sync.Once
}

func (h *heardDeliveries) observe(sinkID string, m hermod.Message) {
	h.mu.Lock()
	h.sinks = append(h.sinks, sinkID)
	h.bodies = append(h.bodies, string(m.Payload()))
	h.mu.Unlock()
	h.once.Do(func() { close(h.heard) })
}

func runUntilAcked(t *testing.T, eng *Engine, source *recordingAckSource) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	go func() { _ = eng.Start(ctx) }()
	select {
	case <-source.ackedC:
	case <-ctx.Done():
		t.Fatal("the engine never acknowledged the message")
	}
}

// A record a sink took is reported with the sink that took it and the
// message as that sink received it, so the registry can close a dead letter
// of the same record that is still waiting there.
func TestTheDeliveredObserverHearsWhatEachSinkTook(t *testing.T) {
	msg := message.AcquireMessage()
	msg.SetID("m-1")
	msg.SetPayload([]byte(`{"doc":"A"}`))
	source := &recordingAckSource{msg: msg, ackedC: make(chan struct{})}
	eng := NewEngine(source, []hermod.Sink{&counterSink{}}, buffer.NewRingBuffer(8))
	eng.SetSinkIDs([]string{"panmail"})
	heard := &heardDeliveries{heard: make(chan struct{})}
	eng.SetDeliveredObserver(heard.observe)

	runUntilAcked(t, eng, source)
	select {
	case <-heard.heard:
	case <-time.After(2 * time.Second):
		t.Fatal("the observer heard no delivery")
	}
	heard.mu.Lock()
	defer heard.mu.Unlock()
	if heard.sinks[0] != "panmail" || heard.bodies[0] != `{"doc":"A"}` {
		t.Fatalf("heard sink %q with %q, want panmail with the delivered payload", heard.sinks[0], heard.bodies[0])
	}
}

// A message a sink refused and the engine parked was not delivered, and the
// observer must not hear it as if it were.
func TestTheDeliveredObserverDoesNotHearAParkedMessage(t *testing.T) {
	msg := message.AcquireMessage()
	msg.SetID("m-1")
	msg.SetPayload([]byte(`{"doc":"A"}`))
	source := &recordingAckSource{msg: msg, ackedC: make(chan struct{})}
	eng := NewEngine(source, []hermod.Sink{refusingSink{}}, buffer.NewRingBuffer(8))
	cfg := config.DefaultConfig()
	cfg.MaxRetries = 1
	cfg.RetryInterval = time.Millisecond
	eng.SetConfig(cfg)
	eng.SetSinkIDs([]string{"panmail"})
	eng.SetDeadLetterSink(dlqSink{})
	heard := &heardDeliveries{heard: make(chan struct{})}
	eng.SetDeliveredObserver(heard.observe)

	runUntilAcked(t, eng, source)
	heard.mu.Lock()
	defer heard.mu.Unlock()
	if len(heard.sinks) != 0 {
		t.Fatalf("the observer heard a parked message as delivered to %v", heard.sinks)
	}
}
