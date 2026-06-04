package control

import (
	"context"
	"testing"

	"github.com/user/hermod"
	"github.com/user/hermod/internal/engine/registry"
	"github.com/user/hermod/internal/storage"
	msgpkg "github.com/user/hermod/pkg/comm/message"
)

// stubCtx is a minimal NodeContext for testing purposes.
type stubCtx struct{ state map[string]any }

func (s *stubCtx) BroadcastLiveMessage(workflowID, nodeID string, msg hermod.Message, isError bool, errMsg string) {
}
func (s *stubCtx) BroadcastLog(workflowID, level, msg, msgID string) {}
func (s *stubCtx) ApplyTransformation(ctx context.Context, msg hermod.Message, transType string, config map[string]any) (hermod.Message, error) {
	return msg, nil
}
func (s *stubCtx) EvaluateConditions(msg hermod.Message, conditions []map[string]any) bool {
	return true
}
func (s *stubCtx) Storage() registry.RegistryStorage { return nil }
func (s *stubCtx) StateStore() hermod.StateStore     { return nil }
func (s *stubCtx) GetNodeState(key string) (any, bool) {
	if s.state == nil {
		return nil, false
	}
	v, ok := s.state[key]
	return v, ok
}
func (s *stubCtx) SetNodeState(key string, val any) {
	if s.state == nil {
		s.state = make(map[string]any)
	}
	s.state[key] = val
}

func TestForeach_Execute_Success(t *testing.T) {
	n := &ForeachNode{}
	node := &storage.WorkflowNode{ID: "n1", Type: "foreach", Config: map[string]any{"arrayPath": "items"}}

	m := msgpkg.AcquireMessage()
	defer msgpkg.ReleaseMessage(m)
	m.SetID("m1")
	m.SetData("items", []any{"a", "b", "c"})

	msgs, branch, err := n.Execute(context.Background(), &stubCtx{}, "wf1", node, m)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if branch != "" {
		t.Fatalf("expected empty branch, got %q", branch)
	}
	if len(msgs) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(msgs))
	}

	for i, cm := range msgs {
		if got := cm.Data()["_item"]; got == nil {
			t.Fatalf("msg %d missing _item", i)
		}
		if got := cm.Data()["_index"]; got != i {
			t.Fatalf("msg %d expected _index=%d, got %v", i, i, got)
		}
		if cm.Metadata()["_fanout_group"] != "m1" {
			t.Fatalf("missing _fanout_group metadata")
		}
		if cm.Metadata()["_fanout_index"] == "" {
			t.Fatalf("missing _fanout_index metadata")
		}
		if cm.Metadata()["_fanout_total"] != "3" {
			t.Fatalf("expected _fanout_total=3, got %q", cm.Metadata()["_fanout_total"])
		}
	}
}

func TestForeach_Execute_MissingArrayPath(t *testing.T) {
	n := &ForeachNode{}
	node := &storage.WorkflowNode{ID: "n1", Type: "foreach", Config: map[string]any{}}
	m := msgpkg.AcquireMessage()
	defer msgpkg.ReleaseMessage(m)

	if _, _, err := n.Execute(context.Background(), &stubCtx{}, "wf1", node, m); err == nil {
		t.Fatalf("expected error for missing arrayPath")
	}
}

func TestForeach_Execute_NonArray(t *testing.T) {
	n := &ForeachNode{}
	node := &storage.WorkflowNode{ID: "n1", Type: "foreach", Config: map[string]any{"arrayPath": "items"}}

	m := msgpkg.AcquireMessage()
	defer msgpkg.ReleaseMessage(m)
	m.SetData("items", "not-an-array")

	if _, _, err := n.Execute(context.Background(), &stubCtx{}, "wf1", node, m); err == nil {
		t.Fatalf("expected error when value at arrayPath is not an array")
	}
}
