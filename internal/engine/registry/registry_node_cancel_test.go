package registry

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gsoultan/hermod/internal/storage"
	"github.com/gsoultan/hermod/pkg/comm/message"

	_ "github.com/gsoultan/hermod/pkg/comm/transformer/lookup"
)

func newCancelTestMessage(t *testing.T, data map[string]any) *message.DefaultMessage {
	t.Helper()
	raw, err := json.Marshal(data)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	msg := message.AcquireMessage()
	msg.SetID("m-1")
	msg.SetAfter(raw)
	msg.SetPayload(raw)
	return msg
}

// hangingEndpoint accepts a request and never answers it. It reports when a
// request arrives and when the client gives up on one, which is the only way
// to see from outside whether a node's call was cancelled or merely abandoned.
type hangingEndpoint struct {
	srv       *httptest.Server
	arrived   chan struct{}
	cancelled chan struct{}
	hits      atomic.Int64
}

func newHangingEndpoint(t *testing.T) *hangingEndpoint {
	t.Helper()
	h := &hangingEndpoint{arrived: make(chan struct{}, 64), cancelled: make(chan struct{}, 64)}
	release := make(chan struct{})
	h.srv = httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		h.hits.Add(1)
		h.arrived <- struct{}{}
		select {
		case <-r.Context().Done():
			h.cancelled <- struct{}{}
		case <-release:
		}
	}))
	t.Cleanup(func() {
		close(release)
		h.srv.Close()
	})
	return h
}

// apiLookupNode calls the endpoint with a timeout far longer than any test, so
// only cancellation can end the call promptly.
func apiLookupNode(url string) storage.WorkflowNode {
	return storage.WorkflowNode{ID: "enrich", Type: "transformation", Config: map[string]any{
		"transType":   "api_lookup",
		"method":      "GET",
		"url":         url + "/customers/C-1",
		"targetField": "tier",
		"timeout":     "1h",
	}}
}

// TestStoppingAWorkflowCancelsAnInFlightNodeCall is 3.10 of the performance
// review: RunWorkflowNode ran every node on context.Background(), so stopping a
// workflow left an api_lookup waiting on its endpoint for as long as the
// node's own timeout allowed, holding the engine's stop with it.
func TestStoppingAWorkflowCancelsAnInFlightNodeCall(t *testing.T) {
	ep := newHangingEndpoint(t)

	store := newPipeStorage()
	reg := NewRegistry(store)
	t.Cleanup(reg.Close)

	sinks := map[string]*pipeSink{"snk-out": {name: "out"}}
	wf := storage.Workflow{
		ID:   "wf-cancel-lookup",
		Name: "wf-cancel-lookup",
		Nodes: []storage.WorkflowNode{
			{ID: "src", Type: "source", RefID: "s-orders"},
			apiLookupNode(ep.srv.URL),
			{ID: "out", Type: "sink", RefID: "snk-out"},
		},
		Edges: []storage.WorkflowEdge{
			{ID: "e1", SourceID: "src", TargetID: "enrich"},
			{ID: "e2", SourceID: "enrich", TargetID: "out"},
		},
		MaxRetries:    1,
		RetryInterval: "10ms",
	}

	stop := startForeachPipeline(t, reg, wf, &arraySource{count: 1}, sinks)

	select {
	case <-ep.arrived:
	case <-time.After(30 * time.Second):
		stop()
		t.Fatal("the api_lookup node never called its endpoint")
	}

	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		stopCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = reg.StopEngine(stopCtx, wf.ID)
	}()

	select {
	case <-ep.cancelled:
	case <-time.After(10 * time.Second):
		t.Fatal("the node's HTTP call was still open 10s after the workflow was stopped; " +
			"node execution does not follow the workflow's context")
	}
	<-stopped
}

// TestRunWorkflowNodeContextHonoursCancellation pins the ctx-taking entry point
// directly: cancelling the context ends a node blocked on the network.
func TestRunWorkflowNodeContextHonoursCancellation(t *testing.T) {
	ep := newHangingEndpoint(t)
	reg := newSimRegistry(t)

	node := apiLookupNode(ep.srv.URL)
	msg := newCancelTestMessage(t, map[string]any{"customer_code": "C-1"})
	t.Cleanup(msg.Release)

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() {
		defer close(done)
		out, _, _ := reg.RunWorkflowNodeContext(ctx, "wf-1", &node, msg)
		for _, m := range out {
			m.Release()
		}
	}()

	select {
	case <-ep.arrived:
	case <-time.After(10 * time.Second):
		t.Fatal("the node never called its endpoint")
	}
	cancel()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("RunWorkflowNodeContext was still running 5s after its context was cancelled")
	}
}

// TestPauseForDebuggerReturnsWhenContextEnds: a breakpoint used to hold a
// message for up to five minutes whatever happened to the workflow, so
// stopping a workflow paused in the debugger waited out the timer.
func TestPauseForDebuggerReturnsWhenContextEnds(t *testing.T) {
	reg := NewRegistry(nil)
	t.Cleanup(reg.Close)

	msg := newCancelTestMessage(t, map[string]any{"id": 1})
	t.Cleanup(msg.Release)

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() {
		defer close(done)
		reg.PauseForDebuggerContext(ctx, "wf-dbg", "n1", msg)
	}()

	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("PauseForDebuggerContext was still waiting 5s after its context was cancelled")
	}
}
