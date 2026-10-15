// Copyright (c) 2026 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Hermod: real-time Postgres CDC and data pipelines in one Go binary.
// Each file is proprietary and confidential; see LICENSE for the terms.
// Resale, sublicensing or selling it to any third party is prohibited.
// Making copies, modifying or distributing it needs written permission
// Of the copyright holder, Gembit Soultan Shirazi.
// Distributed "AS IS", WITHOUT WARRANTY OF ANY KIND, express or implied.

package http

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/gsoultan/hermod/internal/storage"
	"github.com/gsoultan/hermod/pkg/infra/schema"
)

func (h *SchemaHandler) RegisterSchemaRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/schemas", h.ListSchemas)
	// A registered schema governs validation for every workflow that uses it.
	mux.Handle("POST /api/schemas", h.EditorOnly(h.RegisterSchema))
	mux.HandleFunc("GET /api/schemas/{name}", h.GetLatestSchema)
	mux.HandleFunc("GET /api/schemas/{name}/history", h.GetSchemaHistory)
}

func (h *SchemaHandler) ListSchemas(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("name")
	var schemas []storage.Schema
	var err error

	if name != "" {
		schemas, err = h.Storage.ListSchemas(r.Context(), name)
	} else {
		schemas, err = h.Storage.ListAllSchemas(r.Context())
	}

	if err != nil {
		h.JsonError(w, "Failed to list schemas: "+err.Error(), http.StatusInternalServerError)
		return
	}

	if schemas == nil {
		schemas = []storage.Schema{}
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(schemas)
}

func (h *SchemaHandler) GetSchemaHistory(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	schemas, err := h.Storage.ListSchemas(r.Context(), name)
	if err != nil {
		h.JsonError(w, "Failed to get schema history: "+err.Error(), http.StatusInternalServerError)
		return
	}

	if schemas == nil {
		schemas = []storage.Schema{}
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(schemas)
}

func (h *SchemaHandler) GetLatestSchema(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	schema, err := h.Storage.GetLatestSchema(r.Context(), name)
	if err != nil {
		h.JsonError(w, "Failed to get latest schema: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(schema)
}

func (h *SchemaHandler) RegisterSchema(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name    string            `json:"name"`
		Type    schema.SchemaType `json:"type"`
		Content string            `json:"content"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.JsonError(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	reg := schema.NewStorageRegistry(h.Storage)
	version, err := reg.Register(r.Context(), req.Name, req.Type, req.Content)
	if err != nil {
		h.JsonError(w, "Failed to register schema: "+err.Error(), http.StatusInternalServerError)
		return
	}

	h.RecordAuditLog(r, "INFO", "Registered schema "+req.Name+" version "+strconv.Itoa(version), "REGISTER", "", "", "", req)

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"name":    req.Name,
		"version": version,
		"status":  "registered",
	})
}
