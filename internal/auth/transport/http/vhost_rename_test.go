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

type vhostRenameStore struct {
	storage.Storage
	vhosts  map[string]storage.VHost
	updates int
}

func (s *vhostRenameStore) GetVHost(_ context.Context, id string) (storage.VHost, error) {
	v, ok := s.vhosts[id]
	if !ok {
		return storage.VHost{}, storage.ErrNotFound
	}
	return v, nil
}

func (s *vhostRenameStore) UpdateVHost(_ context.Context, v storage.VHost) error {
	s.updates++
	if _, ok := s.vhosts[v.ID]; !ok {
		return storage.ErrNotFound
	}
	for id, other := range s.vhosts {
		if id != v.ID && other.Name == v.Name {
			return fmt.Errorf("renaming: %w", storage.ErrVHostNameTaken)
		}
	}
	s.vhosts[v.ID] = v
	return nil
}

func (s *vhostRenameStore) CreateAuditLog(context.Context, storage.AuditLog) error { return nil }
func (s *vhostRenameStore) CreateLog(context.Context, storage.Log) error           { return nil }

func putVHost(ctx context.Context, h *AuthHandler, id, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequestWithContext(ctx, http.MethodPut, "/api/vhosts/"+id, strings.NewReader(body))
	r.SetPathValue("id", id)
	admin := &storage.User{ID: "root", Username: "root", Role: storage.RoleAdministrator}
	r = r.WithContext(context.WithValue(r.Context(), handlers.UserContextKey, admin))
	w := httptest.NewRecorder()
	h.UpdateVHost(w, r)
	return w
}

func TestUpdateVHostAnswersForEachWayARenameCanFail(t *testing.T) {
	cases := []struct {
		name    string
		id      string
		body    string
		status  int
		updated bool
	}{
		{"renamed", "vh-1", `{"name":"revenue"}`, http.StatusOK, true},
		{"onto a taken name", "vh-1", `{"name":"ops"}`, http.StatusConflict, true},
		{"missing vhost", "nope", `{"name":"x"}`, http.StatusNotFound, false},
		{"empty name", "vh-1", `{"name":"  "}`, http.StatusBadRequest, false},
		// "all" is what a filter sends to mean every vhost.
		{"reserved name", "vh-1", `{"name":"all"}`, http.StatusBadRequest, false},
		// A vhost saved under such a name before this check existed can
		// still have its description edited after an upgrade.
		{"existing reserved name kept", "vh-3", `{"name":"all","description":"legacy"}`, http.StatusOK, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := &vhostRenameStore{vhosts: map[string]storage.VHost{
				"vh-1": {ID: "vh-1", Name: "sales"},
				"vh-2": {ID: "vh-2", Name: "ops"},
				"vh-3": {ID: "vh-3", Name: "all"},
			}}
			h := &AuthHandler{Handler: &handlers.Handler{Storage: store, LogStorage: store}}
			w := putVHost(t.Context(), h, tc.id, tc.body)
			if w.Code != tc.status {
				t.Errorf("status = %d, want %d; body %s", w.Code, tc.status, w.Body)
			}
			if (store.updates > 0) != tc.updated {
				t.Errorf("storage was asked to update %d times, want updated=%v", store.updates, tc.updated)
			}
		})
	}
}
