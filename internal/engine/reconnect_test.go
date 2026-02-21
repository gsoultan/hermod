package engine

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/user/hermod"
	"github.com/user/hermod/internal/storage"
	"github.com/user/hermod/pkg/buffer"
	"github.com/user/hermod/pkg/engine"
)

type mockSource struct {
	hermod.Source
	pingErr   error
	pingCount int
	mu        sync.Mutex
}

func (m *mockSource) Ping(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.pingCount++
	return m.pingErr
}

func (m *mockSource) Read(ctx context.Context) (hermod.Message, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

func (m *mockSource) Close() error {
	return nil
}

type mockSink struct {
	hermod.Sink
}

func (m *mockSink) Ping(ctx context.Context) error {
	return nil
}

func (m *mockSink) Close() error {
	return nil
}

type mockSimpleStorage struct {
	BaseMockStorage
	wf storage.Workflow
	mu sync.Mutex
}

func (m *mockSimpleStorage) GetWorkflow(ctx context.Context, id string) (storage.Workflow, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.wf, nil
}

func (m *mockSimpleStorage) UpdateWorkflow(ctx context.Context, wf storage.Workflow) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.wf = wf
	return nil
}

func TestEngineReconnect(t *testing.T) {
	src := &mockSource{pingErr: errors.New("connection refused")}
	store := &mockSimpleStorage{
		wf: storage.Workflow{ID: "test-wf", Active: true},
	}

	r := NewRegistry(store)
	r.SetConfig(engine.Config{
		ReconnectInterval: 10 * time.Millisecond,
	})

	// Create engine manually to use our mock source
	// Registry doesn't let us inject mock sources easily without registering them.
	// But we can register a mock source type.

	// For simplicity of this test, let's just test pkg/engine directly

	buf := buffer.NewRingBuffer(10)
	eng := engine.NewEngine(src, []hermod.Sink{&mockSink{}}, buf)
	eng.SetConfig(engine.Config{
		ReconnectInterval: 10 * time.Millisecond,
	})

	var status string
	var statusMu sync.Mutex
	eng.SetOnStatusChange(func(u engine.StatusUpdate) {
		statusMu.Lock()
		status = u.EngineStatus
		statusMu.Unlock()
	})

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	go eng.Start(ctx)

	// Wait for reconnecting status
	time.Sleep(50 * time.Millisecond)

	statusMu.Lock()
	if status != "reconnecting:source" {
		t.Errorf("Expected status to be reconnecting:source, got %s", status)
	}
	statusMu.Unlock()

	src.mu.Lock()
	if src.pingCount < 2 {
		t.Errorf("Expected multiple ping attempts, got %d", src.pingCount)
	}
	src.mu.Unlock()

	// Fix source and verify it becomes running
	src.mu.Lock()
	src.pingErr = nil
	src.mu.Unlock()

	time.Sleep(50 * time.Millisecond)

	statusMu.Lock()
	if status != "running" {
		t.Errorf("Expected status to be running, got %s", status)
	}
	statusMu.Unlock()
}
