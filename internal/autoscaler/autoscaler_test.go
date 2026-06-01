package autoscaler

import (
	"context"
	"testing"
	"time"

	"github.com/user/hermod/internal/storage"
)

type mockWorkerManager struct {
	workers  []storage.Worker
	replicas int
}

func (m *mockWorkerManager) ListWorkers(ctx context.Context, filter storage.CommonFilter) ([]storage.Worker, int, error) {
	return m.workers, len(m.workers), nil
}

func (m *mockWorkerManager) Scale(ctx context.Context, replicas int) error {
	m.replicas = replicas
	return nil
}

type mockStorage struct {
	storage.Storage
	workflows []storage.Workflow
}

func (m *mockStorage) ListWorkflows(ctx context.Context, filter storage.CommonFilter) ([]storage.Workflow, int, error) {
	return m.workflows, len(m.workflows), nil
}

func TestAutoscaler_OnlineWorkersOnly(t *testing.T) {
	now := time.Now()
	recent := now.Add(-10 * time.Second)
	old := now.Add(-2 * time.Minute)

	workers := []storage.Worker{
		{ID: "online", LastSeen: &recent},
		{ID: "offline", LastSeen: &old},
	}

	mgr := &mockWorkerManager{workers: workers}
	store := &mockStorage{workflows: []storage.Workflow{
		{ID: "wf1", Active: true, CPURequest: 0.1},
	}}

	as := NewAutoscaler(store, mgr)
	as.check()

	// 1 online worker. targetReplicas should be 1.
	// totalWorkers should have been detected as 1 (online only).
	// Since 1 == 1, Scale should NOT have been called.
	if mgr.replicas != 0 {
		t.Errorf("Expected Scale NOT to be called, but replicas was set to %d", mgr.replicas)
	}

	// Now make both offline
	mgr.workers[0].LastSeen = &old
	as.check()
	// totalWorkers is now 0. targetReplicas is 1.
	// Scale should be called to 1.
	if mgr.replicas != 1 {
		t.Errorf("Expected Scale to 1, got %d", mgr.replicas)
	}
}
