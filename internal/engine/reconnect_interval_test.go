package engine

import (
	"context"
	"testing"

	"github.com/user/hermod/internal/storage"
)

type mockStorageForInterval struct {
	storage.Storage
	src storage.Source
	wf  storage.Workflow
}

func (m *mockStorageForInterval) GetSource(ctx context.Context, id string) (storage.Source, error) {
	return m.src, nil
}

func (m *mockStorageForInterval) GetWorkflow(ctx context.Context, id string) (storage.Workflow, error) {
	return m.wf, nil
}

func (m *mockStorageForInterval) UpdateWorkflow(ctx context.Context, wf storage.Workflow) error {
	m.wf = wf
	return nil
}

func (m *mockStorageForInterval) GetSink(ctx context.Context, id string) (storage.Sink, error) {
	return storage.Sink{ID: id, Type: "stdout"}, nil
}

func (m *mockStorageForInterval) ListWorkflows(ctx context.Context, filter storage.CommonFilter) ([]storage.Workflow, int, error) {
	return []storage.Workflow{m.wf}, 1, nil
}

func (m *mockStorageForInterval) ListSources(ctx context.Context, filter storage.CommonFilter) ([]storage.Source, int, error) {
	return []storage.Source{m.src}, 1, nil
}

func (m *mockStorageForInterval) ListSinks(ctx context.Context, filter storage.CommonFilter) ([]storage.Sink, int, error) {
	return []storage.Sink{{ID: "snk-1", Type: "stdout"}}, 1, nil
}

func (m *mockStorageForInterval) UpdateSource(ctx context.Context, src storage.Source) error {
	m.src = src
	return nil
}

func (m *mockStorageForInterval) UpdateSourceState(ctx context.Context, id string, state map[string]string) error {
	m.src.State = state
	return nil
}

func (m *mockStorageForInterval) UpdateSink(ctx context.Context, snk storage.Sink) error {
	return nil
}

func (m *mockStorageForInterval) ListLogs(ctx context.Context, filter storage.LogFilter) ([]storage.Log, int, error) {
	return nil, 0, nil
}

func (m *mockStorageForInterval) CreateLog(ctx context.Context, log storage.Log) error {
	return nil
}

func (m *mockStorageForInterval) UpdateNodeState(ctx context.Context, workflowID, nodeID string, state interface{}) error {
	return nil
}

func (m *mockStorageForInterval) GetNodeStates(ctx context.Context, workflowID string) (map[string]interface{}, error) {
	return nil, nil
}

func TestSourceReconnectInterval(t *testing.T) {
	store := &mockStorageForInterval{
		src: storage.Source{
			ID:   "src-1",
			Type: "webhook", // easy to use as it doesn't need much config
			Config: map[string]string{
				"reconnect_intervals": "100ms",
			},
		},
		wf: storage.Workflow{
			ID: "wf-1",
			Nodes: []storage.WorkflowNode{
				{ID: "node-1", Type: "source", RefID: "src-1"},
				{ID: "node-2", Type: "sink", RefID: "snk-1"},
			},
			Edges: []storage.WorkflowEdge{
				{ID: "edge-1", SourceID: "node-1", TargetID: "node-2"},
			},
			Active: true,
		},
	}

	r := NewRegistry(store)

	// Start workflow
	err := r.StartWorkflow("wf-1", store.wf)
	if err != nil {
		t.Fatalf("Failed to start workflow: %v", err)
	}
	defer r.StopEngine("wf-1")

	r.mu.Lock()
	ae, ok := r.engines["wf-1"]
	r.mu.Unlock()

	if !ok {
		t.Fatalf("Engine not found")
	}

	// Verify that the engine has the correct reconnect interval
	// eng.sourceConfig is private in pkg/engine.Engine, but we can check ae.srcConfigs
	if len(ae.srcConfigs) == 0 {
		t.Fatalf("srcConfigs should not be empty")
	}

	if ae.srcConfigs[0].Config["reconnect_intervals"] != "100ms" {
		t.Errorf("Expected reconnect_intervals 100ms, got %s", ae.srcConfigs[0].Config["reconnect_intervals"])
	}
}

func TestWorkerRestartOnSourceChange(t *testing.T) {
	store := &mockStorageForInterval{
		src: storage.Source{
			ID:   "src-1",
			Type: "webhook",
			Config: map[string]string{
				"tables": "table1",
			},
		},
		wf: storage.Workflow{
			ID: "wf-1",
			Nodes: []storage.WorkflowNode{
				{ID: "node-1", Type: "source", RefID: "src-1"},
				{ID: "node-2", Type: "sink", RefID: "snk-1"},
			},
			Edges: []storage.WorkflowEdge{
				{ID: "edge-1", SourceID: "node-1", TargetID: "node-2"},
			},
			Active: true,
		},
	}

	r := NewRegistry(store)
	w := NewWorker(store, r)

	// Start workflow
	err := r.StartWorkflow("wf-1", store.wf)
	if err != nil {
		t.Fatalf("Failed to start workflow: %v", err)
	}

	if !r.IsEngineRunning("wf-1") {
		t.Fatalf("Engine should be running")
	}

	// Change source config in DB
	store.src.Config["tables"] = "table1,table2"

	// Sync worker
	sourceMap := map[string]storage.Source{"src-1": store.src}
	sinkMap := map[string]storage.Sink{"snk-1": {ID: "snk-1", Type: "stdout"}}

	w.syncWorkflow(context.Background(), store.wf, "worker-1", sourceMap, sinkMap)

	// After sync, it should have restarted, meaning it was stopped then started.
	// In this test, syncWorkflow will call StopEngineWithoutUpdate then StartWorkflow.
	// Since everything is synchronous in this test (except the eng.Start goroutine),
	// we can check if it's still running (it should be, with new config).

	if !r.IsEngineRunning("wf-1") {
		t.Errorf("Engine should be running after restart")
	}

	configs, ok := r.GetSourceConfigs("wf-1")
	if !ok {
		t.Fatalf("Could not get source configs")
	}

	if configs[0].Config["tables"] != "table1,table2" {
		t.Errorf("Expected new tables config, got %s", configs[0].Config["tables"])
	}
}
