package engine

import (
	"context"
	"testing"
	"time"

	"github.com/user/hermod/internal/storage"
)

type mockStatusStorage struct {
	storage.Storage
	sources     map[string]storage.Source
	sinks       map[string]storage.Sink
	connections map[string]storage.Connection
}

func (m *mockStatusStorage) GetConnection(ctx context.Context, id string) (storage.Connection, error) {
	conn, ok := m.connections[id]
	if !ok {
		return storage.Connection{}, storage.ErrNotFound
	}
	return conn, nil
}

func (m *mockStatusStorage) ListConnections(ctx context.Context, filter storage.CommonFilter) ([]storage.Connection, int, error) {
	var list []storage.Connection
	for _, c := range m.connections {
		list = append(list, c)
	}
	return list, len(list), nil
}

func (m *mockStatusStorage) UpdateConnection(ctx context.Context, conn storage.Connection) error {
	m.connections[conn.ID] = conn
	return nil
}

func (m *mockStatusStorage) GetSource(ctx context.Context, id string) (storage.Source, error) {
	src, ok := m.sources[id]
	if !ok {
		return storage.Source{}, storage.ErrNotFound
	}
	return src, nil
}

func (m *mockStatusStorage) UpdateSource(ctx context.Context, src storage.Source) error {
	m.sources[src.ID] = src
	return nil
}

func (m *mockStatusStorage) GetSink(ctx context.Context, id string) (storage.Sink, error) {
	snk, ok := m.sinks[id]
	if !ok {
		return storage.Sink{}, storage.ErrNotFound
	}
	return snk, nil
}

func (m *mockStatusStorage) UpdateSink(ctx context.Context, snk storage.Sink) error {
	m.sinks[snk.ID] = snk
	return nil
}

func (m *mockStatusStorage) CreateLog(ctx context.Context, l storage.Log) error {
	return nil
}

func (m *mockStatusStorage) GetSetting(ctx context.Context, key string) (string, error) {
	return "", nil
}

func (m *mockStatusStorage) ListSources(ctx context.Context, filter storage.CommonFilter) ([]storage.Source, int, error) {
	var list []storage.Source
	for _, s := range m.sources {
		list = append(list, s)
	}
	return list, len(list), nil
}

func (m *mockStatusStorage) ListSinks(ctx context.Context, filter storage.CommonFilter) ([]storage.Sink, int, error) {
	var list []storage.Sink
	for _, s := range m.sinks {
		list = append(list, s)
	}
	return list, len(list), nil
}

func (m *mockStatusStorage) GetWorker(ctx context.Context, id string) (storage.Worker, error) {
	return storage.Worker{}, nil
}

func (m *mockStatusStorage) CreateWorker(ctx context.Context, worker storage.Worker) error {
	return nil
}

func (m *mockStatusStorage) UpdateWorkerHeartbeat(ctx context.Context, id string) error {
	return nil
}

func TestRegistry_StatusUpdate_MultipleConnections(t *testing.T) {
	store := &mockStatusStorage{
		sources: map[string]storage.Source{
			"src1": {ID: "src1", Active: true},
		},
		sinks: map[string]storage.Sink{
			"snk1": {ID: "snk1", Active: true},
		},
		connections: map[string]storage.Connection{
			"conn1": {ID: "conn1", SourceID: "src1", SinkIDs: []string{"snk1"}, Active: true},
			"conn2": {ID: "conn2", SourceID: "src1", SinkIDs: []string{"snk1"}, Active: true},
		},
	}

	r := NewRegistry(store)

	// Simulate engine 1 starting and then stopping naturally
	// We don't actually need to start the engine, we can just call the logic that handles its stop
	// But registry.StartEngine has that logic in a goroutine.
	// For testing, we might want to refactor the status update logic into a separate method or just trigger it.

	// Let's simulate what happens when conn1 stops
	// Currently, in registry.go:
	/*
				if conn, err := r.storage.GetConnection(ctx, id); err == nil {
					conn.Active = false
					_ = r.storage.UpdateConnection(ctx, conn)

					if src, err := r.storage.GetSource(ctx, conn.SourceID); err == nil {
						src.Active = false
						_ = r.storage.UpdateSource(ctx, src)
					}
		            ...
				}
	*/

	// We can't easily trigger the registry logic without starting an actual engine because it's inside a goroutine in StartEngine.
	// However, we can use StartEngine and wait for it to stop if we use a sink that fails immediately.

	// Let's make the sink fail instead of the source, because source failures now trigger reconnection
	// while sink failures still turn off the connection.
	err := r.StartEngine("conn1", SourceConfig{ID: "src1", Type: "sqlite", Config: map[string]string{"path": ":memory:"}}, []SinkConfig{{ID: "snk1", Type: "postgres", Config: map[string]string{"host": "invalid"}}}, nil, nil, nil)
	if err != nil {
		t.Fatalf("Failed to start engine: %v", err)
	}

	// Wait for engine to stop and update status
	// Since it will fail pinging or starting, it should update status quickly.
	// We need to wait for it. Let's use IsEngineRunning to wait.
	for i := 0; i < 500; i++ {
		if !r.IsEngineRunning("conn1") {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	if r.IsEngineRunning("conn1") {
		t.Fatal("Engine conn1 is still running after waiting")
	}

	// Verify status
	conn1Result, _ := store.GetConnection(context.Background(), "conn1")
	if conn1Result.Active {
		t.Error("conn1 should be inactive")
	}

	src1Result, _ := store.GetSource(context.Background(), "src1")
	if !src1Result.Active {
		t.Error("src1 should still be active because conn2 is active")
	}

	snk1Result, _ := store.GetSink(context.Background(), "snk1")
	if !snk1Result.Active {
		t.Error("snk1 should still be active because conn2 is active")
	}
}

func TestRegistry_StatusUpdate_SingleConnection(t *testing.T) {
	store := &mockStatusStorage{
		sources: map[string]storage.Source{
			"src1": {ID: "src1", Active: true},
		},
		sinks: map[string]storage.Sink{
			"snk1": {ID: "snk1", Active: true},
		},
		connections: map[string]storage.Connection{
			"conn1": {ID: "conn1", SourceID: "src1", SinkIDs: []string{"snk1"}, Active: true},
		},
	}

	r := NewRegistry(store)

	// Use sink failure to trigger deactivation
	err := r.StartEngine("conn1", SourceConfig{ID: "src1", Type: "sqlite", Config: map[string]string{"path": ":memory:"}}, []SinkConfig{{ID: "snk1", Type: "postgres", Config: map[string]string{"host": "invalid"}}}, nil, nil, nil)
	if err != nil {
		t.Fatalf("Failed to start engine: %v", err)
	}

	for i := 0; i < 500; i++ {
		if !r.IsEngineRunning("conn1") {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	if r.IsEngineRunning("conn1") {
		t.Fatal("Engine conn1 is still running after waiting")
	}

	// Verify status
	conn1, _ := store.GetConnection(context.Background(), "conn1")
	if conn1.Active {
		t.Error("conn1 should be inactive")
	}

	src1, _ := store.GetSource(context.Background(), "src1")
	if src1.Active {
		t.Error("src1 should be inactive because it's only used by conn1")
	}

	snk1, _ := store.GetSink(context.Background(), "snk1")
	if snk1.Active {
		t.Error("snk1 should be inactive because it's only used by conn1")
	}
}
