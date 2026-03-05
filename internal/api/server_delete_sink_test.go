package api

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/user/hermod/internal/engine"
	"github.com/user/hermod/internal/storage"
	sqlstorage "github.com/user/hermod/internal/storage/sql"
	_ "modernc.org/sqlite"
)

func TestDeleteSinkProtection(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("failed to open db: %v", err)
	}
	db.SetMaxOpenConns(1)
	store := sqlstorage.NewSQLStorage(db, "sqlite")
	if err := store.Init(t.Context()); err != nil {
		t.Fatalf("failed to init store: %v", err)
	}
	registry := engine.NewRegistry(store)
	server := NewServer(registry, store, nil, nil)

	ctx := t.Context()

	// 1. Create a sink
	sinkID := uuid.New().String()
	if err := store.CreateSink(ctx, storage.Sink{
		ID:   sinkID,
		Name: "test_sink",
		Type: "stdout",
	}); err != nil {
		t.Fatalf("failed to create sink: %v", err)
	}

	// 2. Create a workflow using this sink
	if err := store.CreateWorkflow(ctx, storage.Workflow{
		ID:   "wf1",
		Name: "Workflow 1",
		Nodes: []storage.WorkflowNode{
			{ID: "n1", Type: "sink", RefID: sinkID},
		},
		Active: false,
	}); err != nil {
		t.Fatalf("failed to create workflow: %v", err)
	}

	// 3. Try to delete the sink
	req := httptest.NewRequest("DELETE", "/api/sinks/"+sinkID, nil)
	req.SetPathValue("id", sinkID)
	rr := httptest.NewRecorder()

	server.deleteSink(rr, req)

	if rr.Code != http.StatusConflict {
		t.Errorf("expected status 409 Conflict for sink in use, got %d", rr.Code)
	}

	var resp map[string]string
	json.Unmarshal(rr.Body.Bytes(), &resp)
	if resp["error"] == "" {
		t.Error("expected error message in response")
	}

	// 4. Verify sink still exists
	_, err = store.GetSink(ctx, sinkID)
	if err != nil {
		t.Errorf("sink should still exist after failed deletion: %v", err)
	}
}
