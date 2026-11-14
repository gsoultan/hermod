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
	"strings"
	"testing"

	"github.com/gsoultan/hermod/internal/api/handlers"
	"github.com/gsoultan/hermod/internal/storage"
)

type agentLogStorage struct {
	storage.Storage
	written int
}

func (s *agentLogStorage) CreateLog(context.Context, storage.Log) error { s.written++; return nil }
func (s *agentLogStorage) CreateLogs(_ context.Context, logs []storage.Log) error {
	s.written += len(logs)
	return nil
}

// Workers ship their logs to these two routes. Any session could too, so a
// Viewer -- the read-only role -- could write whatever they liked into the log
// an operator reads to find out what happened, under any workflow's id.
func TestOnlyWorkersAndAdministratorsMayWriteLogs(t *testing.T) {
	for _, route := range []struct{ target, body string }{
		{"/api/logs", `{"level":"ERROR","message":"planted","workflow_id":"wf-1"}`},
		{"/api/logs/batch", `[{"level":"ERROR","message":"planted","workflow_id":"wf-1"}]`},
	} {
		for _, tc := range []struct {
			name string
			user *storage.User
			ok   bool
		}{
			{"a Viewer", &storage.User{ID: "u1", Role: storage.RoleViewer}, false},
			{"an Editor", &storage.User{ID: "u2", Role: storage.RoleEditor}, false},
			{"a worker", &storage.User{ID: "worker:w1", Role: storage.RoleEditor, VHosts: []string{"*"}}, true},
			{"an Administrator", &storage.User{ID: "u3", Role: storage.RoleAdministrator}, true},
		} {
			store := &agentLogStorage{}
			mux := http.NewServeMux()
			NewLogHandler(&handlers.Handler{Storage: store, LogStorage: store}).RegisterLogRoutes(mux)

			req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, route.target, strings.NewReader(route.body))
			req = req.WithContext(context.WithValue(req.Context(), handlers.UserContextKey, tc.user))
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, req)

			if tc.ok && (rec.Code >= 300 || store.written == 0) {
				t.Errorf("%s: %s was answered %d and wrote %d logs, want it accepted: %s",
					route.target, tc.name, rec.Code, store.written, rec.Body.String())
			}
			if !tc.ok && (rec.Code != http.StatusForbidden || store.written != 0) {
				t.Errorf("%s: %s was answered %d and wrote %d logs, want 403 and none",
					route.target, tc.name, rec.Code, store.written)
			}
		}
	}
}
