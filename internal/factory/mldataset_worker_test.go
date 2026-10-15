// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

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
