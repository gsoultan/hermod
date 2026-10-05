package reply

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/gsoultan/hermod/pkg/comm/message"
)

func awaited(t *testing.T) (*Pending, string) {
	t.Helper()
	msg := message.AcquireMessage()
	t.Cleanup(func() { message.ReleaseMessage(msg) })
	p, err := Expect(msg)
	if err != nil {
		t.Fatalf("Expect: %v", err)
	}
	t.Cleanup(p.Cancel)
	id, ok := IDOf(msg)
	if !ok || id == "" {
		t.Fatal("an awaited message carries no reply id for the engine to find")
	}
	return p, id
}

// The caller that asked to wait gets what the engine resolved.
func TestTheWaiterReceivesTheOutcome(t *testing.T) {
	p, id := awaited(t)

	want := Outcome{Status: Delivered, Record: []byte(`{"order_id":1}`)}
	if !Resolve(id, want) {
		t.Fatal("Resolve found no waiter for a message that is awaited")
	}

	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	got, err := p.Wait(ctx)
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if got.Status != Delivered || string(got.Record) != `{"order_id":1}` {
		t.Errorf("the waiter received %+v", got)
	}
}

// A message nobody is waiting for has no reply id, which is how the engine
// knows to do nothing for it.
func TestAMessageNobodyAwaitsHasNoReplyID(t *testing.T) {
	msg := message.AcquireMessage()
	defer message.ReleaseMessage(msg)
	if id, ok := IDOf(msg); ok {
		t.Errorf("a plain message reports reply id %q", id)
	}
}

// The caller gives up at its timeout; the workflow carries on. Its outcome,
// when it comes, has nowhere to go and must not block or leak.
func TestAnOutcomeAfterTheWaiterGaveUpIsDropped(t *testing.T) {
	p, id := awaited(t)

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Millisecond)
	defer cancel()
	if _, err := p.Wait(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Wait returned %v, want the deadline", err)
	}
	p.Cancel()

	if Resolve(id, Outcome{Status: Delivered}) {
		t.Error("an outcome was delivered to a waiter that had already given up")
	}
	if n := pendingCount(); n != 0 {
		t.Errorf("%d waiters are still registered after the only one gave up", n)
	}
}

// One message has one outcome. A second resolve — a replayed message that
// still carries the id — must not reach anyone.
func TestAMessageIsResolvedOnce(t *testing.T) {
	p, id := awaited(t)
	if !Resolve(id, Outcome{Status: Failed, Error: "first"}) {
		t.Fatal("the first resolve found no waiter")
	}
	if Resolve(id, Outcome{Status: Delivered}) {
		t.Error("a second resolve was accepted for the same message")
	}
	got, err := p.Wait(t.Context())
	if err != nil || got.Error != "first" {
		t.Errorf("the waiter received %+v, %v", got, err)
	}
}

// Every waiter is a request held open. The number held at once is bounded, so
// a flood of synchronous requests against a stalled workflow cannot grow the
// table without limit.
func TestTheNumberOfWaitersIsBounded(t *testing.T) {
	restore := setMaxPending(2)
	defer restore()

	p1, _ := awaited(t)
	p2, _ := awaited(t)

	msg := message.AcquireMessage()
	defer message.ReleaseMessage(msg)
	if _, err := Expect(msg); !errors.Is(err, ErrTooManyPending) {
		t.Fatalf("a third waiter was accepted past a limit of two: %v", err)
	}
	if _, ok := IDOf(msg); ok {
		t.Error("the refused message was still marked as awaited")
	}

	p1.Cancel()
	p2.Cancel()
	p3, err := Expect(msg)
	if err != nil {
		t.Fatalf("no waiter could be registered after the others were released: %v", err)
	}
	p3.Cancel()
}

func TestModeOf(t *testing.T) {
	cases := []struct {
		name    string
		config  map[string]string
		wait    bool
		timeout time.Duration
	}{
		{"nothing set answers at once", map[string]string{}, false, 0},
		{"async answers at once", map[string]string{"response_mode": "async", "response_timeout": "5s"}, false, 0},
		{"sync with a timeout", map[string]string{"response_mode": "sync", "response_timeout": "5s"}, true, 5 * time.Second},
		{"sync with no timeout takes the default", map[string]string{"response_mode": "sync"}, true, DefaultTimeout},
		{"an unreadable timeout is the default", map[string]string{"response_mode": "sync", "response_timeout": "soon"}, true, DefaultTimeout},
		{"a zero timeout is the default, not forever", map[string]string{"response_mode": "sync", "response_timeout": "0s"}, true, DefaultTimeout},
		{"a timeout past the cap is capped", map[string]string{"response_mode": "sync", "response_timeout": "24h"}, true, MaxTimeout},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			wait, timeout := ModeOf(tc.config)
			if wait != tc.wait || timeout != tc.timeout {
				t.Errorf("ModeOf = %v, %v; want %v, %v", wait, timeout, tc.wait, tc.timeout)
			}
		})
	}
}

// The engine takes the id as it starts on a message, and the message goes on
// without it.
func TestTakeRemovesTheIDFromTheMessage(t *testing.T) {
	msg := message.AcquireMessage()
	defer message.ReleaseMessage(msg)
	p, err := Expect(msg)
	if err != nil {
		t.Fatalf("Expect: %v", err)
	}
	defer p.Cancel()

	id, ok := Take(msg)
	if !ok || id == "" {
		t.Fatal("Take found no id on an awaited message")
	}
	if _, still := msg.Metadata()[MetaReplyID]; still {
		t.Error("the message still carries the reply id after it was taken")
	}
	if _, again := Take(msg); again {
		t.Error("the id was taken twice")
	}
	if !Resolve(id, Outcome{Status: Delivered}) {
		t.Error("the id that was taken does not resolve the waiter")
	}
}
