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
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/pkg/comm/buffer"
)

// "The workflow is running, but neither the traces nor the logs say it
// succeeded." Reported on a cron workflow that runs a chain of db_lookup and
// execute_sql nodes into a stdout sink once per schedule.
//
// The per-message success line is DEBUG on purpose (see write_logging_test.go):
// at streaming rates it is an unbounded log stream. That left a workflow's log
// with no INFO line at all saying that anything was ever delivered — a healthy
// run and a run that silently did nothing read the same. The engine now writes
// one summary line per message the first time, then at most one per window,
// with the counts of what became of every message in between.

// infoKVLogger keeps every INFO line with its key/value pairs.
type infoKVLogger struct {
	mu    sync.Mutex
	lines []infoKVLine
}

type infoKVLine struct {
	msg string
	kv  map[string]any
}

func (l *infoKVLogger) Debug(string, ...any) {}
func (l *infoKVLogger) Warn(string, ...any)  {}
func (l *infoKVLogger) Error(string, ...any) {}
func (l *infoKVLogger) Info(msg string, kv ...any) {
	m := make(map[string]any, len(kv)/2)
	for i := 0; i+1 < len(kv); i += 2 {
		m[fmt.Sprint(kv[i])] = kv[i+1]
	}
	l.mu.Lock()
	l.lines = append(l.lines, infoKVLine{msg: msg, kv: m})
	l.mu.Unlock()
}

func (l *infoKVLogger) withAction(action string) []infoKVLine {
	l.mu.Lock()
	defer l.mu.Unlock()
	var out []infoKVLine
	for _, ln := range l.lines {
		if ln.kv["action"] == action {
			out = append(out, ln)
		}
	}
	return out
}

func TestADeliveredMessageIsReportedInTheWorkflowLog(t *testing.T) {
	src := &oneShotSource{}
	sink := newTallySink()
	logger := &infoKVLogger{}

	eng := NewEngine(src, []hermod.Sink{sink}, buffer.NewRingBuffer(8))
	eng.SetLogger(logger)
	eng.workflowID = "wf-cron"

	runUntilSinkWrite(t, eng, sink)

	lines := logger.withAction("processed")
	if len(lines) == 0 {
		t.Fatal("a message was delivered and the workflow log has no INFO line saying so; " +
			"a healthy run reads exactly like one that did nothing")
	}
	if got := lines[0].kv["delivered"]; got != 1 {
		t.Errorf("delivered = %v, want 1 (line %q)", got, lines[0].msg)
	}
	if !strings.Contains(lines[0].msg, "1 delivered") {
		t.Errorf("the line reads %q; it should say in words that the message was delivered", lines[0].msg)
	}
}

// The summary must not bring back the per-message stream it replaces.
func TestTheDeliverySummaryIsBoundedAndCountsEveryMessage(t *testing.T) {
	const messages = 50

	src := &countingSource{limit: messages}
	sink := &countingSink{done: make(chan struct{})}
	logger := &infoKVLogger{}

	eng := NewEngine(src, []hermod.Sink{sink}, buffer.NewRingBuffer(64))
	eng.SetLogger(logger)

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	done := make(chan struct{})
	go func() { defer close(done); _ = eng.Start(ctx) }()

	select {
	case <-sink.done:
	case <-ctx.Done():
		t.Fatalf("only %d of %d messages were written before the deadline", sink.count(), messages)
	}
	// Let the last processMessage return before stopping.
	time.Sleep(100 * time.Millisecond)
	cancel()
	<-done

	lines := logger.withAction("processed")
	if len(lines) > 2 {
		t.Errorf("%d summary lines for %d messages inside one window; want the first message's line "+
			"and one more for the rest at most", len(lines), messages)
	}
	total := 0
	for _, ln := range lines {
		n, _ := ln.kv["delivered"].(int)
		total += n
	}
	if total != messages {
		t.Errorf("the summary lines account for %d delivered messages, want %d", total, messages)
	}
}

// A write that failed for good left no step on the trace at all: the step for
// a sink is recorded only after a successful attempt. The journey then ended at
// the router with every step green, which reads as a success.
func TestAFailedSinkWriteIsOnTheTrace(t *testing.T) {
	src := &oneShotSource{}
	sink := &failingSink{err: errors.New("connection refused")}
	recorder := &mockTraceRecorder{}

	eng := NewEngine(src, []hermod.Sink{sink}, buffer.NewRingBuffer(8))
	eng.SetLogger(&testLogger{})
	eng.traceRecorder = recorder
	eng.SetIDs("wf-failing-sink", "src", []string{"snk-1"})
	eng.config.TraceSampleRate = 1.0
	eng.config.MaxRetries = 1
	eng.config.RetryInterval = time.Millisecond

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	go func() { _ = eng.Start(ctx) }()

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		for _, s := range recorder.GetSteps("m-1") {
			if s.NodeID == "snk-1" && s.Error != "" {
				if !strings.Contains(s.Error, "connection refused") {
					t.Errorf("the sink step's error is %q, want the sink's own error", s.Error)
				}
				return
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("no failed step for the sink on the trace; steps: %+v", recorder.GetSteps("m-1"))
}

type failingSink struct{ err error }

func (s *failingSink) Write(context.Context, hermod.Message) error { return s.err }
func (s *failingSink) Ping(context.Context) error                  { return nil }
func (s *failingSink) Close() error                                { return nil }
