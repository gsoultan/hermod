// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

// Package http is the REST surface of self-update: what is running and what
// is available, checking now, installing a release, and choosing between
// automatic and manual updates. Every route is for administrators only, with
// no first-run exception: replacing the binary is not part of setup.
package http

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/gsoultan/hermod/internal/api/handlers"
	"github.com/gsoultan/hermod/internal/selfupdate"
	"github.com/gsoultan/hermod/internal/storage"
)

const maxBody = 4 << 10

// Handler serves the update routes.
type Handler struct {
	*handlers.Handler
	svc *selfupdate.Service
}

// NewHandler wraps the shared API handler. svc may be nil, for a server built
// without an updater; every route then answers 503.
func NewHandler(h *handlers.Handler, svc *selfupdate.Service) *Handler {
	return &Handler{Handler: h, svc: svc}
}

// RegisterRoutes mounts the update routes. AdminOnly is the project's route
// guard; guard inside it is what refuses the first-run bypass AdminOnly allows.
func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.Handle("GET /api/system/updates", h.AdminOnly(h.guard(h.getStatus)))
	mux.Handle("POST /api/system/updates/check", h.AdminOnly(h.guard(h.check)))
	mux.Handle("POST /api/system/updates/install", h.AdminOnly(h.guard(h.install)))
	mux.Handle("PUT /api/system/updates/settings", h.AdminOnly(h.guard(h.putSettings)))
}

// guard admits administrators only, and only when there is an updater.
func (h *Handler) guard(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, _ := r.Context().Value(handlers.UserContextKey).(*storage.User)
		if user == nil || user.Role != storage.RoleAdministrator {
			h.JsonError(w, "Forbidden: updates are managed by administrators", http.StatusForbidden)
			return
		}
		if h.svc == nil {
			h.JsonError(w, "Updates are not available in this process", http.StatusServiceUnavailable)
			return
		}
		next(w, r)
	}
}

func (h *Handler) getStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, h.svc.Status(r.Context()))
}

// check answers 200 with the status even when GitHub could not be reached;
// the status carries check_error, which is what the page shows.
func (h *Handler) check(w http.ResponseWriter, r *http.Request) {
	st, _ := h.svc.Check(r.Context())
	writeJSON(w, http.StatusOK, st)
}

func (h *Handler) install(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Version string `json:"version"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBody)).Decode(&req); err != nil {
		h.JsonError(w, "invalid request body", http.StatusBadRequest)
		return
	}
	req.Version = strings.TrimSpace(req.Version)
	if req.Version == "" {
		h.JsonError(w, "version is required", http.StatusBadRequest)
		return
	}
	err := h.svc.Install(r.Context(), req.Version)
	switch {
	case errors.Is(err, selfupdate.ErrBusy), errors.Is(err, selfupdate.ErrNotOffered), errors.Is(err, selfupdate.ErrCannotInstall):
		h.JsonError(w, err.Error(), http.StatusConflict)
		return
	case err != nil:
		h.JsonError(w, err.Error(), http.StatusBadGateway)
		return
	}
	st := h.svc.Status(r.Context())
	h.RecordAuditLog(r, "WARN", "Started installing Hermod "+req.Version, "UPDATE_INSTALL", "", "", "",
		map[string]string{"from": st.Current, "to": req.Version})
	writeJSON(w, http.StatusAccepted, st)
}

func (h *Handler) putSettings(w http.ResponseWriter, r *http.Request) {
	var in selfupdate.Settings
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBody)).Decode(&in); err != nil {
		h.JsonError(w, "invalid request body", http.StatusBadRequest)
		return
	}
	n, err := in.Normalize()
	if err != nil {
		h.JsonError(w, err.Error(), http.StatusBadRequest)
		return
	}
	saved, err := h.svc.SaveSettings(r.Context(), n)
	if err != nil {
		h.JsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	h.RecordAuditLog(r, "INFO", "Changed how updates are installed", "UPDATE_SETTINGS", "", "", "", saved)
	writeJSON(w, http.StatusOK, saved)
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}
