package http

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/user/hermod/internal/api/handlers"
	"github.com/user/hermod/internal/storage"
	"github.com/user/hermod/internal/testutil"
)

type MockWorkflowStorage struct {
	testutil.BaseMockStorage
	workflows map[string]storage.Workflow
	sources   map[string]storage.Source
	sinks     map[string]storage.Sink
}

func (m *MockWorkflowStorage) GetWorkflow(ctx context.Context, id string) (storage.Workflow, error) {
	wf, ok := m.workflows[id]
	if !ok {
		return storage.Workflow{}, storage.ErrNotFound
	}
	return wf, nil
}

func (m *MockWorkflowStorage) GetSource(ctx context.Context, id string) (storage.Source, error) {
	src, ok := m.sources[id]
	if !ok {
		return storage.Source{}, storage.ErrNotFound
	}
	return src, nil
}

func (m *MockWorkflowStorage) GetSink(ctx context.Context, id string) (storage.Sink, error) {
	snk, ok := m.sinks[id]
	if !ok {
		return storage.Sink{}, storage.ErrNotFound
	}
	return snk, nil
}

func (m *MockWorkflowStorage) CreateWorkflow(ctx context.Context, wf storage.Workflow) error {
	m.workflows[wf.ID] = wf
	return nil
}

func (m *MockWorkflowStorage) CreateSource(ctx context.Context, src storage.Source) error {
	m.sources[src.ID] = src
	return nil
}

func (m *MockWorkflowStorage) CreateSink(ctx context.Context, snk storage.Sink) error {
	m.sinks[snk.ID] = snk
	return nil
}

func TestWorkflowExportImport(t *testing.T) {
	mockStorage := &MockWorkflowStorage{
		workflows: make(map[string]storage.Workflow),
		sources:   make(map[string]storage.Source),
		sinks:     make(map[string]storage.Sink),
	}

	h := &WorkflowHandler{
		Handler: &handlers.Handler{
			Storage:    mockStorage,
			LogStorage: mockStorage,
		},
	}

	mux := http.NewServeMux()
	h.RegisterWorkflowRoutes(mux)

	// 1. Setup a workflow with a source and a sink
	wfID := "wf-1"
	srcID := "src-1"
	snkID := "snk-1"

	mockStorage.workflows[wfID] = storage.Workflow{
		ID:   wfID,
		Name: "Test Workflow",
		Nodes: []storage.WorkflowNode{
			{ID: "n1", Type: "source", RefID: srcID},
			{ID: "n2", Type: "sink", RefID: snkID},
		},
	}
	mockStorage.sources[srcID] = storage.Source{ID: srcID, Name: "Test Source"}
	mockStorage.sinks[snkID] = storage.Sink{ID: snkID, Name: "Test Sink"}

	// 2. Test Export via Mux
	req := httptest.NewRequest("GET", "/api/workflows/"+wfID+"/export", nil)
	rr := httptest.NewRecorder()

	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("Export via Mux failed with status: %d, body: %s", rr.Code, rr.Body.String())
	}

	var bundle storage.WorkflowExportBundle
	if err := json.NewDecoder(rr.Body).Decode(&bundle); err != nil {
		t.Fatalf("Failed to decode export bundle: %v", err)
	}

	if bundle.Workflow.ID != wfID {
		t.Errorf("Expected workflow ID %s, got %s", wfID, bundle.Workflow.ID)
	}
	if len(bundle.Sources) != 1 || bundle.Sources[0].ID != srcID {
		t.Errorf("Expected 1 source with ID %s", srcID)
	}
	if len(bundle.Sinks) != 1 || bundle.Sinks[0].ID != snkID {
		t.Errorf("Expected 1 sink with ID %s", snkID)
	}

	// 3. Test Import (to a \"new instance\", so we clear the storage)
	mockStorage.workflows = make(map[string]storage.Workflow)
	mockStorage.sources = make(map[string]storage.Source)
	mockStorage.sinks = make(map[string]storage.Sink)

	bundleBytes, _ := json.Marshal(bundle)
	importReq := httptest.NewRequest("POST", "/api/workflows/import", bytes.NewReader(bundleBytes))
	importRR := httptest.NewRecorder()

	h.ImportWorkflow(importRR, importReq)

	if importRR.Code != http.StatusCreated {
		t.Fatalf("Import failed with status: %d, body: %s", importRR.Code, importRR.Body.String())
	}

	if _, ok := mockStorage.workflows[wfID]; !ok {
		t.Error("Workflow was not imported")
	}
	if _, ok := mockStorage.sources[srcID]; !ok {
		t.Error("Source was not imported")
	}
	if _, ok := mockStorage.sinks[snkID]; !ok {
		t.Error("Sink was not imported")
	}

	// 4. Test Import Legacy (single workflow)
	mockStorage.workflows = make(map[string]storage.Workflow)
	wfOnlyBytes, _ := json.Marshal(bundle.Workflow)
	legacyReq := httptest.NewRequest("POST", "/api/workflows/import", bytes.NewReader(wfOnlyBytes))
	legacyRR := httptest.NewRecorder()

	h.ImportWorkflow(legacyRR, legacyReq)
	if legacyRR.Code != http.StatusCreated {
		t.Fatalf("Legacy import failed with status: %d, body: %s", legacyRR.Code, legacyRR.Body.String())
	}
	if _, ok := mockStorage.workflows[wfID]; !ok {
		t.Error("Legacy workflow was not imported")
	}
}
