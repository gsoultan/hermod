package handlers

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/user/hermod/internal/storage"
	"github.com/user/hermod/pkg/infra/schema"
)

func (h *Handler) ListSchemas(w http.ResponseWriter, r *http.Request) {
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

func (h *Handler) GetSchemaHistory(w http.ResponseWriter, r *http.Request) {
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

func (h *Handler) GetLatestSchema(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	schema, err := h.Storage.GetLatestSchema(r.Context(), name)
	if err != nil {
		h.JsonError(w, "Failed to get latest schema: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(schema)
}

func (h *Handler) RegisterSchema(w http.ResponseWriter, r *http.Request) {
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
