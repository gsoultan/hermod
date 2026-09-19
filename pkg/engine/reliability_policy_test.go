package engine

import (
	"context"
	"errors"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/pkg/comm/buffer"
	"github.com/gsoultan/hermod/pkg/comm/message"
	"github.com/gsoultan/hermod/pkg/engine/telemetry"
)

// A dry run is a preview, so it must leave the source exactly where it found
// it. Acknowledging is what advances a replication slot or a polling
// watermark, and a slot advanced past messages no sink ever received is data
// destroyed by the safety feature meant to prevent it.
func TestDryRunDoesNotAckSource(t *testing.T) {
	var acks atomic.Int64
	source := &mockSourceWithLimit{limit: 5, onAck: func() { acks.Add(1) }}
	sink := &mockSink{received: make(chan hermod.Message, 64)}
	rb := buffer.NewRingBuffer(16)

	eng := NewEngine(source, []hermod.Sink{sink}, rb)
	eng.SetConfig(Config{
		DryRun:         true,
		StatusInterval: 10 * time.Millisecond,
	})

	ctx, cancel := context.WithTimeout(t.Context(), 300*time.Millisecond)
	defer cancel()
	go func() { _ = eng.Start(ctx) }()

	<-ctx.Done()
	time.Sleep(50 * time.Millisecond)

	if n := acks.Load(); n != 0 {
		t.Errorf("dry-run acknowledged %d message(s) to the source; want 0", n)
	}
	if len(sink.received) != 0 {
		t.Errorf("dry-run wrote %d message(s) to the sink; want 0", len(sink.received))
	}
}

// The safe-mode and failed-validation diverts sit above the dry-run check in
// writeToSink, so a dry run used to perform real writes against the
// dead-letter sink. A dry run writes nowhere, the DLQ included.
func TestDryRunDoesNotWriteToDeadLetterSink(t *testing.T) {
	msg := message.AcquireMessage()
	msg.SetID("dryrun-dlq")
	msg.SetPayload([]byte(`{"status":"invalid"}`))

	source := &mockSource{msg: msg}
	sink := &validatingMockSink{
		mockSink: mockSink{received: make(chan hermod.Message, 64)},
		validateFunc: func(m hermod.Message) error {
			return errors.New("invalid payload")
		},
	}
	dlq := &mockSink{received: make(chan hermod.Message, 64)}
	rb := buffer.NewRingBuffer(16)

	eng := NewEngine(source, []hermod.Sink{sink}, rb)
	eng.SetDeadLetterSink(dlq)
	eng.SetConfig(Config{
		DryRun:         true,
		StatusInterval: 10 * time.Millisecond,
	})

	ctx, cancel := context.WithTimeout(t.Context(), 300*time.Millisecond)
	defer cancel()
	go func() { _ = eng.Start(ctx) }()

	<-ctx.Done()
	time.Sleep(50 * time.Millisecond)

	if n := len(dlq.received); n != 0 {
		t.Errorf("dry-run wrote %d message(s) to the dead-letter sink; want 0", n)
	}
}

// A dry run attempts nothing, so it cannot be evidence that a sink is
// unhealthy. Reporting it as failure would trip the circuit breaker on a sink
// that was never called.
func TestDryRunDoesNotCountAsSinkFailure(t *testing.T) {
	msg := message.AcquireMessage()
	msg.SetID("dryrun-breaker")
	msg.SetPayload([]byte("x"))

	source := &mockSource{msg: msg}
	sink := &mockSink{received: make(chan hermod.Message, 64)}
	rb := buffer.NewRingBuffer(16)

	eng := NewEngine(source, []hermod.Sink{sink}, rb)
	eng.SetConfig(Config{
		DryRun:         true,
		StatusInterval: 10 * time.Millisecond,
	})

	ctx, cancel := context.WithTimeout(t.Context(), 300*time.Millisecond)
	defer cancel()
	go func() { _ = eng.Start(ctx) }()

	<-ctx.Done()
	time.Sleep(50 * time.Millisecond)

	for id, status := range eng.GetStatus().SinkStatuses {
		if status == "reconnecting" || status == "circuit_breaker_open" {
			t.Errorf("sink %s reported %q after a dry run; no write was attempted", id, status)
		}
	}
}

// Dead-lettering only bumped a counter. The threshold alert lives in the
// registry's OnStatusChange callback, so without a status change nothing ever
// evaluated it: a pipeline quietly parking every message stayed "running" and
// silent. Crossing the threshold is itself the event.
func TestDeadLetterThresholdFiresStatusChange(t *testing.T) {
	source := &mockSource{}
	sink := &mockSink{received: make(chan hermod.Message, 64)}
	dlq := &mockSink{received: make(chan hermod.Message, 64)}
	rb := buffer.NewRingBuffer(16)

	eng := NewEngine(source, []hermod.Sink{sink}, rb)
	eng.SetDeadLetterSink(dlq)
	eng.SetConfig(Config{
		StatusInterval: 10 * time.Millisecond,
		DLQThreshold:   5,
	})

	var crossings atomic.Int64
	eng.SetOnStatusChange(func(u telemetry.StatusUpdate) {
		if u.DeadLetterCount >= 5 {
			crossings.Add(1)
		}
	})

	for i := 0; i < 12; i++ {
		m := message.AcquireMessage()
		m.SetID("dl-" + strconv.Itoa(i))
		if err := eng.writeToDLQ(t.Context(), "sink-1", m); err != nil {
			t.Fatalf("writeToDLQ: %v", err)
		}
	}

	if got := eng.GetStatus().DeadLetterCount; got != 12 {
		t.Fatalf("DeadLetterCount = %d, want 12", got)
	}
	// Exactly one: the alert is edge-triggered. Firing per message past the
	// line would have the registry write workflow, source and sink status rows
	// to storage on every dead-lettered message.
	if got := crossings.Load(); got != 1 {
		t.Errorf("threshold status updates = %d, want exactly 1", got)
	}
}

