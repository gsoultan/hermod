package autoscaler

import (
	"context"
	"sync"
	"time"

	"github.com/user/hermod"
	"github.com/user/hermod/internal/storage"
	"github.com/user/hermod/pkg/engine/telemetry"
)

type Autoscaler struct {
	manager WorkerManager
	storage storage.Storage
	logger  hermod.Logger

	interval time.Duration
	stop     chan struct{}
	wg       sync.WaitGroup
}

func NewAutoscaler(s storage.Storage, m WorkerManager) *Autoscaler {
	return &Autoscaler{
		manager:  m,
		storage:  s,
		logger:   telemetry.NewDefaultLogger(),
		interval: 30 * time.Second,
		stop:     make(chan struct{}),
	}
}

func (a *Autoscaler) Start() {
	a.wg.Go(a.run)
}

func (a *Autoscaler) Stop() {
	close(a.stop)
	a.wg.Wait()
}

func (a *Autoscaler) run() {
	ticker := time.NewTicker(a.interval)
	defer ticker.Stop()

	for {
		select {
		case <-a.stop:
			return
		case <-ticker.C:
			a.check()
		}
	}
}

func (a *Autoscaler) check() {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	// 1. Get total requested resources across all ACTIVE workflows
	workflows, _, err := a.storage.ListWorkflows(ctx, storage.CommonFilter{})
	if err != nil {
		a.logger.Error("Autoscaler: failed to list workflows", "error", err)
		return
	}

	targetReplicas := a.calculateTargetReplicas(workflows)

	// 2. Get current worker count. Scaling decisions must be based on workers
	// that are actually alive: stale/offline workers (those whose heartbeat has
	// lapsed) must not be counted, otherwise the autoscaler over-counts capacity
	// and fails to scale up when workers have silently died.
	workers, _, err := a.manager.ListWorkers(ctx, storage.CommonFilter{})
	if err != nil {
		a.logger.Error("Autoscaler: failed to list workers", "error", err)
		return
	}
	onlineWorkers := countOnlineWorkers(workers)

	// Min/Max bounds
	if targetReplicas < 1 {
		targetReplicas = 1
	}
	if targetReplicas > 20 {
		targetReplicas = 20
	}

	if targetReplicas != onlineWorkers {
		a.logger.Info("Autoscaler: proactive scaling", "from", onlineWorkers, "to", targetReplicas)
		if err := a.manager.Scale(ctx, targetReplicas); err != nil {
			a.logger.Error("Autoscaler: scale failed", "error", err)
		}
	}
}

// workerOnlineThreshold is the maximum age of a worker's last heartbeat for it
// to still be considered online for scaling decisions.
const workerOnlineThreshold = time.Minute

// countOnlineWorkers returns the number of workers whose last heartbeat is
// within workerOnlineThreshold of now. Workers without a heartbeat or with a
// stale one are treated as offline.
func countOnlineWorkers(workers []storage.Worker) int {
	cutoff := time.Now().Add(-workerOnlineThreshold)
	count := 0
	for _, w := range workers {
		if w.LastSeen != nil && w.LastSeen.After(cutoff) {
			count++
		}
	}
	return count
}

func (a *Autoscaler) calculateTargetReplicas(workflows []storage.Workflow) int {
	var totalCPU, totalMem float64
	var totalThroughput int
	for _, wf := range workflows {
		if wf.Active {
			// Proactive: Add 20% buffer for growth
			totalCPU += wf.CPURequest * 1.2
			totalMem += wf.MemoryRequest * 1.2
			totalThroughput += int(float64(wf.ThroughputRequest) * 1.2)
		}
	}

	const workerCPUCapacity = 2.0
	const workerMemCapacity = 4096.0

	reqByCPU := int(totalCPU/workerCPUCapacity) + 1
	reqByMem := int(totalMem/workerMemCapacity) + 1
	reqByThroughput := (totalThroughput / 5000) + 1

	return max(reqByCPU, reqByMem, reqByThroughput)
}
