package engine

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/pkg/comm/buffer"
	"github.com/gsoultan/hermod/pkg/comm/message"
	"github.com/gsoultan/hermod/pkg/comm/reply"
)

// What a waiting caller is told.
//
// A push source answers "dispatched" the moment a message is queued, so its
// caller never learns what the workflow did with it. A transport that waits
// instead marks the message with reply.Expect, and the engine has to resolve
// it — once, whatever happened: delivered, parked in the dead-letter sink,
// failed, or nothing to write. A path through processMessage that returns
// without resolving leaves a caller hanging until its timeout, so each one is
// pinned here.

// awaitedSource hands the engine one message that a caller is waiting for.
type awaitedSource struct {
	mu   sync.Mutex
	sent bool
	acks int
	p    *reply.Pending
	err  error
}

func (s *awaitedSource) Read(ctx context.Context) (hermod.Message, error) {
	s.mu.Lock()
	if s.sent {
		s.mu.Unlock()
		<-ctx.Done()
		return nil, ctx.Err()
	}
	s.sent = true
	m := message.AcquireMessage()
	m.SetID("m-1")
	m.SetPayload([]byte(`{"order_id":7}`))
	s.p, s.err = reply.Expect(m)
	s.mu.Unlock()
	return m, nil
}

func (s *awaitedSource) Ack(context.Context, hermod.Message) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.acks++
	return nil
}

func (s *awaitedSource) acked() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.acks
}
func (s *awaitedSource) Ping(context.Context) error { return nil }
func (s *awaitedSource) Close() error               { return nil }

func (s *awaitedSource) pending(t *testing.T) *reply.Pending {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		s.mu.Lock()
		p, err := s.p, s.err
		s.mu.Unlock()
		if err != nil {
			t.Fatalf("Expect: %v", err)
		}
		if p != nil {
			return p
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("the engine never read the message")
	return nil
}

// rejectingSink refuses every write, and says another attempt cannot help.
type rejectingSink struct{}

func (rejectingSink) Write(context.Context, hermod.Message) error {
	return errors.Join(hermod.ErrPermanent, errors.New("the destination rejected the row"))
}
func (rejectingSink) Ping(context.Context) error { return nil }
func (rejectingSink) Close() error               { return nil }

// outcomeOf runs the engine until the awaited message is resolved.
func outcomeOf(t *testing.T, eng *Engine, src *awaitedSource) reply.Outcome {
	t.Helper()
	eng.SetLogger(&testLogger{})

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = eng.Start(ctx)
	}()
	defer func() {
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("the engine did not stop")
		}
	}()

	p := src.pending(t)
	defer p.Cancel()
	waitCtx, stop := context.WithTimeout(t.Context(), 5*time.Second)
	defer stop()
	o, err := p.Wait(waitCtx)
	if err != nil {
		t.Fatalf("the caller was never told what happened to its message: %v", err)
	}
	return o
}

// metadataSink records the metadata keys of what it is sent.
type metadataSink struct {
	mu   sync.Mutex
	n    int
	keys map[string]bool
}

func (s *metadataSink) Write(_ context.Context, msg hermod.Message) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.n++
	if s.keys == nil {
		s.keys = make(map[string]bool)
	}
	for k := range msg.Metadata() {
		s.keys[k] = true
	}
	return nil
}
func (*metadataSink) Ping(context.Context) error { return nil }
func (*metadataSink) Close() error               { return nil }

func (s *metadataSink) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.n
}

func (s *metadataSink) sawMetadata(key string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.keys[key]
}

func TestAWaitingCallerIsToldTheMessageWasDelivered(t *testing.T) {
	src := &awaitedSource{}
	sink := &metadataSink{}
	eng := NewEngine(src, []hermod.Sink{sink}, buffer.NewRingBuffer(8))

	o := outcomeOf(t, eng, src)

	if o.Status != reply.Delivered {
		t.Fatalf("status %q (%s), want delivered", o.Status, o.Error)
	}
	if o.Error != "" {
		t.Errorf("a delivered message carries the error %q", o.Error)
	}
	if !strings.Contains(string(o.Record), `"order_id":7`) {
		t.Errorf("the reply does not carry the record that was delivered: %s", o.Record)
	}
	// The reply id is how the engine finds the caller. It is plumbing: it is
	// taken off the message before the workflow sees it, so neither a sink nor
	// the caller is handed it.
	if strings.Contains(string(o.Record), reply.MetaReplyID) {
		t.Errorf("the record handed back carries the reply id: %s", o.Record)
	}
	if sink.sawMetadata(reply.MetaReplyID) {
		t.Error("the sink was sent the reply id")
	}
	if sink.count() != 1 {
		t.Errorf("the caller was told delivered and the sink was written %d times", sink.count())
	}
}

