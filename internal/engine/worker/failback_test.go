package worker

import (
	"testing"
	"time"

	"github.com/user/hermod"
	"github.com/user/hermod/internal/engine/registry"
	"github.com/user/hermod/internal/factory"
	"github.com/user/hermod/internal/storage"
)

// setWorkerLoad mirrors a heartbeat: it updates both the worker's locally known
// resource usage (used when it scores itself) and the value persisted in
// storage (used when peers score it), keeping the cluster's view consistent.
func setWorkerLoad(store *failoverStorage, w *Worker, cpu, mem float64) {
	w.currentCPU = cpu
	w.currentMem = mem
	store.mu.Lock()
	if entry, ok := store.workers[w.workerGUID]; ok {
		entry.CPUUsage = cpu
		entry.MemoryUsage = mem
		now := time.Now()
		entry.LastSeen = &now
		store.workers[w.workerGUID] = entry
	}
	store.mu.Unlock()
}

func newFailbackStore() *failoverStorage {
	store := &failoverStorage{
		workers:   make(map[string]storage.Worker),
		workflows: make(map[string]storage.Workflow),
		leases:    make(map[string]string),
	}
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
	return store
}

func newFailbackWorker(store *failoverStorage, id string) (*Worker, *registry.Registry) {
	reg := registry.NewRegistry(store)
	reg.SetFactories(func(cfg factory.SourceConfig) (hermod.Source, error) {
		return &mockSource{}, nil
	}, func(cfg factory.SinkConfig) (hermod.Sink, error) {
		return &mockSink{}, nil
	})
	w := NewWorker(store, reg)
	w.SetWorkerConfig(0, 1, id, "token")
	w.SetWorkerCacheTTL(5 * time.Millisecond)
	return w, reg
}

func runningCount(reg *registry.Registry) int {
	count := 0
	for i := range 10 {
		id := "wf-" + string(rune('a'+i))
		if reg.IsEngineRunning(id) {
			count++
		}
	}
	return count
}

// TestWorkerFailback verifies the complete failover -> failback cycle:
//  1. Two workers share the workload.
//  2. worker-1 dies and worker-2 takes over everything (failover).
//  3. worker-1 recovers reporting low load while worker-2 is saturated; the
//     overloaded worker must release its surplus so the recovered worker
//     reclaims a meaningful share (failback / rebalancing).
func TestWorkerFailback(t *testing.T) {
	store := newFailbackStore()
	ctx := t.Context()

	w1, reg1 := newFailbackWorker(store, "worker-1")
	w2, reg2 := newFailbackWorker(store, "worker-2")

	_ = w1.SelfRegister(ctx)
	_ = w2.SelfRegister(ctx)

	w1.sync(ctx, true)
	w2.sync(ctx, true)

	if total := runningCount(reg1) + runningCount(reg2); total != 10 {
		t.Fatalf("initial: expected 10 running workflows, got %d", total)
	}

	// --- Failover: worker-1 dies, worker-2 takes everything ---
	w1.cleanup(ctx)
	time.Sleep(15 * time.Millisecond)
	w2.sync(ctx, false)
	if c := runningCount(reg2); c != 10 {
		t.Fatalf("failover: expected worker-2 to run all 10, got %d", c)
	}

	// --- Failback: worker-1 recovers lightly loaded; worker-2 is saturated ---
	w1b, reg1b := newFailbackWorker(store, "worker-1")
	_ = w1b.SelfRegister(ctx)
	setWorkerLoad(store, w1b, 0.05, 0.05)
	setWorkerLoad(store, w2, 0.95, 0.95)

	// Several reconciliation rounds: the saturated owner releases surplus
	// (async), then the recovered worker acquires the freed leases.
	for range 8 {
		time.Sleep(10 * time.Millisecond)
		w2.sync(ctx, false)
		time.Sleep(10 * time.Millisecond)
		w1b.sync(ctx, false)
	}

	w1Count := runningCount(reg1b)
	w2Count := runningCount(reg2)
	t.Logf("after failback: worker-1=%d, worker-2=%d", w1Count, w2Count)

	if w1Count+w2Count != 10 {
		t.Fatalf("failback: workflows lost/duplicated, total running=%d", w1Count+w2Count)
	}
	if w1Count == 0 {
		t.Fatalf("failback did not occur: recovered worker reclaimed no workflows")
	}
	if w2Count == 0 {
		t.Fatalf("over-rebalanced: saturated worker dropped all workflows")
	}

	// No lease should be held by more than one worker simultaneously.
	assertNoDoubleRun(t, reg1b, reg2)
}

func assertNoDoubleRun(t *testing.T, a, b *registry.Registry) {
	t.Helper()
	for i := range 10 {
		id := "wf-" + string(rune('a'+i))
		if a.IsEngineRunning(id) && b.IsEngineRunning(id) {
			t.Errorf("workflow %s is running on both workers simultaneously", id)
		}
	}
}
