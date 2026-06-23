package control

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/user/hermod"
	"github.com/user/hermod/internal/storage"
	msgpkg "github.com/user/hermod/pkg/comm/message"
	"github.com/user/hermod/pkg/infra/evaluator"
)

type switchStubCtx struct {
	stubCtx
}

func (s *switchStubCtx) EvaluateConditions(msg hermod.Message, conditions []map[string]any) bool {
	return evaluator.EvaluateConditions(msg, conditions)
}

func TestSwitch_Execute_Regex(t *testing.T) {
	n := &SwitchNode{}
	cases := []map[string]any{
		{"label": "match", "operator": "regex", "value": "^active.*"},
		{"label": "other", "operator": "=", "value": "something"},
	}
	casesJSON, _ := json.Marshal(cases)
	node := &storage.WorkflowNode{
		Config: map[string]any{
			"field": "status",
			"cases": string(casesJSON),
		},
	}

	m := msgpkg.AcquireMessage()
	defer msgpkg.ReleaseMessage(m)
	m.SetData("status", "active_session")

	msgs, branch, err := n.Execute(context.Background(), &switchStubCtx{}, "wf1", node, m)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if branch != "match" {
		t.Errorf("expected branch match, got %q", branch)
	}
	if len(msgs) != 1 {
		t.Errorf("expected 1 message, got %d", len(msgs))
	}
}

func TestSwitch_Execute_Function(t *testing.T) {
	n := &SwitchNode{}
	cases := []map[string]any{
		{"label": "match", "operator": "=", "value": "active"},
	}
	casesJSON, _ := json.Marshal(cases)
	node := &storage.WorkflowNode{
		Config: map[string]any{
			"field": "lower(source.status)",
			"cases": string(casesJSON),
		},
	}

	m := msgpkg.AcquireMessage()
	defer msgpkg.ReleaseMessage(m)
	m.SetData("status", "ACTIVE")

	_, branch, err := n.Execute(context.Background(), &switchStubCtx{}, "wf1", node, m)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if branch != "match" {
		t.Errorf("expected branch match, got %q", branch)
	}
}

func TestSwitch_Execute_Default(t *testing.T) {
	n := &SwitchNode{}
	cases := []map[string]any{
		{"label": "match", "operator": "=", "value": "active"},
	}
	casesJSON, _ := json.Marshal(cases)
	node := &storage.WorkflowNode{
		Config: map[string]any{
			"field": "status",
			"cases": string(casesJSON),
		},
	}

	m := msgpkg.AcquireMessage()
	defer msgpkg.ReleaseMessage(m)
	m.SetData("status", "inactive")

	_, branch, err := n.Execute(context.Background(), &switchStubCtx{}, "wf1", node, m)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if branch != "default" {
		t.Errorf("expected branch default, got %q", branch)
	}
}
