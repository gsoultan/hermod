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

	"github.com/gsoultan/hermod/internal/api/handlers"
	"github.com/gsoultan/hermod/internal/storage"
)

// maxVHostRetentionBody bounds a save request: three small numbers.
const maxVHostRetentionBody = 1024

// vhostRetentionAccess names the vhost a retention request is about and
// refuses anyone who may not: a save needs an Administrator, a read an
// Administrator or an Editor who has the vhost.
//
// As with a vhost's secrets there is no check that the vhost exists: "default"
// has no row of its own, and a retention saved for a name nobody uses purges
// nothing.
func (h *AuthHandler) vhostRetentionAccess(w http.ResponseWriter, r *http.Request, admin bool) (string, bool) {
	user, _ := r.Context().Value(handlers.UserContextKey).(*storage.User)
	switch {
	case user == nil:
		h.JsonError(w, "Forbidden", http.StatusForbidden)
		return "", false
	case admin && user.Role != storage.RoleAdministrator:
		h.JsonError(w, "Forbidden: setting how long a vhost keeps its history needs the Administrator role", http.StatusForbidden)
		return "", false
	case user.Role != storage.RoleAdministrator && user.Role != storage.RoleEditor:
		h.JsonError(w, "Forbidden: reading a vhost's retention needs the Editor role", http.StatusForbidden)
		return "", false
	}
	vhost := r.PathValue("vhost")
	if vhost == "" || vhost == "all" {
		h.JsonError(w, "a retention belongs to one vhost: name it", http.StatusBadRequest)
		return "", false
	}
	if user.Role != storage.RoleAdministrator && !h.HasVHostAccess(vhost, user.VHosts) {
		h.JsonError(w, "Forbidden: you do not have access to this vhost", http.StatusForbidden)
		return "", false
	}
	return vhost, true
}

// GetVHostRetention returns how much history a vhost keeps. A vhost that has
// set nothing reads as zero in every field, which keeps everything.
func (h *AuthHandler) GetVHostRetention(w http.ResponseWriter, r *http.Request) {
	vhost, ok := h.vhostRetentionAccess(w, r, false)
	if !ok {
		return
	}
	retention, err := storage.LoadVHostRetention(r.Context(), h.Storage, vhost)
	if err != nil {
		h.JsonError(w, "Failed to read the vhost's retention", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(retention)
}

// PutVHostRetention sets how much history a vhost keeps. The hourly sweep
// applies it; nothing is deleted by the request itself.
func (h *AuthHandler) PutVHostRetention(w http.ResponseWriter, r *http.Request) {
	vhost, ok := h.vhostRetentionAccess(w, r, true)
	if !ok {
		return
	}
	var retention storage.VHostRetention
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxVHostRetentionBody))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&retention); err != nil {
		h.JsonError(w, `the request body must be JSON: {"audit_log_days": 365, "workflow_versions": 50, "approval_days": 180}; 0 keeps everything`, http.StatusBadRequest)
		return
	}
	if err := storage.SaveVHostRetention(r.Context(), h.Storage, vhost, retention); err != nil {
		if errors.Is(err, storage.ErrInvalidVHostRetention) {
			h.JsonError(w, err.Error(), http.StatusBadRequest)
			return
		}
		h.JsonError(w, "Failed to save the vhost's retention", http.StatusInternalServerError)
		return
	}
	h.RecordAuditLog(r, "INFO", "Set how long vhost "+vhost+" keeps its history", "update", vhost, "vhost", "", retention)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(retention)
}
