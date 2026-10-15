// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Hermod: real-time Postgres CDC and data pipelines in one Go binary.
// Each file is proprietary and confidential; see LICENSE for the terms.
// Resale, sublicensing or selling it to any third party is prohibited.
// Making copies, modifying or distributing it needs written permission
// Of the copyright holder, Gembit Soultan Shirazi.
// Distributed "AS IS", WITHOUT WARRANTY OF ANY KIND, express or implied.

package engine

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/pkg/comm/message"
)

// observed collects what a DeadLetterObserver was told. The message itself is
// only valid during the callback, so the payload is copied there, which is
// what a real observer has to do too.
type observed struct {
	mu      sync.Mutex
	letters []DeadLetter
	bodies  []string
}

func (o *observed) observe(d DeadLetter) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.letters = append(o.letters, d)
	o.bodies = append(o.bodies, string(d.Message.Payload()))
}

func (o *observed) all() ([]DeadLetter, []string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]DeadLetter(nil), o.letters...), append([]string(nil), o.bodies...)
}

// A sink that kept failing parks the message, and the observer hears which
// workflow, which sink, why, and what the message was.
func TestDeadLetterObserverHearsASinkFailure(t *testing.T) {
	sink := &scriptedRefuser{err: errTooBig}
	eng := engineWithRetries(t, 1)
	eng.workflowID = "wf-orders"
	eng.SetDeadLetterSink(&parkingSink{})
	obs := &observed{}
	eng.SetDeadLetterObserver(obs.observe)

	msg := message.AcquireMessage()
	t.Cleanup(msg.Release)
	msg.SetID("m-1")
	msg.SetPayload([]byte(`{"order":42}`))

	if err := eng.writeToSink(context.Background(), sink, msg, "sink-a", -1); err != nil {
		t.Fatalf("writeToSink = %v, want nil once parked", err)
	}

	letters, bodies := obs.all()
	if len(letters) != 1 {
		t.Fatalf("observer heard %d dead letters, want 1", len(letters))
	}
	got := letters[0]
	if got.WorkflowID != "wf-orders" || got.SinkID != "sink-a" || got.MessageID != "m-1" {
		t.Errorf("dead letter = %+v, want workflow wf-orders, sink sink-a, message m-1", got)
	}
	if got.Error != errTooBig.Error() {
		t.Errorf("Error = %q, want the sink's refusal %q", got.Error, errTooBig.Error())
	}
	if got.FailedAt.IsZero() {
		t.Error("FailedAt is zero")
	}
	if bodies[0] != `{"order":42}` {
		t.Errorf("payload = %q, want the parked message", bodies[0])
	}
}

// A node that rejected the message names the node.
func TestDeadLetterObserverHearsANodeFailure(t *testing.T) {
	eng := engineWithRetries(t, 0)
	eng.workflowID = "wf-orders"
	eng.SetDeadLetterSink(&parkingSink{})
	obs := &observed{}
	eng.SetDeadLetterObserver(obs.observe)

	msg := message.AcquireMessage()
	t.Cleanup(msg.Release)
	msg.SetID("m-2")

	if !eng.DeadLetterNodeFailure(t.Context(), "transform-1", msg, errors.New("field total is not a number")) {
		t.Fatal("node failure was not parked")
	}

	letters, _ := obs.all()
	if len(letters) != 1 {
		t.Fatalf("observer heard %d dead letters, want 1", len(letters))
	}
	if letters[0].NodeID != "transform-1" || letters[0].Error != "field total is not a number" {
		t.Errorf("dead letter = %+v, want node transform-1 and its error", letters[0])
	}
}

// A park that failed preserved nothing, so there is nothing in the queue to
// tell anyone about.
func TestDeadLetterObserverIsSilentWhenTheParkFails(t *testing.T) {
	eng := engineWithRetries(t, 0)
	eng.SetDeadLetterSink(&scriptedRefuser{err: errors.New("dlq down")})
	obs := &observed{}
	eng.SetDeadLetterObserver(obs.observe)

	msg := message.AcquireMessage()
	t.Cleanup(msg.Release)
	msg.SetID("m-3")

	if eng.DeadLetterNodeFailure(t.Context(), "transform-1", msg, errors.New("boom")) {
		t.Fatal("a refused park was reported as parked")
	}
	if err := eng.writeToDLQ(t.Context(), "sink-a", msg); err == nil {
		t.Fatal("a refused park was reported as parked")
	}
	if letters, _ := obs.all(); len(letters) != 0 {
		t.Errorf("observer heard %d dead letters for parks that failed, want 0", len(letters))
	}
}

// batchParkingSink accepts a whole batch at once.
type batchParkingSink struct{ parkingSink }

func (s *batchParkingSink) WriteBatch(_ context.Context, msgs []hermod.Message) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.parked += len(msgs)
	return nil
}

// A batch park tells the observer about every message in it.
func TestDeadLetterObserverHearsEveryMessageOfABatchPark(t *testing.T) {
	eng := engineWithRetries(t, 0)
	eng.SetDeadLetterSink(&batchParkingSink{})
	obs := &observed{}
	eng.SetDeadLetterObserver(obs.observe)

	a, b := message.AcquireMessage(), message.AcquireMessage()
	t.Cleanup(a.Release)
	t.Cleanup(b.Release)
	a.SetID("a")
	b.SetID("b")

	if err := eng.writeToDLQ(t.Context(), "sink-a", a, b); err != nil {
		t.Fatalf("writeToDLQ = %v", err)
	}
	letters, _ := obs.all()
	if len(letters) != 2 || letters[0].MessageID != "a" || letters[1].MessageID != "b" {
		t.Errorf("observer heard %+v, want a then b", letters)
	}
}
