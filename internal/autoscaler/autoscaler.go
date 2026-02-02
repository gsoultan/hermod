package autoscaler

import (
	"context"
	"fmt"
	"log"
	"os/exec"
	"sync"
	"time"

	"github.com/user/hermod/internal/storage"
)

type WorkerManager interface {
	ListWorkers(ctx context.Context, filter storage.CommonFilter) ([]storage.Worker, int, error)
	Scale(ctx context.Context, replicas int) error
}

type Autoscaler struct {
	manager WorkerManager
	storage storage.Storage

	interval time.Duration
	stop     chan struct{}
	wg       sync.WaitGroup
}

func NewAutoscaler(s storage.Storage, m WorkerManager) *Autoscaler {
	return &Autoscaler{
		manager:  m,
		storage:  s,
		interval: 30 * time.Second,
		stop:     make(chan struct{}),
	}
}

func (a *Autoscaler) Start() {
	a.wg.Add(1)
	go a.run()
}

func (a *Autoscaler) Stop() {
	close(a.stop)
	a.wg.Wait()
}

func (a *Autoscaler) run() {
	defer a.wg.Done()
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
		log.Printf("Autoscaler: failed to list workflows: %v", err)
		return
	}

	var totalCPU, totalMem float64
	var totalThroughput int
	activeWf := 0
	for _, wf := range workflows {
		if wf.Active {
			activeWf++
			totalCPU += wf.CPURequest
			totalMem += wf.MemoryRequest
			totalThroughput += wf.ThroughputRequest
		}
	}

	// 2. Get current worker count
	workers, totalWorkers, err := a.manager.ListWorkers(ctx, storage.CommonFilter{})
	if err != nil {
		log.Printf("Autoscaler: failed to list workers: %v", err)
		return
	}

	// Calculate current capacity
	// Assuming each worker has a baseline capacity if not specified
	const workerCPUCapacity = 2.0    // 2 Cores
	const workerMemCapacity = 4096.0 // 4GB

	requiredByCPU := int(totalCPU/workerCPUCapacity) + 1
	requiredByMem := int(totalMem/workerMemCapacity) + 1
	requiredByThroughput := (totalThroughput / 5000) + 1 // 5k msg/s per worker baseline

	targetReplicas := requiredByCPU
	if requiredByMem > targetReplicas {
		targetReplicas = requiredByMem
	}
	if requiredByThroughput > targetReplicas {
		targetReplicas = requiredByThroughput
	}

	// Min/Max bounds
	if targetReplicas < 1 {
		targetReplicas = 1
	}
	if targetReplicas > 20 {
		targetReplicas = 20
	}

	if targetReplicas != totalWorkers {
		log.Printf("Autoscaler: Scaling from %d to %d replicas (CPU: %.2f, Mem: %.2f, Msg/s: %d)",
			totalWorkers, targetReplicas, totalCPU, totalMem, totalThroughput)
		if err := a.manager.Scale(ctx, targetReplicas); err != nil {
			log.Printf("Autoscaler: Scale failed: %v", err)
		}
	} else {
		// Log health of existing workers
		var avgCPU, avgMem float64
		if totalWorkers > 0 {
			for _, w := range workers {
				avgCPU += w.CPUUsage
				avgMem += w.MemoryUsage
			}
			avgCPU /= float64(totalWorkers)
			avgMem /= float64(totalWorkers)
		}
		log.Printf("Autoscaler: Status OK. Replicas: %d, Avg Load: CPU %.2f, Mem %.2f", totalWorkers, avgCPU, avgMem)
	}
}

// KubernetesWorkerManager implements scaling via kubectl or K8s API
type KubernetesWorkerManager struct {
	Namespace  string
	Deployment string
	Storage    storage.Storage
}

func (k *KubernetesWorkerManager) ListWorkers(ctx context.Context, filter storage.CommonFilter) ([]storage.Worker, int, error) {
	return k.Storage.ListWorkers(ctx, filter)
}

func (k *KubernetesWorkerManager) Scale(ctx context.Context, replicas int) error {
	// In production, use the K8s client-go library.
	// As a robust alternative for this environment, we call kubectl.
	cmd := exec.CommandContext(ctx, "kubectl", "scale", "deployment", k.Deployment,
		"--replicas="+fmt.Sprintf("%d", replicas), "-n", k.Namespace)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("kubectl scale failed: %v, output: %s", err, string(output))
	}
	return nil
}
