package engine

import (
	"context"
	"errors"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	hermod "github.com/gsoultan/Hermod"
	"github.com/gsoultan/Hermod/pkg/comm/buffer"
	"github.com/gsoultan/Hermod/pkg/comm/message"
	"github.com/gsoultan/Hermod/pkg/engine/config"
)

// ---------------------------------------------------------------------------
// Shutdown must not lose messages it has already accepted.
//
// TestEngineGracefilShutdown catches this roughly one run in ten, which is too
// rare to debug against and rare enough to be dismissed as flake — it has been
// in the tree long enough to be named in a comment in pending_message_test.go.
// The tests below make the same defect deterministic by holding the sink long
// enough that messages are certainly still queued when the context is
// cancelled.
//
// Two separate faults produce the loss:
//
//  1. sinkWriter.runOn returned on ctx.Done(). The runner cancels the context
//     *first* and only afterwards closes the writer's input channel, so the
//     writer walked away from everything still queued. The runner's careful
//     drain sequence — wait for in-flight senders, close the channel, wait for
//     the writers — was correct and simply never got the chance to run.
//
//  2. The final flush wrote with the already-cancelled context. Any sink that
//     honours cancellation (which is most of them) rejects immediately, so even
//     the messages the writer did try to deliver were lost. That is the
//     "Sink write error: context canceled" logged immediately before "Engine
//     stopped gracefully".
// ---------------------------------------------------------------------------

// recordingSink captures every message it is asked to write, and can be made
// slow so that messages pile up in the writer's queue.
type recordingSink struct {
	mu       sync.Mutex
	ids      []string
	delay    time.Duration
	ctxErrs  int
	writeErr error
}

func (s *recordingSink) Write(ctx context.Context, msg hermod.Message) error {
	if s.delay > 0 {
		select {
		case <-time.After(s.delay):
		case <-ctx.Done():
			// Record that the write was refused because its context was already
			// cancelled — the exact shape of the shutdown data loss.
			s.mu.Lock()
			s.ctxErrs++
			s.mu.Unlock()
			return ctx.Err()
		}
	}
	if ctx.Err() != nil {
		s.mu.Lock()
		s.ctxErrs++
		s.mu.Unlock()
		return ctx.Err()
	}
	if s.writeErr != nil {
		return s.writeErr
	}
	s.mu.Lock()
	s.ids = append(s.ids, msg.ID())
	s.mu.Unlock()
	return nil
}

func (s *recordingSink) Ping(ctx context.Context) error { return nil }
func (s *recordingSink) Close() error                   { return nil }

func (s *recordingSink) received() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, len(s.ids))
	copy(out, s.ids)
	return out
}

func (s *recordingSink) cancelledWrites() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ctxErrs
}

// drainSource hands over a fixed number of messages and then parks until the
// context is cancelled.
//
// It exists rather than reusing slowMockSource because that fixture blocks on
// <-ctx.Done() *while holding its mutex*, so anything that inspects its state
// concurrently deadlocks. Progress is tracked with an atomic so the test can
// wait for handover without touching a lock.
type drainSource struct {
	mu       sync.Mutex
	messages []hermod.Message
	handed   atomic.Int64
}

func (s *drainSource) Read(ctx context.Context) (hermod.Message, error) {
	s.mu.Lock()
	if len(s.messages) == 0 {
		s.mu.Unlock() // never block holding the lock
		<-ctx.Done()
		return nil, ctx.Err()
	}
	msg := s.messages[0]
	s.messages = s.messages[1:]
	s.mu.Unlock()
	s.handed.Add(1)
	return msg, nil
}

func (s *drainSource) Ack(ctx context.Context, msg hermod.Message) error { return nil }
func (s *drainSource) Ping(ctx context.Context) error                    { return nil }
func (s *drainSource) Close() error                                      { return nil }

