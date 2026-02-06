package api

import (
	"encoding/json"
	"net/http"

	"github.com/google/uuid"
	"github.com/user/hermod/internal/engine"
	"github.com/user/hermod/internal/storage"
)

func (s *Server) registerSinkRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/sinks", s.listSinks)
	mux.HandleFunc("GET /api/sinks/{id}", s.getSink)
	mux.Handle("POST /api/sinks", s.editorOnly(s.createSink))
	mux.Handle("PUT /api/sinks/{id}", s.editorOnly(s.updateSink))
	mux.Handle("POST /api/sinks/test", s.editorOnly(s.testSink))
	mux.Handle("POST /api/sinks/discover/databases", s.editorOnly(s.discoverSinkDatabases))
	mux.Handle("POST /api/sinks/discover/tables", s.editorOnly(s.discoverSinkTables))
	mux.Handle("POST /api/sinks/sample", s.editorOnly(s.sampleSinkTable))
	mux.Handle("POST /api/sinks/browse", s.editorOnly(s.browseSinkTable))
	mux.Handle("POST /api/sinks/query", s.editorOnly(s.querySink))
	mux.Handle("POST /api/sinks/smtp/preview", s.editorOnly(s.previewSmtpTemplate))
	mux.Handle("POST /api/sinks/smtp/validate", s.editorOnly(s.validateEmail))
	mux.Handle("DELETE /api/sinks/{id}", s.editorOnly(s.deleteSink))
}

func (s *Server) listSinks(w http.ResponseWriter, r *http.Request) {
	filter := s.parseCommonFilter(r)
	role, vhosts := s.getRoleAndVHosts(r)

	if filter.VHost != "" && role != storage.RoleAdministrator {
		if !s.hasVHostAccess(filter.VHost, vhosts) {
			s.jsonError(w, "Forbidden", http.StatusForbidden)
			return
		}
	}

	sinks, total, err := s.storage.ListSinks(r.Context(), filter)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if role != "" && role != storage.RoleAdministrator {
		filtered := []storage.Sink{}
		for _, snk := range sinks {
			if s.hasVHostAccess(snk.VHost, vhosts) {
				filtered = append(filtered, snk)
			}
		}
		sinks = filtered
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"data":  sinks,
		"total": total,
	})
}

