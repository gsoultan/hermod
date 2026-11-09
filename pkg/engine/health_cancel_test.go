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
	"strings"
	"testing"
	"time"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/pkg/comm/buffer"
)

// pingsOnContext fails its health check exactly when the check's context is
// done, the way a network connector does when its dial is cancelled.
type pingsOnContext struct{ mockSink }

func (*pingsOnContext) Ping(ctx context.Context) error { return ctx.Err() }

type sourcePingsOnContext struct{ idleSource }

func (sourcePingsOnContext) Ping(ctx context.Context) error { return ctx.Err() }

// A health pass that is still in flight when the workflow is stopped sees its
// pings fail with "operation was canceled". That is the stop, not a sink or a
// source going away: reporting it marked the workflow "reconnecting" and logged
// an error that read like an outage — for panmail, "cannot reach the gateway:
// lookup <host>: operation was canceled" — on every stop and restart.
func TestHealthCheckDoesNotReportAStoppingWorkflowAsReconnecting(t *testing.T) {
	sink := &pingsOnContext{}
	eng := NewEngine(sourcePingsOnContext{}, []hermod.Sink{sink}, buffer.NewRingBuffer(8))
	eng.SetConfig(stallTestConfig(50 * time.Millisecond))
	eng.setStatus("running")
	eng.setSourceStatus("running")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r := &Runner{engine: eng, ctx: ctx}

	r.checkHealth(time.Millisecond)

	got := eng.GetStatus()
	if strings.HasPrefix(got.EngineStatus, "reconnecting") {
		t.Errorf("engine status = %q after a health pass cancelled by a stop, want it left as %q",
			got.EngineStatus, "running")
	}
	if got.SourceStatus == "reconnecting" {
		t.Errorf("source status = %q after a health pass cancelled by a stop", got.SourceStatus)
	}
	for id, status := range got.SinkStatuses {
		if status == "reconnecting" {
			t.Errorf("sink %q status = %q after a health pass cancelled by a stop", id, status)
		}
	}
}
