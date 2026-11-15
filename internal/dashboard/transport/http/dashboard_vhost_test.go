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

// The stats and the history are both totals over the vhost in the query, and both are held to the caller's vhosts.
func TestDashboardRoutesRefuseAnotherTenantsVHost(t *testing.T) {
	mux := http.NewServeMux()
	(&DashboardHandler{Handler: &handlers.Handler{Registry: &registry.Registry{}}}).RegisterDashboardRoutes(mux)

	for _, target := range []string{"/api/dashboard/stats", "/api/dashboard/history"} {
		for vhost, want := range map[string]int{"ops": http.StatusForbidden, "sales": http.StatusOK} {
			req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, target+"?vhost="+vhost, nil)
			req = req.WithContext(context.WithValue(req.Context(), handlers.UserContextKey,
				&storage.User{Role: storage.RoleEditor, VHosts: []string{"sales"}}))
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, req)
			if rec.Code != want {
				t.Errorf("GET %s?vhost=%s as an Editor of sales: status %d, want %d: %s", target, vhost, rec.Code, want, rec.Body)
			}
		}
	}
}
