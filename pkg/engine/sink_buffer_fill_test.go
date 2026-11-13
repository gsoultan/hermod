// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package engine

import (
	"context"
	"testing"
	"time"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/pkg/comm/buffer"
	"github.com/gsoultan/hermod/pkg/engine/config"
)

// A sink buffer's fill is what the Tuning tab, the optimizer and the advisor
// read as "this sink cannot keep up", and all three act above 80%.
//
// It was len(channel)/cap(channel). The channel holds 1000, but the engine
// lets at most MaxInflight (128) messages be in flight, and a message keeps
// its slot until its sink write returns. So a sink that never returned at all
// read 12.7% -- 127 of 1000 -- and a batching sink less, because the messages
// it was writing had left the channel. Nothing could ever see a jammed sink.

// stuckSink accepts a write and never returns until released.
type stuckSink struct{ release chan struct{} }

func (s *stuckSink) Write(ctx context.Context, _ hermod.Message) error {
	select {
	case <-s.release:
	case <-ctx.Done():
	}
	return nil
}
func (s *stuckSink) Ping(context.Context) error { return nil }
func (s *stuckSink) Close() error               { return nil }

// peakFill runs an engine over src into snk and returns the fullest reading
// of sink s1 seen within the deadline, stopping early once it reaches want.
func peakFill(t *testing.T, src hermod.Source, snk hermod.Sink, cfg config.SinkConfig, want float64) float64 {
	t.Helper()
	eng := NewEngine(src, []hermod.Sink{snk}, buffer.NewRingBuffer(2048))
	eng.SetIDs("wf", "src", []string{"s1"})
	eng.SetSinkConfigs([]config.SinkConfig{cfg})
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = eng.Start(ctx)
	}()
	var peak float64
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline) && peak < want; {
		peak = max(peak, eng.GetStatus().SinkBufferFill["s1"])
		time.Sleep(10 * time.Millisecond)
	}
	if s, ok := snk.(*stuckSink); ok {
		close(s.release)
	}
	cancel()
	<-done
	return peak
}

func TestSinkBufferFillReadsAStuckSinkAsFull(t *testing.T) {
	if got := peakFill(t, &mockSourceWithLimit{limit: 5000}, &stuckSink{release: make(chan struct{})}, config.SinkConfig{}, 0.9); got < 0.9 {
		t.Fatalf("a sink that never returns read %.1f%% full; want at least 90%%", got*100)
	}
}

func TestSinkBufferFillCountsTheBatchBeingWritten(t *testing.T) {
	if got := peakFill(t, &mockSourceWithLimit{limit: 5000}, &stuckSink{release: make(chan struct{})}, config.SinkConfig{BatchSize: 100}, 0.9); got < 0.9 {
		t.Fatalf("a batching sink that never returns read %.1f%% full; want at least 90%%", got*100)
	}
}

func TestSinkBufferFillOfAKeepingUpSinkStaysLow(t *testing.T) {
	snk := &mockSink{received: make(chan hermod.Message, 10_000)}
	eng := NewEngine(&mockSourceWithLimit{limit: 200}, []hermod.Sink{snk}, buffer.NewRingBuffer(2048))
	eng.SetIDs("wf", "src", []string{"s1"})
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = eng.Start(ctx)
	}()
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline) && eng.GetStatus().ProcessedCount < 200; {
		time.Sleep(10 * time.Millisecond)
	}
	got := eng.GetStatus().SinkBufferFill["s1"]
	cancel()
	<-done
	if got != 0 {
		t.Fatalf("an idle sink that has written everything reads %.1f%% full", got*100)
	}
}
