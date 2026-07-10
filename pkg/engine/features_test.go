package engine

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/user/hermod"
	"github.com/user/hermod/pkg/comm/buffer"
	"github.com/user/hermod/pkg/comm/message"
	"github.com/user/hermod/pkg/infra/schema"
)

func TestEngineSchemaValidation(t *testing.T) {
	// 1. Setup Source with one valid and one invalid message
	msg1 := message.AcquireMessage()
	msg1.SetID("1")
	msg1.SetData("id", 1)
	msg1.SetData("name", "Valid")

	msg2 := message.AcquireMessage()
	msg2.SetID("2")
	msg2.SetData("id", "invalid")
	msg2.SetData("name", "Invalid")

	src := &schemaMockSource{
		messages: []hermod.Message{msg1, msg2},
	}

	// 2. Setup Sink
	snk := &schemaMockSink{received: make(chan hermod.Message, 2)}
	dlq := &schemaMockSink{received: make(chan hermod.Message, 2)}

	// 3. Setup Engine with JSON Schema
	eng := NewEngine(src, []hermod.Sink{snk}, buffer.NewRingBuffer(10))
	eng.SetDeadLetterSink(dlq)

	jsonSchema := `{
		"type": "object",
		"properties": {
			"id": { "type": "integer" },
			"name": { "type": "string" }
		},
		"required": ["id", "name"]
	}`

	v, err := schema.NewValidator(schema.SchemaConfig{
		Type:   schema.JSONSchema,
		Schema: jsonSchema,
	})
	if err != nil {
		t.Fatalf("failed to create validator: %v", err)
	}
	eng.SetValidator(v)

	// 4. Run Engine
	ctx, cancel := context.WithTimeout(t.Context(), 1*time.Second)
	defer cancel()

	go func() {
		_ = eng.Start(ctx)
	}()

	// 5. Wait for message or timeout
	select {
	case msg := <-snk.received:
		if msg.ID() != "1" {
			t.Errorf("expected message 1 to pass, got %s", msg.ID())
		}
	case <-time.After(500 * time.Millisecond):
		t.Error("timeout waiting for valid message")
	}

	// Ensure second message DID NOT pass but went to DLQ
	select {
	case msg := <-snk.received:
		t.Errorf("unexpected message passed schema validation: %s", msg.ID())
	case msg := <-dlq.received:
		if msg.ID() != "2" {
			t.Errorf("expected message 2 in DLQ, got %s", msg.ID())
		}
	case <-time.After(200 * time.Millisecond):
		t.Error("timeout waiting for DLQ message")
	}
}

type schemaMockSource struct {
	messages []hermod.Message
	index    int
}

