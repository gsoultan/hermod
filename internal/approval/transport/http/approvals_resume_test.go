package http

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/internal/api/handlers"
	"github.com/gsoultan/hermod/internal/engine/registry"
	_ "github.com/gsoultan/hermod/internal/engine/registry/nodes"
	"github.com/gsoultan/hermod/internal/factory"
	"github.com/gsoultan/hermod/internal/storage"
	sqlstorage "github.com/gsoultan/hermod/internal/storage/sql"
	_ "modernc.org/sqlite"
)

type countingSink struct{ writes atomic.Int64 }

func (s *countingSink) Write(context.Context, hermod.Message) error { s.writes.Add(1); return nil }
func (s *countingSink) Ping(context.Context) error                  { return nil }
func (s *countingSink) Close() error                                { return nil }

// Approving a request resumes its workflow and delivers the message. The
// resume runs after the response is written, so it must not run on the
// request's context, which ends when the handler returns.
func TestApprovingResumesTheWorkflowAfterTheResponse(t *testing.T) {
	db, err := sql.Open("sqlite", "file:approvals_"+t.Name()+"?mode=memory&cache=shared")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	store := sqlstorage.NewSQLStorage(db, "sqlite")
	if err := store.Init(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateSink(t.Context(), storage.Sink{ID: "snk-1", Name: "out", Type: "sqlite",
		Config: map[string]string{"path": ":memory:", "table": "t"}}); err != nil {
		t.Fatal(err)
	}
	wf := storage.Workflow{
		ID: "wf-approve", Name: "wf-approve",
		Nodes: []storage.WorkflowNode{
			{ID: "src", Type: "source", RefID: "src-1"},
			{ID: "approve", Type: "approval"},
			{ID: "out", Type: "sink", RefID: "snk-1"},
		},
		Edges: []storage.WorkflowEdge{
			{ID: "e1", SourceID: "src", TargetID: "approve"},
			{ID: "e2", SourceID: "approve", TargetID: "out", SourceHandle: "approved"},
		},
	}
	if err := store.CreateWorkflow(t.Context(), wf); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateApproval(t.Context(), storage.Approval{
		ID: "ap-1", WorkflowID: wf.ID, NodeID: "approve", MessageID: "m-1",
		Data: map[string]any{"amount": 10}, Status: "pending", CreatedAt: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}

	out := &countingSink{}
	reg := registry.NewRegistry(store)
	t.Cleanup(reg.Close)
	reg.SetFactories(nil, func(factory.SinkConfig) (hermod.Sink, error) { return out, nil })

	h := NewApprovalHandler(&handlers.Handler{Storage: store, LogStorage: store, Registry: reg})
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/approvals/{id}/approve", h.ApproveApproval)
	// A real server, because it is the server that ends a request's context
	// when the handler returns; a recorder never does.
	srv := httptest.NewServer(mux)
	defer srv.Close()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, srv.URL+"/api/approvals/ap-1/approve", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}

	deadline := time.Now().Add(10 * time.Second)
	for out.writes.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if out.writes.Load() != 1 {
		t.Fatalf("the approved message was written %d times, want 1", out.writes.Load())
	}
}
