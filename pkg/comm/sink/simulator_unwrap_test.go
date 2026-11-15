// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package sink

import (
	"context"
	"testing"
	"time"

	"github.com/gsoultan/hermod"
)

type simSink struct{ hermod.Sink }

func (simSink) Simulate(context.Context, hermod.Message) (hermod.SinkSimulation, error) {
	return hermod.SinkSimulation{RolledBack: true}, nil
}

// Every sink a workflow runs is wrapped in the retry and tracing decorators,
// so a dry run only finds the sink that can simulate by looking through them.
func TestSimulatorOfLooksThroughTheDecorators(t *testing.T) {
	wrapped := NewRetrySink(NewTracingSink(simSink{}, "s1"), 3, time.Millisecond, nil)
	sim, ok := hermod.SimulatorOf(wrapped)
	if !ok {
		t.Fatal("the simulating sink behind the decorators was not found")
	}
	if rep, _ := sim.Simulate(t.Context(), nil); !rep.RolledBack {
		t.Fatal("found the wrong sink")
	}
}

// A sink that cannot simulate must not appear to, decorated or not: a dry run
// would otherwise report work it never did.
func TestSimulatorOfDoesNotInventOne(t *testing.T) {
	if _, ok := hermod.SimulatorOf(NewRetrySink(NewTracingSink(&mockSink{}, "s1"), 3, time.Millisecond, nil)); ok {
		t.Fatal("a plain sink was reported as able to simulate")
	}
	if _, ok := hermod.SimulatorOf(nil); ok {
		t.Fatal("nil was reported as able to simulate")
	}
}
