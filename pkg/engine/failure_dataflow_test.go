package engine

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/pkg/comm/buffer"
	"github.com/gsoultan/hermod/pkg/comm/message"
	"github.com/gsoultan/hermod/pkg/infra/schema"
)

// ---------------------------------------------------------------------------
// What happens to one message when something in the workflow refuses it.
//
// The individual pieces are each covered elsewhere: the traversal dead-letters
// a failed node, writeToDLQ reports a refused park, reportUnroutable describes
// the disposition. What none of them cover is the assembly — one message going
// through the whole engine once — which is where the two answers that matter
// live: how many copies reach the dead-letter sink, and whether the source is
// acknowledged.
// ---------------------------------------------------------------------------

// oneShotSource emits a single message and then blocks, recording every ack.
type oneShotSource struct {
	mu    sync.Mutex
	sent  bool
	acked []string
}

func (s *oneShotSource) Read(ctx context.Context) (hermod.Message, error) {
	s.mu.Lock()
	if s.sent {
		s.mu.Unlock()
		<-ctx.Done()
		return nil, ctx.Err()
	}
	s.sent = true
	s.mu.Unlock()

	m := message.AcquireMessage()
	m.SetID("m-1")
	m.SetPayload([]byte(`{"v":1}`))
	return m, nil
}

func (s *oneShotSource) Ack(_ context.Context, msg hermod.Message) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if msg != nil {
		s.acked = append(s.acked, msg.ID())
	}
	return nil
}

func (s *oneShotSource) Ping(context.Context) error { return nil }
func (s *oneShotSource) Close() error               { return nil }

func (s *oneShotSource) ackCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.acked)
}

// tallySink counts what it is asked to write and signals the first arrival.
type tallySink struct {
	mu    sync.Mutex
	ids   []string
	first chan struct{}
	once  sync.Once
}

func newTallySink() *tallySink {
	return &tallySink{first: make(chan struct{})}
}

func (s *tallySink) Write(_ context.Context, msg hermod.Message) error {
	s.mu.Lock()
	if msg != nil {
		s.ids = append(s.ids, msg.ID())
	}
	s.mu.Unlock()
	s.once.Do(func() { close(s.first) })
	return nil
}

func (s *tallySink) Ping(context.Context) error { return nil }
func (s *tallySink) Close() error               { return nil }

func (s *tallySink) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.ids)
}

// alwaysInvalid rejects every message, standing in for a schema the data does
// not satisfy — the poison-message case.
type alwaysInvalid struct{}

func (alwaysInvalid) Validate(context.Context, map[string]any) error {
	return errors.New("field \"id\" is required")
}
func (alwaysInvalid) Type() schema.SchemaType { return schema.SchemaType("test") }

// runOneMessage starts the engine, waits for the dead-letter sink to see its
// first write (or times out), lets any follow-up work settle, and stops.
func runOneMessage(t *testing.T, eng *Engine, dlq *tallySink) {
	t.Helper()

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = eng.Start(ctx)
	}()

	select {
	case <-dlq.first:
	case <-time.After(3 * time.Second):
		t.Fatal("the dead-letter sink was never written to")
	}

	// A second park, if there is one, happens on the same goroutine immediately
	// after the first. This is long enough for it to land and be counted.
	time.Sleep(250 * time.Millisecond)

	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the engine did not stop")
	}
}

// A node failure is dead-lettered by the traversal, which then resolves no
// sink. The runner sees a message with no targets and cannot tell it apart from
// one that was never handled at all, so it parks it again: two copies of one
// message in the dead-letter queue, and whoever drains the queue has to work out
// that they are the same event.
func TestANodeFailureParksExactlyOneCopyInTheDeadLetterQueue(t *testing.T) {
	src := &oneShotSource{}
	sink := newTallySink()
	dlq := newTallySink()

	eng := NewEngine(src, []hermod.Sink{sink}, buffer.NewRingBuffer(8))
	eng.SetLogger(&testLogger{})
	eng.SetDeadLetterSink(dlq)

	// Exactly what traversal.processNode does when a node fails: park the
	// message, then resolve no sink for it.
	eng.SetRouter(func(ctx context.Context, msg hermod.Message) ([]RoutedMessage, error) {
		eng.DeadLetterNodeFailure(ctx, "T", msg, errors.New("transformation could not parse the payload"))
		return nil, nil
	})

	runOneMessage(t, eng, dlq)

	if got := dlq.count(); got != 1 {
		t.Errorf("one failed node put %d copies of the message in the dead-letter queue, want 1", got)
	}
	if got := sink.count(); got != 0 {
		t.Errorf("a message that failed a node still reached the real sink %d time(s)", got)
	}
}

// A message parked in the dead-letter queue is preserved, so the source must be
// acknowledged: not acknowledging it pins a replication slot on a message that
// can never succeed, and every restart parks another copy. The unroutable path
// a few lines away already acknowledges after a successful park; validation
// takes the same decision or the queue grows without bound.
func TestAValidationFailureAcknowledgesTheSourceAfterParking(t *testing.T) {
	src := &oneShotSource{}
	sink := newTallySink()
	dlq := newTallySink()

	eng := NewEngine(src, []hermod.Sink{sink}, buffer.NewRingBuffer(8))
	eng.SetLogger(&testLogger{})
	eng.SetDeadLetterSink(dlq)
	eng.SetValidator(alwaysInvalid{})

	runOneMessage(t, eng, dlq)

	if got := dlq.count(); got != 1 {
		t.Errorf("the dead-letter sink took %d copies, want 1", got)
	}
	if got := src.ackCount(); got != 1 {
		t.Errorf("the message was parked in the dead-letter queue but the source was "+
			"acknowledged %d time(s), want 1; the source replays it forever and each "+
			"run parks another copy", got)
	}
}
