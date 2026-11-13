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
	"testing"
	"time"
)

// The traversal asks IsDebuggerAttached before every node of every message,
// and BreakpointsWatched on every edge. Both took the debugger RWMutex's read
// lock, whose shared reader count bounced between cores: ~7% of CPU in the
// workflow benchmark, to answer "no" — nobody is debugging almost all of the
// time. With no debugger attached anywhere they must answer without the lock;
// held here, a lock-taking answer would block.
func TestDebuggerChecksSkipTheLockWhenNoDebuggerIsAttached(t *testing.T) {
	r := NewRegistry(nil)

	r.debuggerSubsMu.Lock()
	done := make(chan [2]bool, 1)
	go func() {
		done <- [2]bool{r.IsDebuggerAttached("wf"), r.BreakpointsWatched("wf")}
	}()
	select {
	case got := <-done:
		r.debuggerSubsMu.Unlock()
		if got[0] || got[1] {
			t.Fatalf("with no debugger attached: attached=%v watched=%v, want false, false", got[0], got[1])
		}
	case <-time.After(time.Second):
		r.debuggerSubsMu.Unlock()
		<-done
		t.Fatal("IsDebuggerAttached/BreakpointsWatched waited on the debugger lock with no debugger attached")
	}
}

func TestDebuggerChecksStillSeeAnAttachedDebugger(t *testing.T) {
	r := NewRegistry(nil)

	step := r.subscribeDebugger("wf", true)
	if !r.IsDebuggerAttached("wf") || !r.BreakpointsWatched("wf") {
		t.Error("a stepping debugger on wf was not seen")
	}
	if r.IsDebuggerAttached("other") || r.BreakpointsWatched("other") {
		t.Error("a debugger on wf was reported on another workflow")
	}

	bp := r.SubscribeBreakpoints("wf")
	r.UnsubscribeDebugger("wf", step)
	if r.IsDebuggerAttached("wf") {
		t.Error("a breakpoint-only subscriber was reported as stepping")
	}
	if !r.BreakpointsWatched("wf") {
		t.Error("a breakpoint subscriber was not seen")
	}

	r.UnsubscribeDebugger("wf", bp)
	if r.IsDebuggerAttached("wf") || r.BreakpointsWatched("wf") {
		t.Error("a debugger was still reported after every subscriber left")
	}
}
