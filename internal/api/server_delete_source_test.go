package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/user/hermod/internal/engine"
	"github.com/user/hermod/internal/storage"
)

type deleteSourceMockStorage struct {
	storage.Storage
	source    storage.Source
	workflows []storage.Workflow
	sources   []storage.Source
	deleted   bool
}

func (m *deleteSourceMockStorage) GetSource(ctx context.Context, id string) (storage.Source, error) {
	if m.source.ID == id {
		return m.source, nil
	}
	return storage.Source{}, storage.ErrNotFound
}

func (m *deleteSourceMockStorage) ListWorkflows(ctx context.Context, filter storage.CommonFilter) ([]storage.Workflow, int, error) {
	return m.workflows, len(m.workflows), nil
}

func (m *deleteSourceMockStorage) ListSources(ctx context.Context, filter storage.CommonFilter) ([]storage.Source, int, error) {
	return m.sources, len(m.sources), nil
}

func (m *deleteSourceMockStorage) DeleteSource(ctx context.Context, id string) error {
	m.deleted = true
	return nil
}

func (m *deleteSourceMockStorage) UpdateWorkflow(ctx context.Context, wf storage.Workflow) error {
	return nil
}

func (m *deleteSourceMockStorage) CreateLog(ctx context.Context, l storage.Log) error {
	return nil
}

func (m *deleteSourceMockStorage) CreateAuditLog(ctx context.Context, log storage.AuditLog) error {
	return nil
}

func (m *deleteSourceMockStorage) ListAuditLogs(ctx context.Context, filter storage.AuditFilter) ([]storage.AuditLog, int, error) {
	return nil, 0, nil
}