// A threshold of zero is "disabled" and must stay silent.
func TestDeadLetterThresholdDisabledStaysSilent(t *testing.T) {
	source := &mockSource{}
	sink := &mockSink{received: make(chan hermod.Message, 64)}
	dlq := &mockSink{received: make(chan hermod.Message, 64)}
	rb := buffer.NewRingBuffer(16)

	eng := NewEngine(source, []hermod.Sink{sink}, rb)
	eng.SetDeadLetterSink(dlq)
	eng.SetConfig(Config{StatusInterval: 10 * time.Millisecond, DLQThreshold: 0})

	var updates atomic.Int64
	eng.SetOnStatusChange(func(u telemetry.StatusUpdate) { updates.Add(1) })

	for i := 0; i < 20; i++ {
		m := message.AcquireMessage()
		m.SetID("dl-" + strconv.Itoa(i))
		if err := eng.writeToDLQ(t.Context(), "sink-1", m); err != nil {
			t.Fatalf("writeToDLQ: %v", err)
		}
	}

	if got := updates.Load(); got != 0 {
		t.Errorf("threshold disabled but %d status updates fired", got)
	}
}

// dlqCapableSink is a sink that can also be read as a source, which is what
// PrioritizeDLQ and the Drain DLQ button both require.
type dlqCapableSink struct {
	mockSink
}

func (s *dlqCapableSink) Read(ctx context.Context) (hermod.Message, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

func (s *dlqCapableSink) Ack(ctx context.Context, msg hermod.Message) error { return nil }

// DrainDLQ swaps the engine's source while the read loop is using it. Run
// under -race: the swap and the loop's Read/Ack must agree on a lock.
func TestDrainDLQIsSafeWhileRunning(t *testing.T) {
	msg := message.AcquireMessage()
	msg.SetID("drain-race")
	msg.SetPayload([]byte("x"))

	source := &mockSource{msg: msg}
	sink := &mockSink{received: make(chan hermod.Message, 1024)}
	dlq := &dlqCapableSink{mockSink: mockSink{received: make(chan hermod.Message, 1024)}}
	rb := buffer.NewRingBuffer(64)

	eng := NewEngine(source, []hermod.Sink{sink}, rb)
	eng.SetDeadLetterSink(dlq)
	eng.SetConfig(Config{StatusInterval: 10 * time.Millisecond})

	ctx, cancel := context.WithTimeout(t.Context(), 400*time.Millisecond)
	defer cancel()
	go func() { _ = eng.Start(ctx) }()

	time.Sleep(50 * time.Millisecond)
	if err := eng.DrainDLQ(ctx); err != nil {
		t.Fatalf("DrainDLQ: %v", err)
	}

	<-ctx.Done()
	time.Sleep(50 * time.Millisecond)
}

// A node failure in a dry run is not parked: nothing is acknowledged, so the
// message stays on the source and comes back on the next read.
func TestDryRunDoesNotParkNodeFailures(t *testing.T) {
	dlq := &mockSink{received: make(chan hermod.Message, 8)}
	eng := NewEngine(&mockSource{}, []hermod.Sink{&mockSink{}}, buffer.NewRingBuffer(8))
	eng.SetDeadLetterSink(dlq)
	eng.SetConfig(Config{DryRun: true})

	msg := message.AcquireMessage()
	msg.SetID("node-fail")

	if eng.DeadLetterNodeFailure(t.Context(), "transform-1", msg, errors.New("boom")) {
		t.Error("dry-run reported a node failure as parked; the caller would acknowledge it")
	}
	if len(dlq.received) != 0 {
		t.Error("dry-run wrote a node failure to the dead-letter sink")
	}
	if msg.Metadata()[MetaDeadLettered] == "true" {
		t.Error("message marked dead-lettered without being written anywhere")
	}
}

// The exception. A resumed message has no source row left to hold it, so
// declining the write destroys it rather than preserving it — and a dry run
// that loses data has defeated its own purpose.
func TestDryRunStillParksOrphanedMessages(t *testing.T) {
	dlq := &mockSink{received: make(chan hermod.Message, 8)}
	eng := NewEngine(&mockSource{}, []hermod.Sink{&mockSink{}}, buffer.NewRingBuffer(8))
	eng.SetDeadLetterSink(dlq)
	eng.SetConfig(Config{DryRun: true})

	msg := message.AcquireMessage()
	msg.SetID("resumed")

	if !eng.DeadLetterOrphanedMessage(t.Context(), "approval-1", msg, errors.New("boom")) {
		t.Fatal("a resumed message was not parked; with no source holding it, it is now lost")
	}
	if len(dlq.received) != 1 {
		t.Errorf("dead-letter sink received %d messages, want 1", len(dlq.received))
	}
}
