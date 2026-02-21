package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/user/hermod"
	"github.com/user/hermod/internal/storage"
	"github.com/user/hermod/pkg/message"
)

type mockStorage struct {
	BaseMockStorage
}

func (m *mockStorage) GetTransformation(ctx context.Context, id string) (storage.Transformation, error) {
	return storage.Transformation{}, nil
}

func (m *mockStorage) GetSource(ctx context.Context, id string) (storage.Source, error) {
	return storage.Source{ID: id}, nil
}

func (m *mockStorage) GetSink(ctx context.Context, id string) (storage.Sink, error) {
	return storage.Sink{ID: id}, nil
}

func (m *mockStorage) UpdateNodeState(ctx context.Context, workflowID, nodeID string, state any) error {
	return nil
}

func (m *mockStorage) GetNodeStates(ctx context.Context, workflowID string) (map[string]any, error) {
	return nil, nil
}

func (m *mockStorage) GetWorkflow(ctx context.Context, id string) (storage.Workflow, error) {
	return storage.Workflow{}, nil
}

func (m *mockStorage) UpdateWorkflow(ctx context.Context, wf storage.Workflow) error {
	return nil
}

func (m *mockStorage) CreateLog(ctx context.Context, log storage.Log) error {
	return nil
}

func (m *mockStorage) ListWorkflows(ctx context.Context, filter storage.CommonFilter) ([]storage.Workflow, int, error) {
	return nil, 0, nil
}

func TestTransformationPipelineRegistry(t *testing.T) {
	registry := NewRegistry(&mockStorage{})
	ctx := t.Context()

	msg := message.AcquireMessage()
	msg.SetID("1")
	msg.SetTable("users")
	msg.SetOperation(hermod.OpCreate)
	msg.SetAfter([]byte(`{"id": 1, "name": "john", "email": "JOHN@EXAMPLE.COM"}`))

	transformations := []storage.Transformation{
		{
			Type: "advanced",
			Config: map[string]any{
				"column.email": "lower(source.email)",
				"column.name":  "upper(source.name)",
			},
		},
		{
			Type: "filter_data",
			Config: map[string]any{
				"field":    "name",
				"operator": "=",
				"value":    "JOHN",
			},
		},
	}

	results, err := registry.TestTransformationPipeline(ctx, transformations, msg)
	if err != nil {
		t.Fatalf("Failed to test pipeline: %v", err)
	}

	if len(results) != 2 {
		t.Fatalf("Expected 2 results, got %d", len(results))
	}

	// First result: transformed
	var firstAfter map[string]any
	if err := json.Unmarshal(results[0].After(), &firstAfter); err != nil {
		t.Fatalf("Failed to unmarshal first result: %v", err)
	}
	if firstAfter["name"] != "JOHN" {
		t.Errorf("Expected name JOHN, got %v", firstAfter["name"])
	}

	// Second result: passed filter
	if results[1] == nil {
		t.Errorf("Expected second result to pass filter, but it was nil")
	}

	// Test with filtering
	transformations[1].Config["value"] = "DOE"
	results, err = registry.TestTransformationPipeline(ctx, transformations, msg)
	if err != nil {
		t.Fatalf("Failed to test pipeline: %v", err)
	}

	if len(results) != 2 {
		t.Fatalf("Expected 2 results, got %d", len(results))
	}
	if results[1] != nil {
		t.Errorf("Expected second result to be nil (filtered), but it was not")
	}

	// Test with numeric operators
	msg2 := message.AcquireMessage()
	msg2.SetAfter([]byte(`{"amount": 150}`))
	transformations2 := []storage.Transformation{
		{
			Type: "filter_data",
			Config: map[string]any{
				"field":    "amount",
				"operator": ">",
				"value":    "100",
			},
		},
	}
	results, err = registry.TestTransformationPipeline(ctx, transformations2, msg2)
	if err != nil {
		t.Fatalf("Failed to test pipeline with numeric: %v", err)
	}
	if results[0] == nil {
		t.Errorf("Expected result to pass > filter")
	}

	transformations2[0].Config["operator"] = "<"
	results, err = registry.TestTransformationPipeline(ctx, transformations2, msg2)
	if results[0] != nil {
		t.Errorf("Expected result to be filtered by <")
	}
}

