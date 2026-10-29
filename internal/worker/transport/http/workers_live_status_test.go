// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package http

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
	"time"

	"github.com/gsoultan/hermod/internal/api/handlers"
	"github.com/gsoultan/hermod/internal/engine/registry"
	"github.com/gsoultan/hermod/internal/storage"
	"github.com/gsoultan/hermod/pkg/engine/telemetry"
)

// liveStatusStorage resolves workflows by ID and nothing else.
type liveStatusStorage struct {
	storage.Storage
	workflows map[string]storage.Workflow
}

func (m *liveStatusStorage) GetWorkflow(_ context.Context, id string) (storage.Workflow, error) {
	wf, ok := m.workflows[id]
	if !ok {
		return storage.Workflow{}, storage.ErrNotFound
	}
	return wf, nil
}

func newLiveStatusHandler(t *testing.T, workflows ...storage.Workflow) (*WorkerHandler, *registry.Registry) {
	t.Helper()
	store := &liveStatusStorage{workflows: map[string]storage.Workflow{}}
	for _, wf := range workflows {
		store.workflows[wf.ID] = wf
	}
	reg := registry.NewRegistry(nil)
	return &WorkerHandler{Handler: &handlers.Handler{Storage: store, Registry: reg}}, reg
}

// postLiveStatus sends report to worker w-1's live-status endpoint as caller,
// the user the auth middleware would have put on the request.
func postLiveStatus(t *testing.T, h *WorkerHandler, caller *storage.User, report telemetry.LiveStatusReport) *httptest.ResponseRecorder {
	const workerID = "w-1"
	t.Helper()
	body, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/workers/"+workerID+"/live-status", bytes.NewReader(body))
	req.SetPathValue("id", workerID)
	if caller != nil {
		req = req.WithContext(context.WithValue(req.Context(), handlers.UserContextKey, caller))
	}
	w := httptest.NewRecorder()
	h.ReportLiveStatus(w, req)
	return w
}

// workerCaller is the user the auth middleware builds for a worker token.
func workerCaller(id string) *storage.User {
	return &storage.User{ID: "worker:" + id, Username: "worker:" + id, Role: storage.RoleEditor, VHosts: []string{"*"}}
}

// subscribe opens a status subscription on wfID, as the editor does, and
// returns the channel frames arrive on.
func subscribe(t *testing.T, reg *registry.Registry, wfID string) chan telemetry.StatusUpdate {
	t.Helper()
	ch := reg.SubscribeWorkflowStatus(wfID)
	t.Cleanup(func() { reg.UnsubscribeStatus(ch) })
	return ch
}

func nextFrame(ch chan telemetry.StatusUpdate) (telemetry.StatusUpdate, bool) {
	select {
	case u := <-ch:
		return u, true
	case <-time.After(200 * time.Millisecond):
		return telemetry.StatusUpdate{}, false
	}
}

// A worker's status for a workflow someone has open reaches the editor
// watching it, and the reply names the workflows the worker should keep
// sending.
func TestReportLiveStatusRelaysWatchedWorkflowToEditors(t *testing.T) {
	h, reg := newLiveStatusHandler(t,
		storage.Workflow{ID: "wf-open", WorkerID: "w-1"},
		storage.Workflow{ID: "wf-closed", WorkerID: "w-1"},
	)
	ch := subscribe(t, reg, "wf-open")

	w := postLiveStatus(t, h, workerCaller("w-1"), telemetry.LiveStatusReport{
		Running: []string{"wf-closed", "wf-open"},
		Updates: []telemetry.StatusUpdate{{
			WorkflowID: "wf-open",
			EdgeCounts: map[string]uint64{"e1": 42},
			SampledAt:  1000,
		}},
	})
	if w.Code != http.StatusOK {
		t.Fatalf("status %d, body %s", w.Code, w.Body.String())
	}

	frame, ok := nextFrame(ch)
	if !ok {
		t.Fatal("the editor watching wf-open got no frame")
	}
	if frame.EdgeCounts["e1"] != 42 || frame.SampledAt != 1000 {
		t.Errorf("relayed frame = %+v, want the worker's edge counts and sample time", frame)
	}

	var reply telemetry.LiveStatusReply
	if err := json.NewDecoder(w.Body).Decode(&reply); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(reply.Watched, []string{"wf-open"}) {
		t.Errorf("reply.Watched = %v, want [wf-open]", reply.Watched)
	}
}