// The record in the reply is the one the workflow produced, not the one the
// caller sent.
func TestTheReplyCarriesTheRecordAsTheWorkflowLeftIt(t *testing.T) {
	src := &awaitedSource{}
	eng := NewEngine(src, []hermod.Sink{newTallySink()}, buffer.NewRingBuffer(8))
	eng.SetRouter(func(_ context.Context, msg hermod.Message) ([]RoutedMessage, error) {
		out := msg.Clone()
		out.SetData("total", 42)
		return []RoutedMessage{{SinkIndex: 0, Message: out}}, nil
	})

	o := outcomeOf(t, eng, src)

	if o.Status != reply.Delivered {
		t.Fatalf("status %q (%s), want delivered", o.Status, o.Error)
	}
	if !strings.Contains(string(o.Record), `"total":42`) {
		t.Errorf("the reply carries the record as it arrived, not as it was delivered: %s", o.Record)
	}
}

func TestAWaitingCallerIsToldWhyTheSinkRefused(t *testing.T) {
	src := &awaitedSource{}
	eng := NewEngine(src, []hermod.Sink{rejectingSink{}}, buffer.NewRingBuffer(8))
	eng.SetConfig(Config{MaxRetries: 1, RetryInterval: 10 * time.Millisecond, StatusInterval: time.Second})

	o := outcomeOf(t, eng, src)

	if o.Status != reply.Failed {
		t.Fatalf("status %q, want failed", o.Status)
	}
	if !strings.Contains(o.Error, "the destination rejected the row") {
		t.Errorf("the reply does not say why it failed: %q", o.Error)
	}
}

// A failed write that was parked is preserved, and the caller is told that
// rather than "delivered": the engine acknowledges both the same way.
func TestAWaitingCallerIsToldTheMessageWasDeadLettered(t *testing.T) {
	src := &awaitedSource{}
	dlq := newTallySink()
	eng := NewEngine(src, []hermod.Sink{rejectingSink{}}, buffer.NewRingBuffer(8))
	eng.SetConfig(Config{MaxRetries: 1, RetryInterval: 10 * time.Millisecond, StatusInterval: time.Second})
	eng.SetDeadLetterSink(dlq)

	o := outcomeOf(t, eng, src)

	if o.Status != reply.DeadLettered {
		t.Fatalf("status %q (%s), want dead_lettered", o.Status, o.Error)
	}
	if !strings.Contains(o.Error, "the destination rejected the row") {
		t.Errorf("the reply does not say why it was parked: %q", o.Error)
	}
	if dlq.count() != 1 {
		t.Errorf("the caller was told dead_lettered and the dead-letter sink holds %d", dlq.count())
	}
}

func TestAWaitingCallerIsToldRoutingFailed(t *testing.T) {
	src := &awaitedSource{}
	eng := NewEngine(src, []hermod.Sink{newTallySink()}, buffer.NewRingBuffer(8))
	eng.SetRouter(func(context.Context, hermod.Message) ([]RoutedMessage, error) {
		return nil, errors.New("node T could not parse the payload")
	})

	o := outcomeOf(t, eng, src)

	if o.Status != reply.Failed || !strings.Contains(o.Error, "node T could not parse the payload") {
		t.Errorf("got %q / %q, want failed with the routing error", o.Status, o.Error)
	}
}

// A node that failed parked the message itself and routed nothing.
func TestAWaitingCallerIsToldANodeDeadLetteredTheMessage(t *testing.T) {
	src := &awaitedSource{}
	dlq := newTallySink()
	eng := NewEngine(src, []hermod.Sink{newTallySink()}, buffer.NewRingBuffer(8))
	eng.SetDeadLetterSink(dlq)
	eng.SetRouter(func(ctx context.Context, msg hermod.Message) ([]RoutedMessage, error) {
		eng.DeadLetterNodeFailure(ctx, "T", msg, errors.New("transformation could not parse the payload"))
		return nil, nil
	})

	o := outcomeOf(t, eng, src)

	if o.Status != reply.DeadLettered || !strings.Contains(o.Error, "transformation could not parse the payload") {
		t.Errorf("got %q / %q, want dead_lettered with the node's error", o.Status, o.Error)
	}
}