// startDrainEngine wires an engine over the given sink, feeds it n messages and
// returns once they have all been read out of the source.
func startDrainEngine(t *testing.T, sink *recordingSink, n int, cfg config.Config) (context.CancelFunc, chan error) {
	t.Helper()
	return startDrainEngineWithSinkConfig(t, sink, n, cfg, nil)
}

// startDrainEngineWithSinkConfig additionally applies a per-sink config, which
// is where the backpressure strategy lives.
func startDrainEngineWithSinkConfig(t *testing.T, sink *recordingSink, n int, cfg config.Config, sinkCfg *config.SinkConfig) (context.CancelFunc, chan error) {
	t.Helper()

	msgs := make([]hermod.Message, n)
	for i := range n {
		m := message.AcquireMessage()
		m.SetID(strconv.Itoa(i))
		m.SetPayload([]byte(`{"k":"v"}`))
		msgs[i] = m
	}

	src := &drainSource{messages: msgs}
	eng := NewEngine(src, []hermod.Sink{sink}, buffer.NewRingBuffer(n*4))
	eng.SetConfig(cfg)
	if sinkCfg != nil {
		eng.SetSinkConfigs([]config.SinkConfig{*sinkCfg})
	}

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() { errCh <- eng.Start(ctx) }()

	// Wait until the source has handed over every message, so the loss under
	// test is "accepted then dropped", not "never read".
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) && src.handed.Load() < int64(n) {
		time.Sleep(5 * time.Millisecond)
	}
	if got := src.handed.Load(); got < int64(n) {
		t.Fatalf("source only handed over %d/%d messages before the test could start", got, n)
	}
	return cancel, errCh
}

