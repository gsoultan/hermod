// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package scale

import (
	"context"
	"fmt"
	"runtime"
	"testing"
	"time"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/internal/engine/registry"
	"github.com/gsoultan/hermod/internal/factory"
	"github.com/gsoultan/hermod/internal/storage"
)

// idleSource never produces a message: it is a running workflow whose source
// has nothing new, which is what almost every workflow is almost all the time
// once there are a great many of them.
type idleSource struct{}

func (idleSource) Read(ctx context.Context) (hermod.Message, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}
func (idleSource) Ack(context.Context, hermod.Message) error { return nil }
func (idleSource) Close() error                              { return nil }
func (idleSource) Ping(context.Context) error                { return nil }

type nopSink struct{}

func (nopSink) Write(context.Context, hermod.Message) error { return nil }
func (nopSink) Close() error                                { return nil }
func (nopSink) Ping(context.Context) error                  { return nil }

// idleFootprint is what one step of TestScaleIdleFootprint measured.
type idleFootprint struct {
	Workflows             int     `json:"workflows"`
	StartSeconds          float64 `json:"start_seconds"`
	GoroutinesPerWorkflow float64 `json:"goroutines_per_workflow"`
	HeapBytesPerWorkflow  float64 `json:"heap_bytes_per_workflow"`
	HeapBytes             int64   `json:"heap_bytes"`
	Goroutines            int     `json:"goroutines"`
}

// idleBudget is what one idle workflow may cost before this test fails.
//
// Measured on 10,000 idle workflows: 11 goroutines and about 49 KiB of heap
// each. It was 139 goroutines and 172 KiB until the message workers started on
// demand (pkg/engine/message_workers.go); 128 of those goroutines were workers
// no message had ever needed. What is left is per-engine machinery that runs on
// timers -- status, checkpoints, two stall watchers, the source loop, the sink
// writer -- which the next step of the plan moves onto one scheduler per
// worker. The budget is the measurement with headroom.
const (
	idleGoroutineBudget = 16
	idleHeapBudget      = 64 << 10 // bytes
)

// TestScaleIdleFootprint starts N workflows whose sources have nothing to read
// and measures what each one costs while it waits: goroutines and live heap.
// It needs no database. Steps default to 1,000 and 10,000; set
// HERMOD_SCALE_IDLE_STEPS (for example "1000,10000,100000") for more.
func TestScaleIdleFootprint(t *testing.T) {
	if testing.Short() {
		t.Skip("load test; runs without -short")
	}
	steps := stepsFromEnv(t, "HERMOD_SCALE_IDLE_STEPS", []int{1000, 10000})

	var rows []idleFootprint
	for _, n := range steps {
		row := measureIdle(t, n)
		rows = append(rows, row)
		t.Logf("idle %7d workflows: %5.1f goroutines and %s heap each, %s in total, started in %.1fs",
			row.Workflows, row.GoroutinesPerWorkflow, mib(int64(row.HeapBytesPerWorkflow)), mib(row.HeapBytes), row.StartSeconds)
		if row.GoroutinesPerWorkflow > idleGoroutineBudget {
			t.Errorf("%d idle workflows: %.1f goroutines each, budget %d", n, row.GoroutinesPerWorkflow, idleGoroutineBudget)
		}
		if row.HeapBytesPerWorkflow > idleHeapBudget {
			t.Errorf("%d idle workflows: %.0f heap bytes each, budget %d", n, row.HeapBytesPerWorkflow, idleHeapBudget)
		}
	}
	writeReport(t, "idle_footprint", rows)
}

func measureIdle(t *testing.T, n int) idleFootprint {
	t.Helper()
	store := newScaleStorage()
	store.putSource(storage.Source{ID: "src", Name: "idle", Type: "idle", Active: true})
	store.putSink(storage.Sink{ID: "snk", Name: "nop", Type: "nop", Active: true})

	reg := registry.NewRegistry(store)
	reg.SetSourceFactory(func(factory.SourceConfig) (hermod.Source, error) { return idleSource{}, nil })
	reg.SetSinkFactory(func(factory.SinkConfig) (hermod.Sink, error) { return nopSink{}, nil })

	g0, h0 := settledFootprint()
	start := time.Now()
	ids := make([]string, n)
	for i := range n {
		wf := idleWorkflow(fmt.Sprintf("idle-%d", i))
		store.putWorkflow(wf)
		if err := reg.StartWorkflow(wf.ID, wf); err != nil {
			t.Fatalf("starting workflow %d of %d: %v", i+1, n, err)
		}
		ids[i] = wf.ID
	}
	started := time.Since(start)
	waitForGoroutinesToSettle()
	g1, h1 := settledFootprint()

	stopAll(t, reg, ids)
	reg.Close()

	return idleFootprint{
		Workflows:             n,
		StartSeconds:          started.Seconds(),
		GoroutinesPerWorkflow: float64(g1-g0) / float64(n),
		HeapBytesPerWorkflow:  float64(h1-h0) / float64(n),
		HeapBytes:             h1 - h0,
		Goroutines:            g1 - g0,
	}
}

func idleWorkflow(id string) storage.Workflow {
	return storage.Workflow{
		ID:     id,
		Name:   id,
		Active: true,
		Nodes: []storage.WorkflowNode{
			{ID: "src", Type: "source", RefID: "src"},
			{ID: "snk", Type: "sink", RefID: "snk"},
		},
		Edges: []storage.WorkflowEdge{{ID: "e", SourceID: "src", TargetID: "snk"}},
	}
}

// settledFootprint returns the goroutine count and live heap after two
// collections, so garbage from starting the workflows is not counted as the
// cost of keeping them.
func settledFootprint() (goroutines int, heap int64) {
	runtime.GC()
	runtime.GC()
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	return runtime.NumGoroutine(), int64(ms.HeapAlloc)
}

// waitForGoroutinesToSettle waits until the goroutine count stops moving:
// StartWorkflow returns before every engine goroutine has been scheduled.
func waitForGoroutinesToSettle() {
	last := -1
	for range 100 {
		now := runtime.NumGoroutine()
		if now == last {
			return
		}
		last = now
		time.Sleep(200 * time.Millisecond)
	}
}

func stopAll(t *testing.T, reg *registry.Registry, ids []string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	for _, id := range ids {
		if err := reg.StopEngineWithoutUpdate(ctx, id); err != nil {
			t.Errorf("stopping %s: %v", id, err)
		}
	}
}
