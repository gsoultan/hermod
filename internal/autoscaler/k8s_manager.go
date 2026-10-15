// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

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
