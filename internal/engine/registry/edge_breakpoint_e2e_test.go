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
	"fmt"
	"testing"
	"time"

	"github.com/gsoultan/hermod/internal/storage"
	"github.com/gsoultan/hermod/pkg/infra/state"
)

// breakpointWorkflow is benchWorkflow with a breakpoint on e3, the edge from the
// mapping into the sink.
func breakpointWorkflow(id string) storage.Workflow {
	wf := benchWorkflow(id)
	for i := range wf.Edges {
		if wf.Edges[i].ID == "e3" {
			wf.Edges[i].Config = map[string]any{"breakpoint": true}
		}
	}
	return wf
}

func nextDebuggerEvent(t *testing.T, ch chan DebuggerEvent) DebuggerEvent {
	t.Helper()
	select {
	case ev := <-ch:
		return ev
	case <-time.After(10 * time.Second):
		t.Fatal("no debugger event arrived")
	}
	return DebuggerEvent{}
}

// With the editor watching breakpoints, a live message stops on the edge that
// has one, before the node it leads to, carrying what it would deliver; the
// sink gets nothing until the editor resumes it by the pause's ID.
func TestLiveMessageStopsAtEdgeBreakpointUntilResumed(t *testing.T) {
	reg := NewRegistry(newPipeStorage())
	reg.SetStateStore(state.NewMemoryStore())
	const wfID = "wf-edge-bp"

	ch := reg.SubscribeBreakpoints(wfID)
	defer reg.UnsubscribeDebugger(wfID, ch)

	snk := &pipeSink{name: "out", countOnly: true}
	stop := startBenchPipeline(t, reg, breakpointWorkflow(wfID), newWideSource(1, 4), snk)
	defer stop()

	ev := nextDebuggerEvent(t, ch)
	if ev.State != "paused" || ev.EdgeID != "e3" || ev.NodeID != "out" || ev.PauseID == "" {
		t.Fatalf("event = %+v, want paused on e3 before out, with a pause ID", ev)
	}
	if _, ok := ev.Data["band"]; !ok {
		t.Errorf("held message %v is not what the mapping emitted (no \"band\")", ev.Data)
	}
	if reg.IsDebuggerAttached(wfID) {
		t.Error("watching breakpoints turned on stepping through every node")
	}

	time.Sleep(200 * time.Millisecond)
	if snk.count() != 0 {
		t.Fatalf("the sink got %d messages while the message was held", snk.count())
	}

	reg.DebuggerCommand(wfID, ev.PauseID, "resume")
	deadline := time.Now().Add(10 * time.Second)
	for snk.count() < 1 {
		if time.Now().After(deadline) {
			t.Fatal("the sink never got the message after it was resumed")
		}
		time.Sleep(time.Millisecond)
	}
	if resumed := nextDebuggerEvent(t, ch); resumed.State != "resume" || resumed.PauseID != ev.PauseID {
		t.Errorf("after resuming got %+v, want a resume for pause %s", resumed, ev.PauseID)
	}
}

// With nobody attached a breakpoint holds nothing.
func TestEdgeBreakpointIsIgnoredWhenNobodyWatches(t *testing.T) {
	reg := NewRegistry(newPipeStorage())
	reg.SetStateStore(state.NewMemoryStore())

	snk := &pipeSink{name: "out", countOnly: true}
	stop := startBenchPipeline(t, reg, breakpointWorkflow("wf-edge-bp-unwatched"), newWideSource(3, 4), snk)
	defer stop()

	deadline := time.Now().Add(10 * time.Second)
	for snk.count() < 3 {
		if time.Now().After(deadline) {
			t.Fatalf("only %d of 3 messages reached the sink with nobody watching breakpoints", snk.count())
		}
		time.Sleep(time.Millisecond)
	}
}

// An editor that connects, or reconnects, while a message is already held must
// be able to see it and answer it; the pause is not announced a second time.
func TestHeldPausesListsWhatIsHeldUntilAnswered(t *testing.T) {
	reg := NewRegistry(nil)
	t.Cleanup(reg.Close)
	msg := newCancelTestMessage(t, map[string]any{"n": 1})
	t.Cleanup(msg.Release)

	done := make(chan struct{})
	go func() {
		defer close(done)
		reg.PauseAtEdge(t.Context(), "wf-held", "e-1", "n-2", msg)
	}()

	var held []DebuggerEvent
	deadline := time.Now().Add(5 * time.Second)
	for len(held) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("the held message was never listed")
		}
		time.Sleep(time.Millisecond)
		held = reg.HeldPauses("wf-held")
	}
	if held[0].EdgeID != "e-1" || held[0].State != "paused" || fmt.Sprint(held[0].Data["n"]) != "1" {
		t.Fatalf("HeldPauses = %+v, want the pause on e-1 with its payload", held)
	}
	if other := reg.HeldPauses("wf-other"); len(other) != 0 {
		t.Errorf("another workflow lists %v", other)
	}

	reg.DebuggerCommand("wf-held", held[0].PauseID, "resume")
	<-done
	if left := reg.HeldPauses("wf-held"); len(left) != 0 {
		t.Errorf("still listed after it was answered: %v", left)
	}
}

// A pause that ends without an answer says so, so an editor stops offering to
// resume a message that has already moved on.
func TestEdgePauseEndedByAStopIsAnnounced(t *testing.T) {
	reg := NewRegistry(nil)
	t.Cleanup(reg.Close)
	ch := reg.SubscribeBreakpoints("wf-stop")
	defer reg.UnsubscribeDebugger("wf-stop", ch)
	msg := newCancelTestMessage(t, map[string]any{"id": 1})
	t.Cleanup(msg.Release)

	ctx, cancel := context.WithCancel(t.Context())
	go reg.PauseAtEdge(ctx, "wf-stop", "e-1", "n-2", msg)
	paused := nextDebuggerEvent(t, ch)
	cancel()

	ended := nextDebuggerEvent(t, ch)
	if ended.State != "stopped" || ended.PauseID != paused.PauseID {
		t.Errorf("after the workflow stopped got %+v, want state stopped for pause %s", ended, paused.PauseID)
	}
}
