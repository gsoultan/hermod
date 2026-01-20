package engine

import (
	"context"
	"github.com/user/hermod/internal/storage"
	"github.com/user/hermod/pkg/message"
	"testing"
)

type mockProdStorage struct {
	storage.Storage
	sources map[string]storage.Source
	sinks   map[string]storage.Sink
}

func (m *mockProdStorage) GetSource(ctx context.Context, id string) (storage.Source, error) {
	s, ok := m.sources[id]
	if !ok {
		return storage.Source{}, storage.ErrNotFound
	}
	return s, nil
}

func (m *mockProdStorage) GetWorkflow(ctx context.Context, id string) (storage.Workflow, error) {
	return storage.Workflow{}, storage.ErrNotFound
}

func (m *mockProdStorage) ListWorkflows(ctx context.Context, filter storage.CommonFilter) ([]storage.Workflow, int, error) {
	return nil, 0, nil
}

func (m *mockProdStorage) GetSink(ctx context.Context, id string) (storage.Sink, error) {
	s, ok := m.sinks[id]
	if !ok {
		return storage.Sink{}, storage.ErrNotFound
	}
	return s, nil
}

func (m *mockProdStorage) CreateLog(ctx context.Context, l storage.Log) error {
	return nil
}

func TestWorkflowOptimizationAndMetrics(t *testing.T) {
	store := &mockProdStorage{
		sources: map[string]storage.Source{
			"src1": {ID: "src1", Type: "cron", Config: map[string]string{"schedule": "@every 1s"}},
		},
		sinks: map[string]storage.Sink{
			"snk1": {ID: "snk1", Type: "stdout", Config: map[string]string{}},
		},
	}

	registry := NewRegistry(store)
	wf := storage.Workflow{
		ID:   "wf1",
		Name: "Production Workflow",
		Nodes: []storage.WorkflowNode{
			{ID: "node1", Type: "source", RefID: "src1"},
			{ID: "node2", Type: "transformation", Config: map[string]interface{}{
				"transType":    "advanced",
				"column.email": "lower(source.email)",
			}},
			{ID: "node3", Type: "sink", RefID: "snk1"},
		},
		Edges: []storage.WorkflowEdge{
			{ID: "e1", SourceID: "node1", TargetID: "node2"},
			{ID: "e2", SourceID: "node2", TargetID: "node3"},
		},
	}

	// Test Validation
	if err := registry.ValidateWorkflow(context.Background(), wf); err != nil {
		t.Fatalf("Validation failed: %v", err)
	}

	// Test Cycle Detection
	wfCycle := wf
	wfCycle.Edges = append(wfCycle.Edges, storage.WorkflowEdge{ID: "e3", SourceID: "node2", TargetID: "node1"})
	if err := registry.ValidateWorkflow(context.Background(), wfCycle); err == nil {
		t.Errorf("Expected cycle detection error, got nil")
	}

	// Test Unconfigured Node
	wfUnconfigured := wf
	wfUnconfigured.Nodes = []storage.WorkflowNode{
		{ID: "node1", Type: "source", RefID: "new"},
		{ID: "node2", Type: "sink", RefID: "snk1"},
	}
	if err := registry.ValidateWorkflow(context.Background(), wfUnconfigured); err == nil {
		t.Errorf("Expected validation error for unconfigured node, got nil")
	}

	// Test Missing Source
	wfMissing := wf
	wfMissing.Nodes = []storage.WorkflowNode{
		{ID: "node1", Type: "source", RefID: "missing_id"},
		{ID: "node2", Type: "sink", RefID: "snk1"},
	}
	if err := registry.ValidateWorkflow(context.Background(), wfMissing); err == nil {
		t.Errorf("Expected validation error for missing source, got nil")
	}

	// Test Edge Integrity
	wfEdgeFail := wf
	wfEdgeFail.Edges = append(wfEdgeFail.Edges, storage.WorkflowEdge{ID: "e_fail", SourceID: "node1", TargetID: "missing_node"})
	if err := registry.ValidateWorkflow(context.Background(), wfEdgeFail); err == nil {
		t.Errorf("Expected validation error for missing target node in edge, got nil")
	}

	// Test runWorkflowNode with new operations
	msg := message.AcquireMessage()
	msg.SetData("email", "USER@EXAMPLE.COM")
	msg.SetData("name", "John Doe")

	// Test replace
	nodeReplace := &storage.WorkflowNode{
		Type: "transformation",
		Config: map[string]interface{}{
			"transType":   "advanced",
			"column.name": "replace(source.name, \"John\", \"Jane\")",
		},
	}
	resMsg, _, err := registry.runWorkflowNode("test", nodeReplace, msg)
	if err != nil {
		t.Fatalf("runWorkflowNode failed: %v", err)
	}
	if resMsg.Data()["name"] != "Jane Doe" {
		t.Errorf("Expected Jane Doe, got %v", resMsg.Data()["name"])
	}

	// Test mapping
	nodeMapping := &storage.WorkflowNode{
		Type: "transformation",
		Config: map[string]interface{}{
			"transType": "mapping",
			"field":     "status",
			"mapping":   "{\"1\": \"active\", \"0\": \"inactive\"}",
		},
	}
	msg.SetData("status", "1")
	resMsg, _, err = registry.runWorkflowNode("test", nodeMapping, msg)
	if err != nil {
		t.Fatalf("runWorkflowNode mapping failed: %v", err)
	}
	if resMsg.Data()["status"] != "active" {
		t.Errorf("Expected active, got %v", resMsg.Data()["status"])
	}
}
