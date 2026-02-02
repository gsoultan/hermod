package engine

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/user/hermod"
)

type mockOutboxStorage struct {
	items []hermod.OutboxItem
	mu    sync.Mutex
}

func (m *mockOutboxStorage) CreateOutboxItem(ctx context.Context, item hermod.OutboxItem) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	item.ID = "1"
	m.items = append(m.items, item)
	return nil
}

func (m *mockOutboxStorage) ListOutboxItems(ctx context.Context, status string, limit int) ([]hermod.OutboxItem, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var filtered []hermod.OutboxItem
	for _, it := range m.items {
		if it.Status == status {
			filtered = append(filtered, it)
		}
		if len(filtered) >= limit {
			break
		}
	}
	return filtered, nil
}

func (m *mockOutboxStorage) DeleteOutboxItem(ctx context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	newItems := []hermod.OutboxItem{}
	for _, item := range m.items {
		if item.ID != id {
			newItems = append(newItems, item)
		}
	}
	m.items = newItems
	return nil
}

func (m *mockOutboxStorage) UpdateOutboxItem(ctx context.Context, item hermod.OutboxItem) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i, it := range m.items {
		if it.ID == item.ID {
			m.items[i] = item
			break
		}
	}
	return nil
}

type mockBuffer struct {
	msgs []hermod.Message
	mu   sync.Mutex
}

func (m *mockBuffer) Produce(ctx context.Context, msg hermod.Message) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.msgs = append(m.msgs, msg)
	return nil
}

func (m *mockBuffer) Close() error { return nil }

func TestEngine_OutboxRelay(t *testing.T) {
	outbox := &mockOutboxStorage{}
	buf := &mockBuffer{}

	e := &Engine{
		workflowID:  "test-wf",
		outboxStore: outbox,
		buffer:      buf,
		logger:      NewDefaultLogger(),
		config: Config{
			OutboxRelayInterval: 10 * time.Millisecond,
		},
	}

	// Add a pending item to outbox
	outbox.CreateOutboxItem(context.Background(), hermod.OutboxItem{
		WorkflowID: "test-wf",
		Payload:    []byte(`{"hello":"world"}`),
		Status:     "pending",
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go e.runOutboxRelay(ctx)

	// Wait for relay to process
	time.Sleep(100 * time.Millisecond)

	buf.mu.Lock()
	if len(buf.msgs) != 1 {
		t.Errorf("Expected 1 message in buffer, got %d", len(buf.msgs))
	}
	buf.mu.Unlock()

	outbox.mu.Lock()
	if len(outbox.items) != 1 {
		t.Errorf("Expected 1 items in outbox after relay, got %d", len(outbox.items))
	} else if outbox.items[0].Status != "processing" {
		t.Errorf("Expected status 'processing', got '%s'", outbox.items[0].Status)
	}
	outbox.mu.Unlock()
}
