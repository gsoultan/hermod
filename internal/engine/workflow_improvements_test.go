package engine

import (
	"github.com/user/hermod/internal/storage"
	"github.com/user/hermod/pkg/message"
	"testing"
)

func TestWorkflowImprovements(t *testing.T) {
	registry := NewRegistry(nil)

	t.Run("Masking Transformation", func(t *testing.T) {
		msg := message.AcquireMessage()
		msg.SetData("email", "john.doe@example.com")
		msg.SetData("phone", "1234567890")

		// Test email masking
		nodeEmail := &storage.WorkflowNode{
			Type: "transformation",
			Config: map[string]any{
				"transType": "mask",
				"field":     "email",
				"maskType":  "email",
			},
		}
		res, _, err := registry.runWorkflowNode("test", nodeEmail, msg)
		if err != nil {
			t.Fatalf("Failed to run email mask: %v", err)
		}
		if res.Data()["email"] != "j****@example.com" {
			t.Errorf("Expected j****@example.com, got %v", res.Data()["email"])
		}

		// Test partial masking
		nodePhone := &storage.WorkflowNode{
			Type: "transformation",
			Config: map[string]any{
				"transType": "mask",
				"field":     "phone",
				"maskType":  "partial",
			},
		}
		res, _, err = registry.runWorkflowNode("test", nodePhone, msg)
		if err != nil {
			t.Fatalf("Failed to run partial mask: %v", err)
		}
		if res.Data()["phone"] != "12****90" {
			t.Errorf("Expected 12****90, got %v", res.Data()["phone"])
		}
	})

	t.Run("Stateful Aggregation", func(t *testing.T) {
		msg1 := message.AcquireMessage()
		msg1.SetData("amount", 100)

		msg2 := message.AcquireMessage()
		msg2.SetData("amount", 200)

		nodeCount := &storage.WorkflowNode{
			ID:   "count1",
			Type: "stateful",
			Config: map[string]any{
				"operation":   "count",
				"field":       "amount",
				"outputField": "total_count",
			},
		}

		nodeSum := &storage.WorkflowNode{
			ID:   "sum1",
			Type: "stateful",
			Config: map[string]any{
				"operation":   "sum",
				"field":       "amount",
				"outputField": "total_amount",
			},
		}

		// First message
		res1, _, _ := registry.runWorkflowNode("wf1", nodeCount, msg1)
		res1, _, _ = registry.runWorkflowNode("wf1", nodeSum, res1)

		if res1.Data()["total_count"].(float64) != 1 {
			t.Errorf("Expected count 1, got %v", res1.Data()["total_count"])
		}
		if res1.Data()["total_amount"].(float64) != 100 {
			t.Errorf("Expected sum 100, got %v", res1.Data()["total_amount"])
		}

		// Second message
		res2, _, _ := registry.runWorkflowNode("wf1", nodeCount, msg2)
		res2, _, _ = registry.runWorkflowNode("wf1", nodeSum, res2)

		if res2.Data()["total_count"].(float64) != 2 {
			t.Errorf("Expected count 2, got %v", res2.Data()["total_count"])
		}
		if res2.Data()["total_amount"].(float64) != 300 {
			t.Errorf("Expected sum 300, got %v", res2.Data()["total_amount"])
		}
	})
}
