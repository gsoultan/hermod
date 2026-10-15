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
