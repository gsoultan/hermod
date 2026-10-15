// Copyright (c) 2026 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Hermod: real-time Postgres CDC and data pipelines in one Go binary.
// Each file is proprietary and confidential; see LICENSE for the terms.
// Resale, sublicensing or selling it to any third party is prohibited.
// Making copies, modifying or distributing it needs written permission
// Of the copyright holder, Gembit Soultan Shirazi.
// Distributed "AS IS", WITHOUT WARRANTY OF ANY KIND, express or implied.

package registry

import (
	"fmt"
	"testing"
	"time"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/internal/factory"
	"github.com/gsoultan/hermod/internal/storage"
)

// Draining a workflow's dead letters replays what it has waiting on the Dead
// Letters page, and settles each one that is delivered, so the page and the
// destination agree on what went through. It is what Drain DLQ does, and what
// "replay dead letters on startup" does each time the workflow starts.

func drainRegistry(t *testing.T, out *pipeSink) (*Registry, *pipeDeadLetterStorage) {
	t.Helper()
	st := newPipeDeadLetterStorage()
	reg := NewRegistry(st)
	t.Cleanup(reg.Close)
	reg.SetFactories(nil, func(cfg factory.SinkConfig) (hermod.Sink, error) {
		if cfg.ID == "snk-1" {
			return out, nil
		}
		return nil, fmt.Errorf("no sink fixture for %q", cfg.ID)
	})
	return reg, st
}

func waiting(id, workflowID, nodeID string) storage.DeadLetter {
	d := failedAtFix()
	d.ID, d.WorkflowID, d.NodeID = id, workflowID, nodeID
	d.Payload = []byte(`{"id":"ord-88412","total":"12.50"}`)
	return d
}

func TestDrainingReplaysAndSettlesTheWorkflowsWaitingDeadLetters(t *testing.T) {
	out := &pipeSink{name: "out"}
	reg, st := drainRegistry(t, out)
	wf := replayWorkflow()
	wf.DeadLetters = true
	for _, d := range []storage.DeadLetter{
		waiting("dl-1", wf.ID, "fix"),
		waiting("dl-2", wf.ID, "fix"),
		waiting("dl-other", "wf-other", "fix"),
	} {
		_ = st.PutDeadLetter(t.Context(), d)
	}

	replayed, failed := reg.ReplayWaitingDeadLetters(t.Context(), wf, "drain")

	if replayed != 2 || failed != 0 {
		t.Fatalf("replayed %d, failed %d; want 2 and 0", replayed, failed)
	}
	if got := len(out.received()); got != 2 {
		t.Fatalf("the sink received %d messages, want 2", got)
	}
	for id, want := range map[string]storage.DeadLetterStatus{
		"dl-1": storage.DeadLetterReplayed, "dl-2": storage.DeadLetterReplayed, "dl-other": storage.DeadLetterQueued,
	} {
		if got := st.status(id); got != want {
			t.Errorf("%s is %q, want %q", id, got, want)
		}
	}
}

// A dead letter that cannot go through stays waiting, with the attempt in its
// history; the ones after it are still tried.
func TestDrainingLeavesWhatFailsWaitingAndCarriesOn(t *testing.T) {
	out := &pipeSink{name: "out"}
	reg, st := drainRegistry(t, out)
	wf := replayWorkflow()
	_ = st.PutDeadLetter(t.Context(), waiting("dl-gone", wf.ID, "a-node-since-removed"))
	_ = st.PutDeadLetter(t.Context(), waiting("dl-ok", wf.ID, "fix"))

	replayed, failed := reg.ReplayWaitingDeadLetters(t.Context(), wf, "drain")

	if replayed != 1 || failed != 1 {
		t.Fatalf("replayed %d, failed %d; want 1 and 1", replayed, failed)
	}
	if st.status("dl-gone") != storage.DeadLetterQueued || st.status("dl-ok") != storage.DeadLetterReplayed {
		t.Fatalf("dl-gone is %q, dl-ok is %q", st.status("dl-gone"), st.status("dl-ok"))
	}
	gone, _ := st.GetDeadLetter(t.Context(), "dl-gone")
	if last := gone.History[len(gone.History)-1]; last.Action != "replay_failed" {
		t.Fatalf("the failed attempt is not in the history: %+v", gone.History)
	}
}

// When the cause is still there every replay fails the same way, so a drain
// gives up after a few in a row instead of walking the whole queue.
func TestDrainingStopsAfterFailuresInARow(t *testing.T) {
	out := &pipeSink{name: "out"}
	reg, st := drainRegistry(t, out)
	wf := replayWorkflow()
	for i := range maxDrainFailuresInARow + 3 {
		_ = st.PutDeadLetter(t.Context(), waiting("dl-"+string(rune('a'+i)), wf.ID, "gone"))
	}

	_, failed := reg.ReplayWaitingDeadLetters(t.Context(), wf, "drain")

	if failed != maxDrainFailuresInARow {
		t.Fatalf("tried %d failing dead letters, want to stop at %d", failed, maxDrainFailuresInARow)
	}
}

// Drain DLQ runs in the background on the workflow as stored.
func TestDrainWorkflowDLQReplaysInTheBackground(t *testing.T) {
	out := &pipeSink{name: "out"}
	reg, st := drainRegistry(t, out)
	wf := replayWorkflow()
	wf.DeadLetters = true
	st.workflow = &wf
	_ = st.PutDeadLetter(t.Context(), waiting("dl-1", wf.ID, "fix"))

	if err := reg.DrainWorkflowDLQ(t.Context(), wf.ID); err != nil {
		t.Fatal(err)
	}
	if !waitUntil(t, 10*time.Second, "the dead letter to be replayed", func() bool {
		return st.status("dl-1") == storage.DeadLetterReplayed
	}) {
		t.Fatal("Drain DLQ did not replay the waiting dead letter")
	}
}

// "Replay dead letters on startup" drains the workflow as it starts.
func TestAWorkflowReplaysItsDeadLettersWhenItStarts(t *testing.T) {
	st := newPipeDeadLetterStorage()
	reg := NewRegistry(st)
	t.Cleanup(reg.Close)
	sinks := map[string]*pipeSink{"snk-out": {name: "out"}}
	wf := filterWorkflow(true)
	wf.PrioritizeDLQ = true
	d := storage.DeadLetter{
		ID: "dl-start", WorkflowID: wf.ID, MessageID: "m-1", SinkID: "snk-out", Error: "connection refused",
		Payload: []byte(`{"keep":"yes"}`), Status: storage.DeadLetterQueued,
	}
	_ = st.PutDeadLetter(t.Context(), d)

	stop := startForeachPipeline(t, reg, wf, &twoRecordSource{sent: 2}, sinks)
	defer stop()

	if !waitUntil(t, 10*time.Second, "the dead letter to be replayed", func() bool {
		return st.status("dl-start") == storage.DeadLetterReplayed
	}) {
		t.Fatal("the workflow did not replay its waiting dead letter when it started")
	}
	if got := sinks["snk-out"].count(); got != 1 {
		t.Fatalf("the sink received %d messages, want the 1 replayed", got)
	}
}
