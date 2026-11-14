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
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gsoultan/hermod/internal/api/handlers"
	"github.com/gsoultan/hermod/internal/storage"
)

// vhostScopeStorage records every write a handler makes to a workflow, so a
// request that should have been refused cannot pass by being answered with an
// error from somewhere further in.
type vhostScopeStorage struct {
	*mockWorkspaceStorage
	writes []string
}

func (s *vhostScopeStorage) note(what, id string) { s.writes = append(s.writes, what+" "+id) }

func (s *vhostScopeStorage) CreateWorkflow(ctx context.Context, wf storage.Workflow) error {
	s.note("create in "+wf.VHost, wf.ID)
	return s.mockWorkspaceStorage.CreateWorkflow(ctx, wf)
}

func (s *vhostScopeStorage) UpdateWorkflow(ctx context.Context, wf storage.Workflow) error {
	s.note("update", wf.ID)
	return s.mockWorkspaceStorage.UpdateWorkflow(ctx, wf)
}

func (s *vhostScopeStorage) DeleteWorkflow(_ context.Context, id string) error {
	s.note("delete", id)
	return nil
}

func (s *vhostScopeStorage) UpdateWorkflowStatus(_ context.Context, id, _ string) error {
	s.note("status", id)
	return nil
}

func (s *vhostScopeStorage) UpdateWorkflowStats(_ context.Context, id string, _, _, _ uint64) error {
	s.note("stats", id)
	return nil
}

func vhostScopeMux() (*http.ServeMux, *vhostScopeStorage) {
	store := &vhostScopeStorage{mockWorkspaceStorage: newMockWorkspaceStorage()}
	store.workflows["wf-sales"] = storage.Workflow{ID: "wf-sales", Name: "Sales orders", VHost: "sales"}
	mux := http.NewServeMux()
	NewWorkflowHandler(&handlers.Handler{Storage: store}).RegisterWorkflowRoutes(mux)
	return mux, store
}

// vhostScopeCall sends one request as user. A handler that got past its checks
// reaches for a registry these tests do not have and panics; that is reported
// as the request having been let through, which it was.
func vhostScopeCall(t *testing.T, mux *http.ServeMux, user *storage.User, method, target, body string) (code int, reached string) {
	t.Helper()
	req := httptest.NewRequestWithContext(t.Context(), method, target, strings.NewReader(body))
	req = req.WithContext(context.WithValue(req.Context(), handlers.UserContextKey, user))
	rec := httptest.NewRecorder()
	defer func() {
		if p := recover(); p != nil {
			code, reached = 0, fmt.Sprintf("handler ran past its checks and panicked: %v", p)
		}
	}()
	mux.ServeHTTP(rec, req)
	return rec.Code, rec.Body.String()
}