func (s *schemaMockSource) Read(ctx context.Context) (hermod.Message, error) {
	if s.index >= len(s.messages) {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	msg := s.messages[s.index]
	s.index++
	return msg, nil
}

func (s *schemaMockSource) Ack(ctx context.Context, msg hermod.Message) error { return nil }
func (s *schemaMockSource) Ping(ctx context.Context) error                    { return nil }
func (s *schemaMockSource) Close() error                                      { return nil }

type schemaMockSink struct {
	received chan hermod.Message
}

func (s *schemaMockSink) Write(ctx context.Context, msg hermod.Message) error {
	s.received <- msg.Clone()
	return nil
}

func (s *schemaMockSink) Ping(ctx context.Context) error { return nil }
func (s *schemaMockSink) Close() error                   { return nil }

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
	e := NewEngine(nil, nil, nil)
	e.traceRecorder = recorder
	e.workflowID = "test-workflow"
	e.config.TraceSampleRate = 0.5 // 50% sampling

	msgID := "consistent-msg-id"
	msg := &mockMessage{id: msgID}

	// Record multiple steps for the same message
	for i := 0; i < 100; i++ {
		e.RecordTraceStep(t.Context(), msg, "node-1", time.Now(), nil, nil)
	}

	// Wait for async processing
	var steps []hermod.TraceStep
	for i := 0; i < 100; i++ {
		steps = recorder.GetSteps(msgID)
		if len(steps) > 0 {
			if len(steps) == 100 {
				break
			}
		} else {
			// If not sampled in after some time, it might be sampled out
			if i > 20 {
				break
			}
		}
		time.Sleep(10 * time.Millisecond)
	}

	// With deterministic sampling, it should be either 100 or 0 steps.
	if len(steps) > 0 && len(steps) < 100 {
		t.Errorf("Inconsistent sampling for same message: got %d steps, expected 0 or 100", len(steps))
	}

	// Now check that different messages can have different sampling outcomes
	sampledIn := 0
	sampledOut := 0
	for i := 0; i < 1000; i++ {
		mID := fmt.Sprintf("msg-%d", i)
		m := &mockMessage{id: mID}
		e.RecordTraceStep(t.Context(), m, "node-1", time.Now(), nil, nil)
	}

	// Wait for async processing
	time.Sleep(500 * time.Millisecond)

	for i := 0; i < 1000; i++ {
		mID := fmt.Sprintf("msg-%d", i)
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
	e := NewEngine(nil, nil, nil)
	e.traceRecorder = recorder
	e.workflowID = "test-workflow"
	e.config = DefaultConfig()

	msgID := "test-msg"
	msg := &mockMessage{id: msgID}

	e.RecordTraceStep(t.Context(), msg, "node-1", time.Now(), nil, nil)

	// Wait for async processing
	var steps []hermod.TraceStep
	for i := 0; i < 50; i++ {
		steps = recorder.GetSteps(msgID)
		if len(steps) == 1 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	if len(steps) != 1 {
		t.Errorf("Expected 1 trace step, got %d", len(steps))
	}
}

type mockMessage struct {
	hermod.Message
	id   string
	meta map[string]string
}

func (m *mockMessage) ID() string {
	return m.id
}

func (m *mockMessage) Data() map[string]any {
	return nil
}

func (m *mockMessage) DataRef() map[string]any {
	return nil
}

func (m *mockMessage) Metadata() map[string]string {
	if m.meta == nil {
		m.meta = make(map[string]string)
	}
	return m.meta
}

func (m *mockMessage) MetadataRef() map[string]string {
	if m.meta == nil {
		m.meta = make(map[string]string)
	}
	return m.meta
}

func (m *mockMessage) SetMetadata(key, value string) {
	if m.meta == nil {
		m.meta = make(map[string]string)
	}
	m.meta[key] = value
}

func (m *mockMessage) Retain()  {}
func (m *mockMessage) Release() {}
func (m *mockMessage) ToMap() map[string]any {
	return nil
}

type safeModeMockSink struct {
	writeCount int
}

func (m *safeModeMockSink) Write(ctx context.Context, msg hermod.Message) error {
	m.writeCount++
	return nil
}
func (m *safeModeMockSink) Ping(ctx context.Context) error { return nil }
func (m *safeModeMockSink) Close() error                   { return nil }

func TestSafeModeDivertsToDLQ(t *testing.T) {
	sink := &safeModeMockSink{}
	dlq := &safeModeMockSink{}

	eng := NewEngine(nil, []hermod.Sink{sink}, nil)
	eng.SetDeadLetterSink(dlq)

	msg1 := message.AcquireMessage()
	msg1.SetData("test", "normal")

	// Test normal mode
	err := eng.writeToSink(context.Background(), sink, msg1, "sink1", 0)
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}
	if sink.writeCount != 1 {
		t.Errorf("Expected 1 write to primary sink, got %d", sink.writeCount)
	}
	if dlq.writeCount != 0 {
		t.Errorf("Expected 0 writes to DLQ, got %d", dlq.writeCount)
	}

	// Test safe mode
	eng.SetSafeMode(true)
	msg2 := message.AcquireMessage()
	msg2.SetData("test", "safe")

	err = eng.writeToSink(context.Background(), sink, msg2, "sink1", 0)
	if err != nil {
		t.Fatalf("Expected no error in safe mode, got %v", err)
	}
	if sink.writeCount != 1 {
		t.Errorf("Expected primary sink count to remain 1, got %d", sink.writeCount)
	}
	if dlq.writeCount != 1 {
		t.Errorf("Expected 1 write to DLQ, got %d", dlq.writeCount)
	}

	if msg2.Metadata()["_hermod_safe_mode"] != "true" {
		t.Errorf("Expected _hermod_safe_mode metadata to be set")
	}
}