func (s *Server) getSink(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	snk, err := s.storage.GetSink(r.Context(), id)
	if err != nil {
		if err == storage.ErrNotFound {
			s.jsonError(w, "Sink not found", http.StatusNotFound)
		} else {
			s.jsonError(w, "Failed to retrieve sink: "+err.Error(), http.StatusInternalServerError)
		}
		return
	}

	role, vhosts := s.getRoleAndVHosts(r)
	if role != "" && role != storage.RoleAdministrator {
		if !s.hasVHostAccess(snk.VHost, vhosts) {
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(snk)
}

func (s *Server) createSink(w http.ResponseWriter, r *http.Request) {
	var snk storage.Sink
	if err := json.NewDecoder(r.Body).Decode(&snk); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	if snk.Name == "" || snk.Type == "" || snk.VHost == "" {
		http.Error(w, "Name, Type, and VHost are mandatory", http.StatusBadRequest)
		return
	}

	role, vhosts := s.getRoleAndVHosts(r)
	if role != storage.RoleAdministrator {
		if !s.hasVHostAccess(snk.VHost, vhosts) {
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}
	}

	snk.ID = uuid.New().String()
	snk.Active = true
	if err := s.storage.CreateSink(r.Context(), snk); err != nil {
		s.jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	s.recordAuditLog(r, "INFO", "Created sink "+snk.Name, "create", "", "", snk.ID, snk)

	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(snk)
}

func (s *Server) updateSink(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var snk storage.Sink
	if err := json.NewDecoder(r.Body).Decode(&snk); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	snk.ID = id

	role, vhosts := s.getRoleAndVHosts(r)
	if role != storage.RoleAdministrator {
		if !s.hasVHostAccess(snk.VHost, vhosts) {
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}
	}

	if err := s.storage.UpdateSink(r.Context(), snk); err != nil {
		s.jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	s.recordAuditLog(r, "INFO", "Updated sink "+snk.Name, "update", "", "", snk.ID, snk)

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(snk)
}

func (s *Server) deleteSink(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	ctx := r.Context()

	snk, err := s.storage.GetSink(ctx, id)
	if err != nil {
		if err == storage.ErrNotFound {
			s.jsonError(w, "Sink not found", http.StatusNotFound)
		} else {
			s.jsonError(w, "Failed to get sink: "+err.Error(), http.StatusInternalServerError)
		}
		return
	}

	// RBAC check
	role, vhosts := s.getRoleAndVHosts(r)
	if role != storage.RoleAdministrator {
		if !s.hasVHostAccess(snk.VHost, vhosts) {
			s.jsonError(w, "Forbidden", http.StatusForbidden)
			return
		}
	}

	wfs, _, err := s.storage.ListWorkflows(ctx, storage.CommonFilter{})
	if err == nil {
		for _, wf := range wfs {
			for _, node := range wf.Nodes {
				if node.Type == "sink" && node.RefID == id {
					s.jsonError(w, "Cannot delete sink: it is used by workflow "+wf.Name, http.StatusConflict)
					return
				}
			}
		}
	}

	if err := s.storage.DeleteSink(ctx, id); err != nil {
		s.jsonError(w, "Failed to delete sink", http.StatusInternalServerError)
		return
	}

	s.recordAuditLog(r, "INFO", "Deleted sink "+snk.Name, "delete", "", "", id, nil)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) testSink(w http.ResponseWriter, r *http.Request) {
	var snk storage.Sink
	if err := json.NewDecoder(r.Body).Decode(&snk); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	cfg := engine.SinkConfig{Type: snk.Type, Config: snk.Config}
	if err := s.registry.TestSink(r.Context(), cfg); err != nil {
		s.jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

func (s *Server) discoverSinkDatabases(w http.ResponseWriter, r *http.Request) {
	var snk storage.Sink
	if err := json.NewDecoder(r.Body).Decode(&snk); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	cfg := engine.SinkConfig{Type: snk.Type, Config: snk.Config}
	dbs, err := s.registry.DiscoverSinkDatabases(r.Context(), cfg)
	if err != nil {
		s.jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(dbs)
}

func (s *Server) discoverSinkTables(w http.ResponseWriter, r *http.Request) {
	var snk storage.Sink
	if err := json.NewDecoder(r.Body).Decode(&snk); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	cfg := engine.SinkConfig{Type: snk.Type, Config: snk.Config}
	tables, err := s.registry.DiscoverSinkTables(r.Context(), cfg)
	if err != nil {
		s.jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(tables)
}

func (s *Server) sampleSinkTable(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Sink  storage.Sink `json:"sink"`
		Table string       `json:"table"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}

	cfg := engine.SinkConfig{Type: req.Sink.Type, Config: req.Sink.Config}
	msg, err := s.registry.SampleSinkTable(r.Context(), cfg, req.Table)
	if err != nil {
		s.jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"id":        msg.ID(),
		"operation": msg.Operation(),
		"table":     msg.Table(),
		"after":     string(msg.After()),
	})
}

func (s *Server) browseSinkTable(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Sink  storage.Sink `json:"sink"`
		Table string       `json:"table"`
		Limit int          `json:"limit"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}

	cfg := engine.SinkConfig{Type: req.Sink.Type, Config: req.Sink.Config}
	msgs, err := s.registry.BrowseSinkTable(r.Context(), cfg, req.Table, req.Limit)
	if err != nil {
		s.jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}

	var results []any
	for _, m := range msgs {
		results = append(results, m.Data())
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(results)
}

func (s *Server) querySink(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Config engine.SinkConfig `json:"config"`
		Query  string            `json:"query"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.jsonError(w, "Invalid request", http.StatusBadRequest)
		return
	}

	results, err := s.registry.ExecuteSinkSQL(r.Context(), req.Config, req.Query)
	if err != nil {
		s.jsonError(w, "Query failed: "+err.Error(), http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(results)
}

func (s *Server) previewSmtpTemplate(w http.ResponseWriter, r *http.Request) {
	// Full implementation would go here, moved from server.go
}

func (s *Server) validateEmail(w http.ResponseWriter, r *http.Request) {
	// Full implementation would go here, moved from server.go
}
