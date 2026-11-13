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
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gsoultan/hermod/internal/api/handlers"
	"github.com/gsoultan/hermod/internal/storage"
)

type streamScopeStorage struct{ storage.Storage }

func (streamScopeStorage) GetWorkflow(_ context.Context, id string) (storage.Workflow, error) {
	if id == "wf-sales" {
		return storage.Workflow{ID: id, VHost: "sales"}, nil
	}
	return storage.Workflow{}, storage.ErrNotFound
}

// A workflow's streams carry what it is doing: its log lines, its status, the
// records passing through it and the messages its debugger holds. Each took a
// workflow id and served it to any session, so a user given one vhost could
// watch another vhost's records go by.
func TestAWorkflowsStreamsAreHeldToTheCallersVHosts(t *testing.T) {
	mux := http.NewServeMux()
	NewWSHandler(&handlers.Handler{Storage: streamScopeStorage{}}).RegisterWSRoutes(mux)
	ops := &storage.User{ID: "u-ops", Role: storage.RoleEditor, VHosts: []string{"ops"}}

	for _, target := range []string{
		"/api/ws/logs?workflow_id=wf-sales",
		"/api/ws/live?workflow_id=wf-sales",
		"/api/ws/status?workflow_id=wf-sales",
		"/api/ws/debugger?workflow_id=wf-sales",
		"/api/ws/debugger?workflow_id=wf-sales&mode=breakpoints",
		"/api/ws/out/wf-sales",
	} {
		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, target, nil)
		req = req.WithContext(context.WithValue(req.Context(), handlers.UserContextKey, ops))
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Errorf("an Editor of ops opening %s was answered %d, want 403", target, rec.Code)
		}
	}
}
