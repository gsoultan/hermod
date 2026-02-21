package engine

import (
	"context"
	"sync"
	"testing"

	"github.com/user/hermod/internal/storage"
)

type mockWorkerStorage struct {
	storage.Storage
	workflows []storage.Workflow
	mu        sync.Mutex
	released  []string
}

func (m *mockWorkerStorage) ListWorkflows(ctx context.Context, filter storage.CommonFilter) ([]storage.Workflow, int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.workflows, len(m.workflows), nil
}

func (m *mockWorkerStorage) ReleaseWorkflowLease(ctx context.Context, workflowID, ownerID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.released = append(m.released, workflowID)
	// Update the workflow in the mock list
	for i, wf := range m.workflows {
		if wf.ID == workflowID && wf.OwnerID == ownerID {
			m.workflows[i].OwnerID = ""
		}
	}
	return nil
}

func TestWorker_ReleaseAllLeases(t *testing.T) {
	workerID := "worker-1"
	store := &mockWorkerStorage{
		workflows: []storage.Workflow{
			{ID: "wf1", OwnerID: workerID},
			{ID: "wf2", OwnerID: "other-worker"},
			{ID: "wf3", OwnerID: workerID},
		},
	}

	w := NewWorker(store, nil)
	w.SetWorkerConfig(0, 1, workerID, "")

	ctx := t.Context()
	w.ReleaseAllLeases(ctx)

	store.mu.Lock()
	defer store.mu.Unlock()

	if len(store.released) != 2 {
		t.Errorf("expected 2 leases to be released, got %d", len(store.released))
	}

	expectedReleased := map[string]bool{"wf1": true, "wf3": true}
	for _, id := range store.released {
		if !expectedReleased[id] {
			t.Errorf("unexpected workflow released: %s", id)
		}
	}

	for _, wf := range store.workflows {
		if wf.ID == "wf1" || wf.ID == "wf3" {
			if wf.OwnerID != "" {
				t.Errorf("workflow %s should have no owner, but has %s", wf.ID, wf.OwnerID)
			}
		}
		if wf.ID == "wf2" && wf.OwnerID != "other-worker" {
			t.Errorf("workflow wf2 should still be owned by other-worker, but has %s", wf.OwnerID)
		}
	}
}
