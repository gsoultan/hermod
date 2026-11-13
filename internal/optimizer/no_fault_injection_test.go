// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package optimizer

import (
	"strings"
	"sync"
	"testing"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/pkg/engine"
)

type warnRecorder struct {
	mu    sync.Mutex
	warns []string
}

func (*warnRecorder) Debug(string, ...any) {}
func (*warnRecorder) Info(string, ...any)  {}
func (*warnRecorder) Error(string, ...any) {}
func (w *warnRecorder) Warn(msg string, _ ...any) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.warns = append(w.warns, msg)
}

// The optimizer used to run a "chaos monkey": with probability 0.001 per
// 30-second cycle it picked a registered workflow and made every sink write
// fail for 10 seconds. That is one unexplained outage roughly every eight
// hours, in production, with retries, dead letters and alerts to match.
// Fault injection belongs in tests, never in the running optimizer.
func TestOptimizerNeverInjectsFailures(t *testing.T) {
	rec := &warnRecorder{}
	opt := NewOptimizer(rec, nil, nil)

	eng := engine.NewEngine(nil, []hermod.Sink{}, nil)
	eng.SetLogger(rec)
	eng.SetIDs("wf-1", "src-1", nil)
	opt.Register("wf-1", eng)

	// 20,000 cycles: the old 0.001 chance fails to fire in all of them with
	// probability 0.999^20000, about 2e-9.
	for range 20_000 {
		opt.optimizeAll()
	}

	rec.mu.Lock()
	defer rec.mu.Unlock()
	for _, w := range rec.warns {
		lw := strings.ToLower(w)
		if strings.Contains(lw, "simulat") || strings.Contains(lw, "chaos") {
			t.Fatalf("optimizer injected a failure: %q", w)
		}
	}
}
