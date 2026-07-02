package worker

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/user/hermod/internal/engine/registry"
	"github.com/user/hermod/internal/factory"
	"github.com/user/hermod/internal/testutil"

	"github.com/user/hermod"
	"github.com/user/hermod/internal/storage"
)

// Reconciliation tests
type mockReconStorage struct {
	testutil.BaseMockStorage
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

func (m *mockReconStorage) UpdateWorkflowStatus(ctx context.Context, id string, status string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if wf, ok := m.workflows[id]; ok {
		wf.Status = status
		m.workflows[id] = wf
	}
	return nil
}

func (m *mockReconStorage) UpdateWorkflowStats(ctx context.Context, id string, processed, errors, lag uint64) error {
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

	r := registry.NewRegistry(store)
	r.SetFactories(func(cfg factory.SourceConfig) (hermod.Source, error) {
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

// Reconnect tests
type mockSource struct{ hermod.Source }

func (m *mockSource) Read(ctx context.Context) (hermod.Message, error)  { return nil, nil }
func (m *mockSource) Ack(ctx context.Context, msg hermod.Message) error { return nil }
func (m *mockSource) Close() error                                      { return nil }
func (m *mockSource) Ping(ctx context.Context) error                    { return nil }

type mockSink struct{ hermod.Sink }

func (m *mockSink) Write(ctx context.Context, msg hermod.Message) error { return nil }
func (m *mockSink) Close() error                                        { return nil }
func (m *mockSink) Ping(ctx context.Context) error                      { return nil }

type mockSimpleStorage struct {
	testutil.BaseMockStorage
}

func (m *mockSimpleStorage) GetSource(ctx context.Context, id string) (storage.Source, error) {
	return storage.Source{ID: id, Type: "test-source"}, nil
}

func (m *mockSimpleStorage) GetSink(ctx context.Context, id string) (storage.Sink, error) {
	return storage.Sink{ID: id, Type: "stdout"}, nil
}

func TestReconnectAfterRegistryRestart(t *testing.T) {
	ms := &mockSimpleStorage{}
	reg := registry.NewRegistry(ms)
	w := NewWorker(ms, reg)
	w.SetWorkerConfig(0, 1, "test-worker", "test-token")

	// Mock factories
	reg.SetFactories(func(cfg factory.SourceConfig) (hermod.Source, error) {
		return &mockSource{}, nil
	}, func(cfg factory.SinkConfig) (hermod.Sink, error) {
		return &mockSink{}, nil
	})

	wfID := "wf1"
	wf := storage.Workflow{
		ID:     wfID,
		Active: true,
		Nodes: []storage.WorkflowNode{
			{ID: "n1", Type: "source", RefID: "s1"},
			{ID: "n2", Type: "sink", RefID: "snk1"},
		},
		Edges: []storage.WorkflowEdge{
			{ID: "e1", SourceID: "n1", TargetID: "n2"},
		},
	}

	// Sync should start it
	w.SyncWorkflow(t.Context(), wf, SyncContext{WorkerID: "test-worker"})
	if !reg.IsEngineRunning(wfID) {
		t.Fatalf("workflow should be running")
	}

	// Simulation: Registry loses its state (internal map cleared)
	reg.StopAll()

	// Another sync should restart it
	w.SyncWorkflow(t.Context(), wf, SyncContext{WorkerID: "test-worker"})
	if !reg.IsEngineRunning(wfID) {
		t.Fatalf("workflow should be running again after reconnect")
	}
}

// Resource sharding tests
type mockShardingStorage struct {
	testutil.BaseMockStorage
	workers []storage.Worker
}

func (m *mockShardingStorage) ListWorkers(ctx context.Context, filter storage.CommonFilter) ([]storage.Worker, int, error) {
	return m.workers, len(m.workers), nil
}

func TestResourceAwareSharding_Distribution(t *testing.T) {
	ms := &mockShardingStorage{
		workers: []storage.Worker{
			{ID: "worker-1", CPUUsage: 0.1, MemoryUsage: 0.1},
			{ID: "worker-2", CPUUsage: 0.1, MemoryUsage: 0.1},
			{ID: "worker-3", CPUUsage: 0.1, MemoryUsage: 0.1},
		},
	}

	w1 := &Worker{workerGUID: "worker-1", storage: ms}
	w2 := &Worker{workerGUID: "worker-2", storage: ms}
	w3 := &Worker{workerGUID: "worker-3", storage: ms}

	now := time.Now()
	w1.workerCache = ms.workers
	w1.workerCacheTime = now
	w2.workerCache = ms.workers
	w2.workerCacheTime = now
	w3.workerCache = ms.workers
	w3.workerCacheTime = now

	workflows := make([]string, 100)
	for i := range 100 {
		workflows[i] = "wf-" + string(rune(i+100))
	}
	counts := make(map[string]int)

	for _, id := range workflows {
		assignedCount := 0
		if w1.isAssigned(id, "") {
			counts["worker-1"]++
			assignedCount++
		}
		if w2.isAssigned(id, "") {
			counts["worker-2"]++
			assignedCount++
		}
		if w3.isAssigned(id, "") {
			counts["worker-3"]++
			assignedCount++
		}

		if assignedCount != 1 {
			t.Errorf("Workflow %s assigned to %d workers, expected 1", id, assignedCount)
		}
	}

	if len(counts) < 2 {
		t.Errorf("Expected at least 2 workers to have assignments, got %d", len(counts))
	}
}

func TestResourceAwareSharding_LoadBalance(t *testing.T) {
	ms := &mockShardingStorage{
		workers: []storage.Worker{
			{ID: "worker-heavy", CPUUsage: 0.9, MemoryUsage: 0.9},
			{ID: "worker-light", CPUUsage: 0.1, MemoryUsage: 0.1},
		},
	}

	wHeavy := &Worker{workerGUID: "worker-heavy", storage: ms, workerCache: ms.workers, workerCacheTime: time.Now()}
	wLight := &Worker{workerGUID: "worker-light", storage: ms, workerCache: ms.workers, workerCacheTime: time.Now()}

	workflows := make([]string, 100)
	for i := range 100 {
		workflows[i] = "wf-" + string(rune(i))
	}

	heavyCount := 0
	lightCount := 0

	for _, id := range workflows {
		if wHeavy.isAssigned(id, "") {
			heavyCount++
		}
		if wLight.isAssigned(id, "") {
			lightCount++
		}
	}

	if lightCount <= heavyCount {
		t.Errorf("Expected light worker to have more assignments, got light=%d, heavy=%d", lightCount, heavyCount)
	}
}

func TestResourceAwareSharding_Hysteresis(t *testing.T) {
	ms := &mockShardingStorage{
		workers: []storage.Worker{
			{ID: "worker-1", CPUUsage: 0.4, MemoryUsage: 0.4},
			{ID: "worker-2", CPUUsage: 0.5, MemoryUsage: 0.5},
		},
	}

	w1 := &Worker{workerGUID: "worker-1", storage: ms, workerCache: ms.workers, workerCacheTime: time.Now()}
	w2 := &Worker{workerGUID: "worker-2", storage: ms, workerCache: ms.workers, workerCacheTime: time.Now()}

	workflowID := "wf-sticky"
	initialOwner := ""
	if w1.isAssigned(workflowID, "") {
		initialOwner = "worker-1"
	} else if w2.isAssigned(workflowID, "") {
		initialOwner = "worker-2"
	}

	if initialOwner == "worker-1" {
		ms.workers[0].CPUUsage = 0.5
		ms.workers[1].CPUUsage = 0.45
	} else {
		ms.workers[1].CPUUsage = 0.5
		ms.workers[0].CPUUsage = 0.45
	}

	w1.workerCache = ms.workers
	w2.workerCache = ms.workers

	newOwner := ""
	if w1.isAssigned(workflowID, initialOwner) {
		newOwner = "worker-1"
	} else if w2.isAssigned(workflowID, initialOwner) {
		newOwner = "worker-2"
	}

	if newOwner != initialOwner {
		t.Errorf("Expected owner to remain %s due to hysteresis, but changed to %s", initialOwner, newOwner)
	}
}

// Registration tests
type mockRegStorage struct {
	testutil.BaseMockStorage
	workers map[string]storage.Worker
}

func (m *mockRegStorage) GetWorker(ctx context.Context, id string) (storage.Worker, error) {
	w, ok := m.workers[id]
	if !ok {
		return storage.Worker{}, storage.ErrNotFound
	}
	return w, nil
}
func (m *mockRegStorage) CreateWorker(ctx context.Context, w storage.Worker) error {
	m.workers[w.ID] = w
	return nil
}
func (m *mockRegStorage) ListWorkers(ctx context.Context, filter storage.CommonFilter) ([]storage.Worker, int, error) {
	var workers []storage.Worker
	for _, w := range m.workers {
		workers = append(workers, w)
	}
	return workers, len(workers), nil
}
func (m *mockRegStorage) DeleteWorker(ctx context.Context, id string) error {
	delete(m.workers, id)
	return nil
}

func TestWorker_SelfRegister(t *testing.T) {
	store := &mockRegStorage{workers: make(map[string]storage.Worker)}
	reg := registry.NewRegistry(store)
	worker := NewWorker(store, reg)

	worker.SetWorkerConfig(0, 1, "test-worker-1", "test-token")
	worker.SetRegistrationInfo("Test Worker", "localhost", 8081, "A test worker")

	ctx := t.Context()
	err := worker.SelfRegister(ctx)
	if err != nil {
		t.Fatalf("SelfRegister failed: %v", err)
	}

	w, err := store.GetWorker(ctx, "test-worker-1")
	if err != nil {
		t.Fatalf("Failed to get worker after registration: %v", err)
	}

	if w.Name != "Test Worker" {
		t.Errorf("Expected name 'Test Worker', got '%s'", w.Name)
	}
}

// Sharding stability tests
func TestWorkerIsAssigned_StabilityAndDistribution(t *testing.T) {
	w := &Worker{}
	w.SetWorkerConfig(0, 3, "", "")

	ids := []string{"a", "b", "c", "d", "e", "f", "g", "h", "i"}
	counts := make([]int, 3)

	for _, id := range ids {
		for shard := 0; shard < 3; shard++ {
			w.workerID = shard
			if w.isAssigned(id, "") {
				counts[shard]++
			}
		}
	}

	total := counts[0] + counts[1] + counts[2]
	if total != len(ids) {
		t.Fatalf("expected total assignments %d, got %d (counts=%v)", len(ids), total, counts)
	}
}

// Shutdown tests
type mockWorkerStorage struct {
	testutil.BaseMockStorage
	leases map[string]string
}

func (m *mockWorkerStorage) ListWorkflows(ctx context.Context, filter storage.CommonFilter) ([]storage.Workflow, int, error) {
	var res []storage.Workflow
	for id, owner := range m.leases {
		res = append(res, storage.Workflow{ID: id, OwnerID: owner})
	}
	return res, len(res), nil
}

func (m *mockWorkerStorage) ReleaseWorkflowLease(ctx context.Context, workflowID, ownerID string) error {
	delete(m.leases, workflowID)
	return nil
}

func TestWorker_ReleaseAllLeases(t *testing.T) {
	ms := &mockWorkerStorage{leases: map[string]string{"wf1": "worker1", "wf2": "worker1"}}
	w := NewWorker(ms, nil)
	w.workerGUID = "worker1"
	w.renewCancel = map[string]context.CancelFunc{
		"wf1": func() {},
		"wf2": func() {},
	}

	w.ReleaseAllLeases(t.Context())

	if len(ms.leases) != 0 {
		t.Errorf("Expected leases to be empty, got %d", len(ms.leases))
	}
	if len(w.renewCancel) != 0 {
		t.Errorf("Expected renewCancel to be empty, got %d", len(w.renewCancel))
	}
}
