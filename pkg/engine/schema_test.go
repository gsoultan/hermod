package engine

import (
	"context"
	"testing"
	"time"

	"github.com/user/hermod"
	"github.com/user/hermod/pkg/buffer"
	"github.com/user/hermod/pkg/message"
	"github.com/user/hermod/pkg/schema"
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
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
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