// TestShutdownDeliversMessagesAlreadyAcceptedFromTheSource is the deterministic
// version of the intermittent failure. The sink is slow enough that most of the
// batch is still queued when the context is cancelled; every one of those
// messages has already been taken from the source, so dropping them is
// unrecoverable data loss.
func TestShutdownDeliversMessagesAlreadyAcceptedFromTheSource(t *testing.T) {
	const n = 20

	sink := &recordingSink{delay: 15 * time.Millisecond}
	cfg := config.DefaultConfig()
	cfg.DrainTimeout = 10 * time.Second

	cancel, errCh := startDrainEngine(t, sink, n, cfg)

	// Cancel while the sink is still working through the queue.
	time.Sleep(60 * time.Millisecond)
	cancel()

	select {
	case err := <-errCh:
		if err != nil && !errors.Is(err, context.Canceled) {
			t.Fatalf("engine stopped with an unexpected error: %v", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("engine did not stop within 30s of cancellation")
	}

	got := sink.received()
	if len(got) != n {
		t.Errorf("shutdown lost data: source handed over %d messages, sink received %d "+
			"(%d writes were refused because their context was already cancelled)",
			n, len(got), sink.cancelledWrites())
	}
	if c := sink.cancelledWrites(); c != 0 {
		t.Errorf("%d sink writes were attempted with an already-cancelled context; "+
			"drain must write with a context that outlives the shutdown", c)
	}
}

// TestShutdownDrainIsBoundedByDrainTimeout keeps the fix honest in the other
// direction: draining must not become an unbounded wait. A sink that never
// returns has to be abandoned, or a rolling deploy hangs on a single bad
// destination.
func TestShutdownDrainIsBoundedByDrainTimeout(t *testing.T) {
	const n = 10

	// A sink that never completes on its own: every write parks until its
	// context is cancelled. The only thing that can end this shutdown is the
	// drain budget, so termination alone proves the bound.
	sink := &recordingSink{delay: time.Hour}
	cfg := config.DefaultConfig()
	cfg.DrainTimeout = 500 * time.Millisecond

	cancel, errCh := startDrainEngine(t, sink, n, cfg)

	time.Sleep(50 * time.Millisecond)
	start := time.Now()
	cancel()

	select {
	case <-errCh:
	case <-time.After(60 * time.Second):
		t.Fatal("engine hung on shutdown behind a sink that never completes")
	}

	// Once the budget expires every remaining write fails immediately, so the
	// whole shutdown should be close to the budget rather than to the sink's
	// latency. The ceiling is generous because a loaded machine schedules the
	// writer goroutines unevenly; what matters is that it is a ceiling at all.
	if elapsed := time.Since(start); elapsed > 30*time.Second {
		t.Errorf("shutdown took %v with a %v drain budget; the drain is not bounded",
			elapsed, cfg.DrainTimeout)
	}
}

// TestShutdownWithNoInFlightWorkIsImmediate guards the common case: an idle
// engine must still stop promptly, not sit out the whole drain budget.
func TestShutdownWithNoInFlightWorkIsImmediate(t *testing.T) {
	sink := &recordingSink{}
	cfg := config.DefaultConfig()
	cfg.DrainTimeout = 10 * time.Second

	cancel, errCh := startDrainEngine(t, sink, 1, cfg)

	// Let the single message settle so nothing is in flight.
	time.Sleep(300 * time.Millisecond)

	start := time.Now()
	cancel()
	select {
	case <-errCh:
	case <-time.After(30 * time.Second):
		t.Fatal("idle engine did not stop")
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Errorf("idle shutdown took %v; it should not wait out the drain budget", elapsed)
	}
}

// TestShutdownDrainsUnderEveryBackpressureStrategy checks the drain path across
// all five strategies, not just the default.
//
// The shutdown fix touched two of them directly (BPBlock and BPSampling share
// enqueueOrDrain). The other three take different branches: drop_oldest and
// drop_newest shed only when the queue is *full*, and spill_to_disk diverts to a
// file. None of them should lose a message that fits in the queue simply
// because the engine is stopping — the source has already handed it over.
func TestShutdownDrainsUnderEveryBackpressureStrategy(t *testing.T) {
	strategies := []config.BackpressureStrategy{
		config.BPBlock,
		config.BPDropOldest,
		config.BPDropNewest,
		config.BPSampling,
		config.BPSpillToDisk,
	}

	for _, strategy := range strategies {
		t.Run(string(strategy), func(t *testing.T) {
			const n = 15

			sink := &recordingSink{delay: 10 * time.Millisecond}
			cfg := config.DefaultConfig()
			cfg.DrainTimeout = 10 * time.Second

			sinkCfg := config.SinkConfig{
				BackpressureStrategy: strategy,
				// Sampling drops by design; keep it at 1.0 so this measures the
				// drain rather than the sampler.
				SamplingRate: 1.0,
			}
			if strategy == config.BPSpillToDisk {
				sinkCfg.SpillPath = filepath.Join(t.TempDir(), "spill")
			}

			cancel, errCh := startDrainEngineWithSinkConfig(t, sink, n, cfg, &sinkCfg)

			time.Sleep(40 * time.Millisecond)
			cancel()

			select {
			case err := <-errCh:
				if err != nil && !errors.Is(err, context.Canceled) {
					t.Fatalf("engine stopped with an unexpected error: %v", err)
				}
			case <-time.After(60 * time.Second):
				t.Fatal("engine did not stop within 60s")
			}

			got := len(sink.received())
			// spill_to_disk deliberately parks overflow in a file that is
			// replayed on the next start, so its messages are durable rather
			// than delivered now. The others must all arrive.
			if strategy == config.BPSpillToDisk {
				if got == 0 {
					t.Errorf("spill_to_disk delivered nothing at all (%d writes refused with a cancelled context)",
						sink.cancelledWrites())
				}
				return
			}
			if got != n {
				t.Errorf("%s: source handed over %d messages, sink received %d "+
					"(%d writes refused because their context was already cancelled)",
					strategy, n, got, sink.cancelledWrites())
			}
			if c := sink.cancelledWrites(); c != 0 {
				t.Errorf("%s: %d writes were attempted with an already-cancelled context", strategy, c)
			}
		})
	}
}

// ackTrackingSource records which message ids were acknowledged, and refuses to
// acknowledge on a cancelled context the way a real CDC source does — the
// acknowledgement is a network round trip to the upstream server.
type ackTrackingSource struct {
	mu       sync.Mutex
	messages []hermod.Message
	acked    []string
	ackErrs  int
	handed   atomic.Int64
}

func (s *ackTrackingSource) Read(ctx context.Context) (hermod.Message, error) {
	s.mu.Lock()
	if len(s.messages) == 0 {
		s.mu.Unlock()
		<-ctx.Done()
		return nil, ctx.Err()
	}
	msg := s.messages[0]
	s.messages = s.messages[1:]
	s.mu.Unlock()
	s.handed.Add(1)
	return msg, nil
}

func (s *ackTrackingSource) Ack(ctx context.Context, msg hermod.Message) error {
	if ctx.Err() != nil {
		s.mu.Lock()
		s.ackErrs++
		s.mu.Unlock()
		return ctx.Err()
	}
	s.mu.Lock()
	s.acked = append(s.acked, msg.ID())
	s.mu.Unlock()
	return nil
}

func (s *ackTrackingSource) Ping(ctx context.Context) error { return nil }
func (s *ackTrackingSource) Close() error                   { return nil }

func (s *ackTrackingSource) ackedIDs() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, len(s.acked))
	copy(out, s.acked)
	return out
}