// A vhost is the boundary between tenants: a user is given the vhosts they may
// work in. Reading one workflow checked that; nothing that changes, starts,
// stops, drains, rebuilds, rolls back or deletes one did, so an Editor of one
// vhost could delete every other vhost's workflows by id.
func TestWorkflowActionsAreHeldToTheCallersVHosts(t *testing.T) {
	const wf = `{"id":"wf-sales","name":"Sales orders","vhost":"sales","nodes":[],"edges":[]}`
	for _, tc := range []struct{ method, target, body string }{
		{http.MethodPut, "/api/workflows/wf-sales", wf},
		{http.MethodDelete, "/api/workflows/wf-sales", ""},
		{http.MethodPost, "/api/workflows/wf-sales/toggle", ""},
		{http.MethodPost, "/api/workflows/wf-sales/drain", ""},
		{http.MethodPost, "/api/workflows/wf-sales/rebuild", `{}`},
		{http.MethodPost, "/api/workflows/wf-sales/rollback/1", ""},
		{http.MethodPost, "/api/workflows/wf-sales/test", `{"message":{}}`},
		{http.MethodPost, "/api/workflows/wf-sales/nodes/n1/test", `{}`},
		{http.MethodGet, "/api/workflows/wf-sales", ""},
		{http.MethodGet, "/api/workflows/wf-sales/export", ""},
		{http.MethodGet, "/api/workflows/wf-sales/health", ""},
		{http.MethodGet, "/api/workflows/wf-sales/report", ""},
		{http.MethodGet, "/api/workflows/wf-sales/versions", ""},
		{http.MethodGet, "/api/workflows/wf-sales/versions/1", ""},
		{http.MethodGet, "/api/workflows/wf-sales/validate", ""},
		{http.MethodGet, "/api/workflows/wf-sales/transactions", ""},
		{http.MethodGet, "/api/workflows/wf-sales/callers", ""},
		{http.MethodGet, "/api/workflows/wf-sales/traces", ""},
		{http.MethodGet, "/api/workflows/wf-sales/traces/?message_id=m1", ""},
		{http.MethodPost, "/api/ai/analyze-workflow/wf-sales", `{}`},
		// Naming another vhost in the body is the same reach, by a different door.
		{http.MethodPost, "/api/workflows", `{"name":"Planted","vhost":"sales","nodes":[],"edges":[]}`},
		{http.MethodPost, "/api/workflows/batch/toggle", `{"ids":["wf-sales"],"active":true}`},
		{http.MethodPost, "/api/workflows/batch/delete", `{"ids":["wf-sales"]}`},
		{http.MethodPost, "/api/workflows/batch/workspace", `{"ids":["wf-sales"],"workspace_id":""}`},
	} {
		t.Run(tc.method+" "+tc.target, func(t *testing.T) {
			mux, store := vhostScopeMux()
			editorOfOps := &storage.User{ID: "u-ops", Role: storage.RoleEditor, VHosts: []string{"ops"}}

			code, body := vhostScopeCall(t, mux, editorOfOps, tc.method, tc.target, tc.body)
			if code != http.StatusForbidden {
				t.Errorf("an Editor of ops was answered %d on a workflow of sales, want 403: %.200s", code, body)
			}
			if len(store.writes) != 0 {
				t.Errorf("an Editor of ops wrote to a workflow of sales: %v", store.writes)
			}
		})
	}
}

// Moving a workflow is a write to the vhost it lands in.
func TestAWorkflowCannotBeMovedIntoAVHostTheCallerLacks(t *testing.T) {
	mux, store := vhostScopeMux()
	store.workflows["wf-ops"] = storage.Workflow{ID: "wf-ops", Name: "Ops alerts", VHost: "ops"}
	editorOfOps := &storage.User{ID: "u-ops", Role: storage.RoleEditor, VHosts: []string{"ops"}}

	code, body := vhostScopeCall(t, mux, editorOfOps, http.MethodPut, "/api/workflows/wf-ops",
		`{"id":"wf-ops","name":"Ops alerts","vhost":"sales","nodes":[],"edges":[]}`)
	if code != http.StatusForbidden {
		t.Errorf("moving a workflow from ops into sales was answered %d, want 403: %.200s", code, body)
	}
	if len(store.writes) != 0 {
		t.Errorf("the move was written: %v", store.writes)
	}
}

// The status and throughput of a workflow are reported by the worker running
// it. Both routes took any session, so a Viewer could mark any workflow failed
// or rewrite its counters.
func TestWorkflowStatusAndStatsAnswerWorkersAndAdministratorsOnly(t *testing.T) {
	for _, route := range []struct{ target, body string }{
		{"/api/workflows/wf-sales/status", `{"status":"Error"}`},
		{"/api/workflows/wf-sales/stats", `{"processed":0,"errors":999,"lag":0}`},
	} {
		for _, tc := range []struct {
			name string
			user *storage.User
			want int
		}{
			{"a Viewer of the workflow's vhost", &storage.User{ID: "u1", Role: storage.RoleViewer, VHosts: []string{"sales"}}, http.StatusForbidden},
			{"an Editor of the workflow's vhost", &storage.User{ID: "u2", Role: storage.RoleEditor, VHosts: []string{"sales"}}, http.StatusForbidden},
			{"a worker", &storage.User{ID: "worker:w1", Role: storage.RoleEditor, VHosts: []string{"*"}}, http.StatusOK},
			{"an Administrator", &storage.User{ID: "u3", Role: storage.RoleAdministrator}, http.StatusOK},
		} {
			mux, store := vhostScopeMux()
			code, body := vhostScopeCall(t, mux, tc.user, http.MethodPatch, route.target, route.body)
			if code != tc.want {
				t.Errorf("%s: %s answered %d, want %d: %.200s", route.target, tc.name, code, tc.want, body)
			}
			if wrote := len(store.writes) != 0; wrote != (tc.want == http.StatusOK) {
				t.Errorf("%s: %s: writes = %v", route.target, tc.name, store.writes)
			}
		}
	}
}
