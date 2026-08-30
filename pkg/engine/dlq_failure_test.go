package engine

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/user/hermod"
	"github.com/user/hermod/pkg/comm/message"
)

// A dead-letter sink that refuses everything, and counts what it was asked to
// take.
type refusingDLQ struct {
	mu sync.Mutex
	n  int
}

func (d *refusingDLQ) Write(context.Context, hermod.Message) error {
	d.mu.Lock()
	d.n++
	d.mu.Unlock()
	return errors.New("dead-letter sink is unreachable")
}
func (d *refusingDLQ) Ping(context.Context) error { return nil }
func (d *refusingDLQ) Close() error               { return nil }

func (d *refusingDLQ) count() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.n
}

// A failed park is not a delivery.
//
// writeToDLQ used to return nothing, so every caller followed it with
// `return nil` — telling the engine the message was delivered. One of them
// said so in a comment: "Message preserved in DLQ". That was a claim, not a
// check. With the sink unreachable the failure was logged, a metric was
// incremented, and the message was acknowledged and lost — precisely what a
// dead-letter sink exists to prevent.
//
// The engine already held the opposite position a few lines away, in the
// branch for a workflow with no dead-letter sink at all: it deliberately does
// not acknowledge, because retention is visible and recoverable while a silent
// drop is neither. A DLQ write that failed leaves the message in exactly that
// position.
func TestAFailedDeadLetterParkIsReportedAsAFailure(t *testing.T) {
	dlq := &refusingDLQ{}
	eng := NewEngine(&countingSource{}, []hermod.Sink{&countingSink{done: make(chan struct{})}}, nil)
	eng.SetLogger(&testLogger{})
	eng.SetDeadLetterSink(dlq)

	msg := message.AcquireMessage()
	t.Cleanup(msg.Release)
	msg.SetID("m-1")

	err := eng.writeToDLQ(context.Background(), "sink-a", msg)
	if err == nil {
		t.Fatal("a dead-letter sink that refused the message reported success; " +
			"every caller turns that into an acknowledgement, so the message is lost")
	}
	if !strings.Contains(err.Error(), "dead-letter") {
		t.Errorf("the error does not say what failed: %v", err)
	}
	if dlq.count() != 1 {
		t.Errorf("the sink was asked %d times, want 1", dlq.count())
	}
}

// A sink that accepts still reports success, so the failure path above is not
// simply refusing everything.
func TestASuccessfulDeadLetterParkReportsSuccess(t *testing.T) {
	accepting := &countingSink{done: make(chan struct{})}
	eng := NewEngine(&countingSource{}, []hermod.Sink{&countingSink{done: make(chan struct{})}}, nil)
	eng.SetLogger(&testLogger{})
	eng.SetDeadLetterSink(accepting)

	msg := message.AcquireMessage()
	t.Cleanup(msg.Release)
	msg.SetID("m-2")

	if err := eng.writeToDLQ(context.Background(), "sink-a", msg); err != nil {
		t.Fatalf("a dead-letter sink that accepted the message reported failure: %v", err)
	}
}

// With no dead-letter sink configured there is nothing to park and nothing to
// report — that case is handled by the caller not acknowledging, and must not
// be turned into an error here.
func TestNoDeadLetterSinkIsNotAnError(t *testing.T) {
	eng := NewEngine(&countingSource{}, []hermod.Sink{&countingSink{done: make(chan struct{})}}, nil)
	eng.SetLogger(&testLogger{})

	msg := message.AcquireMessage()
	t.Cleanup(msg.Release)
	msg.SetID("m-3")

	if err := eng.writeToDLQ(context.Background(), "sink-a", msg); err != nil {
		t.Errorf("writeToDLQ reported an error with no sink configured: %v", err)
	}
}
