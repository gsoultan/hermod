package engine

import (
	"context"
	"testing"
	"time"

	"github.com/user/hermod"
)

type priorityMockSource struct {
	msgs []hermod.Message
	idx  int
	acks []string
}

func (m *priorityMockSource) Read(ctx context.Context) (hermod.Message, error) {
	if m.idx >= len(m.msgs) {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(100 * time.Millisecond):
			return nil, nil
		}
	}
	msg := m.msgs[m.idx]
	m.idx++
	return msg, nil
}

func (m *priorityMockSource) Ack(ctx context.Context, msg hermod.Message) error {
	m.acks = append(m.acks, msg.ID())
	return nil
}

func (m *priorityMockSource) Ping(ctx context.Context) error { return nil }
func (m *priorityMockSource) Close() error                   { return nil }

type priorityMockMessage struct {
	id string
	md map[string]string
}

func (m *priorityMockMessage) ID() string                  { return m.id }
func (m *priorityMockMessage) Metadata() map[string]string { return m.md }
func (m *priorityMockMessage) SetMetadata(k, v string)     { m.md[k] = v }
func (m *priorityMockMessage) Operation() hermod.Operation { return hermod.OpCreate }
func (m *priorityMockMessage) Table() string               { return "" }
func (m *priorityMockMessage) Schema() string              { return "" }
func (m *priorityMockMessage) Before() []byte              { return nil }
func (m *priorityMockMessage) After() []byte               { return nil }
func (m *priorityMockMessage) Payload() []byte             { return nil }
func (m *priorityMockMessage) Data() map[string]any        { return nil }
func (m *priorityMockMessage) SetData(k string, v any)     {}
func (m *priorityMockMessage) Clone() hermod.Message       { return m }
func (m *priorityMockMessage) ClearPayloads()              {}

func TestPrioritySource(t *testing.T) {
	recovery := &priorityMockSource{
		msgs: []hermod.Message{
			&priorityMockMessage{id: "rec-1", md: make(map[string]string)},
		},
	}
	primary := &priorityMockSource{
		msgs: []hermod.Message{
			&priorityMockMessage{id: "pri-1", md: make(map[string]string)},
		},
	}

	ps := NewPrioritySource(recovery, primary, nil)

	ctx := context.Background()

	// 1. Should read from recovery first
	m1, err := ps.Read(ctx)
	if err != nil {
		t.Fatalf("Failed to read m1: %v", err)
	}
	if m1.ID() != "rec-1" {
		t.Errorf("Expected rec-1, got %s", m1.ID())
	}
	if m1.Metadata()["_hermod_source"] != "recovery" {
		t.Errorf("Expected source metadata 'recovery', got %s", m1.Metadata()["_hermod_source"])
	}

	// 2. Should fallback to primary when recovery is empty
	m2, err := ps.Read(ctx)
	if err != nil {
		t.Fatalf("Failed to read m2: %v", err)
	}
	if m2.ID() != "pri-1" {
		t.Errorf("Expected pri-1, got %s", m2.ID())
	}
	if m2.Metadata()["_hermod_source"] != "primary" {
		t.Errorf("Expected source metadata 'primary', got %s", m2.Metadata()["_hermod_source"])
	}

	// 3. Test Ack routing
	err = ps.Ack(ctx, m1)
	if err != nil {
		t.Errorf("Ack m1 failed: %v", err)
	}
	if len(recovery.acks) != 1 || recovery.acks[0] != "rec-1" {
		t.Errorf("Ack m1 did not reach recovery source correctly")
	}

	err = ps.Ack(ctx, m2)
	if err != nil {
		t.Errorf("Ack m2 failed: %v", err)
	}
	if len(primary.acks) != 1 || primary.acks[0] != "pri-1" {
		t.Errorf("Ack m2 did not reach primary source correctly")
	}
}
