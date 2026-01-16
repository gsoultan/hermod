package engine

import (
	"context"
	"testing"

	"github.com/user/hermod/internal/storage"
)

func TestWorker_InitialSyncStartsActiveConnections(t *testing.T) {
	store := &mockStatusStorage{
		sources: map[string]storage.Source{
			"src1": {ID: "src1", Active: true, Type: "postgres", Config: map[string]string{"host": "invalid"}},
		},
		sinks: map[string]storage.Sink{
			"snk1": {ID: "snk1", Active: true, Type: "stdout"},
		},
		connections: map[string]storage.Connection{
			"conn1": {ID: "conn1", Name: "Connection 1", SourceID: "src1", SinkIDs: []string{"snk1"}, Active: true},
		},
	}

	r := NewRegistry(store)
	w := NewWorker(store, r)

	// Initial sync should start conn1
	w.sync(context.Background(), true)

	// Check if conn1 is running
	if !r.IsEngineRunning("conn1") {
		t.Error("Expected conn1 to be running after initial sync")
	}

	// Clean up
	_ = r.StopEngine("conn1")
}

func TestWorker_SyncRecoversFromPanic(t *testing.T) {
	// A mock storage that panics on ListConnections
	store := &panickingStorage{}
	r := NewRegistry(nil)
	w := NewWorker(store, r)

	// This should not panic the whole process because of recover()
	w.sync(context.Background(), true)
}

type panickingStorage struct {
	WorkerStorage
}

func (m *panickingStorage) ListConnections(ctx context.Context, filter storage.CommonFilter) ([]storage.Connection, int, error) {
	panic("database failure simulation")
}
