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
	db, _ := sql.Open("sqlite", ":memory:")
	store := sqlstorage.NewSQLStorage(db, "sqlite")
	store.Init(t.Context())
	registry := engine.NewRegistry(store)
	server := NewServer(registry, store, nil, nil)

	ctx := t.Context()

	// 1. Create a sink
	sinkID := uuid.New().String()
	store.CreateSink(ctx, storage.Sink{
		ID:   sinkID,
		Name: "test_sink",
		Type: "stdout",
	})

	// 2. Create a workflow using this sink
	store.CreateWorkflow(ctx, storage.Workflow{
		ID:   "wf1",
		Name: "Workflow 1",
		Nodes: []storage.WorkflowNode{
			{ID: "n1", Type: "sink", RefID: sinkID},
		},
		Active: false,
	})

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
	_, err := store.GetSink(ctx, sinkID)
	if err != nil {
		t.Errorf("sink should still exist after failed deletion: %v", err)
	}
}
