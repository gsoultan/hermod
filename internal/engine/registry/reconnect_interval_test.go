package registry_test

import (
	"context"
	"testing"

	"github.com/user/hermod/internal/engine/registry"
	"github.com/user/hermod/internal/engine/worker"
	"github.com/user/hermod/internal/storage"
	"github.com/user/hermod/internal/testutil"
)

type mockStorageForInterval struct {
	testutil.BaseMockStorage
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

func (m *mockStorageForInterval) UpdateSourceStatus(ctx context.Context, id string, status string) error {
	return nil
}

func (m *mockStorageForInterval) UpdateSinkStatus(ctx context.Context, id string, status string) error {
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

func (m *mockStorageForInterval) UpdateNodeState(ctx context.Context, workflowID, nodeID string, state any) error {
	return nil
}

func (m *mockStorageForInterval) GetNodeStates(ctx context.Context, workflowID string) (map[string]any, error) {
	return nil, nil
}

func (m *mockStorageForInterval) UpdateWorkflowStatus(ctx context.Context, id string, status string) error {
	return nil
}

func TestSourceReconnectInterval(t *testing.T) {
	store := &mockStorageForInterval{
		src: storage.Source{
			ID:   "src-1",
			Type: "webhook",
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

	r := registry.NewRegistry(store)

	// Start workflow
	err := r.StartWorkflow("wf-1", store.wf)
	if err != nil {
		t.Fatalf("Failed to start workflow: %v", err)
	}
	defer r.StopEngine("wf-1")

	if !r.IsEngineRunning("wf-1") {
		t.Fatalf("Engine not found")
	}

	configs, ok := r.GetSourceConfigs("wf-1")
	if !ok {
		t.Fatalf("srcConfigs should not be empty")
	}

	if configs[0].Config["reconnect_intervals"] != "100ms" {
		t.Errorf("Expected reconnect_intervals 100ms, got %s", configs[0].Config["reconnect_intervals"])
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

	r := registry.NewRegistry(store)
	w := worker.NewWorker(store, r)

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

	w.SyncWorkflow(t.Context(), store.wf, "worker-1", sourceMap, sinkMap)

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
