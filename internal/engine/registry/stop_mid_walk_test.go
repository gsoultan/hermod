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
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/internal/storage"
	"github.com/gsoultan/hermod/pkg/comm/message"
)

// oneShotAckSource emits one message and counts the acknowledgements it gets.
type oneShotAckSource struct {
	sent  atomic.Bool
	acked atomic.Int64
}

func (s *oneShotAckSource) Read(ctx context.Context) (hermod.Message, error) {
	if s.sent.CompareAndSwap(false, true) {
		m := message.AcquireMessage()
		m.SetID("order-1")
		m.SetData("id", "order-1")
		return m, nil
	}
	<-ctx.Done()
	return nil, ctx.Err()
}

func (s *oneShotAckSource) Ack(context.Context, hermod.Message) error {
	s.acked.Add(1)
	return nil
}
func (s *oneShotAckSource) Ping(context.Context) error { return nil }
func (s *oneShotAckSource) Close() error               { return nil }

// A stop that cuts one branch of a split short must leave the message
// unacknowledged, however far the other branches got.
//
//	src ─┬─> audit                      (reaches its sink at once)
//	     └─> score (api_lookup) ─> out  (still waiting on the API at the stop)
//
// The interrupted node was neither dead-lettered nor counted as a filter, but
// the walk had routed the message to audit, so the engine wrote audit and
// acknowledged the source. The out branch was gone: the branching load test
// lost a RabbitMQ record from its enriched table across a restart this way.
// Left unacknowledged, it is delivered again on the next run and both branches
// get it.
func TestStopMidWalkLeavesASplitMessageUnacknowledged(t *testing.T) {
	entered := make(chan struct{})
	var once atomic.Bool
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if once.CompareAndSwap(false, true) {
			close(entered)
		}
		<-r.Context().Done()
	}))
	defer api.Close()

	store := newPipeStorage()
	reg := NewRegistry(store)
	sinks := map[string]*pipeSink{"snk-audit": {name: "audit"}, "snk-out": {name: "out"}}
	src := &oneShotAckSource{}

	wf := storage.Workflow{
		ID: "wf-stop-mid-walk", Name: "wf-stop-mid-walk",
		Nodes: []storage.WorkflowNode{
			{ID: "src", Type: "source", RefID: "s-orders"},
			{ID: "audit", Type: "sink", RefID: "snk-audit"},
			{ID: "score", Type: "transformation", Config: map[string]any{
				"transType": "api_lookup", "url": api.URL, "targetField": "score", "timeout": "30s",
			}},
			{ID: "out", Type: "sink", RefID: "snk-out"},
		},
		Edges: []storage.WorkflowEdge{
			{ID: "e1", SourceID: "src", TargetID: "audit"},
			{ID: "e2", SourceID: "src", TargetID: "score"},
			{ID: "e3", SourceID: "score", TargetID: "out"},
		},
	}

	stop := startForeachPipeline(t, reg, wf, src, sinks)
	select {
	case <-entered:
	case <-time.After(10 * time.Second):
		stop()
		t.Fatal("the message never reached the API")
	}
	stop()

	if n := src.acked.Load(); n != 0 {
		t.Errorf("the source was acknowledged %d time(s) although the stop cut the score branch short "+
			"(out received %d): the message will not be delivered again and out never gets it",
			n, len(sinks["snk-out"].received()))
	}
}
