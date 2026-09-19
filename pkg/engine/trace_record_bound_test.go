package engine

// Recording a trace step spawned a goroutine and armed a 5-second timer, per
// node, per message, with nothing bounding either. At the default
// TraceSampleRate of 0 that costs nothing because WillTrace returns first — but
// tracing is turned on precisely when a workflow is busy and someone is trying
// to understand it, and a five-node graph at 50k msgs/s is 250k goroutines and
// 250k timers in flight against a recorder that writes to PostgreSQL.
//
// internal/engine/registry already solved this for its own trace recording with
// a semaphore that drops under pressure. The engine does the same, so a slow
// recorder costs trace fidelity rather than the process.

import (
	"context"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/pkg/comm/message"
)

// blockingRecorder never finishes a step until it is released, which is what a
// recorder backed up behind a slow database looks like.
type blockingRecorder struct {
	release chan struct{}
	calls   chan struct{}
}

func newBlockingRecorder() *blockingRecorder {
	return &blockingRecorder{release: make(chan struct{}), calls: make(chan struct{}, 1<<16)}
}

func (r *blockingRecorder) RecordStep(ctx context.Context, workflowID, messageID string, step hermod.TraceStep) {
	select {
	case r.calls <- struct{}{}:
	default:
	}
	select {
	case <-r.release:
	case <-ctx.Done():
	}
}

func TestTraceRecordingDoesNotSpawnUnboundedGoroutines(t *testing.T) {
	rec := newBlockingRecorder()

	e := NewEngine(nil, nil, nil)
	e.logger = &plainLogger{}
	e.workflowID = "wf-trace"
	e.traceRecorder = rec
	cfg := e.config
	cfg.TraceSampleRate = 1.0
	e.config = cfg

	msg := message.AcquireMessage()
	defer msg.Release()
	msg.SetID("m-1")
	msg.SetPayload([]byte(`{"v":1}`))

	before := runtime.NumGoroutine()

	const steps = 4000
	for i := range steps {
		e.RecordTraceStep(context.Background(), msg, "node-1", time.Now(), nil, nil)
		_ = i
	}

	// Give the spawned work a moment to actually be running.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && runtime.NumGoroutine() <= before {
		time.Sleep(time.Millisecond)
	}
	peak := runtime.NumGoroutine() - before

	close(rec.release)

	if peak >= steps/2 {
		t.Errorf("%d trace steps against a stalled recorder left %d goroutines in flight; nothing bounds them",
			steps, peak)
	}
}

// Whatever the bound is, a recorder that keeps up must still receive every
// step: the cap exists for a stalled recorder, not as a sampling mechanism.
func TestTraceRecordingStillRecordsWhenTheRecorderKeepsUp(t *testing.T) {
	var mu sync.Mutex
	var got []string
	done := make(chan struct{})

	e := NewEngine(nil, nil, nil)
	e.logger = &plainLogger{}
	e.workflowID = "wf-trace"
	e.traceRecorder = recorderFunc(func(ctx context.Context, workflowID, messageID string, step hermod.TraceStep) {
		mu.Lock()
		got = append(got, step.NodeID)
		n := len(got)
		mu.Unlock()
		if n == 20 {
			close(done)
		}
	})
	cfg := e.config
	cfg.TraceSampleRate = 1.0
	e.config = cfg

	msg := message.AcquireMessage()
	defer msg.Release()
	msg.SetID("m-1")
	msg.SetPayload([]byte(`{"v":1}`))

	for i := range 20 {
		e.RecordTraceStep(context.Background(), msg, "node-1", time.Now(), nil, nil)
		_ = i
	}

	select {
	case <-done:
	case <-time.After(10 * time.Second):
		mu.Lock()
		n := len(got)
		mu.Unlock()
		t.Fatalf("only %d of 20 trace steps were recorded by a recorder that keeps up", n)
	}
}

type recorderFunc func(ctx context.Context, workflowID, messageID string, step hermod.TraceStep)

func (f recorderFunc) RecordStep(ctx context.Context, workflowID, messageID string, step hermod.TraceStep) {
	f(ctx, workflowID, messageID, step)
}
