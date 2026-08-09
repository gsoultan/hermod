package http

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/user/hermod/internal/api/handlers"
	"github.com/user/hermod/internal/storage"
	"github.com/user/hermod/internal/testutil"
)

// failingWorkflowStorage accepts the sources and sinks in a bundle but refuses
// to persist the workflow itself — the shape of a constraint violation, a
// permission error or a database outage part-way through an import.
type failingWorkflowStorage struct {
	testutil.BaseMockStorage
	existing  map[string]storage.Workflow
	createErr error
	updateErr error

	created int
	updated int
}

func (m *failingWorkflowStorage) GetWorkflow(ctx context.Context, id string) (storage.Workflow, error) {
	wf, ok := m.existing[id]
	if !ok {
		return storage.Workflow{}, storage.ErrNotFound
	}
	return wf, nil
}

func (m *failingWorkflowStorage) CreateWorkflow(ctx context.Context, wf storage.Workflow) error {
	m.created++
	return m.createErr
}

func (m *failingWorkflowStorage) UpdateWorkflow(ctx context.Context, wf storage.Workflow) error {
	m.updated++
	return m.updateErr
}

func (m *failingWorkflowStorage) GetSource(ctx context.Context, id string) (storage.Source, error) {
	return storage.Source{}, storage.ErrNotFound
}
func (m *failingWorkflowStorage) GetSink(ctx context.Context, id string) (storage.Sink, error) {
	return storage.Sink{}, storage.ErrNotFound
}
func (m *failingWorkflowStorage) CreateSource(ctx context.Context, s storage.Source) error {
	return nil
}
func (m *failingWorkflowStorage) CreateSink(ctx context.Context, s storage.Sink) error { return nil }

// TestImportWorkflowReportsStorageFailure covers a silently swallowed error in
// the import handler.
//
// ImportWorkflow decided create-vs-update with
//
//	if _, err := h.Storage.GetWorkflow(ctx, id); err == nil {
//	    err = h.Storage.UpdateWorkflow(...)
//	} else {
//	    err = h.Storage.CreateWorkflow(...)
//	}
//	if err != nil { ... }
//
// The `err` declared in the if's init statement is scoped to the if/else, so
// both assignments wrote to a variable that died at the closing brace. The
// `if err != nil` that follows reads the *function-scoped* err from the earlier
// io.ReadAll, which is always nil by then. A workflow that failed to save was
// therefore reported to the user as imported successfully — the UI shows a
// green result and the workflow simply is not there.
func TestImportWorkflowReportsStorageFailure(t *testing.T) {
	bundle := storage.WorkflowExportBundle{
		Workflow: storage.Workflow{ID: "wf-import-1", Name: "Imported"},
	}
	body, err := json.Marshal(bundle)
	if err != nil {
		t.Fatalf("marshalling bundle: %v", err)
	}

	for _, tc := range []struct {
		name     string
		existing map[string]storage.Workflow
		store    *failingWorkflowStorage
	}{
		{
			name: "create fails",
			store: &failingWorkflowStorage{
				existing:  map[string]storage.Workflow{},
				createErr: errors.New("insert violates a constraint"),
			},
		},
		{
			name: "update fails",
			store: &failingWorkflowStorage{
				existing:  map[string]storage.Workflow{"wf-import-1": {ID: "wf-import-1"}},
				updateErr: errors.New("database is read-only"),
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := &WorkflowHandler{
				Handler: &handlers.Handler{Storage: tc.store, LogStorage: tc.store},
			}
			mux := http.NewServeMux()
			h.RegisterWorkflowRoutes(mux)

			req := httptest.NewRequestWithContext(t.Context(), "POST", "/api/workflows/import", bytes.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			rr := httptest.NewRecorder()
			mux.ServeHTTP(rr, req)

			if rr.Code < 400 {
				t.Errorf("import returned HTTP %d after storage refused to save the workflow; "+
					"the caller is told the import succeeded when nothing was written (body: %s)",
					rr.Code, rr.Body.String())
			}
		})
	}
}

// TestImportWorkflowSucceedsWhenStorageAccepts is the control: the handler must
// still report success on the happy path.
func TestImportWorkflowSucceedsWhenStorageAccepts(t *testing.T) {
	bundle := storage.WorkflowExportBundle{
		Workflow: storage.Workflow{ID: "wf-import-ok", Name: "Imported"},
	}
	body, err := json.Marshal(bundle)
	if err != nil {
		t.Fatalf("marshalling bundle: %v", err)
	}

	store := &failingWorkflowStorage{existing: map[string]storage.Workflow{}}
	h := &WorkflowHandler{Handler: &handlers.Handler{Storage: store, LogStorage: store}}
	mux := http.NewServeMux()
	h.RegisterWorkflowRoutes(mux)

	req := httptest.NewRequestWithContext(t.Context(), "POST", "/api/workflows/import", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code >= 400 {
		t.Fatalf("import failed on the happy path: HTTP %d, body %s", rr.Code, rr.Body.String())
	}
	if store.created != 1 {
		t.Errorf("CreateWorkflow called %d times, want 1", store.created)
	}
}
