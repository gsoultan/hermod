package autoscaler

import (
	"context"
	"fmt"
	"os/exec"

	"github.com/user/hermod/internal/storage"
)

// KubernetesWorkerManager implements scaling via kubectl.
type KubernetesWorkerManager struct {
	Namespace  string
	Deployment string
	Storage    storage.Storage
}

// ListWorkers retrieves worker information from the storage.
func (k *KubernetesWorkerManager) ListWorkers(ctx context.Context, filter storage.CommonFilter) ([]storage.Worker, int, error) {
	return k.Storage.ListWorkers(ctx, filter)
}

// Scale updates the worker deployment replicas using kubectl.
func (k *KubernetesWorkerManager) Scale(ctx context.Context, replicas int) error {
	cmd := exec.CommandContext(ctx, "kubectl", "scale", "deployment", k.Deployment,
		fmt.Sprintf("--replicas=%d", replicas), "-n", k.Namespace)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("kubectl scale failed: %v, output: %s", err, string(output))
	}
	return nil
}