// The workflow has a sink and reached none: the engine does not acknowledge
// this, and the caller must not be told it worked.
func TestAWaitingCallerIsToldNoSinkWasReached(t *testing.T) {
	src := &awaitedSource{}
	sink := newTallySink()
	eng := NewEngine(src, []hermod.Sink{sink}, buffer.NewRingBuffer(8))
	eng.SetRouter(func(context.Context, hermod.Message) ([]RoutedMessage, error) {
		return nil, nil
	})

	o := outcomeOf(t, eng, src)

	if o.Status != reply.Failed {
		t.Fatalf("status %q, want failed", o.Status)
	}
	if !strings.Contains(o.Error, "no sink") {
		t.Errorf("the reply does not say that no sink was reached: %q", o.Error)
	}
}

// A sink node that writes inline routes nothing and has delivered.
func TestAWaitingCallerIsToldAnInlineWriteDelivered(t *testing.T) {
	src := &awaitedSource{}
	eng := NewEngine(src, []hermod.Sink{newTallySink()}, buffer.NewRingBuffer(8))
	eng.SetRouter(func(_ context.Context, msg hermod.Message) ([]RoutedMessage, error) {
		msg.SetMetadata(MetaDeliveredInline, "true")
		return nil, nil
	})

	o := outcomeOf(t, eng, src)

	if o.Status != reply.Delivered {
		t.Errorf("status %q (%s), want delivered", o.Status, o.Error)
	}
}

func TestAWaitingCallerIsToldValidationFailed(t *testing.T) {
	src := &awaitedSource{}
	eng := NewEngine(src, []hermod.Sink{newTallySink()}, buffer.NewRingBuffer(8))
	eng.SetValidator(alwaysInvalid{})

	o := outcomeOf(t, eng, src)

	if o.Status != reply.Failed || !strings.Contains(o.Error, `field "id" is required`) {
		t.Errorf("got %q / %q, want failed with the validation error", o.Status, o.Error)
	}
}

// A dry run writes nothing by design. The caller is told that, not that the
// record was delivered.
func TestAWaitingCallerIsToldADryRunWroteNothing(t *testing.T) {
	src := &awaitedSource{}
	sink := newTallySink()
	eng := NewEngine(src, []hermod.Sink{sink}, buffer.NewRingBuffer(8))
	eng.SetConfig(Config{DryRun: true, StatusInterval: time.Second})

	o := outcomeOf(t, eng, src)

	if o.Status != reply.Completed {
		t.Errorf("status %q (%s), want completed", o.Status, o.Error)
	}
	if sink.count() != 0 {
		t.Errorf("a dry run wrote %d record(s)", sink.count())
	}
}

// The workflow dropped the message on purpose. That is not a failure: the
// source is acknowledged, nothing is parked, and the caller is told so —
// unlike the message no sink could be resolved for, which looks the same
// without the marker and is neither acknowledged nor called handled.
func TestAWaitingCallerIsToldAFilterDroppedTheMessage(t *testing.T) {
	src := &awaitedSource{}
	sink := newTallySink()
	dlq := newTallySink()
	eng := NewEngine(src, []hermod.Sink{sink}, buffer.NewRingBuffer(8))
	eng.SetDeadLetterSink(dlq)
	eng.SetRouter(func(_ context.Context, msg hermod.Message) ([]RoutedMessage, error) {
		msg.SetMetadata(MetaFiltered, "true")
		return nil, nil
	})

	o := outcomeOf(t, eng, src)

	if o.Status != reply.Filtered || o.Error != "" {
		t.Errorf("got %q / %q, want filtered", o.Status, o.Error)
	}
	if dlq.count() != 0 {
		t.Errorf("a message the workflow dropped on purpose was parked in the dead-letter sink %d time(s)", dlq.count())
	}
	if sink.count() != 0 {
		t.Errorf("a filtered message reached the sink %d time(s)", sink.count())
	}
	if src.acked() != 1 {
		t.Errorf("a filtered message was acknowledged %d time(s), want 1", src.acked())
	}
}
