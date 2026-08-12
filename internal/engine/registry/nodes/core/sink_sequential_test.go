package core

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/user/hermod"
	"github.com/user/hermod/internal/engine/registry/interfaces"
	"github.com/user/hermod/internal/storage"
	msgpkg "github.com/user/hermod/pkg/comm/message"
)

// ---------------------------------------------------------------------------
// Sequential sinks: the execution difference.
//
// The flag selects between two genuinely different models. Left off, the node
// is a pass-through and the write happens later, asynchronously, through the
// engine's sink writers. Turned on, the node writes inline and reports success
// or failure as a branch the workflow can act on — which is the entire reason
// someone turns it on.
//
// Only the flag's *resolution* was tested. The behaviour it selects was
// covered by a nightly browser spec that is not a gate, so what a workflow
// actually does when a sequential sink fails — the case the feature exists for
// — had nothing holding it in place.
// ---------------------------------------------------------------------------

// countingSink records what it was asked to write and can be told to fail.
type countingSink struct {
	hermod.Sink
	writes atomic.Int64
	err    error
}

func (c *countingSink) Write(context.Context, hermod.Message) error {
	c.writes.Add(1)
	return c.err
}

func (c *countingSink) Close() error { return nil }

// sinkCtx is a NodeContext that hands out one sink.
type sinkCtx struct {
	sink  hermod.Sink
	found bool
	logs  []string
}

func (s *sinkCtx) BroadcastLiveMessage(string, string, hermod.Message, bool, string) {}
func (s *sinkCtx) BroadcastLog(_, level, msg, _ string) {
	s.logs = append(s.logs, level+": "+msg)
}
func (s *sinkCtx) ApplyTransformation(_ context.Context, msg hermod.Message, _ string, _ map[string]any) (hermod.Message, error) {
	return msg, nil
}
func (s *sinkCtx) ContextWithPipelineSnapshot(ctx context.Context) context.Context { return ctx }
func (s *sinkCtx) EvaluateConditions(hermod.Message, []map[string]any) bool        { return true }
func (s *sinkCtx) Storage() interfaces.RegistryStorage                             { return nil }
func (s *sinkCtx) StateStore() hermod.StateStore                                   { return nil }
func (s *sinkCtx) GetNodeState(string) (any, bool)                                 { return nil, false }
func (s *sinkCtx) SetNodeState(string, any)                                        {}
func (s *sinkCtx) GetSink(string, string) (hermod.Sink, bool)                      { return s.sink, s.found }

func sinkNode(sequential bool) *storage.WorkflowNode {
	return &storage.WorkflowNode{
		ID:     "sink-1",
		Type:   "sink",
		Config: map[string]any{"sequential": sequential},
	}
}

func testMessage(t *testing.T) hermod.Message {
	t.Helper()
	m := msgpkg.AcquireMessage()
	m.SetID("m-1")
	t.Cleanup(func() { msgpkg.ReleaseMessage(m) })
	return m
}

// TestAPassThroughSinkDoesNotWriteInline. With the flag off the write belongs
// to the engine's sink writers. Writing here as well would deliver every
// message twice.
func TestAPassThroughSinkDoesNotWriteInline(t *testing.T) {
	snk := &countingSink{}
	nctx := &sinkCtx{sink: snk, found: true}

	out, status, err := (&SinkExecutor{}).Execute(
		t.Context(), nctx, "wf-1", sinkNode(false), testMessage(t))

	if err != nil {
		t.Fatalf("pass-through returned an error: %v", err)
	}
	if got := snk.writes.Load(); got != 0 {
		t.Errorf("the node wrote %d time(s) with sequential off; the sink writers "+
			"also write, so every message would be delivered twice", got)
	}
	if len(out) != 1 {
		t.Errorf("pass-through emitted %d message(s), want 1", len(out))
	}
	if status != "" {
		t.Errorf("status = %q, want empty; branches belong to sequential mode", status)
	}
}

// TestASequentialSinkWritesExactlyOnce.
func TestASequentialSinkWritesExactlyOnce(t *testing.T) {
	snk := &countingSink{}
	nctx := &sinkCtx{sink: snk, found: true}

	out, status, err := (&SinkExecutor{}).Execute(
		t.Context(), nctx, "wf-1", sinkNode(true), testMessage(t))

	if err != nil {
		t.Fatalf("a successful sequential write returned an error: %v", err)
	}
	if got := snk.writes.Load(); got != 1 {
		t.Errorf("the sink was written %d time(s), want exactly 1", got)
	}
	if status != "success" {
		t.Errorf("status = %q, want success; the success branch is what the workflow routes on", status)
	}
	if len(out) != 1 {
		t.Errorf("emitted %d message(s), want 1", len(out))
	}
}

// TestAFailedSequentialSinkTakesTheErrorBranch is the case the feature exists
// for. A workflow turns this on precisely so it can do something else when the
// write does not land — route to a dead-letter sink, raise an alert, stop.
func TestAFailedSequentialSinkTakesTheErrorBranch(t *testing.T) {
	snk := &countingSink{err: errors.New("connection refused")}
	nctx := &sinkCtx{sink: snk, found: true}

	out, status, err := (&SinkExecutor{}).Execute(
		t.Context(), nctx, "wf-1", sinkNode(true), testMessage(t))

	if err == nil {
		t.Fatal("a failed write reported success; the workflow cannot tell the " +
			"difference between landing and not landing, which is the only reason " +
			"to run a sink sequentially")
	}
	if status != "error" {
		t.Errorf("status = %q, want error; the error branch is what the workflow routes on", status)
	}
	if len(out) != 1 {
		t.Errorf("emitted %d message(s), want 1 — the error branch needs the message "+
			"to route it somewhere", len(out))
	}
	if got := snk.writes.Load(); got != 1 {
		t.Errorf("the sink was written %d time(s); a failure must not be retried here, "+
			"the engine owns retries", got)
	}
}

// TestTheFailureIsLogged: the branch is machine-readable, the log is what a
// person reads at 3am.
func TestTheFailureIsLogged(t *testing.T) {
	nctx := &sinkCtx{sink: &countingSink{err: errors.New("connection refused")}, found: true}

	_, _, _ = (&SinkExecutor{}).Execute(t.Context(), nctx, "wf-1", sinkNode(true), testMessage(t))

	var logged bool
	for _, l := range nctx.logs {
		if len(l) > 6 && l[:6] == "ERROR:" {
			logged = true
		}
	}
	if !logged {
		t.Error("a failed sequential write logged nothing at ERROR")
	}
}

// TestAMissingSinkIsAnError. A sequential node whose sink could not be built
// must not pass the message on as though it were written.
func TestAMissingSinkIsAnError(t *testing.T) {
	nctx := &sinkCtx{found: false}

	out, status, err := (&SinkExecutor{}).Execute(
		t.Context(), nctx, "wf-1", sinkNode(true), testMessage(t))

	if err == nil {
		t.Fatal("a sequential node with no sink reported success")
	}
	if status != "error" {
		t.Errorf("status = %q, want error", status)
	}
	if len(out) != 0 {
		t.Errorf("emitted %d message(s); nothing was written, so nothing should "+
			"continue as though it had been", len(out))
	}
}
