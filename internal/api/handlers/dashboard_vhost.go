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
	"net/http"
	"slices"
	"strings"

	"github.com/gsoultan/hermod/internal/storage"
)

// DashboardVHost is the vhost the dashboard is served for: the one the request
// names, held to what its caller may see. It writes a 403 and reports false
// when the caller may not see it.
//
// The dashboard's numbers are totals over one vhost, or over every vhost when
// none is named ("" and "all" both mean that). Nothing checked the caller
// against either, so an Editor of one vhost could read how much the other
// tenants process, how far behind they are and how often they fail.
//
// An Administrator, a caller given every vhost ("*") and a request the server
// made itself are served what they asked for. Anyone else:
//
//   - naming a vhost, is served it if it is one of theirs;
//   - naming none, is served their own vhost when they have exactly one. That
//     is all they have, and the UI asks for "all" until someone picks;
//   - naming none with several vhosts, is refused and told to pick one. The
//     store answers for one vhost or for all of them, and the total over all
//     of them is not theirs to see.
func (h *Handler) DashboardVHost(w http.ResponseWriter, r *http.Request) (string, bool) {
	asked := r.URL.Query().Get("vhost")
	role, vhosts := h.GetRoleAndVHosts(r)
	if role == "" || role == storage.RoleAdministrator || slices.Contains(vhosts, "*") {
		return asked, true
	}
	if storage.NormalizeVHost(asked) != "" {
		if h.HasVHostAccess(asked, vhosts) {
			return asked, true
		}
		h.JsonError(w, "Forbidden: no access to vhost "+asked, http.StatusForbidden)
		return "", false
	}
	switch len(vhosts) {
	case 0:
		// HasVHostAccess gives everyone the default vhost.
		return "default", true
	case 1:
		return vhosts[0], true
	}
	h.JsonError(w, "Forbidden: choose a virtual host. You have access to "+strings.Join(vhosts, ", ")+
		", and the dashboard shows one at a time.", http.StatusForbidden)
	return "", false
}
