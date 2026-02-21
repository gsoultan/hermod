package engine

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/user/hermod"
	"github.com/user/hermod/internal/storage"
)

type mockReconStorage struct {
	BaseMockStorage
	workflows map[string]storage.Workflow
	mu        sync.Mutex
}

func (m *mockReconStorage) GetWorkflow(ctx context.Context, id string) (storage.Workflow, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	wf, ok := m.workflows[id]
	if !ok {
		return storage.Workflow{}, errors.New("not found")
	}
	return wf, nil
}

func (m *mockReconStorage) ListWorkflows(ctx context.Context, filter storage.CommonFilter) ([]storage.Workflow, int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var res []storage.Workflow
	for _, wf := range m.workflows {
		res = append(res, wf)
	}
	return res, len(res), nil
}

func (m *mockReconStorage) UpdateWorkflow(ctx context.Context, wf storage.Workflow) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.workflows[wf.ID] = wf
	return nil
}

func (m *mockReconStorage) GetSource(ctx context.Context, id string) (storage.Source, error) {
	return storage.Source{ID: id, Type: "test-source"}, nil
}

func (m *mockReconStorage) GetSink(ctx context.Context, id string) (storage.Sink, error) {
	return storage.Sink{ID: id, Type: "stdout"}, nil
}

func (m *mockReconStorage) ListSources(ctx context.Context, filter storage.CommonFilter) ([]storage.Source, int, error) {
	return nil, 0, nil
}

func (m *mockReconStorage) ListSinks(ctx context.Context, filter storage.CommonFilter) ([]storage.Sink, int, error) {
	return nil, 0, nil
}

func (m *mockReconStorage) CreateLog(ctx context.Context, l storage.Log) error {
	return nil
}

func (m *mockReconStorage) UpdateSource(ctx context.Context, src storage.Source) error {
	return nil
}

func (m *mockReconStorage) UpdateSink(ctx context.Context, snk storage.Sink) error {
	return nil
}

func (m *mockReconStorage) UpdateNodeState(ctx context.Context, workflowID, nodeID string, state any) error {
	return nil
}

func (m *mockReconStorage) GetNodeStates(ctx context.Context, workflowID string) (map[string]any, error) {
	return nil, nil
}

type failingSource struct {
	hermod.Source
}

func (f *failingSource) Read(ctx context.Context) (hermod.Message, error) {
	return nil, errors.New("fatal source error")
}
func (f *failingSource) Ack(ctx context.Context, msg hermod.Message) error { return nil }
func (f *failingSource) Ping(ctx context.Context) error                    { return nil }
func (f *failingSource) Close() error                                      { return nil }

func TestWorkflowAutoDeactivation(t *testing.T) {
	store := &mockReconStorage{
		workflows: make(map[string]storage.Workflow),
	}
	wfID := "test-wf"
	store.workflows[wfID] = storage.Workflow{
		ID:     wfID,
		Active: true,
		Nodes: []storage.WorkflowNode{
			{ID: "n1", Type: "source", RefID: "s1"},
			{ID: "n2", Type: "sink", RefID: "k1"},
		},
		Edges: []storage.WorkflowEdge{
			{ID: "e1", SourceID: "n1", TargetID: "n2"},
		},
	}

	r := NewRegistry(store)
	r.SetFactories(func(cfg SourceConfig) (hermod.Source, error) {
		return &failingSource{}, nil
	}, nil)

	// Start workflow directly
	wf, _ := store.GetWorkflow(t.Context(), wfID)
	err := r.StartWorkflow(wfID, wf)
	if err != nil {
		t.Fatalf("Failed to start workflow: %v", err)
	}

	// Wait for it to fail
	time.Sleep(200 * time.Millisecond)

	// Check storage
	updatedWf, _ := store.GetWorkflow(t.Context(), wfID)
	if !updatedWf.Active {
		t.Errorf("Workflow should still be active after error, but it was deactivated")
	}
	if updatedWf.Status == "" {
		t.Errorf("Workflow status should contain error message, but it's empty")
	}
}
