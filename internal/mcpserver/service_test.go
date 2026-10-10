package mcpserver_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/internal/mcpserver"
	"github.com/gsoultan/hermod/internal/storage"
	"github.com/gsoultan/hermod/pkg/comm/buffer"
	"github.com/gsoultan/hermod/pkg/comm/source/webhook"
	pkgengine "github.com/gsoultan/hermod/pkg/engine"
)

// fakeStore holds workflows and sources in memory.
type fakeStore struct {
	mu        sync.Mutex
	workflows []storage.Workflow
	sources   map[string]storage.Source
	requests  []storage.WebhookRequest
}

func (f *fakeStore) ListWorkflows(_ context.Context, filter storage.CommonFilter) ([]storage.Workflow, int, error) {
	all := f.workflows
	if filter.Limit <= 0 {
		return all, len(all), nil
	}
	start := (max(filter.Page, 1) - 1) * filter.Limit
	if start >= len(all) {
		return nil, len(all), nil
	}
	end := min(start+filter.Limit, len(all))
	return all[start:end], len(all), nil
}

func (f *fakeStore) GetWorkflow(_ context.Context, id string) (storage.Workflow, error) {
	for _, wf := range f.workflows {
		if wf.ID == id {
			return wf, nil
		}
	}
	return storage.Workflow{}, storage.ErrNotFound
}

func (f *fakeStore) GetSource(_ context.Context, id string) (storage.Source, error) {
	if s, ok := f.sources[id]; ok {
		return s, nil
	}
	return storage.Source{}, storage.ErrNotFound
}

func (f *fakeStore) CreateWebhookRequest(_ context.Context, req storage.WebhookRequest) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requests = append(f.requests, req)
	return nil
}

func webhookWorkflow(id, vhost, path string, tags ...string) (storage.Workflow, storage.Source) {
	src := storage.Source{ID: "src-" + id, Type: "webhook", VHost: vhost, Config: map[string]string{"path": path}}
	wf := storage.Workflow{
		ID: id, Name: "wf " + id, VHost: vhost, Active: true, Tags: tags,
		Nodes: []storage.WorkflowNode{{ID: "n1", Type: "source", RefID: src.ID}, {ID: "n2", Type: "sink", RefID: "snk"}},
		Edges: []storage.WorkflowEdge{{ID: "e1", SourceID: "n1", TargetID: "n2"}},
	}
	return wf, src
}

func newStore(pairs ...any) *fakeStore {
	f := &fakeStore{sources: map[string]storage.Source{}}
	for _, p := range pairs {
		switch v := p.(type) {
		case storage.Workflow:
			f.workflows = append(f.workflows, v)
		case storage.Source:
			f.sources[v.ID] = v
		}
	}
	return f
}

func editorOf(vhosts ...string) mcpserver.Caller {
	return mcpserver.Caller{Name: "ed", CanRun: true, MayAccess: func(v string) bool {
		if v == "" { // the default vhost, as handlers.HasVHostAccess treats it
			return true
		}
		for _, a := range vhosts {
			if a == v {
				return true
			}
		}
		return false
	}}
}

func TestListShowsOnlyExposedWorkflowsTheCallerCanSee(t *testing.T) {
	exposed, src1 := webhookWorkflow("a", "tenant-a", "/api/webhooks/a", "mcp")
	src1.Config["response_mode"] = "sync"
	hidden, src2 := webhookWorkflow("b", "tenant-a", "/api/webhooks/b", "billing")
	otherTenant, src3 := webhookWorkflow("c", "tenant-b", "/api/webhooks/c", "MCP")
	noWebhook := storage.Workflow{ID: "d", Name: "cdc", VHost: "tenant-a", Tags: []string{" Mcp "},
		Nodes: []storage.WorkflowNode{{ID: "n1", Type: "source", RefID: "pg"}}}
	pg := storage.Source{ID: "pg", Type: "postgres"}
	svc := &mcpserver.Service{Store: newStore(exposed, src1, hidden, src2, otherTenant, src3, noWebhook, pg)}

	got, err := svc.List(t.Context(), editorOf("tenant-a"))
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("listed %d workflows, want 2 (a and d): %+v", len(got), got)
	}
	byID := map[string]mcpserver.WorkflowSummary{}
	for _, s := range got {
		byID[s.ID] = s
	}
	if a := byID["a"]; !a.Runnable || !a.RepliesWithResult {
		t.Errorf("a sync webhook workflow is listed as %+v", a)
	}
	if d, ok := byID["d"]; !ok || d.Runnable {
		t.Errorf("a workflow without a webhook source is listed as %+v (present %v)", d, ok)
	}
}

func TestStatusAndRunTreatHiddenWorkflowsAsMissing(t *testing.T) {
	hidden, src := webhookWorkflow("b", "tenant-a", "/api/webhooks/hidden")
	foreign, fsrc := webhookWorkflow("c", "tenant-b", "/api/webhooks/foreign", "mcp")
	svc := &mcpserver.Service{Store: newStore(hidden, src, foreign, fsrc)}
	caller := editorOf("tenant-a")

	for _, id := range []string{"b", "c", "nope"} {
		if _, err := svc.Status(t.Context(), caller, id); !errors.Is(err, mcpserver.ErrNotFound) {
			t.Errorf("Status(%q) = %v, want ErrNotFound", id, err)
		}
		if _, err := svc.Run(t.Context(), caller, id, nil); !errors.Is(err, mcpserver.ErrNotFound) {
			t.Errorf("Run(%q) = %v, want ErrNotFound", id, err)
		}
	}
}