func TestTransformationEdgeCases(t *testing.T) {
	registry := NewRegistry(&mockStorage{})
	ctx := t.Context()

	t.Run("Advanced Parsing Spaces", func(t *testing.T) {
		msg := message.AcquireMessage()
		msg.SetAfter([]byte(`{"email": "JOHN@EXAMPLE.COM"}`))

		transformations := []storage.Transformation{
			{
				Type: "advanced",
				Config: map[string]any{
					"column.email": "lower( source.email )",
				},
			},
		}

		results, err := registry.TestTransformationPipeline(ctx, transformations, msg)
		if err != nil {
			t.Fatalf("Failed to test pipeline: %v", err)
		}

		var after map[string]any
		json.Unmarshal(results[0].After(), &after)
		if after["email"] != "john@example.com" {
			t.Errorf("Expected john@example.com, got %v", after["email"])
		}
	})

	t.Run("Replace with Commas", func(t *testing.T) {
		msg := message.AcquireMessage()
		msg.SetAfter([]byte(`{"field": "a,b,c"}`))

		transformations := []storage.Transformation{
			{
				Type: "advanced",
				Config: map[string]any{
					"column.field": `replace(source.field, ",", ";")`,
				},
			},
		}

		results, err := registry.TestTransformationPipeline(ctx, transformations, msg)
		if err != nil {
			t.Fatalf("Failed to test pipeline: %v", err)
		}

		var after map[string]any
		json.Unmarshal(results[0].After(), &after)
		if after["field"] != "a;b;c" {
			t.Errorf("Expected a;b;c, got %v", after["field"])
		}
	})

	t.Run("Mask Invalid Email", func(t *testing.T) {
		msg := message.AcquireMessage()
		msg.SetAfter([]byte(`{"email": "invalid-email"}`))

		transformations := []storage.Transformation{
			{
				Type: "mask",
				Config: map[string]any{
					"field":    "email",
					"maskType": "email",
				},
			},
		}

		results, err := registry.TestTransformationPipeline(ctx, transformations, msg)
		if err != nil {
			t.Fatalf("Failed to test pipeline: %v", err)
		}

		var after map[string]any
		json.Unmarshal(results[0].After(), &after)
		if after["email"] != "****" {
			t.Errorf("Expected **** for invalid email, got %v", after["email"])
		}
	})

	t.Run("Filter Data Missing Field", func(t *testing.T) {
		msg := message.AcquireMessage()
		msg.SetAfter([]byte(`{"other": "value"}`))

		transformations := []storage.Transformation{
			{
				Type: "filter_data",
				Config: map[string]any{
					"field":    "missing",
					"operator": "=",
					"value":    "something",
				},
			},
		}

		results, err := registry.TestTransformationPipeline(ctx, transformations, msg)
		if err != nil {
			t.Fatalf("Failed to test pipeline: %v", err)
		}

		if results[0] != nil {
			t.Errorf("Expected result to be nil (filtered) because field is missing")
		}
	})

	t.Run("Filter Data Multiple Conditions", func(t *testing.T) {
		msg := message.AcquireMessage()
		msg.SetAfter([]byte(`{"product": "iPhone", "status": "active", "price": 1000}`))

		// Both match
		transformations := []storage.Transformation{
			{
				Type: "filter_data",
				Config: map[string]any{
					"conditions": `[{"field": "product", "operator": "=", "value": "iPhone"}, {"field": "status", "operator": "=", "value": "active"}]`,
				},
			},
		}
		results, err := registry.TestTransformationPipeline(ctx, transformations, msg)
		if err != nil || results[0] == nil {
			t.Fatalf("Expected match, got error: %v or nil result", err)
		}

		// One matches, one doesn't
		transformations[0].Config["conditions"] = `[{"field": "product", "operator": "=", "value": "iPhone"}, {"field": "status", "operator": "=", "value": "inactive"}]`
		results, err = registry.TestTransformationPipeline(ctx, transformations, msg)
		if err != nil || results[0] != nil {
			t.Fatalf("Expected filter, got error: %v or non-nil result", err)
		}

		// Numeric condition in multiple
		transformations[0].Config["conditions"] = `[{"field": "price", "operator": ">", "value": "500"}, {"field": "status", "operator": "contains", "value": "act"}]`
		results, err = registry.TestTransformationPipeline(ctx, transformations, msg)
		if err != nil || results[0] == nil {
			t.Fatalf("Expected match for numeric/contains, got error: %v or nil result", err)
		}
	})

	t.Run("Nested Field Masking", func(t *testing.T) {
		msg := message.AcquireMessage()
		msg.SetAfter([]byte(`{"user": {"email": "john@example.com"}}`))

		transformations := []storage.Transformation{
			{
				Type: "mask",
				Config: map[string]any{
					"field":    "user.email",
					"maskType": "email",
				},
			},
		}

		results, err := registry.TestTransformationPipeline(ctx, transformations, msg)
		if err != nil {
			t.Fatalf("Failed to test pipeline: %v", err)
		}

		var after map[string]any
		json.Unmarshal(results[0].After(), &after)
		user := after["user"].(map[string]any)
		if user["email"] != "j****@example.com" {
			t.Errorf("Expected j****@example.com, got %v", user["email"])
		}
	})

	t.Run("Set Field Transformation", func(t *testing.T) {
		msg := message.AcquireMessage()
		msg.SetAfter([]byte(`{"id": 1}`))

		transformations := []storage.Transformation{
			{
				Type: "set",
				Config: map[string]any{
					"column.new_field":    "value",
					"column.nested.field": "nested_value",
				},
			},
		}

		results, err := registry.TestTransformationPipeline(ctx, transformations, msg)
		if err != nil {
			t.Fatalf("Failed to test pipeline: %v", err)
		}

		var after map[string]any
		json.Unmarshal(results[0].After(), &after)
		if after["new_field"] != "value" {
			t.Errorf("Expected value, got %v", after["new_field"])
		}
		nested := after["nested"].(map[string]any)
		if nested["field"] != "nested_value" {
			t.Errorf("Expected nested_value, got %v", nested["field"])
		}
	})

	t.Run("Set Field with Reference", func(t *testing.T) {
		msg := message.AcquireMessage()
		msg.SetAfter([]byte(`{"existing": "old_value"}`))

		transformations := []storage.Transformation{
			{
				Type: "set",
				Config: map[string]any{
					"column.copied": "source.existing",
				},
			},
		}

		results, err := registry.TestTransformationPipeline(ctx, transformations, msg)
		if err != nil {
			t.Fatalf("Failed to test pipeline: %v", err)
		}

		var after map[string]any
		json.Unmarshal(results[0].After(), &after)
		if after["copied"] != "old_value" {
			t.Errorf("Expected old_value, got %v", after["copied"])
		}
	})

	t.Run("Nested Field Mapping", func(t *testing.T) {
		msg := message.AcquireMessage()
		msg.SetAfter([]byte(`{"user": {"status": 1}}`))

		transformations := []storage.Transformation{
			{
				Type: "mapping",
				Config: map[string]any{
					"field":   "user.status",
					"mapping": `{"1": "Active", "0": "Inactive"}`,
				},
			},
		}

		results, err := registry.TestTransformationPipeline(ctx, transformations, msg)
		if err != nil {
			t.Fatalf("Failed to test pipeline: %v", err)
		}

		var after map[string]any
		json.Unmarshal(results[0].After(), &after)
		user := after["user"].(map[string]any)
		if user["status"] != "Active" {
			t.Errorf("Expected Active, got %v", user["status"])
		}
	})

	t.Run("API Lookup Template", func(t *testing.T) {
		// Mock HTTP server
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/users/123" {
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprint(w, `{"name": "John Doe", "role": "admin"}`)
			} else {
				w.WriteHeader(http.StatusNotFound)
			}
		}))
		defer server.Close()

		msg := message.AcquireMessage()
		msg.SetAfter([]byte(`{"user_id": 123}`))

		transformations := []storage.Transformation{
			{
				Type: "api_lookup",
				Config: map[string]any{
					"url":          server.URL + "/users/{{user_id}}",
					"targetField":  "user_profile",
					"responsePath": ".",
				},
			},
		}

		results, err := registry.TestTransformationPipeline(ctx, transformations, msg)
		if err != nil {
			t.Fatalf("Failed to test pipeline: %v", err)
		}

		var after map[string]any
		json.Unmarshal(results[0].After(), &after)
		profile := after["user_profile"].(map[string]any)
		if profile["name"] != "John Doe" {
			t.Errorf("Expected John Doe, got %v", profile["name"])
		}
	})
}

