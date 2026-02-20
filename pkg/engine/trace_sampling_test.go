package engine

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/user/hermod"
)

type mockTraceRecorder struct {
	mu    sync.Mutex
	steps map[string][]hermod.TraceStep
}

func (m *mockTraceRecorder) RecordStep(ctx context.Context, workflowID, messageID string, step hermod.TraceStep) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.steps == nil {
		m.steps = make(map[string][]hermod.TraceStep)
	}
	m.steps[messageID] = append(m.steps[messageID], step)
}

func (m *mockTraceRecorder) GetSteps(messageID string) []hermod.TraceStep {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.steps[messageID]
}

func TestRecordTraceStep_DeterministicSampling(t *testing.T) {
	recorder := &mockTraceRecorder{}
	e := &Engine{
		traceRecorder: recorder,
		workflowID:    "test-workflow",
		config: Config{
			TraceSampleRate: 0.5, // 50% sampling
		},
	}

	msgID := "consistent-msg-id"
	msg := &mockMessage{id: msgID}

	// Record multiple steps for the same message
	for i := 0; i < 100; i++ {
		e.RecordTraceStep(context.Background(), msg, "node-1", time.Now(), nil)
	}

	steps := recorder.GetSteps(msgID)
	// With deterministic sampling, it should be either 100 or 0 steps.
	if len(steps) > 0 && len(steps) < 100 {
		t.Errorf("Inconsistent sampling for same message: got %d steps, expected 0 or 100", len(steps))
	}

	// Now check that different messages can have different sampling outcomes
	sampledIn := 0
	sampledOut := 0
	for i := 0; i < 1000; i++ {
		mID := string(rune(i)) // Different ID each time
		m := &mockMessage{id: mID}
		e.RecordTraceStep(context.Background(), m, "node-1", time.Now(), nil)
		if len(recorder.GetSteps(mID)) > 0 {
			sampledIn++
		} else {
			sampledOut++
		}
	}

	t.Logf("Sampled in: %d, Sampled out: %d", sampledIn, sampledOut)
	if sampledIn == 0 || sampledOut == 0 {
		t.Errorf("Sampling seems broken, either all in or all out: in=%d, out=%d", sampledIn, sampledOut)
	}
}

func TestRecordTraceStep_Default(t *testing.T) {
	recorder := &mockTraceRecorder{}
	e := &Engine{
		traceRecorder: recorder,
		workflowID:    "test-workflow",
		config:        DefaultConfig(),
	}

	msgID := "test-msg"
	msg := &mockMessage{id: msgID}

	e.RecordTraceStep(context.Background(), msg, "node-1", time.Now(), nil)

	steps := recorder.GetSteps(msgID)
	if len(steps) != 1 {
		t.Errorf("Expected 1 trace step, got %d", len(steps))
	}
}

type mockMessage struct {
	hermod.Message
	id string
}

func (m *mockMessage) ID() string {
	return m.id
}

func (m *mockMessage) Data() map[string]any {
	return nil
}
