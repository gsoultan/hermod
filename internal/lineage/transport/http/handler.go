// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Granted to no one is any right to use, copy, modify or distribute this
// Except by written permission of the copyright holder named above.
// May not be sold, resold, sublicensed or offered as a paid or hosted
// Business service to any third party. Proprietary and confidential;
// Its full terms are in LICENSE at the repository root. THE SOFTWARE
// IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR IMPLIED.

// Package http is the REST surface of data lineage: a vhost's lineage graph,
// and everything downstream of one of its assets or workflows.
package http

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/gsoultan/hermod/internal/api/handlers"
	"github.com/gsoultan/hermod/internal/lineage"
	"github.com/gsoultan/hermod/internal/lineage/endpoint"
	"github.com/gsoultan/hermod/internal/lineage/repository"
	"github.com/gsoultan/hermod/internal/lineage/service"
	"github.com/gsoultan/hermod/internal/storage"
)

// Handler serves the lineage routes.
type Handler struct {
	*handlers.Handler
}

// NewHandler wraps the shared API handler.
func NewHandler(h *handlers.Handler) *Handler {
	return &Handler{Handler: h}
}

// RegisterRoutes mounts the lineage routes. Both are reads, open to every
// role; scope() checks the vhost in the path, which a role wrapper cannot.
func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/vhosts/{vhost}/lineage", h.GetGraph)
	mux.HandleFunc("GET /api/vhosts/{vhost}/lineage/impact", h.GetImpact)
}

// Service is the lineage service over a store. It is exported for the other
// handlers that answer with lineage (the schema-change analysis).
func Service(store storage.Storage) *service.Service {
	return service.New(repository.New(store))
}

// Scope is the caller's lineage scope for vhost: an administrator sees every
// vhost, anyone else the vhosts their session names, by the same rule the
// workflow list applies (handlers.HasVHostAccess). ok is false when there is
// no user on the request.
func Scope(h *handlers.Handler, r *http.Request, vhost string) (service.Scope, bool) {
	user, _ := r.Context().Value(handlers.UserContextKey).(*storage.User)
	if user == nil {
		return service.Scope{}, false
	}
	if user.Role == storage.RoleAdministrator {
		return service.Scope{VHost: vhost, Allows: func(string) bool { return true }}, true
	}
	allowed := user.VHosts
	return service.Scope{VHost: vhost, Allows: func(v string) bool { return h.HasVHostAccess(v, allowed) }}, true
}

// endpoints is the lineage endpoints over the current store, or false before
// setup has opened one.
func (h *Handler) endpoints(w http.ResponseWriter) (endpoint.Endpoints, bool) {
	if h.Storage == nil {
		h.JsonError(w, "Lineage is not available until setup has opened the metadata database", http.StatusServiceUnavailable)
		return endpoint.Endpoints{}, false
	}
	return endpoint.New(Service(h.Storage)), true
}

func (h *Handler) scope(w http.ResponseWriter, r *http.Request) (service.Scope, bool) {
	sc, ok := Scope(h.Handler, r, r.PathValue("vhost"))
	if !ok {
		h.JsonError(w, "Unauthorized", http.StatusUnauthorized)
	}
	return sc, ok
}

// fail answers an endpoint error. A refusal and an unknown node say nothing
// about what the vhost holds.
func (h *Handler) fail(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, service.ErrForbidden):
		h.JsonError(w, "Forbidden: you do not have access to this vhost", http.StatusForbidden)
	case errors.Is(err, lineage.ErrUnknownNode):
		h.JsonError(w, "No such asset or workflow in this vhost", http.StatusNotFound)
	case errors.Is(err, endpoint.ErrInvalid):
		h.JsonError(w, "Name the asset or workflow with ?node=<id> (at most 2048 characters)", http.StatusBadRequest)
	default:
		slog.ErrorContext(r.Context(), "lineage request failed", "path", r.URL.Path, "error", err)
		h.JsonError(w, "Failed to build the lineage", http.StatusInternalServerError)
	}
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

// GetGraph returns the vhost's lineage graph; "all" is every vhost the
// caller may see.
func (h *Handler) GetGraph(w http.ResponseWriter, r *http.Request) {
	sc, ok := h.scope(w, r)
	if !ok {
		return
	}
	eps, ok := h.endpoints(w)
	if !ok {
		return
	}
	res, err := eps.Graph(r.Context(), endpoint.GraphRequest{Scope: sc})
	if err != nil {
		h.fail(w, r, err)
		return
	}
	writeJSON(w, res)
}

// GetImpact returns everything downstream of the node named by ?node=.
func (h *Handler) GetImpact(w http.ResponseWriter, r *http.Request) {
	sc, ok := h.scope(w, r)
	if !ok {
		return
	}
	eps, ok := h.endpoints(w)
	if !ok {
		return
	}
	res, err := eps.Impact(r.Context(), endpoint.ImpactRequest{Scope: sc, Node: r.URL.Query().Get("node")})
	if err != nil {
		h.fail(w, r, err)
		return
	}
	writeJSON(w, res)
}
