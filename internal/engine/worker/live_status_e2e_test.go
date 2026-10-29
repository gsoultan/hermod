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
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gsoultan/hermod/internal/api/handlers"
	"github.com/gsoultan/hermod/internal/engine/registry"
	"github.com/gsoultan/hermod/internal/storage"
	"github.com/gsoultan/hermod/internal/testutil"
	workerhttp "github.com/gsoultan/hermod/internal/worker/transport/http"
	"github.com/gsoultan/hermod/pkg/engine/telemetry"
)

// platformWorkflows is the platform's database as the live-status route reads it.
type platformWorkflows struct {
	testutil.BaseMockStorage
}

func (*platformWorkflows) GetWorkflow(_ context.Context, id string) (storage.Workflow, error) {
	return storage.Workflow{ID: id, WorkerID: "w-1"}, nil
}

// An editor open on the platform sees the edge counts of a workflow running on a
// remote worker: through the worker's API client, the platform's real route and
// JSON on the wire, into the platform's status feed.
func TestRemoteWorkflowEdgeCountsReachThePlatformEditor(t *testing.T) {
	platform := registry.NewRegistry(nil)
	h := workerhttp.NewWorkerHandler(&handlers.Handler{Storage: &platformWorkflows{}, Registry: platform})
	mux := http.NewServeMux()
	h.RegisterWorkerRoutes(mux)
	// Stands in for the auth middleware, which turns the worker's token into this user.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Worker-Token") != "tok" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		user := &storage.User{ID: "worker:w-1", Role: storage.RoleEditor, VHosts: []string{"*"}}
		mux.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), handlers.UserContextKey, user)))
	}))
	defer srv.Close()

	editor := platform.SubscribeWorkflowStatus("wf-a")
	defer platform.UnsubscribeStatus(editor)

	src := &fakeLiveSource{
		running:  []string{"wf-a"},
		statuses: map[string]telemetry.StatusUpdate{"wf-a": {EdgeCounts: map[string]uint64{"e1": 9}, SampledAt: 1234}},
	}
	relay := &liveStatusRelay{workerID: "w-1", source: src, reporter: NewWorkerAPIClient(srv.URL, "tok")}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	go relay.run(ctx)

	select {
	case frame := <-editor:
		if frame.WorkflowID != "wf-a" || frame.EdgeCounts["e1"] != 9 || frame.SampledAt != 1234 {
			t.Fatalf("editor got %+v, want wf-a's edge counts from the worker", frame)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the editor got no frame from the remote worker")
	}
}
