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
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/pkg/comm/buffer"
	"github.com/gsoultan/hermod/pkg/comm/message"
	"github.com/gsoultan/hermod/pkg/engine"
	"github.com/gsoultan/hermod/pkg/engine/config"
)

type quiet struct{}

func (quiet) Debug(string, ...any) {}
func (quiet) Info(string, ...any)  {}
func (quiet) Warn(string, ...any)  {}
func (quiet) Error(string, ...any) {}

// burst hands out n messages and then waits.
type burst struct{ left atomic.Int64 }

func (b *burst) Read(ctx context.Context) (hermod.Message, error) {
	if b.left.Add(-1) < 0 {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	m := message.AcquireMessage()
	m.SetID("m")
	return m, nil
}
func (*burst) Ack(context.Context, hermod.Message) error { return nil }
func (*burst) Ping(context.Context) error                { return nil }
func (*burst) Close() error                              { return nil }

type discard struct{}

func (discard) Write(context.Context, hermod.Message) error { return nil }
func (discard) Ping(context.Context) error                  { return nil }
func (discard) Close() error                                { return nil }

// The optimizer used to rewrite a running engine's sink batch size every 30
// seconds: up by a fifth when the buffer was full, down by a fifth when it
// was idle. Nobody approved it and nothing recorded it, and it changed only
// the engine's copy of the settings: the writer reads its batch size once,
// when it starts. So the one visible effect was a configuration that no
// longer matched what the operator had saved. Sizing a sink is now the
// advisor's recommendation for a person to act on.
func TestOptimizerLeavesSinkSettingsAsTheOperatorSavedThem(t *testing.T) {
	src := &burst{}
	src.left.Store(1500)
	eng := engine.NewEngine(src, []hermod.Sink{discard{}}, buffer.NewRingBuffer(2048))
	eng.SetIDs("wf", "src", []string{"snk"})
	eng.SetSinkConfigs([]config.SinkConfig{{BatchSize: 100, BatchTimeout: 50 * time.Millisecond}})
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = eng.Start(ctx)
	}()
	defer func() {
		cancel()
		<-done
	}()
	for deadline := time.Now().Add(10 * time.Second); eng.GetStatus().ProcessedCount < 1500; {
		if time.Now().After(deadline) {
			t.Fatalf("only %d of 1500 messages processed", eng.GetStatus().ProcessedCount)
		}
		time.Sleep(10 * time.Millisecond)
	}

	o := NewOptimizer(quiet{}, nil, nil)
	o.Register("wf", eng)
	// An idle sink that has processed more than 1000 messages: the reading
	// that used to shrink the batch size.
	for range 3 {
		o.optimizeAll()
	}

	if got := eng.GetSinkConfigs()[0]; got.BatchSize != 100 || got.BatchTimeout != 50*time.Millisecond {
		t.Fatalf("the optimizer changed the sink's settings to batch %d, timeout %s", got.BatchSize, got.BatchTimeout)
	}
}
