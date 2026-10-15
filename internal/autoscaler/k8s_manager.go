// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Granted to no one is any right to use, copy, modify or distribute this
// Except by written permission of the copyright holder named above.
// May not be sold, resold, sublicensed or offered as a paid or hosted
// Business service to any third party. Proprietary and confidential;
// Its full terms are in LICENSE at the repository root. THE SOFTWARE
// IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR IMPLIED.

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
