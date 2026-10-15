// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Granted to no one is any right to use, copy, modify or distribute this
// Except by written permission of the copyright holder named above.
// May not be sold, resold, sublicensed or offered as a paid or hosted
// Business service to any third party. Proprietary and confidential;
// Its full terms are in LICENSE at the repository root. THE SOFTWARE
// IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR IMPLIED.

package factory

import (
	"strings"
	"testing"

	"github.com/gsoultan/hermod/pkg/ml/worker"
)

// A Collect Dataset sink built outside the registry (the sink health check)
// uses the ML worker in force, which may be one saved from the console after
// Hermod started, not only one HERMOD_ML_WORKER_URL named at start.
func TestMLDatasetSinkUsesTheWorkerSavedWhileRunning(t *testing.T) {
	if worker.Default.Env() != nil {
		t.Skip("HERMOD_ML_WORKER_URL is set in this environment")
	}
	cfg := SinkConfig{ID: "s1", Type: "ml_dataset", Config: map[string]string{"dataset": "orders"}}

	worker.Default.SetSaved(nil)
	t.Cleanup(func() { worker.Default.SetSaved(nil) })
	if _, err := CreateSink(cfg); err == nil || !strings.Contains(err.Error(), "no ML worker") {
		t.Fatalf("without a worker: %v, want it refused", err)
	}

	worker.Default.SetSaved(worker.New("http://hermod-ml:8090", "tok", nil))
	snk, err := CreateSink(cfg)
	if err != nil {
		t.Fatalf("with a saved worker: %v", err)
	}
	_ = snk.Close()
}
