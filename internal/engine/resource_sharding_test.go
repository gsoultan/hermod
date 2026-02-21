package engine

import (
	"context"
	"testing"
	"time"

	"github.com/user/hermod/internal/storage"
)

type mockShardingStorage struct {
	BaseMockStorage
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

	// Mock cache time to avoid frequent storage calls but ensure it's loaded
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

	t.Logf("Counts with equal load: %v", counts)
	if len(counts) < 2 {
		t.Errorf("Expected at least 2 workers to have assignments with 100 workflows, got %d", len(counts))
	}
}

func TestResourceAwareSharding_LoadBalance(t *testing.T) {
	ms := &mockShardingStorage{
		workers: []storage.Worker{
			{ID: "worker-heavy", CPUUsage: 0.9, MemoryUsage: 0.9}, // Heavy load
			{ID: "worker-light", CPUUsage: 0.1, MemoryUsage: 0.1}, // Light load
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

	t.Logf("Heavy count: %d, Light count: %d", heavyCount, lightCount)
	if lightCount <= heavyCount {
		t.Errorf("Expected light worker to have more assignments than heavy worker, got light=%d, heavy=%d", lightCount, heavyCount)
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

	// Find which worker owns it initially without stickiness
	initialOwner := ""
	if w1.isAssigned(workflowID, "") {
		initialOwner = "worker-1"
	} else if w2.isAssigned(workflowID, "") {
		initialOwner = "worker-2"
	}

	t.Logf("Initial owner: %s", initialOwner)

	// Now increase load on the initial owner slightly, but not enough to overcome 15% hysteresis
	if initialOwner == "worker-1" {
		ms.workers[0].CPUUsage = 0.5  // w1 load 0.4 -> 0.5
		ms.workers[1].CPUUsage = 0.45 // w2 load 0.5 -> 0.45
		// w2 is now slightly better than w1
	} else {
		ms.workers[1].CPUUsage = 0.5  // w2 load 0.4 -> 0.5
		ms.workers[0].CPUUsage = 0.45 // w1 load 0.5 -> 0.45
		// w1 is now slightly better than w2
	}

	// Update caches
	w1.workerCache = ms.workers
	w2.workerCache = ms.workers

	// Check if owner remains the same due to stickiness
	newOwner := ""
	if w1.isAssigned(workflowID, initialOwner) {
		newOwner = "worker-1"
	} else if w2.isAssigned(workflowID, initialOwner) {
		newOwner = "worker-2"
	}

	if newOwner != initialOwner {
		t.Errorf("Expected owner to remain %s due to hysteresis, but changed to %s", initialOwner, newOwner)
	}

	// Now increase load significantly on the initial owner to overcome hysteresis
	if initialOwner == "worker-1" {
		ms.workers[0].CPUUsage = 0.9
		ms.workers[0].MemoryUsage = 0.9
		ms.workers[1].CPUUsage = 0.1
		ms.workers[1].MemoryUsage = 0.1
	} else {
		ms.workers[1].CPUUsage = 0.9
		ms.workers[1].MemoryUsage = 0.9
		ms.workers[0].CPUUsage = 0.1
		ms.workers[0].MemoryUsage = 0.1
	}

	// Update caches
	w1.workerCache = ms.workers
	w2.workerCache = ms.workers

	// Check if owner changed
	finalOwner := ""
	if w1.isAssigned(workflowID, initialOwner) {
		finalOwner = "worker-1"
	} else if w2.isAssigned(workflowID, initialOwner) {
		finalOwner = "worker-2"
	}

	if finalOwner == initialOwner {
		t.Errorf("Expected owner to change from %s due to significant load increase", initialOwner)
	}
}