func TestWorkflowNodesEnhanced(t *testing.T) {
	registry := NewRegistry(&mockStorage{})
	ctx := t.Context()

	t.Run("Condition Multi-Condition", func(t *testing.T) {
		msg := message.AcquireMessage()
		msg.SetAfter([]byte(`{"status": "active", "priority": 10}`))

		wf := storage.Workflow{
			Nodes: []storage.WorkflowNode{
				{ID: "src-1", Type: "source", RefID: "src-1"},
				{
					ID:   "cond-1",
					Type: "condition",
					Config: map[string]any{
						"conditions": `[{"field": "status", "operator": "=", "value": "active"}, {"field": "priority", "operator": ">", "value": "5"}]`,
					},
				},
				{ID: "sink-1", Type: "sink", RefID: "sink-1"},
			},
			Edges: []storage.WorkflowEdge{
				{SourceID: "src-1", TargetID: "cond-1"},
				{SourceID: "cond-1", TargetID: "sink-1", Config: map[string]any{"label": "true"}},
			},
		}

		results, err := registry.TestWorkflow(ctx, wf, msg)
		if err != nil {
			t.Fatalf("Workflow test failed: %v", err)
		}

		// Find results for cond-1
		var condRes *WorkflowStepResult
		for _, r := range results {
			if r.NodeID == "cond-1" {
				condRes = &r
				break
			}
		}

		if condRes == nil {
			t.Fatalf("No result for cond-1")
		}

		if condRes.Branch != "true" {
			t.Errorf("Expected branch true, got %v", condRes.Branch)
		}

		// Change priority to fail one condition
		msg2 := message.AcquireMessage()
		msg2.SetAfter([]byte(`{"status": "active", "priority": 3}`))
		results2, _ := registry.TestWorkflow(ctx, wf, msg2)
		var condRes2 *WorkflowStepResult
		for _, r := range results2 {
			if r.NodeID == "cond-1" {
				condRes2 = &r
				break
			}
		}
		if condRes2.Branch != "false" {
			t.Errorf("Expected branch false for priority 3, got %v", condRes2.Branch)
		}
	})

	t.Run("Switch Multi-Condition", func(t *testing.T) {
		msg := message.AcquireMessage()
		msg.SetAfter([]byte(`{"category": "electronics", "price": 1500}`))

		wf := storage.Workflow{
			Nodes: []storage.WorkflowNode{
				{ID: "src-1", Type: "source", RefID: "src-1"},
				{
					ID:   "switch-1",
					Type: "switch",
					Config: map[string]any{
						"cases": `[
							{"label": "premium_electronics", "conditions": [{"field": "category", "operator": "=", "value": "electronics"}, {"field": "price", "operator": ">", "value": "1000"}]},
							{"label": "budget_electronics", "conditions": [{"field": "category", "operator": "=", "value": "electronics"}, {"field": "price", "operator": "<=", "value": "1000"}]},
							{"label": "other", "value": "something"}
						]`,
					},
				},
				{ID: "sink-1", Type: "sink", RefID: "sink-1"},
			},
			Edges: []storage.WorkflowEdge{
				{SourceID: "src-1", TargetID: "switch-1"},
				{SourceID: "switch-1", TargetID: "sink-1", Config: map[string]any{"label": "premium_electronics"}},
			},
		}

		results, err := registry.TestWorkflow(ctx, wf, msg)
		if err != nil {
			t.Fatalf("Workflow test failed: %v", err)
		}

		var switchRes *WorkflowStepResult
		for _, r := range results {
			if r.NodeID == "switch-1" {
				switchRes = &r
				break
			}
		}

		if switchRes == nil {
			t.Fatalf("No result for switch-1")
		}

		if switchRes.Branch != "premium_electronics" {
			t.Errorf("Expected branch premium_electronics, got %v", switchRes.Branch)
		}

		// Test second case
		msg2 := message.AcquireMessage()
		msg2.SetAfter([]byte(`{"category": "electronics", "price": 500}`))
		results2, _ := registry.TestWorkflow(ctx, wf, msg2)
		var switchRes2 *WorkflowStepResult
		for _, r := range results2 {
			if r.NodeID == "switch-1" {
				switchRes2 = &r
				break
			}
		}
		if switchRes2.Branch != "budget_electronics" {
			t.Errorf("Expected branch budget_electronics, got %v", switchRes2.Branch)
		}

		// Test default/no match
		msg3 := message.AcquireMessage()
		msg3.SetAfter([]byte(`{"category": "books", "price": 20}`))
		results3, _ := registry.TestWorkflow(ctx, wf, msg3)
		var switchRes3 *WorkflowStepResult
		for _, r := range results3 {
			if r.NodeID == "switch-1" {
				switchRes3 = &r
				break
			}
		}
		if switchRes3.Branch != "default" {
			t.Errorf("Expected branch default, got %v", switchRes3.Branch)
		}
	})
}
