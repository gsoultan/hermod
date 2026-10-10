package registry

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/internal/storage"
	"github.com/gsoultan/hermod/pkg/comm/transformer"
)

// flakyTransformer fails its first failUntil calls, then passes messages
// through. failUntil < 0 fails every call.
type flakyTransformer struct {
	calls     atomic.Int64
	failUntil atomic.Int64
}

func (f *flakyTransformer) Transform(_ context.Context, msg hermod.Message, _ map[string]any) (hermod.Message, error) {
	n := f.calls.Add(1)
	if limit := f.failUntil.Load(); limit < 0 || n <= limit {
		return nil, errors.New("upstream answered 503: transient")
	}
	return msg, nil
}

var flaky = func() *flakyTransformer {
	f := &flakyTransformer{}
	transformer.Register("test_flaky_retry", f)
	return f
}()

func retryWorkflow(retry map[string]any) storage.Workflow {
	return storage.Workflow{
		ID:   "wf-node-retry",
		Name: "wf-node-retry",
		Nodes: []storage.WorkflowNode{
			{ID: "src", Type: "source", RefID: "s-records"},
			{ID: "flaky", Type: "transformation", Config: map[string]any{
				"transType": "test_flaky_retry",
				"retry":     retry,
			}},
			{ID: "out", Type: "sink", RefID: "snk-out"},
		},
		Edges: []storage.WorkflowEdge{
			{ID: "e1", SourceID: "src", TargetID: "flaky"},
			{ID: "e2", SourceID: "flaky", TargetID: "out"},
		},
		DeadLetterSinkID: "snk-dlq",
		MaxRetries:       1,
		RetryInterval:    "10ms",
	}
}

// A node with a retry policy is retried by the running workflow until it
// succeeds, and the message is delivered once.
func TestANodeRetryPolicyRetriesInARunningWorkflow(t *testing.T) {
	flaky.calls.Store(0)
	flaky.failUntil.Store(2)
	reg := NewRegistry(newPipeStorage())
	sinks := map[string]*pipeSink{"snk-out": {name: "out"}, "snk-dlq": {name: "dlq"}}

	stop := startForeachPipeline(t, reg,
		retryWorkflow(map[string]any{"maxAttempts": float64(3), "backoff": "5ms"}),
		&arraySource{count: 1}, sinks)
	defer stop()

	if !waitUntil(t, 15*time.Second, "the retried message to be delivered", func() bool {
		return sinks["snk-out"].count() >= 1
	}) {
		t.Fatalf("the message was never delivered; the node ran %d time(s)", flaky.calls.Load())
	}
	if got := flaky.calls.Load(); got != 3 {
		t.Errorf("the node ran %d times, want 3 (two failures, then success)", got)
	}
	if got := sinks["snk-dlq"].count(); got != 0 {
		t.Errorf("%d message(s) were dead-lettered although a retry succeeded", got)
	}
}

// Once the last attempt fails the message is dead-lettered, as a failing node
// without a retry policy is.
func TestANodeThatFailsEveryAttemptIsDeadLettered(t *testing.T) {
	flaky.calls.Store(0)
	flaky.failUntil.Store(-1)
	reg := NewRegistry(newPipeStorage())
	sinks := map[string]*pipeSink{"snk-out": {name: "out"}, "snk-dlq": {name: "dlq"}}

	stop := startForeachPipeline(t, reg,
		retryWorkflow(map[string]any{"maxAttempts": float64(3), "backoff": "5ms"}),
		&arraySource{count: 1}, sinks)
	defer stop()

	if !waitUntil(t, 15*time.Second, "the failed message to be dead-lettered", func() bool {
		return sinks["snk-dlq"].count() >= 1
	}) {
		t.Fatalf("the message was never dead-lettered; the node ran %d time(s)", flaky.calls.Load())
	}
	if got := flaky.calls.Load(); got != 3 {
		t.Errorf("the node ran %d times, want exactly maxAttempts (3)", got)
	}
	if got := sinks["snk-out"].count(); got != 0 {
		t.Errorf("%d message(s) reached the sink through a node that never succeeded", got)
	}
}
