package engine

import (
	"context"
	"testing"

	"github.com/user/hermod/internal/storage"
)

type mockConfigStorage struct {
	storage.Storage
	sources     map[string]storage.Source
	sinks       map[string]storage.Sink
	connections []storage.Connection
}

func (m *mockConfigStorage) ListConnections(ctx context.Context, filter storage.CommonFilter) ([]storage.Connection, int, error) {
	return m.connections, len(m.connections), nil
}

func (m *mockConfigStorage) GetSource(ctx context.Context, id string) (storage.Source, error) {
	s, ok := m.sources[id]
	if !ok {
		return storage.Source{}, storage.ErrNotFound
	}
	return s, nil
}

func (m *mockConfigStorage) GetSink(ctx context.Context, id string) (storage.Sink, error) {
	s, ok := m.sinks[id]
	if !ok {
		return storage.Sink{}, storage.ErrNotFound
	}
	return s, nil
}

func (m *mockConfigStorage) GetWorker(ctx context.Context, id string) (storage.Worker, error) {
	return storage.Worker{}, nil
}

func TestWorker_ConfigChange(t *testing.T) {
	src := storage.Source{
		ID:   "src1",
		Type: "sqlite",
		Config: map[string]string{
			"path": "old.db",
		},
	}
	conn := storage.Connection{
		ID:       "conn1",
		SourceID: "src1",
		Active:   true,
	}

	store := &mockConfigStorage{
		sources:     map[string]storage.Source{"src1": src},
		sinks:       make(map[string]storage.Sink),
		connections: []storage.Connection{conn},
	}

	registry := NewRegistry(nil)
	worker := NewWorker(store, registry)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Initial sync
	worker.sync(ctx, true)

	if !registry.IsEngineRunning("conn1") {
		t.Fatal("Engine should be running after initial sync")
	}

	// Update source config in storage
	// Use a new map to avoid reference sharing in the mock
	store.sources["src1"] = storage.Source{
		ID:   "src1",
		Type: "sqlite",
		Config: map[string]string{
			"path": "new.db",
		},
	}

	// Sync again
	worker.sync(ctx, false)

	// Now it should have restarted. We can check if the config in registry is updated.
	newCurSrcCfg, _, _, _, ok := registry.GetEngineConfigs("conn1")
	if !ok {
		t.Fatal("Engine should be running after second sync")
	}

	if newCurSrcCfg.Config["path"] != "new.db" {
		t.Errorf("Expected config path to be 'new.db', got '%s'", newCurSrcCfg.Config["path"])
	}
}
