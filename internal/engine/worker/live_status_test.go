// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package worker

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
	"time"

	"github.com/gsoultan/hermod/internal/engine/registry"
	"github.com/gsoultan/hermod/pkg/engine/telemetry"
)

// The client posts the report to its own worker's live-status endpoint, signed
// with its token, and hands back the platform's reply.
func TestWorkerAPIClientReportLiveStatus(t *testing.T) {
	var gotPath, gotToken string
	var got telemetry.LiveStatusReport
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotToken = r.Method+" "+r.URL.Path, r.Header.Get("X-Worker-Token")
		_ = json.NewDecoder(r.Body).Decode(&got)
		_ = json.NewEncoder(w).Encode(telemetry.LiveStatusReply{Watched: []string{"wf-1"}})
	}))
	defer srv.Close()

	c := NewWorkerAPIClient(srv.URL, "tok")
	reply, err := c.ReportLiveStatus(t.Context(), "w-1", telemetry.LiveStatusReport{Running: []string{"wf-1"}})
	if err != nil {
		t.Fatal(err)
	}
	if gotPath != "POST /api/workers/w-1/live-status" || gotToken != "tok" {
		t.Errorf("request = %q with token %q", gotPath, gotToken)
	}
	if !slices.Equal(got.Running, []string{"wf-1"}) {
		t.Errorf("platform received Running = %v", got.Running)
	}
	if !slices.Equal(reply.Watched, []string{"wf-1"}) {
		t.Errorf("reply.Watched = %v", reply.Watched)
	}
}

// A platform from before the endpoint answers 404; the worker is told so it can
// stop asking every second.
func TestWorkerAPIClientReportLiveStatusOnOlderPlatform(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	defer srv.Close()

	_, err := NewWorkerAPIClient(srv.URL, "tok").ReportLiveStatus(t.Context(), "w-1", telemetry.LiveStatusReport{})
	if !errors.Is(err, ErrLiveStatusUnsupported) {
		t.Fatalf("err = %v, want ErrLiveStatusUnsupported", err)
	}
}

// fakeLiveSource is the registry as the relay sees it.
type fakeLiveSource struct {
	running  []string
	statuses map[string]telemetry.StatusUpdate
	watched  []string // last SetRemotelyWatched
}

func (f *fakeLiveSource) RunningWorkflowIDs() []string { return f.running }
func (f *fakeLiveSource) GetWorkflowStatus(id string) (telemetry.StatusUpdate, bool) {
	s, ok := f.statuses[id]
	return s, ok
}
func (f *fakeLiveSource) SetRemotelyWatched(ids []string) { f.watched = slices.Clone(ids) }

// fakeReporter is the platform: it records each report and answers with reply.
type fakeReporter struct {
	reports []telemetry.LiveStatusReport
	reply   telemetry.LiveStatusReply
	err     error
}

func (f *fakeReporter) ReportLiveStatus(_ context.Context, _ string, report telemetry.LiveStatusReport) (telemetry.LiveStatusReply, error) {
	f.reports = append(f.reports, report)
	return f.reply, f.err
}

func newTestRelay(src *fakeLiveSource, rep *fakeReporter) *liveStatusRelay {
	return &liveStatusRelay{workerID: "w-1", source: src, reporter: rep}
}

// The worker sends status only for the workflows the platform says are open,
// learns which those are from the reply, and while any are open reports every
// second, so the editor can turn edge counts into rates.
func TestLiveStatusRelaySendsOnlyWatchedWorkflows(t *testing.T) {
	src := &fakeLiveSource{
		running: []string{"wf-a", "wf-b"},
		statuses: map[string]telemetry.StatusUpdate{
			"wf-a": {ProcessedCount: 1},
			"wf-b": {ProcessedCount: 2},
		},
	}
	rep := &fakeReporter{reply: telemetry.LiveStatusReply{Watched: []string{"wf-b"}}}
	relay := newTestRelay(src, rep)

	// First report: nothing is known to be open yet, so no frames, just the list.
	if next := relay.once(t.Context()); next != liveStatusWatchedInterval {
		t.Errorf("next report in %v with a workflow open, want %v", next, liveStatusWatchedInterval)
	}
	if first := rep.reports[0]; len(first.Updates) != 0 || !slices.Equal(first.Running, src.running) {
		t.Errorf("first report = %+v, want the running list and no frames", first)
	}
	if !slices.Equal(src.watched, []string{"wf-b"}) {
		t.Errorf("registry told %v is open, want [wf-b] so it samples payloads", src.watched)
	}

	relay.once(t.Context())
	second := rep.reports[1]
	if len(second.Updates) != 1 || second.Updates[0].WorkflowID != "wf-b" || second.Updates[0].ProcessedCount != 2 {
		t.Errorf("second report's frames = %+v, want wf-b's alone, stamped with its ID", second.Updates)
	}
}

