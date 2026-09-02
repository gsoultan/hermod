package engine

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/pkg/comm/buffer"
	"github.com/gsoultan/hermod/pkg/comm/message"
	"github.com/gsoultan/hermod/pkg/engine/config"
)

// blockingSink accepts a write and never completes it, which is what a sink
// whose far end has gone away looks like from inside the engine.
type blockingSink struct {
	entered chan struct{}
	once    atomic.Bool
}

func (s *blockingSink) Write(ctx context.Context, _ hermod.Message) error {
	if s.once.CompareAndSwap(false, true) && s.entered != nil {
		close(s.entered)
	}
	<-ctx.Done()
	return ctx.Err()
}

func (s *blockingSink) Ping(context.Context) error { return nil }
func (s *blockingSink) Close() error               { return nil }

// silentLaggingSource is the shape of the wedge seen in production: the source
// holds its replication connection open and answers health checks, but hands
// over no messages, while the slot behind it retains WAL. From the engine's own
// counters this is indistinguishable from an idle pipeline — the source's lag is
// the only remaining evidence that work is outstanding.
type silentLaggingSource struct {
	lag atomic.Uint64
}

func (s *silentLaggingSource) Read(ctx context.Context) (hermod.Message, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

func (s *silentLaggingSource) Ack(context.Context, hermod.Message) error { return nil }
func (s *silentLaggingSource) Ping(context.Context) error                { return nil }
func (s *silentLaggingSource) Close() error                              { return nil }

func (s *silentLaggingSource) GetLag(context.Context) (uint64, error) {
	return s.lag.Load(), nil
}

// idleSource hands over nothing and reports no lag: a healthy pipeline with
// nothing to do. It must never be reported as stalled.
type idleSource struct{}

func (idleSource) Read(ctx context.Context) (hermod.Message, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}
func (idleSource) Ack(context.Context, hermod.Message) error { return nil }
func (idleSource) Ping(context.Context) error                { return nil }
func (idleSource) Close() error                              { return nil }

func stallTestConfig(threshold time.Duration) config.Config {
	cfg := config.DefaultConfig()
	cfg.StallThreshold = threshold
	cfg.StatusInterval = 100 * time.Millisecond
	cfg.MaxInflight = 4
	return cfg
}

// The watchdog and the supervisor were each unit-tested in isolation, and the
// wiring between them was not: nothing exercised watchForStalls against a real
// running engine, so there was no evidence that a wedged pipeline ever reaches
// the supervisor at all. These tests close that gap — they drive an actual
// Engine into each wedge shape and assert the stall hook fires.
func TestWatchdogReportsAWedgedEngineToTheSupervisor(t *testing.T) {
	t.Run("a sink that never completes a write is reported", func(t *testing.T) {
		msg := message.AcquireMessage()
		msg.SetID("wedge-1")
		msg.SetPayload([]byte("payload"))

		sink := &blockingSink{entered: make(chan struct{})}
		eng := NewEngine(&mockSource{msg: msg}, []hermod.Sink{sink}, buffer.NewRingBuffer(64))
		eng.SetConfig(stallTestConfig(time.Second))
		eng.SetWorkflowID("wf-wedge")

		stalled := make(chan string, 1)
		eng.SetOnStall(func(reason string) {
			select {
			case stalled <- reason:
			default:
			}
		})

		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		go func() { _ = eng.Start(ctx) }()

		select {
		case <-sink.entered:
		case <-time.After(5 * time.Second):
			t.Fatal("precondition: the sink never received a write")
		}

		select {
		case reason := <-stalled:
			if reason == "" {
				t.Error("the supervisor was handed an empty reason")
			}
			if got := eng.GetStatus().EngineStatus; got != "stalled" {
				t.Errorf("engine status = %q, want %q: the UI would still show this workflow as healthy", got, "stalled")
			}
		case <-time.After(15 * time.Second):
			t.Fatal("a pipeline holding work it never completes was never reported to the supervisor")
		}
	})

	t.Run("a source that stops delivering while its slot retains WAL is reported", func(t *testing.T) {
		src := &silentLaggingSource{}
		src.lag.Store(21 * 1024 * 1024) // the 21 MB of retained WAL seen in production

		eng := NewEngine(src, []hermod.Sink{&mockSink{}}, buffer.NewRingBuffer(64))
		eng.SetConfig(stallTestConfig(time.Second))
		eng.SetWorkflowID("wf-silent")

		stalled := make(chan string, 1)
		eng.SetOnStall(func(reason string) {
			select {
			case stalled <- reason:
			default:
			}
		})

		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		go func() { _ = eng.Start(ctx) }()

		select {
		case <-stalled:
		case <-time.After(15 * time.Second):
			t.Fatal("a source retaining WAL while delivering nothing was never reported: this is the wedge that went unnoticed for minutes in production")
		}
	})

	t.Run("an idle pipeline is never reported", func(t *testing.T) {
		eng := NewEngine(idleSource{}, []hermod.Sink{&mockSink{}}, buffer.NewRingBuffer(64))
		eng.SetConfig(stallTestConfig(500 * time.Millisecond))
		eng.SetWorkflowID("wf-idle")

		stalled := make(chan string, 1)
		eng.SetOnStall(func(reason string) {
			select {
			case stalled <- reason:
			default:
			}
		})

		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		go func() { _ = eng.Start(ctx) }()

		select {
		case reason := <-stalled:
			t.Fatalf("a healthy idle pipeline was restarted by the supervisor: %s", reason)
		case <-time.After(4 * time.Second):
		}
	})

	t.Run("one stall episode reaches the supervisor exactly once", func(t *testing.T) {
		src := &silentLaggingSource{}
		src.lag.Store(4096)

		eng := NewEngine(src, []hermod.Sink{&mockSink{}}, buffer.NewRingBuffer(64))
		eng.SetConfig(stallTestConfig(time.Second))
		eng.SetWorkflowID("wf-once")

		var reports atomic.Int64
		eng.SetOnStall(func(string) { reports.Add(1) })

		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		go func() { _ = eng.Start(ctx) }()

		deadline := time.After(15 * time.Second)
		for reports.Load() == 0 {
			select {
			case <-deadline:
				t.Fatal("precondition: no stall was ever reported")
			case <-time.After(50 * time.Millisecond):
			}
		}

		// The watchdog keeps running after handing off, so that a restart the
		// supervisor declines still leaves the engine supervised. It must not
		// turn that into a restart per tick against an engine already being
		// rebuilt.
		time.Sleep(4 * time.Second)
		if n := reports.Load(); n != 1 {
			t.Errorf("supervisor invoked %d times for one stall episode, want 1", n)
		}
	})
}
