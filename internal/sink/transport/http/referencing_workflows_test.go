package http

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gsoultan/hermod/internal/api/handlers"
	"github.com/gsoultan/hermod/internal/storage"
)

// fakeSinkStorage answers only what this handler asks for. Anything else is a
// nil call on the embedded interface, which is a panic that names the method —
// better than a zero value that quietly changes what the test proves.
type fakeSinkStorage struct {
	storage.Storage
	sink storage.Sink
	wfs  []storage.Workflow
}

func (f *fakeSinkStorage) GetSink(_ context.Context, id string) (storage.Sink, error) {
	if f.sink.ID != id {
		return storage.Sink{}, storage.ErrNotFound
	}
	return f.sink, nil
}

func (f *fakeSinkStorage) ListWorkflows(_ context.Context, _ storage.CommonFilter) ([]storage.Workflow, int, error) {
	return f.wfs, len(f.wfs), nil
}

func asAdministrator(r *http.Request) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), handlers.UserContextKey,
		&storage.User{Role: storage.RoleAdministrator}))
}

// The sink form asks which workflows use a sink before letting anyone edit it,
// and shows "stop these first" when any of them is running. It asked
// /api/sinks/{id}/workflows, which was never registered: the request 404'd, the
// UI raised "Request Failed — Not Found" on every sink edit page, and the
// warning could never appear. The source side has had the route all along.
func TestListWorkflowsReferencingSink(t *testing.T) {
	store := &fakeSinkStorage{
		sink: storage.Sink{ID: "sink-1", Name: "mailer"},
		wfs: []storage.Workflow{
			{
				ID: "wf-live", Name: "orders", Active: true, Status: "running",
				Nodes: []storage.WorkflowNode{{Type: "sink", RefID: "sink-1"}},
			},
			{
				ID: "wf-stopped", Name: "old", Active: false,
				Nodes: []storage.WorkflowNode{{Type: "sink", RefID: "sink-1"}},
			},
			{
				ID: "wf-other", Name: "elsewhere", Active: true,
				Nodes: []storage.WorkflowNode{{Type: "sink", RefID: "sink-2"}},
			},
			{
				// A source that happens to carry the same id must not count.
				ID: "wf-source", Name: "reader", Active: true,
				Nodes: []storage.WorkflowNode{{Type: "source", RefID: "sink-1"}},
			},
		},
	}
	h := &SinkHandler{Handler: &handlers.Handler{Storage: store}}

	mux := http.NewServeMux()
	h.RegisterSinkRoutes(mux)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, asAdministrator(httptest.NewRequestWithContext(
		t.Context(), http.MethodGet, "/api/sinks/sink-1/workflows", nil)))

	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	var got struct {
		Data []struct {
			ID     string `json:"id"`
			Name   string `json:"name"`
			Active bool   `json:"active"`
		} `json:"data"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decoding: %v", err)
	}
	if len(got.Data) != 1 {
		t.Fatalf("listed %+v, want only the running workflow that uses this sink", got.Data)
	}
	if got.Data[0].ID != "wf-live" || !got.Data[0].Active {
		t.Errorf("listed %+v, want wf-live", got.Data[0])
	}
}

func TestListWorkflowsReferencingSink_UnknownSink(t *testing.T) {
	h := &SinkHandler{Handler: &handlers.Handler{Storage: &fakeSinkStorage{sink: storage.Sink{ID: "sink-1"}}}}
	mux := http.NewServeMux()
	h.RegisterSinkRoutes(mux)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, asAdministrator(httptest.NewRequestWithContext(
		t.Context(), http.MethodGet, "/api/sinks/nope/workflows", nil)))

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status %d, want 404: %s", rec.Code, rec.Body.String())
	}
}
