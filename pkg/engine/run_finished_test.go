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
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/pkg/comm/buffer"
	"github.com/gsoultan/hermod/pkg/comm/message"
)

// runReportingSource hands over one message and then nothing, and records what the
// engine told it about the run.
type runReportingSource struct {
	once     sync.Once
	mu       sync.Mutex
	finished map[string]bool
	done     chan struct{}
}

func (s *runReportingSource) Read(ctx context.Context) (hermod.Message, error) {
	var m hermod.Message
	s.once.Do(func() {
		mm := message.AcquireMessage()
		mm.SetID("run-1")
		mm.SetData("x", 1)
		m = mm
	})
	if m != nil {
		return m, nil
	}
	<-ctx.Done()
	return nil, ctx.Err()
}
func (s *runReportingSource) Ack(context.Context, hermod.Message) error { return nil }
func (s *runReportingSource) Ping(context.Context) error                { return nil }
func (s *runReportingSource) Close() error                              { return nil }

func (s *runReportingSource) RunFinished(m hermod.Message, ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.finished[m.ID()] = ok
	close(s.done)
}

type rowRefusingSink struct{}

func (rowRefusingSink) Write(context.Context, hermod.Message) error {
	return fmt.Errorf("bad row: %w", hermod.ErrPermanent)
}
func (rowRefusingSink) Ping(context.Context) error { return nil }
func (rowRefusingSink) Close() error               { return nil }

// A scheduled source runs one run at a time and keeps a "last successful run"
// point, so it must hear when the engine is done with each message and whether
// it succeeded. Ack is not that signal: it is skipped for a failure that was
// not parked, and sent for one that was.
func TestTheSourceHearsHowEachRunEnded(t *testing.T) {
	cases := []struct {
		name string
		sink hermod.Sink
		want bool
	}{
		{"delivered", &mockSink{received: make(chan hermod.Message, 1)}, true},
		{"failed", rowRefusingSink{}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			src := &runReportingSource{finished: map[string]bool{}, done: make(chan struct{})}
			eng := NewEngine(src, []hermod.Sink{tc.sink}, buffer.NewRingBuffer(16))
			cfg := stallTestConfig(time.Minute)
			cfg.RetryInterval = time.Millisecond
			eng.SetConfig(cfg)
			ctx, cancel := context.WithCancel(t.Context())
			stopped := make(chan struct{})
			go func() {
				defer close(stopped)
				_ = eng.Start(ctx)
			}()
			select {
			case <-src.done:
			case <-time.After(10 * time.Second):
				t.Fatal("the source never heard that the run ended")
			}
			cancel()
			<-stopped
			src.mu.Lock()
			defer src.mu.Unlock()
			if got, ok := src.finished["run-1"]; !ok || got != tc.want {
				t.Fatalf("run-1 finished = %v (reported %v), want %v", got, ok, tc.want)
			}
		})
	}
}
