package engine

import (
	"testing"

	"github.com/user/hermod/internal/storage"
	"github.com/user/hermod/pkg/message"
)

func TestNestedPathAccess(t *testing.T) {
	registry := NewRegistry(nil)

	msg := message.AcquireMessage()
	defer message.ReleaseMessage(msg)

	msg.SetData("user", map[string]interface{}{
		"profile": map[string]interface{}{
			"name": "John Doe",
			"age":  30,
		},
		"status": "active",
	})

	t.Run("Filter nested path", func(t *testing.T) {
		node := &storage.WorkflowNode{
			Type: "transformation",
			Config: map[string]interface{}{
				"transType": "filter_data",
				"field":     "user.profile.name",
				"operator":  "=",
				"value":     "John Doe",
			},
		}

		res, _, err := registry.runWorkflowNode("test", node, msg)
		if err != nil {
			t.Fatalf("Failed to run node: %v", err)
		}
		if res == nil {
			t.Errorf("Expected message to pass filter, but it was nil")
		}

		node.Config["value"] = "Jane Doe"
		res, _, err = registry.runWorkflowNode("test", node, msg)
		if res != nil {
			t.Errorf("Expected message to be filtered, but it was not nil")
		}
	})

	t.Run("Advanced transformation nested path", func(t *testing.T) {
		node := &storage.WorkflowNode{
			Type: "transformation",
			Config: map[string]interface{}{
				"transType":                "advanced",
				"column.user.profile.name": "lower(source.user.profile.name)",
			},
		}

		res, _, err := registry.runWorkflowNode("test", node, msg)
		if err != nil {
			t.Fatalf("Failed to run node: %v", err)
		}
		if res == nil {
			t.Fatalf("Result is nil")
		}

		data := res.Data()
		user := data["user"].(map[string]interface{})
		profile := user["profile"].(map[string]interface{})
		if profile["name"] != "john doe" {
			t.Errorf("Expected name to be 'john doe', got %v", profile["name"])
		}
	})

	t.Run("Condition nested path", func(t *testing.T) {
		node := &storage.WorkflowNode{
			Type: "condition",
			Config: map[string]interface{}{
				"field":    "user.profile.age",
				"operator": ">",
				"value":    "25",
			},
		}

		res, branch, err := registry.runWorkflowNode("test", node, msg)
		if err != nil {
			t.Fatalf("Failed to run node: %v", err)
		}
		if res == nil {
			t.Fatalf("Result is nil")
		}
		if branch != "true" {
			t.Errorf("Expected branch 'true', got '%s'", branch)
		}

		node.Config["value"] = "35"
		_, branch, _ = registry.runWorkflowNode("test", node, msg)
		if branch != "false" {
			t.Errorf("Expected branch 'false', got '%s'", branch)
		}
	})

	t.Run("Mapping nested path", func(t *testing.T) {
		node := &storage.WorkflowNode{
			Type: "transformation",
			Config: map[string]interface{}{
				"transType": "mapping",
				"field":     "user.status",
				"mapping":   "{\"active\": \"ENABLED\", \"inactive\": \"DISABLED\"}",
			},
		}

		res, _, err := registry.runWorkflowNode("test", node, msg)
		if err != nil {
			t.Fatalf("Failed to run node: %v", err)
		}
		if res == nil {
			t.Fatalf("Result is nil")
		}

		data := res.Data()
		user := data["user"].(map[string]interface{})
		if user["status"] != "ENABLED" {
			t.Errorf("Expected status ENABLED, got %v", user["status"])
		}
	})
}