func (s *ackTrackingSource) refusedAcks() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ackErrs
}

// TestShutdownAcknowledgesWhatItDelivers closes the loop between the drain and
// the source's read position.
//
// Delivering a message during shutdown is only half the job: for a CDC source
// the acknowledgement is what advances the replication slot. If the drain
// writes a message to its sink but the acknowledgement is refused because the
// engine context has already been cancelled, the message is delivered *and*
// replayed on the next start — a duplicate for every message in flight at every
// deploy. The acknowledgement has to survive shutdown for exactly as long as
// the write it confirms.
//
// The reverse must also hold: a message the drain could not deliver must NOT be
// acknowledged, or it is lost rather than replayed.
func TestShutdownAcknowledgesWhatItDelivers(t *testing.T) {
	const n = 15

	msgs := make([]hermod.Message, n)
	for i := range n {
		m := message.AcquireMessage()
		m.SetID(strconv.Itoa(i))
		m.SetPayload([]byte(`{"k":"v"}`))
		msgs[i] = m
	}

	src := &ackTrackingSource{messages: msgs}
	sink := &recordingSink{delay: 10 * time.Millisecond}
	cfg := config.DefaultConfig()
	cfg.DrainTimeout = 10 * time.Second

	eng := NewEngine(src, []hermod.Sink{sink}, buffer.NewRingBuffer(n*4))
	eng.SetConfig(cfg)

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() { errCh <- eng.Start(ctx) }()

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) && src.handed.Load() < int64(n) {
		time.Sleep(5 * time.Millisecond)
	}

	time.Sleep(40 * time.Millisecond)
	cancel()

	select {
	case err := <-errCh:
		if err != nil && !errors.Is(err, context.Canceled) {
			t.Fatalf("engine stopped with an unexpected error: %v", err)
		}
	case <-time.After(60 * time.Second):
		t.Fatal("engine did not stop within 60s")
	}

	delivered := len(sink.received())
	acked := len(src.ackedIDs())

	if delivered != n {
		t.Errorf("shutdown delivered %d/%d messages", delivered, n)
	}
	if acked != delivered {
		t.Errorf("delivered %d messages but acknowledged %d (%d acknowledgements refused "+
			"because the context was already cancelled); every unacknowledged delivery is a "+
			"duplicate on the next start", delivered, acked, src.refusedAcks())
	}
	if r := src.refusedAcks(); r != 0 {
		t.Errorf("%d acknowledgements were attempted with an already-cancelled context", r)
	}
}
