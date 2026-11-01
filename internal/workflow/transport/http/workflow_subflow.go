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
	"encoding/json"
	"errors"
	"net/http"

	"github.com/gsoultan/hermod/internal/engine/registry/subflow"
	"github.com/gsoultan/hermod/internal/storage"
)

// ListSubflowCallers answers which steps, in the sub-flow's own vhost, run the
// sub-flow, and whether each pins a version or runs the latest save.
func (h *WorkflowHandler) ListSubflowCallers(w http.ResponseWriter, r *http.Request) {
	sub, err := h.Storage.GetWorkflow(r.Context(), r.PathValue("id"))
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			h.JsonError(w, "Workflow not found", http.StatusNotFound)
		} else {
			h.JsonError(w, "Failed to get workflow: "+err.Error(), http.StatusInternalServerError)
		}
		return
	}
	role, vhosts := h.GetRoleAndVHosts(r)
	if role != "" && role != storage.RoleAdministrator && !h.HasVHostAccess(sub.VHost, vhosts) {
		h.JsonError(w, "Forbidden", http.StatusForbidden)
		return
	}
	callers := []subflow.Caller{}
	if h.Registry != nil {
		found, err := h.Registry.SubflowCallers(r.Context(), sub)
		if err != nil {
			h.JsonError(w, "Failed to list callers: "+err.Error(), http.StatusInternalServerError)
			return
		}
		callers = append(callers, found...)
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"data": callers})
}
