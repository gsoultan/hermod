package flow

import (
	"context"
	"testing"

	"github.com/user/hermod"
	"github.com/user/hermod/internal/engine/registry/interfaces"
	"github.com/user/hermod/internal/storage"
	msgpkg "github.com/user/hermod/pkg/comm/message"
)

// ---------------------------------------------------------------------------
// The join node had no tests at all.
//
// It holds messages in node state until enough have arrived for a key, then
// merges and emits them. Two things follow from that and neither was covered:
// the state it reads back has to be the shape it wrote, and the messages it
// holds are retained, so whatever stops holding them has to release them.
// ---------------------------------------------------------------------------

type joinCtx struct {
	state map[string]any
	logs  []string
}

func newJoinCtx() *joinCtx { return &joinCtx{state: map[string]any{}} }

func (c *joinCtx) BroadcastLiveMessage(string, string, hermod.Message, bool, string) {}
func (c *joinCtx) BroadcastLog(_, level, msg, _ string)                              { c.logs = append(c.logs, level+": "+msg) }
func (c *joinCtx) ApplyTransformation(_ context.Context, m hermod.Message, _ string, _ map[string]any) (hermod.Message, error) {
	return m, nil
}
func (c *joinCtx) ContextWithPipelineSnapshot(ctx context.Context) context.Context { return ctx }
func (c *joinCtx) EvaluateConditions(hermod.Message, []map[string]any) bool        { return true }
func (c *joinCtx) Storage() interfaces.RegistryStorage                             { return nil }
func (c *joinCtx) StateStore() hermod.StateStore                                   { return nil }

// GetNodeState mirrors the registry: a key set to nil still exists.
func (c *joinCtx) GetNodeState(key string) (any, bool) {
	v, ok := c.state[key]
	return v, ok
}
func (c *joinCtx) SetNodeState(key string, val any)           { c.state[key] = val }
func (c *joinCtx) GetSink(string, string) (hermod.Sink, bool) { return nil, false }

func joinNode() *storage.WorkflowNode {
	return &storage.WorkflowNode{
		ID:     "j1",
		Type:   "join",
		Config: map[string]any{"key_path": "order_id", "expected_sources": float64(2)},
	}
}

func joinMessage(t *testing.T, orderID, field string) hermod.Message {
	t.Helper()
	m := msgpkg.AcquireMessage()
	m.SetID(field)
	m.SetData("order_id", orderID)
	m.SetData(field, true)
	return m
}

// TestASecondJoinOnTheSameKeyDoesNotPanic.
//
// A completed join clears its slot by setting the state to nil rather than
// deleting it, and the registry stores nil as a value — so the key still
// exists. The next message for that key read it back and asserted it to a
// message slice without checking, which panics on a nil interface and takes the
// workflow's goroutine with it.
//
// A join key is a value out of the data, so this is not an edge case: it fires
// the second time any key repeats.
func TestASecondJoinOnTheSameKeyDoesNotPanic(t *testing.T) {
	e := &JoinExecutor{}
	nctx := newJoinCtx()

	// First pair completes the join.
	if _, _, err := e.Execute(t.Context(), nctx, "wf", joinNode(), joinMessage(t, "o-1", "a")); err != nil {
		t.Fatalf("first: %v", err)
	}
	out, status, err := e.Execute(t.Context(), nctx, "wf", joinNode(), joinMessage(t, "o-1", "b"))
	if err != nil {
		t.Fatalf("second: %v", err)
	}
	if status != "success" || len(out) != 1 {
		t.Fatalf("the join did not complete: status=%q out=%d", status, len(out))
	}

	// The same key again. This is where it panicked.
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("the join panicked on a repeated key (%v); one repeated value in the "+
				"data takes down the workflow", r)
		}
	}()
	if _, _, err := e.Execute(t.Context(), nctx, "wf", joinNode(), joinMessage(t, "o-1", "c")); err != nil {
		t.Fatalf("third: %v", err)
	}
}

// TestAnUnexpectedStateShapeDoesNotPanic. Node state is a shared map keyed by
// strings; the join must not assume it is the only writer of its key.
func TestAnUnexpectedStateShapeDoesNotPanic(t *testing.T) {
	e := &JoinExecutor{}
	nctx := newJoinCtx()
	nctx.SetNodeState("join_j1_o-1", "not a message slice")

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("the join panicked on unexpected state (%v)", r)
		}
	}()
	if _, _, err := e.Execute(t.Context(), nctx, "wf", joinNode(), joinMessage(t, "o-1", "a")); err != nil {
		t.Fatalf("execute: %v", err)
	}
}

// TestAWaitingJoinHoldsExactlyOneReference. The join retains what it holds. If
// it retained without a matching release the pool would leak; if it released
// early the message would be recycled while still in the join.
func TestAWaitingJoinHoldsExactlyOneReference(t *testing.T) {
	e := &JoinExecutor{}
	nctx := newJoinCtx()

	out, status, err := e.Execute(t.Context(), nctx, "wf", joinNode(), joinMessage(t, "o-2", "a"))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if status != "waiting" {
		t.Errorf("status = %q, want waiting", status)
	}
	if len(out) != 0 {
		t.Errorf("a waiting join emitted %d message(s)", len(out))
	}

	held, ok := nctx.GetNodeState("join_j1_o-2")
	if !ok {
		t.Fatal("nothing was held for the waiting join")
	}
	if msgs, ok := held.([]hermod.Message); !ok || len(msgs) != 1 {
		t.Errorf("held %v, want one message", held)
	}
}

// TestJoinRequiresAKeyPath: without it every message shares one slot and
// unrelated records merge into each other.
func TestJoinRequiresAKeyPath(t *testing.T) {
	e := &JoinExecutor{}
	node := &storage.WorkflowNode{ID: "j1", Type: "join", Config: map[string]any{}}

	_, status, err := e.Execute(t.Context(), newJoinCtx(), "wf", node, joinMessage(t, "o-1", "a"))
	if err == nil {
		t.Error("a join with no key_path was accepted; every message would merge into one slot")
	}
	if status != "error" {
		t.Errorf("status = %q, want error", status)
	}
}
