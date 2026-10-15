// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Granted to no one is any right to use, copy, modify or distribute this
// Except by written permission of the copyright holder named above.
// May not be sold, resold, sublicensed or offered as a paid or hosted
// Business service to any third party. Proprietary and confidential;
// Its full terms are in LICENSE at the repository root. THE SOFTWARE
// IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR IMPLIED.

package worker

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/gsoultan/hermod/internal/storage"
	"github.com/gsoultan/hermod/internal/testutil"
)

// panicStorage panics on GetWorker, which is invoked unguarded from the worker
// loop via pollShutdownRequest. It is used to verify that Worker.Start never
// lets a panic escape and crash the host process.
type panicStorage struct {
	testutil.BaseMockStorage
}

func (s *panicStorage) GetWorker(ctx context.Context, id string) (storage.Worker, error) {
	panic("boom: simulated storage failure")
}

func (s *panicStorage) ListWorkflows(ctx context.Context, filter storage.CommonFilter) ([]storage.Workflow, int, error) {
	return nil, 0, nil
}

// TestWorkerStartRecoversFromPanic ensures that an unexpected panic raised deep
// inside a worker step is recovered by Start and surfaced as an error instead
// of propagating and crashing the process.
func TestWorkerStartRecoversFromPanic(t *testing.T) {
	w := NewWorker(&panicStorage{}, nil)
	w.SetWorkerConfig(0, 1, "worker-guid", "")
	w.SetSyncInterval(200 * time.Millisecond)

	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()

	err := w.Start(ctx)
	if err == nil {
		t.Fatal("expected Start to return an error after recovering from panic")
	}
	if !strings.Contains(err.Error(), "panicked") {
		t.Fatalf("expected panic-recovery error, got: %v", err)
	}
}
