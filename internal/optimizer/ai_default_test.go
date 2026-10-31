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

	"github.com/gsoultan/hermod/pkg/engine"
	"github.com/gsoultan/hermod/pkg/engine/telemetry"
)

type nopLogger struct{}

func (nopLogger) Debug(string, ...any) {}
func (nopLogger) Info(string, ...any)  {}
func (nopLogger) Warn(string, ...any)  {}
func (nopLogger) Error(string, ...any) {}

type countingAI struct{ calls atomic.Int32 }

func (c *countingAI) Optimize(context.Context, string, *engine.Engine, telemetry.StatusUpdate) error {
	c.calls.Add(1)
	return nil
}

// The status frame now carries a real error rate and backpressure. Until it
// did, both read 0 and the AI optimizer's branches never fired: a failing flow
// would now send its status, payload samples included, to an LLM every cycle
// and throw the answer away, and a busy one would have its concurrency raised
// without bound. So the AI pass is off unless someone turns it on.
func TestNewOptimizerLeavesTheAIPassOff(t *testing.T) {
	ai := &countingAI{}
	o := NewOptimizer(nopLogger{}, ai, nil)
	o.Register("wf", engine.NewEngine(nil, nil, nil))

	o.optimizeAll()

	if n := ai.calls.Load(); n != 0 {
		t.Errorf("AI optimizer ran %d times on a default optimizer, want 0", n)
	}
}
