package engine

import (
	"testing"

	"github.com/user/hermod/internal/storage"
	"github.com/user/hermod/pkg/message"
)

func TestRouterNode_Regex(t *testing.T) {
	reg := NewRegistry(nil)

	node := &storage.WorkflowNode{
		ID:   "router1",
		Type: "router",
		Config: map[string]any{
			"rules": `[
				{"label": "high_priority", "field": "severity", "operator": "regex", "value": "^(high|critical)$"},
				{"label": "low_priority", "field": "severity", "operator": "regex", "value": "^(low|medium)$"}
			]`,
		},
	}

	msg1 := message.AcquireMessage()
	msg1.SetData("severity", "critical")

	_, branch1, err := reg.runWorkflowNode("wf1", node, msg1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if branch1 != "high_priority" {
		t.Errorf("expected high_priority, got %s", branch1)
	}

	msg2 := message.AcquireMessage()
	msg2.SetData("severity", "medium")

	_, branch2, err := reg.runWorkflowNode("wf1", node, msg2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if branch2 != "low_priority" {
		t.Errorf("expected low_priority, got %s", branch2)
	}

	msg3 := message.AcquireMessage()
	msg3.SetData("severity", "unknown")

	_, branch3, err := reg.runWorkflowNode("wf1", node, msg3)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if branch3 != "default" {
		t.Errorf("expected default, got %s", branch3)
	}
}
