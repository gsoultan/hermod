// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Hermod: real-time Postgres CDC and data pipelines in one Go binary.
// Each file is proprietary and confidential; see LICENSE for the terms.
// Resale, sublicensing or selling it to any third party is prohibited.
// Making copies, modifying or distributing it needs written permission
// Of the copyright holder, Gembit Soultan Shirazi.
// Distributed "AS IS", WITHOUT WARRANTY OF ANY KIND, express or implied.

package autoscaler

import (
	"context"
	"fmt"
	"os/exec"

	"github.com/gsoultan/hermod/internal/storage"
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
		return fmt.Errorf("kubectl scale failed: %w, output: %s", err, string(output))
	}
	return nil
}
