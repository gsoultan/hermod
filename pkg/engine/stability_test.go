package engine

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/user/hermod"
	"github.com/user/hermod/pkg/comm/buffer"
	"github.com/user/hermod/pkg/comm/message"
)

// panicSource is a source whose Read panics, simulating a buggy/crashing
// workflow source. The engine must isolate this so the worker process and any
// sibling workflows are unaffected.
type panicSource struct {
	closed bool
	mu     sync.Mutex
}

func (s *panicSource) Read(ctx context.Context) (hermod.Message, error) {
	panic("simulated source crash")
}

func (s *panicSource) Ack(ctx context.Context, msg hermod.Message) error { return nil }
func (s *panicSource) Ping(ctx context.Context) error                    { return nil }
func (s *panicSource) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	return nil
}

// TestEnginePanicIsolation verifies that a panic in the source ingestion loop
// is recovered and surfaced as an error from Start rather than crashing the
// process. If the panic were not recovered, the test binary would abort.
func TestEnginePanicIsolation(t *testing.T) {
	src := &panicSource{}
	snk := &mockSink{received: make(chan hermod.Message, 1)}
	rb := buffer.NewRingBuffer(10)

	eng := NewEngine(src, []hermod.Sink{snk}, rb)
	eng.SetWorkflowID("wf-panic")

	errCh := make(chan error, 1)
	go func() {
		errCh <- eng.Start(t.Context())
	}()

	select {
	case err := <-errCh:
		if err == nil {
			t.Fatal("expected an error from Start after source panic, got nil")
		}
		if !strings.Contains(err.Error(), "panic") {
			t.Fatalf("expected a panic error, got: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("engine did not return after source panic; panic was not isolated")
	}

	// The engine status must reflect the failure.
	if status := eng.GetStatus().EngineStatus; !strings.HasPrefix(status, "Error") {
		t.Errorf("expected engine status to indicate error, got %q", status)
	}
}

// TestEnginePanicDoesNotAffectSiblingWorkflow verifies that a crashing workflow
// running concurrently does not prevent a healthy workflow from processing
// messages, mirroring multiple workflows running inside the same worker.
func TestEnginePanicDoesNotAffectSiblingWorkflow(t *testing.T) {
	// Crashing workflow.
	crashEng := NewEngine(&panicSource{}, []hermod.Sink{&mockSink{}}, buffer.NewRingBuffer(10))
	crashEng.SetWorkflowID("wf-crash")

	// Healthy workflow.
	msg := message.AcquireMessage()
	msg.SetID("healthy-1")
	msg.SetPayload([]byte("ok"))
	healthySink := &mockSink{received: make(chan hermod.Message, 8)}
	healthyEng := NewEngine(&mockSource{msg: msg}, []hermod.Sink{healthySink}, buffer.NewRingBuffer(10))
	healthyEng.SetWorkflowID("wf-healthy")

	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()

	go func() { _ = crashEng.Start(ctx) }()
	go func() { _ = healthyEng.Start(ctx) }()

	select {
	case <-healthySink.received:
		// Healthy workflow processed a message despite the sibling crash.
	case <-time.After(2 * time.Second):
		t.Fatal("healthy workflow did not process any message; sibling crash affected it")
	}

	cancel()

	// Ensure the crashing engine eventually exits (it must not hang).
	deadline := time.After(2 * time.Second)
	for {
		select {
		case <-deadline:
			t.Fatal("crashing engine did not stop")
		default:
			status := crashEng.GetStatus().EngineStatus
			if strings.HasPrefix(status, "Error") || errors.Is(ctx.Err(), context.Canceled) {
				return
			}
			time.Sleep(20 * time.Millisecond)
		}
	}
}