func TestDeleteSourceLogic(t *testing.T) {
	registry := engine.NewRegistry(nil)

	t.Run("Prevent deletion of non-CDC source in use by workflow", func(t *testing.T) {
		store := &deleteSourceMockStorage{
			source: storage.Source{
				ID:     "src1",
				Config: map[string]string{"use_cdc": "false"},
			},
			workflows: []storage.Workflow{
				{
					ID: "wf1", Name: "Workflow 1",
					Nodes: []storage.WorkflowNode{{Type: "source", RefID: "src1"}},
				},
			},
		}
		s := NewServer(registry, store, nil, nil)

		// Create a mux to handle the path value
		mux := http.NewServeMux()
		mux.HandleFunc("DELETE /api/sources/{id}", s.deleteSource)

		req := httptest.NewRequest("DELETE", "/api/sources/src1", nil)
		rr := httptest.NewRecorder()
		mux.ServeHTTP(rr, req)

		if rr.Code != http.StatusConflict {
			t.Errorf("Expected status 409 Conflict, got %d. Body: %s", rr.Code, rr.Body.String())
		}
		if store.deleted {
			t.Error("Source was deleted but shouldn't have been")
		}
		if !strings.Contains(rr.Body.String(), "Cannot delete source: it is used by workflow") {
			t.Errorf("Expected error message about workflow usage, got: %s", rr.Body.String())
		}
	})

	t.Run("Allow deletion of CDC source in use by stopping workflow", func(t *testing.T) {
		store := &deleteSourceMockStorage{
			source: storage.Source{
				ID:     "src_cdc",
				Type:   "postgres",
				Config: map[string]string{"use_cdc": "true", "slot_name": "s1"},
			},
			workflows: []storage.Workflow{
				{
					ID: "wf1", Name: "Workflow 1", Active: true,
					Nodes: []storage.WorkflowNode{{Type: "source", RefID: "src_cdc"}},
				},
			},
			sources: []storage.Source{
				{ID: "src_cdc", Type: "postgres", Config: map[string]string{"use_cdc": "true", "slot_name": "s1"}},
				{ID: "other", Type: "postgres", Config: map[string]string{"use_cdc": "true", "slot_name": "s1"}}, // Sharing slot
			},
		}
		s := NewServer(registry, store, nil, nil)

		mux := http.NewServeMux()
		mux.HandleFunc("DELETE /api/sources/{id}", s.deleteSource)

		req := httptest.NewRequest("DELETE", "/api/sources/src_cdc", nil)
		rr := httptest.NewRecorder()
		mux.ServeHTTP(rr, req)

		if rr.Code != http.StatusNoContent {
			t.Errorf("Expected status 204 No Content, got %d. Body: %s", rr.Code, rr.Body.String())
		}
		if !store.deleted {
			t.Error("Source was not deleted but should have been")
		}
	})

	t.Run("CDC Cleanup: No other source uses slot on same DB", func(t *testing.T) {
		// This test mainly verifies that it doesn't return an error and proceeds with deletion
		store := &deleteSourceMockStorage{
			source: storage.Source{
				ID:     "src_cdc",
				Type:   "postgres",
				Config: map[string]string{"use_cdc": "true", "slot_name": "s1", "host": "localhost", "database": "db1"},
			},
			workflows: []storage.Workflow{},
			sources: []storage.Source{
				{ID: "src_cdc", Type: "postgres", Config: map[string]string{"use_cdc": "true", "slot_name": "s1", "host": "localhost", "database": "db1"}},
				{ID: "other", Type: "postgres", Config: map[string]string{"use_cdc": "true", "slot_name": "s1", "host": "other_host", "database": "db1"}}, // Same slot but different DB
			},
		}
		s := NewServer(registry, store, nil, nil)

		mux := http.NewServeMux()
		mux.HandleFunc("DELETE /api/sources/{id}", s.deleteSource)

		req := httptest.NewRequest("DELETE", "/api/sources/src_cdc", nil)
		rr := httptest.NewRecorder()
		mux.ServeHTTP(rr, req)

		if rr.Code != http.StatusNoContent {
			t.Errorf("Expected status 204 No Content, got %d", rr.Code)
		}
		if !store.deleted {
			t.Error("Source was not deleted but should have been")
		}
	})

	t.Run("MSSQL CDC Cleanup: Table used by another source", func(t *testing.T) {
		store := &deleteSourceMockStorage{
			source: storage.Source{
				ID:     "mssql1",
				Type:   "mssql",
				Config: map[string]string{"use_cdc": "true", "tables": "dbo.table1", "host": "localhost", "dbname": "db1"},
			},
			workflows: []storage.Workflow{},
			sources: []storage.Source{
				{ID: "mssql1", Type: "mssql", Config: map[string]string{"use_cdc": "true", "tables": "dbo.table1", "host": "localhost", "dbname": "db1"}},
				{ID: "mssql2", Type: "mssql", Config: map[string]string{"use_cdc": "true", "tables": "dbo.table1", "host": "localhost", "dbname": "db1"}},
			},
		}
		s := NewServer(registry, store, nil, nil)

		mux := http.NewServeMux()
		mux.HandleFunc("DELETE /api/sources/{id}", s.deleteSource)

		req := httptest.NewRequest("DELETE", "/api/sources/mssql1", nil)
		rr := httptest.NewRecorder()
		mux.ServeHTTP(rr, req)

		if rr.Code != http.StatusNoContent {
			t.Errorf("Expected status 204 No Content, got %d", rr.Code)
		}
		if !store.deleted {
			t.Error("Source was not deleted but should have been")
		}
		// Note: We can't easily verify if dropMSSQLCDC was called without more mocking,
		// but we verify the code path doesn't crash and proceeds with deletion.
	})

	t.Run("Yugabyte CDC Cleanup logic", func(t *testing.T) {
		store := &deleteSourceMockStorage{
			source: storage.Source{
				ID:     "yb1",
				Type:   "yugabyte",
				Config: map[string]string{"use_cdc": "true", "slot_name": "s1", "host": "localhost", "dbname": "db1"},
			},
			workflows: []storage.Workflow{},
			sources: []storage.Source{
				{ID: "yb1", Type: "yugabyte", Config: map[string]string{"use_cdc": "true", "slot_name": "s1", "host": "localhost", "dbname": "db1"}},
				{ID: "other", Type: "postgres", Config: map[string]string{"use_cdc": "true", "slot_name": "s1", "host": "localhost", "dbname": "db1"}}, // Cross-type sharing
			},
		}
		s := NewServer(registry, store, nil, nil)

		mux := http.NewServeMux()
		mux.HandleFunc("DELETE /api/sources/{id}", s.deleteSource)

		req := httptest.NewRequest("DELETE", "/api/sources/yb1", nil)
		rr := httptest.NewRecorder()
		mux.ServeHTTP(rr, req)

		if rr.Code != http.StatusNoContent {
			t.Errorf("Expected status 204 No Content, got %d", rr.Code)
		}
		if !store.deleted {
			t.Error("Source was not deleted but should have been")
		}
	})

	t.Run("Allow deletion of unused non-CDC source", func(t *testing.T) {
		store := &deleteSourceMockStorage{
			source: storage.Source{
				ID:     "src_unused",
				Config: map[string]string{"use_cdc": "false"},
			},
			workflows: []storage.Workflow{},
		}
		s := NewServer(registry, store, nil, nil)

		mux := http.NewServeMux()
		mux.HandleFunc("DELETE /api/sources/{id}", s.deleteSource)

		req := httptest.NewRequest("DELETE", "/api/sources/src_unused", nil)
		rr := httptest.NewRecorder()
		mux.ServeHTTP(rr, req)

		if rr.Code != http.StatusNoContent {
			t.Errorf("Expected status 204 No Content, got %d", rr.Code)
		}
		if !store.deleted {
			t.Error("Source was not deleted but should have been")
		}
	})
}
