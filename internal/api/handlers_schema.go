package api

import (
	"encoding/json"
	"fmt"
	"github.com/user/hermod/internal/storage"
	"github.com/user/hermod/pkg/schema"
	"net/http"
)

func (s *Server) listSchemas(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("name")
	var schemas []storage.Schema
	var err error

	if name != "" {
		schemas, err = s.storage.ListSchemas(r.Context(), name)
	} else {
		schemas, err = s.storage.ListAllSchemas(r.Context())
	}

	if err != nil {
		s.jsonError(w, "Failed to list schemas: "+err.Error(), http.StatusInternalServerError)
		return
	}

	if schemas == nil {
		schemas = []storage.Schema{}
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(schemas)
}

func (s *Server) getSchemaHistory(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	schemas, err := s.storage.ListSchemas(r.Context(), name)
	if err != nil {
		s.jsonError(w, "Failed to get schema history: "+err.Error(), http.StatusInternalServerError)
		return
	}

	if schemas == nil {
		schemas = []storage.Schema{}
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(schemas)
}

func (s *Server) getLatestSchema(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	schema, err := s.storage.GetLatestSchema(r.Context(), name)
	if err != nil {
		s.jsonError(w, "Failed to get latest schema: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(schema)
}

func (s *Server) registerSchema(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name    string            `json:"name"`
		Type    schema.SchemaType `json:"type"`
		Content string            `json:"content"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.jsonError(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	reg := schema.NewStorageRegistry(s.storage)
	version, err := reg.Register(r.Context(), req.Name, req.Type, req.Content)
	if err != nil {
		s.jsonError(w, "Failed to register schema: "+err.Error(), http.StatusInternalServerError)
		return
	}

	s.recordAuditLog(r, "INFO", "Registered schema "+req.Name+" version "+fmt.Sprintf("%d", version), "REGISTER", "", "", "", req)

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"name":    req.Name,
		"version": version,
		"status":  "registered",
	})
}
