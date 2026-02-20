package engine

import (
	"encoding/json"
	"testing"

	"github.com/user/hermod/internal/storage"
	"github.com/user/hermod/pkg/message"
)

func TestRouterNode(t *testing.T) {
	r := NewRegistry(nil)

	msg := message.AcquireMessage()
	msg.SetData("status", "error_404")
	msg.SetData("payload", "critical failure")

	rules := []map[string]any{
		{
			"label": "critical",
			"conditions": []map[string]any{
				{"field": "payload", "operator": "contains", "value": "critical"},
			},
		},
		{
			"label": "not_found",
			"conditions": []map[string]any{
				{"field": "status", "operator": "regex", "value": "error_.*"},
			},
		},
	}
	rulesJSON, _ := json.Marshal(rules)

	node := &storage.WorkflowNode{
		ID:   "router-1",
		Type: "router",
		Config: map[string]any{
			"rules": string(rulesJSON),
		},
	}

	// Test matching first rule
	_, branch, err := r.runWorkflowNode("wf-1", node, msg)
	if err != nil {
		t.Fatalf("runWorkflowNode failed: %v", err)
	}
	if branch != "critical" {
		t.Errorf("expected branch critical, got %s", branch)
	}

	// Test matching second rule (regex)
	msg2 := message.AcquireMessage()
	msg2.SetData("status", "error_500")
	msg2.SetData("payload", "something else")

	_, branch2, _ := r.runWorkflowNode("wf-1", node, msg2)
	if branch2 != "not_found" {
		t.Errorf("expected branch not_found, got %s", branch2)
	}

	// Test default branch
	msg3 := message.AcquireMessage()
	msg3.SetData("status", "ok")

	_, branch3, _ := r.runWorkflowNode("wf-1", node, msg3)
	if branch3 != "default" {
		t.Errorf("expected branch default, got %s", branch3)
	}
}
