package engine

import (
	"context"
	"testing"
	"time"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/pkg/comm/buffer"
)

// The "router" trace step is not a node in anyone's workflow — it is the
// engine's record of the routing decision. For a node-graph workflow the router
// *is* the traversal: setupWorkflowRouter walks the whole DAG inside it, so
// every transformation has already run by the time it returns.
//
// That made the step's two halves describe different moments. Its timestamp is
// when routing began, which puts it second in a trace ordered by timestamp,
// right after workflow_start and before every node. Its payload was captured
// when the step was recorded, which is after the last node. The viewer
// reconstructs each step's "before" from the previous step's "after"
// (sqlStorage.GetMessageTrace), so a reader saw the pipeline's *output* filed
// between the message arriving and the source node emitting it — and then saw
// the source node appear to delete every field the pipeline had added.
//
// A router decides where a message goes; it is not a transformation. Its step
// records the message it was handed.
func TestRouterTraceStepRecordsTheMessageItWasHanded(t *testing.T) {
	src := &oneShotSource{}
	sink := newTallySink()
	recorder := &mockTraceRecorder{}

	eng := NewEngine(src, []hermod.Sink{sink}, buffer.NewRingBuffer(8))
	eng.SetLogger(&testLogger{})
	eng.traceRecorder = recorder
	eng.workflowID = "wf-router-trace"
	eng.config.TraceSampleRate = 1.0

	// Stand in for a db_lookup node: the traversal enriches the message in place
	// before the router returns.
	eng.SetRouter(func(ctx context.Context, msg hermod.Message) ([]RoutedMessage, error) {
		msg.SetData("enriched", "added by a node inside the traversal")
		msg.Retain()
		return []RoutedMessage{{SinkIndex: 0, Message: msg}}, nil
	})

	runUntilSinkWrite(t, eng, sink)

	var router *hermod.TraceStep
	var start *hermod.TraceStep
	for i, s := range recorder.GetSteps("m-1") {
		switch s.NodeID {
		case "router":
			router = &recorder.GetSteps("m-1")[i]
		case "workflow_start":
			start = &recorder.GetSteps("m-1")[i]
		}
	}
	if start == nil || router == nil {
		t.Fatalf("expected workflow_start and router steps, got %+v", recorder.GetSteps("m-1"))
	}

	if _, ok := start.After["enriched"]; ok {
		t.Fatalf("workflow_start recorded a field the traversal added later: %#v", start.After)
	}
	if _, ok := router.After["enriched"]; ok {
		t.Errorf("the router step recorded %#v, which includes a field added by a node that runs *inside* the router; "+
			"the step is timestamped before every node, so the viewer shows the pipeline's output between the "+
			"message arriving and the first node running", router.After)
	}
	if router.After["v"] != start.After["v"] {
		t.Errorf("router step recorded v=%#v, want the routed message's v=%#v", router.After["v"], start.After["v"])
	}
}

// A router that fails still records a step, and it still records the message it
// was handed rather than nothing.
func TestRouterTraceStepRecordsThePayloadWhenRoutingFails(t *testing.T) {
	src := &oneShotSource{}
	sink := newTallySink()
	recorder := &mockTraceRecorder{}

	eng := NewEngine(src, []hermod.Sink{sink}, buffer.NewRingBuffer(8))
	eng.SetLogger(&testLogger{})
	eng.traceRecorder = recorder
	eng.workflowID = "wf-router-trace-err"
	eng.config.TraceSampleRate = 1.0

	eng.SetRouter(func(ctx context.Context, msg hermod.Message) ([]RoutedMessage, error) {
		return nil, context.DeadlineExceeded
	})

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() { defer close(done); _ = eng.Start(ctx) }()

	var router *hermod.TraceStep
	for range 100 {
		for i, s := range recorder.GetSteps("m-1") {
			if s.NodeID == "router" {
				router = &recorder.GetSteps("m-1")[i]
			}
		}
		if router != nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	<-done

	if router == nil {
		t.Fatalf("a failed routing recorded no router step: %+v", recorder.GetSteps("m-1"))
	}
	if router.Error == "" {
		t.Errorf("the router step recorded no error for a routing failure: %+v", router)
	}
	if router.After["v"] == nil {
		t.Errorf("the router step recorded no payload for the message it failed to route: %#v", router.After)
	}
}

// runUntilSinkWrite starts the engine, waits for the sink's first write, and
// stops. It is runOneMessage against the real sink rather than the dead-letter
// one.
func runUntilSinkWrite(t *testing.T, eng *Engine, sink *tallySink) {
	t.Helper()

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = eng.Start(ctx)
	}()

	select {
	case <-sink.first:
	case <-time.After(3 * time.Second):
		t.Fatal("the sink was never written to")
	}

	// Trace steps are recorded on their own goroutines, so the last of them can
	// land after the write that released this test.
	time.Sleep(250 * time.Millisecond)

	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the engine did not stop")
	}
}
