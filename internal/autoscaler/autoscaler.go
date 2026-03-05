package autoscaler

import (
	"context"
	"fmt"
	"os/exec"
	"sync"
	"time"

	"github.com/user/hermod"
	"github.com/user/hermod/internal/storage"
	pkgengine "github.com/user/hermod/pkg/engine"
)

type WorkerManager interface {
	ListWorkers(ctx context.Context, filter storage.CommonFilter) ([]storage.Worker, int, error)
	Scale(ctx context.Context, replicas int) error
}

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
		logger:   pkgengine.NewDefaultLogger(),
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
		a.logger.Error("Autoscaler: failed to list workers", "error", err)
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
		a.logger.Info("Autoscaler: scaling", "from", totalWorkers, "to", targetReplicas, "cpu", totalCPU, "mem", totalMem, "throughput", totalThroughput)
		if err := a.manager.Scale(ctx, targetReplicas); err != nil {
			a.logger.Error("Autoscaler: scale failed", "error", err)
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
		a.logger.Debug("Autoscaler: status OK", "replicas", totalWorkers, "avg_cpu", avgCPU, "avg_mem", avgMem)
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
