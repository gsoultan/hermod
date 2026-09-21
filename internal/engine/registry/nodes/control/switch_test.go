package control

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/internal/storage"
	msgpkg "github.com/gsoultan/hermod/pkg/comm/message"
	"github.com/gsoultan/hermod/pkg/infra/evaluator"
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

// TestSwitch_Execute_CasesSavedAsArray pins the shape the editor actually
// saves. SwitchConfig.tsx writes `cases` straight into node.data as a JSON
// array -- it never stringifies the way RouterEditor.tsx does -- so every
// workflow built in the UI reaches the engine with []any under "cases".
// Reading only the string form made the type assertion fail, leaving an empty
// case list, and a switch with perfectly good numeric cases fell through to
// "default" on every message.
func TestSwitch_Execute_CasesSavedAsArray(t *testing.T) {
	for _, tc := range []struct {
		name   string
		amount any
		want   string
	}{
		{"greater-than wins", 150, "high"},
		{"equals wins", 100, "exact"},
		{"no case matches", 10, "default"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			n := &SwitchNode{}
			node := &storage.WorkflowNode{
				Config: map[string]any{
					"field": "amount",
					// Exactly what a UI-built workflow round trips through
					// JSONB: a slice of maps, not a string.
					"cases": []any{
						map[string]any{"label": "high", "operator": ">", "value": "100"},
						map[string]any{"label": "exact", "operator": "=", "value": "100"},
					},
				},
			}

			m := msgpkg.AcquireMessage()
			defer msgpkg.ReleaseMessage(m)
			m.SetData("amount", tc.amount)

			_, branch, err := n.Execute(context.Background(), &switchStubCtx{}, "wf1", node, m)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if branch != tc.want {
				t.Errorf("amount %v: expected branch %q, got %q", tc.amount, tc.want, branch)
			}
		})
	}
}
