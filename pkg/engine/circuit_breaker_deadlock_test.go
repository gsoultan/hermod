package engine

import (
	"testing"
	"time"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/pkg/comm/buffer"
	"github.com/gsoultan/hermod/pkg/engine/telemetry"
)

// recordFailure and recordSuccess mutated the circuit breaker under sw.cbMu and
// then, still holding it, called engine.setSinkStatus. That notifies the status
// listener, which calls Engine.GetStatus, which locks every sink writer's cbMu
// to read its breaker state. Go mutexes are not reentrant, so the writer
// goroutine deadlocked against itself.
//
// It only fires when the breaker CHANGES state — a sink failing past its
// threshold, or recovering — which is why it took a sink outage to surface and
// why only a workflow restart (fresh writers, fresh mutexes) cleared it.
//
// The consequence was the whole pipeline, not just one sink: flush() never
// returned, so sw.ch stopped draining, which blocked the per-message
// goroutines, which filled the ring buffer, which blocked the source's dispatch,
// which stopped replication acknowledgement and pinned WAL on the source
// database. Observed as 0 of 2000 messages delivered after the sink was healthy
// again, with six goroutines parked in GetStatus for over a minute.
func TestCircuitBreakerStateChangeDoesNotDeadlock(t *testing.T) {
	newWiredEngine := func() (*Engine, *sinkWriter) {
		e := NewEngine(&mockSource{}, []hermod.Sink{&mockSink{}}, buffer.NewRingBuffer(4))
		sw := &sinkWriter{
			engine: e,
			sink:   &mockSink{},
			sinkID: "sink-1",
			ch:     make(chan *pendingMessage, 1),
		}
		e.stopMu.Lock()
		e.sinkWriters = []*sinkWriter{sw}
		e.stopMu.Unlock()

		// Exactly what the registry wires in production: the listener reads the
		// engine's status, which locks every writer's breaker mutex.
		e.SetOnStatusChange(func(telemetry.StatusUpdate) { _ = e.GetStatus() })
		return e, sw
	}

	t.Run("opening the breaker", func(t *testing.T) {
		_, sw := newWiredEngine()
		done := make(chan struct{})
		go func() {
			defer close(done)
			// Default threshold is 5; drive well past it so the breaker opens.
			for range 10 {
				sw.recordFailure()
			}
		}()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			t.Fatal("recordFailure deadlocked: the sink writer holds cbMu while GetStatus waits for it, wedging the whole pipeline")
		}
	})

	t.Run("closing the breaker after recovery", func(t *testing.T) {
		_, sw := newWiredEngine()
		sw.cbMu.Lock()
		sw.cbStatus = "half-open"
		sw.cbMu.Unlock()

		done := make(chan struct{})
		go func() {
			defer close(done)
			sw.recordSuccess()
		}()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			t.Fatal("recordSuccess deadlocked on recovery, so a sink that came back never resumed")
		}
	})
}
