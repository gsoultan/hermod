// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gsoultan/hermod/internal/storage"
)

// The dashboard's numbers are totals over a vhost, or over every vhost when
// none is named. Nothing checked the caller against either: an Editor of
// "sales" could ask for vhost=ops, or for all of them, and read how much the
// other tenants process, how far behind they are and how often they fail.
func TestDashboardVHostHoldsACallerToTheirOwnVHosts(t *testing.T) {
	admin := &storage.User{Role: storage.RoleAdministrator}
	sales := &storage.User{Role: storage.RoleEditor, VHosts: []string{"sales"}}
	two := &storage.User{Role: storage.RoleViewer, VHosts: []string{"sales", "ops"}}
	every := &storage.User{Role: storage.RoleEditor, VHosts: []string{"*"}}
	none := &storage.User{Role: storage.RoleViewer}

	for _, tc := range []struct {
		name      string
		user      *storage.User
		asked     string
		wantVHost string
		wantCode  int
	}{
		{"an Administrator asks for all", admin, "all", "all", http.StatusOK},
		{"an Administrator asks for one", admin, "ops", "ops", http.StatusOK},
		{"the server itself asks for all", nil, "", "", http.StatusOK},
		{"an Editor asks for their vhost", sales, "sales", "sales", http.StatusOK},
		{"an Editor asks for another vhost", sales, "ops", "", http.StatusForbidden},
		// "All" for someone with one vhost is that vhost: it is all they have.
		{"an Editor of one vhost asks for all", sales, "all", "sales", http.StatusOK},
		{"an Editor of one vhost names none", sales, "", "sales", http.StatusOK},
		// One total over two vhosts is not something the store can answer, and
		// the total over every vhost is not theirs to see.
		{"a Viewer of two vhosts asks for all", two, "all", "", http.StatusForbidden},
		{"a Viewer of two vhosts asks for one of them", two, "ops", "ops", http.StatusOK},
		{"an Editor given every vhost asks for all", every, "all", "all", http.StatusOK},
		// HasVHostAccess gives everyone the default vhost.
		{"a Viewer with no vhost asks for all", none, "all", "default", http.StatusOK},
		{"an Editor asks for the default vhost", sales, "default", "default", http.StatusOK},
	} {
		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/dashboard/stats?vhost="+tc.asked, nil)
		if tc.user != nil {
			req = req.WithContext(context.WithValue(req.Context(), UserContextKey, tc.user))
		}
		rec := httptest.NewRecorder()
		vhost, ok := (&Handler{}).DashboardVHost(rec, req)

		if wantOK := tc.wantCode == http.StatusOK; ok != wantOK {
			t.Errorf("%s: allowed = %v, want %v (%s)", tc.name, ok, wantOK, rec.Body)
			continue
		}
		if !ok && rec.Code != tc.wantCode {
			t.Errorf("%s: status %d, want %d", tc.name, rec.Code, tc.wantCode)
		}
		if ok && vhost != tc.wantVHost {
			t.Errorf("%s: served vhost %q, want %q", tc.name, vhost, tc.wantVHost)
		}
	}
}