// Only a worker may report, and only as itself: a signed-in user, or a worker
// using another worker's ID, could otherwise draw made-up traffic on anyone's
// canvas.
func TestReportLiveStatusRefusesCallersThatAreNotThatWorker(t *testing.T) {
	cases := []struct {
		name   string
		caller *storage.User
	}{
		{"no user", nil},
		{"signed-in administrator", &storage.User{ID: "u-1", Username: "admin", Role: storage.RoleAdministrator}},
		{"a different worker", workerCaller("w-2")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h, reg := newLiveStatusHandler(t, storage.Workflow{ID: "wf-open", WorkerID: "w-1"})
			ch := subscribe(t, reg, "wf-open")

			w := postLiveStatus(t, h, tc.caller, telemetry.LiveStatusReport{
				Running: []string{"wf-open"},
				Updates: []telemetry.StatusUpdate{{WorkflowID: "wf-open"}},
			})
			if w.Code != http.StatusForbidden {
				t.Errorf("status %d, want 403", w.Code)
			}
			if _, ok := nextFrame(ch); ok {
				t.Error("a refused report still reached the editor")
			}
		})
	}
}

// A worker cannot speak for a workflow pinned to another worker, one that does
// not exist, or one nobody has open.
func TestReportLiveStatusDropsUpdatesTheWorkerCannotSpeakFor(t *testing.T) {
	h, reg := newLiveStatusHandler(t,
		storage.Workflow{ID: "wf-pinned-elsewhere", WorkerID: "w-2"},
		storage.Workflow{ID: "wf-unwatched", WorkerID: "w-1"},
	)
	pinned := subscribe(t, reg, "wf-pinned-elsewhere")
	missing := subscribe(t, reg, "wf-missing")

	w := postLiveStatus(t, h, workerCaller("w-1"), telemetry.LiveStatusReport{
		Running: []string{"wf-unwatched"},
		Updates: []telemetry.StatusUpdate{
			{WorkflowID: "wf-pinned-elsewhere"},
			{WorkflowID: "wf-missing"},
			{WorkflowID: "wf-unwatched"},
		},
	})
	if w.Code != http.StatusOK {
		t.Fatalf("status %d, body %s", w.Code, w.Body.String())
	}
	if _, ok := nextFrame(pinned); ok {
		t.Error("a frame for a workflow pinned to another worker was relayed")
	}
	if _, ok := nextFrame(missing); ok {
		t.Error("a frame for a workflow that does not exist was relayed")
	}
}

// An unpinned workflow is placed by sharding, which the platform does not
// record, so any worker may report it.
func TestReportLiveStatusAcceptsUnpinnedWorkflow(t *testing.T) {
	h, reg := newLiveStatusHandler(t, storage.Workflow{ID: "wf-sharded"})
	ch := subscribe(t, reg, "wf-sharded")

	w := postLiveStatus(t, h, workerCaller("w-1"), telemetry.LiveStatusReport{
		Running: []string{"wf-sharded"},
		Updates: []telemetry.StatusUpdate{{WorkflowID: "wf-sharded", ProcessedCount: 7}},
	})
	if w.Code != http.StatusOK {
		t.Fatalf("status %d, body %s", w.Code, w.Body.String())
	}
	if frame, ok := nextFrame(ch); !ok || frame.ProcessedCount != 7 {
		t.Errorf("frame = %+v, relayed = %v; want the worker's frame", frame, ok)
	}
}

func TestReportLiveStatusRejectsMalformedBody(t *testing.T) {
	h, _ := newLiveStatusHandler(t)
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/workers/w-1/live-status", bytes.NewReader([]byte("{not json")))
	req.SetPathValue("id", "w-1")
	req = req.WithContext(context.WithValue(req.Context(), handlers.UserContextKey, workerCaller("w-1")))
	w := httptest.NewRecorder()
	h.ReportLiveStatus(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status %d, want 400", w.Code)
	}
}
