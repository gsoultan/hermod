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

	"github.com/gsoultan/hermod/internal/storage"
)

// GetWorkflowTransactions lists the workflow's transactions with what their
// runs did: commits, rollbacks, runs left in doubt, events refused for a
// missing row, each member's share, the latest runs, and what the transaction
// log holds now. It reads the state store, so it answers for a stopped
// workflow, and on any server, as it does for a running one.
func (h *WorkflowHandler) GetWorkflowTransactions(w http.ResponseWriter, r *http.Request) {
	wf, err := h.Storage.GetWorkflow(r.Context(), r.PathValue("id"))
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			h.JsonError(w, "Workflow not found", http.StatusNotFound)
		} else {
			h.JsonError(w, "Failed to get workflow: "+err.Error(), http.StatusInternalServerError)
		}
		return
	}

	role, vhosts := h.GetRoleAndVHosts(r)
	if role != "" && role != storage.RoleAdministrator && !h.HasVHostAccess(wf.VHost, vhosts) {
		h.JsonError(w, "Forbidden", http.StatusForbidden)
		return
	}
	if h.Registry == nil {
		h.JsonError(w, "the engine registry is not available", http.StatusServiceUnavailable)
		return
	}

	txs, err := h.Registry.WorkflowTransactions(r.Context(), wf)
	if err != nil {
		h.JsonError(w, "Failed to read the workflow's transactions: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"transactions": txs})
}
