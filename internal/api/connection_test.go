package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/user/hermod/internal/engine"
	"github.com/user/hermod/internal/storage"
)

type connectionMockStorage struct {
	storage.Storage
	conn storage.Connection
}

func (m *connectionMockStorage) GetConnection(ctx context.Context, id string) (storage.Connection, error) {
	return m.conn, nil
}

func (m *connectionMockStorage) UpdateConnection(ctx context.Context, conn storage.Connection) error {
	m.conn = conn
	return nil
}

type fullMockStorage struct {
	storage.Storage
	connections map[string]storage.Connection
	sources     map[string]storage.Source
	sinks       map[string]storage.Sink
}

func (m *fullMockStorage) GetConnection(ctx context.Context, id string) (storage.Connection, error) {
	c, ok := m.connections[id]
	if !ok {
		return storage.Connection{}, storage.ErrNotFound
	}
	return c, nil
}

func (m *fullMockStorage) ListConnections(ctx context.Context) ([]storage.Connection, error) {
	var list []storage.Connection
	for _, c := range m.connections {
		list = append(list, c)
	}
	return list, nil
}

func (m *fullMockStorage) UpdateConnection(ctx context.Context, conn storage.Connection) error {
	m.connections[conn.ID] = conn
	return nil
}

func (m *fullMockStorage) GetSource(ctx context.Context, id string) (storage.Source, error) {
	s, ok := m.sources[id]
	if !ok {
		return storage.Source{}, storage.ErrNotFound
	}
	return s, nil
}

func (m *fullMockStorage) UpdateSource(ctx context.Context, src storage.Source) error {
	m.sources[src.ID] = src
	return nil
}

func (m *fullMockStorage) GetSink(ctx context.Context, id string) (storage.Sink, error) {
	s, ok := m.sinks[id]
	if !ok {
		return storage.Sink{}, storage.ErrNotFound
	}
	return s, nil
}

func (m *fullMockStorage) UpdateSink(ctx context.Context, snk storage.Sink) error {
	m.sinks[snk.ID] = snk
	return nil
}

func (m *fullMockStorage) CreateLog(ctx context.Context, l storage.Log) error {
	return nil
}

func TestToggleConnection_MultipleConnections(t *testing.T) {
	store := &fullMockStorage{
		connections: map[string]storage.Connection{
			"conn1": {ID: "conn1", SourceID: "src1", SinkIDs: []string{"snk1"}, Active: true},
			"conn2": {ID: "conn2", SourceID: "src1", SinkIDs: []string{"snk1"}, Active: true},
		},
		sources: map[string]storage.Source{
			"src1": {ID: "src1", Active: true},
		},
		sinks: map[string]storage.Sink{
			"snk1": {ID: "snk1", Active: true},
		},
	}

	registry := engine.NewRegistry(store)
	server := NewServer(registry, store)

	// conn1 is already running in registry for this test to work (StopEngine is called)
	// Actually we don't need it to be running if we just want to test the status update logic in toggleConnection
	// but registry.StopEngine will return an error if it's not running.
	// We can manually add it to registry.

	// Since we can't easily access registry.engines, we'll start it and then stop it.
	// But StartEngine might fail if it can't connect.
	// Let's use a source that succeeds ping.
	// actually, toggleConnection doesn't care if StopEngine fails, it will return 500.

	// Let's just mock the engine running.
	// We can't.

	// Okay, let's just test that it calls StopEngine and handles status.
	// I'll start an engine that will stay running (sqlite with a real-ish path maybe?)
	// Actually, I'll just use the registry as is and expect it to fail if I don't start it.

	t.Run("Toggle conn1 off - shared source/sink should stay active", func(t *testing.T) {
		// Start engine first so toggle (stop) works
		_ = registry.StartEngine("conn1", engine.SourceConfig{ID: "src1", Type: "sqlite", Config: map[string]string{"path": "test.db"}}, []engine.SinkConfig{{ID: "snk1", Type: "stdout"}}, nil, nil)

		req := httptest.NewRequest("POST", "/api/connections/conn1/toggle", nil)
		req.SetPathValue("id", "conn1")
		rr := httptest.NewRecorder()

		server.toggleConnection(rr, req)

		if rr.Code != http.StatusOK {
			t.Errorf("expected status 200, got %d. Body: %s", rr.Code, rr.Body.String())
		}

		conn1, _ := store.GetConnection(context.Background(), "conn1")
		if conn1.Active {
			t.Error("conn1 should be inactive")
		}

		src1, _ := store.GetSource(context.Background(), "src1")
		if !src1.Active {
			t.Error("src1 should still be active because conn2 is active")
		}

		snk1, _ := store.GetSink(context.Background(), "snk1")
		if !snk1.Active {
			t.Error("snk1 should still be active because conn2 is active")
		}
	})
}

func TestUpdateConnectionActiveCheck(t *testing.T) {
	registry := engine.NewRegistry(nil)

	t.Run("Update active connection should fail", func(t *testing.T) {
		mockStore := &connectionMockStorage{
			conn: storage.Connection{
				ID:     "test-conn",
				Name:   "Active Connection",
				Active: true,
			},
		}
		server := NewServer(registry, mockStore)

		connUpdate := storage.Connection{
			Name:     "Updated Name",
			VHost:    "default",
			SourceID: "src1",
			SinkIDs:  []string{"snk1"},
		}
		body, _ := json.Marshal(connUpdate)

		req := httptest.NewRequest("PUT", "/api/connections/test-conn", bytes.NewBuffer(body))
		req.SetPathValue("id", "test-conn")

		rr := httptest.NewRecorder()
		server.updateConnection(rr, req)

		if rr.Code != http.StatusBadRequest {
			t.Errorf("expected status 400, got %d", rr.Code)
		}

		expectedError := "connection must be inactive to edit\n"
		if rr.Body.String() != expectedError {
			t.Errorf("expected error message %q, got %q", expectedError, rr.Body.String())
		}
	})

	t.Run("Update inactive connection should succeed", func(t *testing.T) {
		mockStore := &connectionMockStorage{
			conn: storage.Connection{
				ID:     "test-conn-2",
				Name:   "Inactive Connection",
				Active: false,
			},
		}
		server := NewServer(registry, mockStore)

		connUpdate := storage.Connection{
			Name:     "Updated Name",
			VHost:    "default",
			SourceID: "src1",
			SinkIDs:  []string{"snk1"},
		}
		body, _ := json.Marshal(connUpdate)

		req := httptest.NewRequest("PUT", "/api/connections/test-conn-2", bytes.NewBuffer(body))
		req.SetPathValue("id", "test-conn-2")

		rr := httptest.NewRecorder()
		server.updateConnection(rr, req)

		if rr.Code != http.StatusOK {
			t.Errorf("expected status 200, got %d. Body: %s", rr.Code, rr.Body.String())
		}

		if mockStore.conn.Name != "Updated Name" {
			t.Errorf("expected name to be updated to 'Updated Name', got %q", mockStore.conn.Name)
		}
	})
}
