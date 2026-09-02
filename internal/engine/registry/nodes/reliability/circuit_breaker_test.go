package reliability

import (
	"context"
	"testing"
	"time"

	hermod "github.com/gsoultan/Hermod"
	"github.com/gsoultan/Hermod/internal/engine/registry/interfaces"
	"github.com/gsoultan/Hermod/internal/storage"
	msgpkg "github.com/gsoultan/Hermod/pkg/comm/message"
)

// ---------------------------------------------------------------------------
// The circuit breaker had no tests, and it cannot trip.
//
// It is offered in the editor as "Stop flow on failure threshold". It reads a
// failure count out of node state and opens once that count passes a threshold
// — but nothing anywhere increments the count. Every message takes the success
// branch, whatever is happening downstream, so a workflow relying on it for
// protection has none.
//
// A breaker that cannot open is worse than no breaker: it is a control someone
// believes is there.
// ---------------------------------------------------------------------------

type cbCtx struct{ state map[string]any }

func newCBCtx() *cbCtx { return &cbCtx{state: map[string]any{}} }

func (c *cbCtx) BroadcastLiveMessage(string, string, hermod.Message, bool, string) {}
func (c *cbCtx) BroadcastLog(string, string, string, string)                       {}
func (c *cbCtx) ApplyTransformation(_ context.Context, m hermod.Message, _ string, _ map[string]any) (hermod.Message, error) {
	return m, nil
}
func (c *cbCtx) ContextWithPipelineSnapshot(ctx context.Context) context.Context { return ctx }
func (c *cbCtx) EvaluateConditions(hermod.Message, []map[string]any) bool        { return true }
func (c *cbCtx) Storage() interfaces.RegistryStorage                             { return nil }
func (c *cbCtx) StateStore() hermod.StateStore                                   { return nil }
func (c *cbCtx) GetNodeState(key string) (any, bool)                             { v, ok := c.state[key]; return v, ok }
func (c *cbCtx) SetNodeState(key string, val any)                                { c.state[key] = val }
func (c *cbCtx) GetSink(string, string) (hermod.Sink, bool)                      { return nil, false }

func cbNode(threshold float64) *storage.WorkflowNode {
	return &storage.WorkflowNode{
		ID:     "cb1",
		Type:   "circuit_breaker",
		Config: map[string]any{"failure_threshold": threshold},
	}
}

func cbMessage(t *testing.T) hermod.Message {
	t.Helper()
	m := msgpkg.AcquireMessage()
	m.SetID("m-1")
	t.Cleanup(func() { msgpkg.ReleaseMessage(m) })
	return m
}

// TestTheBreakerOpensOnceFailuresPassTheThreshold is the behaviour the node is
// named for.
func TestTheBreakerOpensOnceFailuresPassTheThreshold(t *testing.T) {
	e := &CircuitBreakerExecutor{}
	nctx := newCBCtx()
	node := cbNode(3)

	for i := range 3 {
		_, status, err := e.Execute(t.Context(), nctx, "wf", node, cbMessage(t))
		if err != nil {
			t.Fatalf("execute %d: %v", i, err)
		}
		if status != "success" {
			t.Fatalf("message %d took the %q branch before the threshold was reached", i, status)
		}
		// The downstream failed, which is what a breaker counts.
		e.RecordFailure(nctx, node.ID)
	}

	_, status, err := e.Execute(t.Context(), nctx, "wf", node, cbMessage(t))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if status != "failure" {
		t.Errorf("after 3 failures against a threshold of 3 the breaker is %q; it never "+
			"opens, so a workflow relying on it for protection has none", status)
	}
}

// TestASuccessResetsTheCount. A breaker that counts failures for all time trips
// on a healthy system that has ever had a bad hour.
func TestASuccessResetsTheCount(t *testing.T) {
	e := &CircuitBreakerExecutor{}
	nctx := newCBCtx()
	node := cbNode(3)

	e.RecordFailure(nctx, node.ID)
	e.RecordFailure(nctx, node.ID)
	e.RecordSuccess(nctx, node.ID)
	e.RecordFailure(nctx, node.ID)
	e.RecordFailure(nctx, node.ID)

	_, status, err := e.Execute(t.Context(), nctx, "wf", node, cbMessage(t))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if status != "success" {
		t.Errorf("the breaker opened at %q after two failures either side of a success; "+
			"consecutive failures are what indicate a broken downstream", status)
	}
}

// TestAnOpenBreakerHalfOpensAfterTheCooldown, so a recovered downstream is
// retried rather than shut out for good.
func TestAnOpenBreakerHalfOpensAfterTheCooldown(t *testing.T) {
	e := &CircuitBreakerExecutor{}
	nctx := newCBCtx()
	node := cbNode(1)

	e.RecordFailure(nctx, node.ID)
	if _, status, _ := e.Execute(t.Context(), nctx, "wf", node, cbMessage(t)); status != "failure" {
		t.Fatalf("the breaker did not open: %q", status)
	}

	// Age the failure past the cooldown.
	nctx.SetNodeState("cb_"+node.ID, cbState{
		Status:      "OPEN",
		Failures:    1,
		LastFailure: time.Now().Add(-time.Hour),
	})

	if _, status, _ := e.Execute(t.Context(), nctx, "wf", node, cbMessage(t)); status != "success" {
		t.Errorf("after the cooldown the breaker is still %q; a downstream that recovered "+
			"would never be retried", status)
	}
}

// TestStaleFailuresDoNotCount.
//
// A breaker counts *recent* failures, not every failure since the process
// started. Without that, a healthy system that had one bad ten minutes last
// week trips on its next single failure and stays tripped.
//
// It also decides where the counting happens. Resetting on success would mean
// every successful message doing a reverse-edge lookup to find its breaker,
// which is a cost on the hot path to serve a rare case. Ageing the count out
// costs nothing when nothing is failing.
func TestStaleFailuresDoNotCount(t *testing.T) {
	e := &CircuitBreakerExecutor{}
	nctx := newCBCtx()
	node := cbNode(2)

	// Two failures, long enough ago to be irrelevant.
	nctx.SetNodeState("cb_"+node.ID, cbState{
		Status:      "CLOSED",
		Failures:    2,
		LastFailure: time.Now().Add(-time.Hour),
	})

	_, status, err := e.Execute(t.Context(), nctx, "wf", node, cbMessage(t))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if status != "success" {
		t.Errorf("the breaker is %q on failures an hour old; a bad hour last week would "+
			"leave it tripped for good", status)
	}
}

// TestRecentFailuresStillCount is the other half — ageing them out must not
// mean ignoring them.
func TestRecentFailuresStillCount(t *testing.T) {
	e := &CircuitBreakerExecutor{}
	nctx := newCBCtx()
	node := cbNode(2)

	nctx.SetNodeState("cb_"+node.ID, cbState{
		Status:      "CLOSED",
		Failures:    2,
		LastFailure: time.Now(),
	})

	_, status, err := e.Execute(t.Context(), nctx, "wf", node, cbMessage(t))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if status != "failure" {
		t.Errorf("the breaker is %q after two failures just now against a threshold of 2", status)
	}
}
