package engine

import (
	"context"
	"strings"
	"testing"

	"github.com/user/hermod"
	"github.com/user/hermod/internal/storage"
	"github.com/user/hermod/pkg/message"
)

type mockCheckpointStorage struct {
	storage.Storage
	nodeStates map[string]interface{}
	source     storage.Source
	sink       storage.Sink
	workflow   storage.Workflow
}

func (m *mockCheckpointStorage) GetSource(ctx context.Context, id string) (storage.Source, error) {
	return m.source, nil
}

func (m *mockCheckpointStorage) GetSink(ctx context.Context, id string) (storage.Sink, error) {
	return m.sink, nil
}

func (m *mockCheckpointStorage) GetWorkflow(ctx context.Context, id string) (storage.Workflow, error) {
	return m.workflow, nil
}

func (m *mockCheckpointStorage) UpdateSourceState(ctx context.Context, id string, state map[string]string) error {
	m.source.State = state
	return nil
}

func (m *mockCheckpointStorage) UpdateNodeState(ctx context.Context, workflowID, nodeID string, state interface{}) error {
	if m.nodeStates == nil {
		m.nodeStates = make(map[string]interface{})
	}
	m.nodeStates[workflowID+":"+nodeID] = state
	return nil
}

func (m *mockCheckpointStorage) GetNodeStates(ctx context.Context, workflowID string) (map[string]interface{}, error) {
	res := make(map[string]interface{})
	prefix := workflowID + ":"
	for k, v := range m.nodeStates {
		if strings.HasPrefix(k, prefix) {
			res[strings.TrimPrefix(k, prefix)] = v
		}
	}
	return res, nil
}

func (m *mockCheckpointStorage) UpdateWorkflow(ctx context.Context, wf storage.Workflow) error {
	m.workflow = wf
	return nil
}

func (m *mockCheckpointStorage) ListWorkflows(ctx context.Context, filter storage.CommonFilter) ([]storage.Workflow, int, error) {
	return []storage.Workflow{m.workflow}, 1, nil
}

func (m *mockCheckpointStorage) CreateLog(ctx context.Context, l storage.Log) error {
	return nil
}

func (m *mockCheckpointStorage) UpdateSource(ctx context.Context, src storage.Source) error {
	m.source = src
	return nil
}

func (m *mockCheckpointStorage) UpdateSink(ctx context.Context, snk storage.Sink) error {
	m.sink = snk
	return nil
}

type mockCheckpointSource struct {
	hermod.Source
	state map[string]string
}

func (m *mockCheckpointSource) Read(ctx context.Context) (hermod.Message, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

func (m *mockCheckpointSource) Ack(ctx context.Context, msg hermod.Message) error {
	return nil
}

func (m *mockCheckpointSource) GetState() map[string]string {
	return m.state
}

func (m *mockCheckpointSource) SetState(state map[string]string) {
	m.state = state
}

func (m *mockCheckpointSource) Ping(ctx context.Context) error { return nil }
func (m *mockCheckpointSource) Close() error                   { return nil }

func TestCheckpointAndRecovery(t *testing.T) {
	store := &mockCheckpointStorage{
		nodeStates: make(map[string]interface{}),
		source: storage.Source{
			ID:   "src1",
			Type: "mock",
		},
		sink: storage.Sink{
			ID:   "snk1",
			Type: "stdout",
		},
		workflow: storage.Workflow{
			ID: "wf1",
			Nodes: []storage.WorkflowNode{
				{ID: "n1", Type: "source", RefID: "src1"},
				{ID: "n2", Type: "stateful", Config: map[string]interface{}{
					"operation":   "count",
					"field":       "val",
					"outputField": "cnt",
				}},
				{ID: "n3", Type: "sink", RefID: "snk1"},
			},
			Edges: []storage.WorkflowEdge{
				{ID: "e1", SourceID: "n1", TargetID: "n2"},
				{ID: "e2", SourceID: "n2", TargetID: "n3"},
			},
			Active: true,
		},
	}

	registry := NewRegistry(store)

	registry.SetFactories(func(cfg SourceConfig) (hermod.Source, error) {
		return &mockCheckpointSource{state: cfg.State}, nil
	}, nil)

	// Pre-populate state
	store.nodeStates["wf1:n2"] = float64(10)

	// Start workflow
	err := registry.StartWorkflow("wf1", store.workflow)
	if err != nil {
		t.Fatalf("Failed to start workflow: %v", err)
	}

	// Verify state loaded
	registry.nodeStatesMu.Lock()
	if registry.nodeStates["wf1:n2"] != float64(10) {
		t.Errorf("Expected initial state 10, got %v", registry.nodeStates["wf1:n2"])
	}
	registry.nodeStatesMu.Unlock()

	// Process a message
	msg := message.AcquireMessage()
	msg.SetData("val", 1)

	_, _, err = registry.runWorkflowNode("wf1", &store.workflow.Nodes[1], msg)
	if err != nil {
		t.Fatalf("Failed to run node: %v", err)
	}

	// Verify state updated in memory
	registry.nodeStatesMu.Lock()
	if registry.nodeStates["wf1:n2"] != float64(11) {
		t.Errorf("Expected updated state 11, got %v", registry.nodeStates["wf1:n2"])
	}
	registry.nodeStatesMu.Unlock()

	// Update source state
	active, ok := registry.engines["wf1"]
	if !ok {
		t.Fatalf("Engine not found")
	}
	if stateful, ok := active.engine.GetSource().(hermod.Stateful); ok {
		stateful.SetState(map[string]string{"offset": "100"})
	}

	// Trigger checkpoint manually
	err = active.engine.Checkpoint(context.Background())
	if err != nil {
		t.Fatalf("Checkpoint failed: %v", err)
	}

	// Verify state persisted to storage
	if store.nodeStates["wf1:n2"] != float64(11) {
		t.Errorf("Expected persisted state 11, got %v", store.nodeStates["wf1:n2"])
	}
	if store.source.State["offset"] != "100" {
		t.Errorf("Expected persisted source offset 100, got %v", store.source.State["offset"])
	}

	// Stop and restart
	registry.StopEngine("wf1")

	err = registry.StartWorkflow("wf1", store.workflow)
	if err != nil {
		t.Fatalf("Failed to restart workflow: %v", err)
	}

	// Verify state recovered from storage
	registry.nodeStatesMu.Lock()
	if registry.nodeStates["wf1:n2"] != float64(11) {
		t.Errorf("Expected recovered state 11, got %v", registry.nodeStates["wf1:n2"])
	}
	registry.nodeStatesMu.Unlock()

	// Verify source state recovered
	active2, ok := registry.engines["wf1"]
	if !ok {
		t.Fatalf("Engine not found after restart")
	}
	if stateful, ok := active2.engine.GetSource().(hermod.Stateful); ok {
		if stateful.GetState()["offset"] != "100" {
			t.Errorf("Expected recovered source offset 100, got %v", stateful.GetState()["offset"])
		}
	} else {
		t.Errorf("Source does not implement Stateful after restart")
	}
}
