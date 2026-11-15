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
	"github.com/gsoultan/hermod/internal/engine/registry"
	"github.com/gsoultan/hermod/internal/storage"
)

// The live dashboard stream carries the same totals as the stats endpoint, so
// it is refused the same way, and before the upgrade: once the connection is
// a websocket there is no status left to answer with.
func TestDashboardStreamRefusesAnotherTenantsVHost(t *testing.T) {
	mux := http.NewServeMux()
	(&WSHandler{Handler: &handlers.Handler{Registry: &registry.Registry{}}}).RegisterWSRoutes(mux)

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/ws/dashboard?vhost=ops", nil)
	req = req.WithContext(context.WithValue(req.Context(), handlers.UserContextKey,
		&storage.User{Role: storage.RoleEditor, VHosts: []string{"sales"}}))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("an Editor of sales opening the ops dashboard stream: status %d, want 403: %s", rec.Code, rec.Body)
	}
}
