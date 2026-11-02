// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package registry

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/internal/factory"
	"github.com/gsoultan/hermod/internal/storage"
	"github.com/gsoultan/hermod/pkg/comm/message"
	"github.com/gsoultan/hermod/pkg/engine/config"
)

// resendingSource is a polling source whose query keeps selecting a row that
// is still pending: the same record, under a fresh message id, every read.
type resendingSource struct{ reads atomic.Int64 }

func (s *resendingSource) Read(ctx context.Context) (hermod.Message, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-time.After(10 * time.Millisecond):
	}
	n := s.reads.Add(1)
	msg := message.AcquireMessage()
	msg.SetID(fmt.Sprintf("read-%d", n))
	msg.SetOperation("create")
	msg.SetPayload([]byte(`{"ApplicationId":"app-1","Subject":"Supporting documents"}`))
	return msg, nil
}
func (s *resendingSource) Ack(context.Context, hermod.Message) error { return nil }
func (s *resendingSource) Ping(context.Context) error                { return nil }
func (s *resendingSource) Close() error                              { return nil }

// switchSink refuses every write while it is down, like a mail gateway with a
// rejected API key.
type switchSink struct {
	down      atomic.Bool
	delivered atomic.Int64
}

func (s *switchSink) Write(context.Context, hermod.Message) error {
	if s.down.Load() {
		return errors.New("panmail: the api key was not accepted: invalid credentials")
	}
	s.delivered.Add(1)
	return nil
}
func (s *switchSink) Ping(context.Context) error { return nil }
func (s *switchSink) Close() error               { return nil }

// The case from the dead letter export: a source re-sends one record every few
// seconds and the sink refuses each copy. The running workflow keeps one row
// that counts the copies, and once the sink is fixed the next copy goes
// through and closes that row, so it is not left for someone to replay.
func TestARecordTheSourceKeepsResendingIsOneRowThatClosesWhenDelivered(t *testing.T) {
	st := newPipeDeadLetterStorage()
	reg := NewRegistry(st)
	t.Cleanup(reg.Close)
	src := &resendingSource{}
	out := &switchSink{}
	out.down.Store(true)
	reg.SetFactories(
		func(factory.SourceConfig) (hermod.Source, error) { return src, nil },
		func(cfg factory.SinkConfig) (hermod.Sink, error) { return out, nil },
	)
	wf := storage.Workflow{
		ID: "wf-docs", Name: "Supporting Document Send",
		Nodes: []storage.WorkflowNode{
			{ID: "src", Type: "source", RefID: "s-outbox"},
			{ID: "mail", Type: "sink", RefID: "snk-panmail"},
		},
		Edges:       []storage.WorkflowEdge{{ID: "e1", SourceID: "src", TargetID: "mail"}},
		DeadLetters: true, MaxRetries: 1, RetryInterval: "1ms",
	}
	if err := reg.StartWorkflow(wf.ID, wf); err != nil {
		t.Fatal(err)
	}
	if eng, ok := reg.GetEngine(wf.ID); ok {
		eng.UpdateSinkConfig("snk-panmail", func(cfg *config.SinkConfig) {
			cfg.BatchSize = 1
			cfg.BatchTimeout = 5 * time.Millisecond
			// The breaker opens after a run of refusals, as it should; a
			// short cool-down lets the fixed sink be tried again in time.
			cfg.CircuitBreakerCoolDown = 20 * time.Millisecond
		})
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_ = reg.StopEngine(ctx, wf.ID)
	})

	if !waitUntil(t, 15*time.Second, "several copies to be parked", func() bool {
		kept := st.all()
		return len(kept) > 0 && kept[0].Attempts() >= 5
	}) {
		t.Fatalf("the copies were not counted into one row: %d rows", len(st.all()))
	}
	if kept := st.all(); len(kept) != 1 {
		t.Fatalf("%d copies of one record made %d rows, want 1", kept[0].Attempts(), len(kept))
	}
	id := st.all()[0].ID

	out.down.Store(false)
	if !waitUntil(t, 15*time.Second, "the waiting row to close once a copy is delivered", func() bool {
		return st.status(id) == storage.DeadLetterDelivered
	}) {
		t.Fatalf("the row is %q after %d deliveries, want delivered", st.status(id), out.delivered.Load())
	}
}
