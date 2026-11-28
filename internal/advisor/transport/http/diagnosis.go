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
	"encoding/json"
	"errors"
	"net/http"

	"github.com/gsoultan/hermod/internal/advisor/dbdiag"
	"github.com/gsoultan/hermod/internal/api/handlers"
	"github.com/gsoultan/hermod/internal/storage"
)

// DiagnosisHandler serves a source database's diagnosis.
type DiagnosisHandler struct {
	*handlers.Handler

	// run is set by tests; nil diagnoses through the registry.
	run func(ctx context.Context, src storage.Source) (dbdiag.Report, error)
}

// NewDiagnosisHandler wraps the shared handler.
func NewDiagnosisHandler(h *handlers.Handler) *DiagnosisHandler {
	return &DiagnosisHandler{Handler: h}
}

// RegisterDiagnosisRoutes registers the endpoint. A diagnosis connects to the
// source's database and reads its activity, so it takes an editor.
func (h *DiagnosisHandler) RegisterDiagnosisRoutes(mux *http.ServeMux) {
	mux.Handle("POST /api/sources/{id}/diagnosis", h.EditorOnly(h.Diagnose))
}

// Diagnose answers POST /api/sources/{id}/diagnosis: how Hermod reaches the
// database, who holds its connections, what they wait on, how close it is to
// its limits and what PgBouncer says. Nothing is changed on the database.
func (h *DiagnosisHandler) Diagnose(w http.ResponseWriter, r *http.Request) {
	src, err := h.Storage.GetSource(r.Context(), r.PathValue("id"))
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			h.JsonError(w, "Source not found", http.StatusNotFound)
		} else {
			h.JsonError(w, "Failed to load source", http.StatusInternalServerError)
		}
		return
	}
	role, vhosts := h.GetRoleAndVHosts(r)
	if role != storage.RoleAdministrator && !h.HasVHostAccess(src.VHost, vhosts) {
		h.JsonError(w, "Forbidden: you do not have access to this source's vhost", http.StatusForbidden)
		return
	}
	report, err := h.diagnose(r.Context(), src)
	if err != nil {
		// The database is the one not answering; the error names what
		// failed, never the credentials (see dbdiag.Run).
		h.JsonError(w, err.Error(), http.StatusBadGateway)
		return
	}
	h.RecordAuditLog(r, "INFO", "Diagnosed a source database", "SOURCE_DIAGNOSIS", "", src.ID, "", nil)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(report)
}

func (h *DiagnosisHandler) diagnose(ctx context.Context, src storage.Source) (dbdiag.Report, error) {
	if h.run != nil {
		return h.run(ctx, src)
	}
	if h.Registry == nil {
		return dbdiag.Report{}, errors.New("the engine registry is not available")
	}
	connStr, driver, err := h.Registry.SourceConnString(ctx, src)
	if err != nil {
		return dbdiag.Report{}, err
	}
	if driver != "pgx" {
		return dbdiag.Report{}, dbdiag.ErrUnsupported
	}
	db, err := h.Registry.GetOrOpenDB(src)
	if err != nil {
		return dbdiag.Report{}, err
	}
	return dbdiag.Run(ctx, db, connStr)
}
