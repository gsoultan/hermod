package worker

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/user/hermod"
	"github.com/user/hermod/internal/engine/registry"
	"github.com/user/hermod/internal/factory"
	"github.com/user/hermod/internal/storage"
	"github.com/user/hermod/internal/testutil"
)

type failoverStorage struct {
	testutil.BaseMockStorage
	mu        sync.Mutex
	workers   map[string]storage.Worker
	workflows map[string]storage.Workflow
	leases    map[string]string // workflowID -> workerID
}

func (s *failoverStorage) ListWorkers(ctx context.Context, filter storage.CommonFilter) ([]storage.Worker, int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var res []storage.Worker
	for _, w := range s.workers {
		res = append(res, w)
	}
	return res, len(res), nil
}

func (s *failoverStorage) CreateWorker(ctx context.Context, w storage.Worker) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.workers[w.ID] = w
	return nil
}

func (s *failoverStorage) DeleteWorker(ctx context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.workers, id)
	return nil
}

func (s *failoverStorage) ListWorkflows(ctx context.Context, filter storage.CommonFilter) ([]storage.Workflow, int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var res []storage.Workflow
	for _, wf := range s.workflows {
		wf.OwnerID = s.leases[wf.ID]
		if wf.OwnerID != "" {
			now := time.Now().Add(1 * time.Minute)
			wf.LeaseUntil = &now
		}

		if filter.Active != nil && wf.Active != *filter.Active {
			continue
		}
		if filter.WorkerID != "" && wf.WorkerID != filter.WorkerID {
			continue
		}
		if filter.OwnerID != "" && wf.OwnerID != filter.OwnerID {
			continue
		}
		res = append(res, wf)
	}
	return res, len(res), nil
}

func (s *failoverStorage) AcquireWorkflowLease(ctx context.Context, workflowID, ownerID string, ttlSeconds int) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if current, ok := s.leases[workflowID]; ok && current != "" && current != ownerID {
		return false, nil
	}
	s.leases[workflowID] = ownerID
	return true, nil
}

func (s *failoverStorage) ReleaseWorkflowLease(ctx context.Context, workflowID, ownerID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.leases[workflowID] == ownerID {
		delete(s.leases, workflowID)
	}
	return nil
}

func (s *failoverStorage) GetSource(ctx context.Context, id string) (storage.Source, error) {
	return storage.Source{ID: id, Type: "test-source"}, nil
}

func (s *failoverStorage) GetSink(ctx context.Context, id string) (storage.Sink, error) {
	return storage.Sink{ID: id, Type: "stdout"}, nil
}

func (s *failoverStorage) ListSources(ctx context.Context, filter storage.CommonFilter) ([]storage.Source, int, error) {
	return nil, 0, nil
}

func (s *failoverStorage) ListSinks(ctx context.Context, filter storage.CommonFilter) ([]storage.Sink, int, error) {
	return nil, 0, nil
}

func (s *failoverStorage) UpdateWorkflowStatus(ctx context.Context, id string, status string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if wf, ok := s.workflows[id]; ok {
		wf.Status = status
		s.workflows[id] = wf
	}
	return nil
}

func TestWorkerFailover(t *testing.T) {
	store := &failoverStorage{
		workers:   make(map[string]storage.Worker),
		workflows: make(map[string]storage.Workflow),
		leases:    make(map[string]string),
	}

	// Add workflows
	for i := range 10 {
		id := "wf-" + string(rune('a'+i))
		store.workflows[id] = storage.Workflow{
			ID:     id,
			Active: true,
			Nodes: []storage.WorkflowNode{
				{ID: "n1", Type: "source", RefID: "s1"},
				{ID: "n2", Type: "sink", RefID: "snk1"},
			},
			Edges: []storage.WorkflowEdge{
				{ID: "e1", SourceID: "n1", TargetID: "n2"},
			},
		}
	}

	createWorker := func(id string) (*Worker, *registry.Registry) {
		reg := registry.NewRegistry(store)
		reg.SetFactories(func(cfg factory.SourceConfig) (hermod.Source, error) {
			return &mockSource{}, nil
		}, func(cfg factory.SinkConfig) (hermod.Sink, error) {
			return &mockSink{}, nil
		})
		w := NewWorker(store, reg)
		w.SetWorkerConfig(0, 1, id, "token")
		w.SetWorkerCacheTTL(10 * time.Millisecond)
		return w, reg
	}

	w1, reg1 := createWorker("worker-1")
	w2, reg2 := createWorker("worker-2")

	ctx := t.Context()

	// Register workers
	_ = w1.SelfRegister(ctx)
	_ = w2.SelfRegister(ctx)

	// Trigger sync on both
	w1.sync(ctx, true)
	w2.sync(ctx, true)

	// Check distribution
	checkRunningCount := func(reg *registry.Registry) int {
		count := 0
		for i := range 10 {
			id := "wf-" + string(rune('a'+i))
			if reg.IsEngineRunning(id) {
				count++
			}
		}
		return count
	}

	w1Count := checkRunningCount(reg1)
	w2Count := checkRunningCount(reg2)
	totalCount := w1Count + w2Count

	t.Logf("Initial distribution: worker-1=%d, worker-2=%d", w1Count, w2Count)
	if totalCount != 10 {
		t.Errorf("Expected 10 running workflows, got %d", totalCount)
	}
	if w1Count == 0 || w2Count == 0 {
		t.Errorf("Expected both workers to have workflows, got w1=%d, w2=%d", w1Count, w2Count)
	}

	// Shutdown worker-1
	t.Log("Shutting down worker-1")
	w1.cleanup(ctx)

	// Wait for cache TTL to expire
	time.Sleep(50 * time.Millisecond)

	// Trigger sync on worker-2
	t.Log("Triggering sync on worker-2 after worker-1 shutdown")
	w2.sync(ctx, false)

	// Check that worker-2 took over all
	finalCount := checkRunningCount(reg2)
	t.Logf("Final count on worker-2: %d", finalCount)
	if finalCount != 10 {
		t.Errorf("Expected worker-2 to take over all 10 workflows, got %d", finalCount)
	}
}