func TestStatusReportsTheStoredCounters(t *testing.T) {
	wf, src := webhookWorkflow("a", "", "/api/webhooks/status", "mcp")
	wf.Status, wf.TotalProcessed, wf.TotalErrors, wf.TotalLag = "running", 10, 2, 3
	svc := &mcpserver.Service{Store: newStore(wf, src)}

	st, err := svc.Status(t.Context(), editorOf(), "a")
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if st.Status != "running" || st.Processed != 10 || st.Errors != 2 || st.Lag != 3 || !st.Active {
		t.Errorf("status = %+v", st)
	}
}

func TestAViewerCannotRun(t *testing.T) {
	wf, src := webhookWorkflow("a", "", "/api/webhooks/viewer", "mcp")
	svc := &mcpserver.Service{Store: newStore(wf, src)}
	viewer := editorOf()
	viewer.CanRun = false

	if _, err := svc.Run(t.Context(), viewer, "a", map[string]any{"x": 1}); !errors.Is(err, mcpserver.ErrNotAllowed) {
		t.Fatalf("Run as viewer = %v, want ErrNotAllowed", err)
	}
}

func TestRunRefusesAWorkflowWithoutAWebhookSource(t *testing.T) {
	wf := storage.Workflow{ID: "a", Tags: []string{"mcp"}, Nodes: []storage.WorkflowNode{{ID: "n1", Type: "source", RefID: "pg"}}}
	svc := &mcpserver.Service{Store: newStore(wf, storage.Source{ID: "pg", Type: "postgres"})}

	if _, err := svc.Run(t.Context(), editorOf(), "a", nil); !errors.Is(err, mcpserver.ErrNotRunnable) {
		t.Fatalf("Run = %v, want ErrNotRunnable", err)
	}
}

func TestRunOfAWorkflowThatIsNotListeningSaysSo(t *testing.T) {
	wf, src := webhookWorkflow("a", "", "/api/webhooks/nobody-listens", "mcp")
	woke := false
	svc := &mcpserver.Service{Store: newStore(wf, src), Wake: func(context.Context, string, string) bool {
		woke = true
		return false
	}}

	if _, err := svc.Run(t.Context(), editorOf(), "a", nil); !errors.Is(err, mcpserver.ErrNotListening) {
		t.Fatalf("Run = %v, want ErrNotListening", err)
	}
	if !woke {
		t.Error("a parked workflow was not offered a wake-up")
	}
}

// recordingSink counts what reached it.
type recordingSink struct {
	mu   sync.Mutex
	msgs []string
}

func (s *recordingSink) Write(_ context.Context, m hermod.Message) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.msgs = append(s.msgs, string(m.Payload()))
	return nil
}
func (*recordingSink) Ping(context.Context) error { return nil }
func (*recordingSink) Close() error               { return nil }

func (s *recordingSink) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.msgs)
}

// runEngine starts a real engine on the webhook path, as the registry would
// for a running workflow.
func runEngine(t *testing.T, path string, sink hermod.Sink) {
	t.Helper()
	src := webhook.NewWebhookSource(path)
	eng := pkgengine.NewEngine(src, []hermod.Sink{sink}, buffer.NewRingBuffer(8))
	eng.SetConfig(pkgengine.Config{MaxRetries: 1, RetryInterval: 10 * time.Millisecond, StatusInterval: time.Second})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = eng.Start(ctx)
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("the engine did not stop")
		}
	})
}

func TestRunOfAReplyingWorkflowReturnsWhatItDelivered(t *testing.T) {
	path := "/api/webhooks/mcp-sync"
	wf, src := webhookWorkflow("a", "", path, "mcp")
	src.Config["response_mode"] = "sync"
	store := newStore(wf, src)
	svc := &mcpserver.Service{Store: store}
	sink := &recordingSink{}
	runEngine(t, path, sink)

	res, err := svc.Run(t.Context(), editorOf(), "a", map[string]any{"order_id": 7})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Status != "delivered" || res.ID == "" || res.Error != "" {
		t.Fatalf("result = %+v", res)
	}
	rec, err := json.Marshal(res.Record)
	if err != nil || !strings.Contains(string(rec), `"order_id":7`) {
		t.Errorf("the reply does not carry the record: %s (%v)", rec, err)
	}
	if sink.count() != 1 {
		t.Errorf("the sink was written %d times", sink.count())
	}
	if len(store.requests) != 1 || store.requests[0].Path != path {
		t.Errorf("the run was not logged for replay: %+v", store.requests)
	}
}

func TestRunOfAnAsynchronousWorkflowConfirmsDispatch(t *testing.T) {
	path := "/api/webhooks/mcp-async"
	wf, src := webhookWorkflow("a", "", path, "mcp")
	svc := &mcpserver.Service{Store: newStore(wf, src)}
	sink := &recordingSink{}
	runEngine(t, path, sink)

	res, err := svc.Run(t.Context(), editorOf(), "a", map[string]any{"x": 1})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Status != "dispatched" || res.ID == "" || res.Record != nil {
		t.Fatalf("result = %+v", res)
	}
}