// With nothing open the worker only checks in now and then, and the registry
// stops sampling payloads.
func TestLiveStatusRelayIdlesWhenNothingIsOpen(t *testing.T) {
	src := &fakeLiveSource{running: []string{"wf-a"}, watched: []string{"wf-a"}}
	rep := &fakeReporter{}
	relay := newTestRelay(src, rep)
	relay.watched = []string{"wf-a"}

	if next := relay.once(t.Context()); next != liveStatusIdleInterval {
		t.Errorf("next report in %v with nothing open, want %v", next, liveStatusIdleInterval)
	}
	if len(src.watched) != 0 {
		t.Errorf("registry still told %v is open", src.watched)
	}
}

// A worker running nothing has nothing to report.
func TestLiveStatusRelaySkipsWhenNothingRuns(t *testing.T) {
	rep := &fakeReporter{}
	if next := newTestRelay(&fakeLiveSource{}, rep).once(t.Context()); next != liveStatusIdleInterval {
		t.Errorf("next report in %v, want %v", next, liveStatusIdleInterval)
	}
	if len(rep.reports) != 0 {
		t.Errorf("sent %d reports with nothing running", len(rep.reports))
	}
}

// An unreachable platform is retried at the idle pace, and nothing counts as
// open meanwhile: nobody can be watching frames that cannot arrive.
func TestLiveStatusRelayBacksOffWhenThePlatformFails(t *testing.T) {
	src := &fakeLiveSource{running: []string{"wf-a"}}
	rep := &fakeReporter{err: errors.New("connection refused")}
	relay := newTestRelay(src, rep)
	relay.watched = []string{"wf-a"}
	src.watched = []string{"wf-a"}

	if next := relay.once(t.Context()); next != liveStatusIdleInterval {
		t.Errorf("next report in %v after a failure, want %v", next, liveStatusIdleInterval)
	}
	if len(src.watched) != 0 || len(relay.watched) != 0 {
		t.Errorf("still treating %v / %v as open after a failed report", src.watched, relay.watched)
	}

	rep.err = ErrLiveStatusUnsupported
	if next := relay.once(t.Context()); next != liveStatusUnsupportedInterval {
		t.Errorf("next report in %v on an older platform, want %v", next, liveStatusUnsupportedInterval)
	}
}

// The relay runs until its context ends.
func TestLiveStatusRelayRunStopsWithContext(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() {
		newTestRelay(&fakeLiveSource{}, &fakeReporter{}).run(ctx)
		close(done)
	}()
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("relay still running after its context ended")
	}
}

// A worker backed by the platform API reports live status as itself; one on a
// shared database has no platform to report to and starts no relay.
func TestWorkerStartsLiveStatusRelayOnlyAgainstThePlatform(t *testing.T) {
	reported := make(chan string, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case reported <- r.URL.Path:
		default:
		}
		_ = json.NewEncoder(w).Encode(telemetry.LiveStatusReply{})
	}))
	defer srv.Close()

	reg := registry.NewRegistry(nil)
	w := NewWorker(NewWorkerAPIClient(srv.URL, "tok"), reg)
	w.SetWorkerConfig(0, 1, "w-1", "tok")
	w.relaySource = &fakeLiveSource{running: []string{"wf-a"}}

	if !w.startLiveStatusRelay(t.Context()) {
		t.Fatal("no relay started for a worker backed by the platform API")
	}
	select {
	case path := <-reported:
		if path != "/api/workers/w-1/live-status" {
			t.Errorf("reported to %s", path)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the relay never reported")
	}

	shared := NewWorker(&mockSimpleStorage{}, reg)
	shared.SetWorkerConfig(0, 1, "w-2", "tok")
	if shared.startLiveStatusRelay(t.Context()) {
		t.Error("a relay started for a worker with no platform to report to")
	}
}
