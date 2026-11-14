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

type heartbeatStorage struct {
	storage.Storage
	beats []string
}

func (s *heartbeatStorage) UpdateWorkerHeartbeat(_ context.Context, id string, _ storage.WorkerResources) error {
	s.beats = append(s.beats, id)
	return nil
}

// A heartbeat is how a worker says it is alive and how loaded it is, and
// workflows are placed on what it says. Any session could send one for any
// worker: a Viewer could keep a dead worker looking alive, or report a healthy
// one as saturated.
func TestOnlyTheWorkerItselfOrAnAdministratorMaySendAHeartbeat(t *testing.T) {
	for _, tc := range []struct {
		name string
		user *storage.User
		ok   bool
	}{
		{"a Viewer", &storage.User{ID: "u1", Role: storage.RoleViewer}, false},
		{"an Editor", &storage.User{ID: "u2", Role: storage.RoleEditor}, false},
		{"another worker", &storage.User{ID: "worker:w2", Role: storage.RoleEditor, VHosts: []string{"*"}}, false},
		{"the worker itself", &storage.User{ID: "worker:w1", Role: storage.RoleEditor, VHosts: []string{"*"}}, true},
		{"the master key", &storage.User{ID: masterWorkerUserID, Role: storage.RoleAdministrator}, true},
		{"an Administrator", &storage.User{ID: "u3", Role: storage.RoleAdministrator}, true},
	} {
		store := &heartbeatStorage{}
		mux := http.NewServeMux()
		NewWorkerHandler(&handlers.Handler{Storage: store}).RegisterWorkerRoutes(mux)

		req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/workers/w1/heartbeat", strings.NewReader(`{"cpu_usage":0.99}`))
		req = req.WithContext(context.WithValue(req.Context(), handlers.UserContextKey, tc.user))
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

		if tc.ok && (rec.Code != http.StatusNoContent || len(store.beats) != 1) {
			t.Errorf("%s was answered %d (beats %v), want 204 and one heartbeat", tc.name, rec.Code, store.beats)
		}
		if !tc.ok && (rec.Code != http.StatusForbidden || len(store.beats) != 0) {
			t.Errorf("%s was answered %d (beats %v), want 403 and none", tc.name, rec.Code, store.beats)
		}
	}
}
