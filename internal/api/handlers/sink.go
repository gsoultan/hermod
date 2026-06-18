package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/user/hermod/internal/factory"
	"github.com/user/hermod/internal/storage"
	"github.com/user/hermod/pkg/infra/sqlutil"
)

func (h *Handler) RegisterSinkRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/sinks", h.ListSinks)
	mux.HandleFunc("GET /api/sinks/{id}", h.GetSink)
	mux.Handle("POST /api/sinks", h.EditorOnly(h.CreateSink))
	mux.Handle("PUT /api/sinks/{id}", h.EditorOnly(h.UpdateSink))
	mux.Handle("POST /api/sinks/test", h.EditorOnly(h.TestSink))
	mux.Handle("POST /api/sinks/discover/databases", h.EditorOnly(h.DiscoverSinkDatabases))
	mux.Handle("POST /api/sinks/discover/tables", h.EditorOnly(h.DiscoverSinkTables))
	mux.Handle("POST /api/sinks/discover/columns", h.EditorOnly(h.DiscoverSinkColumns))
	mux.Handle("POST /api/sinks/sample", h.EditorOnly(h.SampleSinkTable))
	mux.Handle("POST /api/sinks/browse", h.EditorOnly(h.BrowseSinkTable))
	mux.Handle("POST /api/sinks/query", h.EditorOnly(h.QuerySink))
	mux.Handle("POST /api/sinks/truncate", h.EditorOnly(h.TruncateSinkTable))
	mux.Handle("POST /api/sinks/smtp/preview", h.EditorOnly(h.PreviewSmtpTemplate))
	mux.Handle("POST /api/sinks/smtp/validate", h.EditorOnly(h.ValidateEmail))
	mux.Handle("DELETE /api/sinks/{id}", h.EditorOnly(h.DeleteSink))
}

func (h *Handler) ListSinks(w http.ResponseWriter, r *http.Request) {
	filter := h.ParseCommonFilter(r)
	role, vhosts := h.GetRoleAndVHosts(r)

	if filter.VHost != "" && role != storage.RoleAdministrator {
		if !h.HasVHostAccess(filter.VHost, vhosts) {
			h.JsonError(w, "Forbidden", http.StatusForbidden)
			return
		}
	}

	sinks, total, err := h.Storage.ListSinks(r.Context(), filter)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if role != "" && role != storage.RoleAdministrator {
		filtered := []storage.Sink{}
		for _, snk := range sinks {
			if h.HasVHostAccess(snk.VHost, vhosts) {
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

func (h *Handler) GetSink(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	snk, err := h.Storage.GetSink(r.Context(), id)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			h.JsonError(w, "Sink not found", http.StatusNotFound)
		} else {
			h.JsonError(w, "Failed to retrieve sink: "+err.Error(), http.StatusInternalServerError)
		}
		return
	}

	role, vhosts := h.GetRoleAndVHosts(r)
	if role != "" && role != storage.RoleAdministrator {
		if !h.HasVHostAccess(snk.VHost, vhosts) {
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(snk)
}

func (h *Handler) CreateSink(w http.ResponseWriter, r *http.Request) {
	var snk storage.Sink
	if err := json.NewDecoder(r.Body).Decode(&snk); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	if snk.Name == "" || snk.Type == "" || snk.VHost == "" {
		http.Error(w, "Name, Type, and VHost are mandatory", http.StatusBadRequest)
		return
	}

	role, vhosts := h.GetRoleAndVHosts(r)
	if role != storage.RoleAdministrator {
		if !h.HasVHostAccess(snk.VHost, vhosts) {
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}
	}

	snk.ID = uuid.New().String()
	snk.Active = true
	if err := h.Storage.CreateSink(r.Context(), snk); err != nil {
		h.JsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	h.RecordAuditLog(r, "INFO", "Created sink "+snk.Name, "create", "", "", snk.ID, snk)

	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(snk)
}

func (h *Handler) UpdateSink(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var snk storage.Sink
	if err := json.NewDecoder(r.Body).Decode(&snk); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	snk.ID = id

	role, vhosts := h.GetRoleAndVHosts(r)
	if role != storage.RoleAdministrator {
		if !h.HasVHostAccess(snk.VHost, vhosts) {
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}
	}

	if err := h.Storage.UpdateSink(r.Context(), snk); err != nil {
		h.JsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	h.RecordAuditLog(r, "INFO", "Updated sink "+snk.Name, "update", "", "", snk.ID, snk)

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(snk)
}

func (h *Handler) DeleteSink(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	ctx := r.Context()

	snk, err := h.Storage.GetSink(ctx, id)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			h.JsonError(w, "Sink not found", http.StatusNotFound)
		} else {
			h.JsonError(w, "Failed to get sink: "+err.Error(), http.StatusInternalServerError)
		}
		return
	}

	// RBAC check
	role, vhosts := h.GetRoleAndVHosts(r)
	if role != storage.RoleAdministrator {
		if !h.HasVHostAccess(snk.VHost, vhosts) {
			h.JsonError(w, "Forbidden", http.StatusForbidden)
			return
		}
	}

	wfs, _, err := h.Storage.ListWorkflows(ctx, storage.CommonFilter{})
	if err == nil {
		for _, wf := range wfs {
			for _, node := range wf.Nodes {
				if node.Type == "sink" && node.RefID == id {
					h.JsonError(w, "Cannot delete sink: it is used by workflow "+wf.Name, http.StatusConflict)
					return
				}
			}
		}
	}

	if err := h.Storage.DeleteSink(ctx, id); err != nil {
		h.JsonError(w, "Failed to delete sink", http.StatusInternalServerError)
		return
	}

	h.RecordAuditLog(r, "INFO", "Deleted sink "+snk.Name, "delete", "", "", id, nil)
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) TestSink(w http.ResponseWriter, r *http.Request) {
	var snk storage.Sink
	if err := json.NewDecoder(r.Body).Decode(&snk); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	cfg := factory.SinkConfig{Type: snk.Type, Config: snk.Config}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()

	if err := h.Registry.TestSink(ctx, cfg); err != nil {
		h.JsonError(w, err.Error(), http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

func (h *Handler) DiscoverSinkDatabases(w http.ResponseWriter, r *http.Request) {
	var snk storage.Sink
	if err := json.NewDecoder(r.Body).Decode(&snk); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	cfg := factory.SinkConfig{Type: snk.Type, Config: snk.Config}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()

	dbs, err := h.Registry.DiscoverSinkDatabases(ctx, cfg)
	if err != nil {
		h.JsonError(w, err.Error(), http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(dbs)
}

func (h *Handler) DiscoverSinkTables(w http.ResponseWriter, r *http.Request) {
	var snk storage.Sink
	if err := json.NewDecoder(r.Body).Decode(&snk); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	cfg := factory.SinkConfig{Type: snk.Type, Config: snk.Config}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()

	tables, err := h.Registry.DiscoverSinkTables(ctx, cfg)
	if err != nil {
		h.JsonError(w, err.Error(), http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(tables)
}

func (h *Handler) DiscoverSinkColumns(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Sink  storage.Sink `json:"sink"`
		Table string       `json:"table"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	cfg := factory.SinkConfig{Type: req.Sink.Type, Config: req.Sink.Config}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()

	columns, err := h.Registry.DiscoverSinkColumns(ctx, cfg, req.Table)
	if err != nil {
		h.JsonError(w, err.Error(), http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(columns)
}

func (h *Handler) SampleSinkTable(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Sink  storage.Sink `json:"sink"`
		Table string       `json:"table"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.JsonError(w, err.Error(), http.StatusBadRequest)
		return
	}

	cfg := factory.SinkConfig{Type: req.Sink.Type, Config: req.Sink.Config}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()

	msg, err := h.Registry.SampleSinkTable(ctx, cfg, req.Table)
	if err != nil {
		h.JsonError(w, err.Error(), http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(msg)
}

func (h *Handler) BrowseSinkTable(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Sink  storage.Sink `json:"sink"`
		Table string       `json:"table"`
		Limit int          `json:"limit"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.JsonError(w, err.Error(), http.StatusBadRequest)
		return
	}

	cfg := factory.SinkConfig{Type: req.Sink.Type, Config: req.Sink.Config}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()

	msgs, err := h.Registry.BrowseSinkTable(ctx, cfg, req.Table, req.Limit)
	if err != nil {
		h.JsonError(w, err.Error(), http.StatusBadRequest)
		return
	}

	var results []any
	for _, m := range msgs {
		results = append(results, m.Data())
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(results)
}

func (h *Handler) QuerySink(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Config factory.SinkConfig `json:"config"`
		Query  string             `json:"query"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.JsonError(w, "Invalid request", http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	results, err := h.Registry.ExecuteSinkSQL(ctx, req.Config, req.Query)
	if err != nil {
		h.JsonError(w, "Query failed: "+err.Error(), http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(results)
}

func (h *Handler) TruncateSinkTable(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Sink  storage.Sink `json:"sink"`
		Table string       `json:"table"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.JsonError(w, "Invalid request", http.StatusBadRequest)
		return
	}
	if req.Table == "" || req.Sink.Type == "" {
		h.JsonError(w, "Missing sink type or table", http.StatusBadRequest)
		return
	}

	// RBAC: editorOnly wrapper already applied.

	// Build a dialect-appropriate truncate statement
	stmt := ""
	typeName := strings.ToLower(req.Sink.Type)
	target := req.Table

	// Helper to quote identifiers
	quote := func(driver, name string) (string, error) {
		return sqlutil.QuoteIdent(driver, name)
	}

	switch typeName {
	case "postgres", "yugabyte":
		q, err := quote("pgx", target)
		if err != nil {
			h.JsonError(w, err.Error(), http.StatusBadRequest)
			return
		}
		stmt = "TRUNCATE TABLE " + q
	case "mysql", "mariadb":
		q, err := quote("mysql", target)
		if err != nil {
			h.JsonError(w, err.Error(), http.StatusBadRequest)
			return
		}
		stmt = "TRUNCATE TABLE " + q
	case "mssql":
		q, err := quote("mssql", target)
		if err != nil {
			h.JsonError(w, err.Error(), http.StatusBadRequest)
			return
		}
		stmt = "TRUNCATE TABLE " + q
	case "sqlite":
		q, err := quote("sqlite", target)
		if err != nil {
			h.JsonError(w, err.Error(), http.StatusBadRequest)
			return
		}
		stmt = "DELETE FROM " + q
	case "oracle":
		q, err := quote("oracle", target)
		if err != nil {
			h.JsonError(w, err.Error(), http.StatusBadRequest)
			return
		}
		stmt = "TRUNCATE TABLE " + q
	case "clickhouse":
		full := target
		if !strings.Contains(target, ".") {
			db := getString(req.Sink.Config, "database")
			if db != "" {
				full = db + "." + target
			}
		}
		q, err := quote("clickhouse", full)
		if err != nil {
			h.JsonError(w, err.Error(), http.StatusBadRequest)
			return
		}
		stmt = "TRUNCATE TABLE " + q
	case "snowflake":
		q, err := quote("snowflake", target)
		if err != nil {
			h.JsonError(w, err.Error(), http.StatusBadRequest)
			return
		}
		stmt = "TRUNCATE TABLE " + q
	case "cassandra":
		full := target
		if !strings.Contains(target, ".") {
			ks := getString(req.Sink.Config, "keyspace")
			if ks != "" {
				full = ks + "." + target
			}
		}
		q, err := quote("cassandra", full)
		if err != nil {
			h.JsonError(w, err.Error(), http.StatusBadRequest)
			return
		}
		// CQL supports TRUNCATE without TABLE
		stmt = "TRUNCATE " + q
	default:
		h.JsonError(w, "Unsupported sink type for truncate: "+req.Sink.Type, http.StatusBadRequest)
		return
	}

	cfg := factory.SinkConfig{Type: req.Sink.Type, Config: req.Sink.Config}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()

	if err := h.Registry.ExecSinkStatement(ctx, cfg, stmt); err != nil {
		h.JsonError(w, "Truncate failed: "+err.Error(), http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

// helper to read string from string map
func getString(m map[string]string, key string) string {
	if v, ok := m[key]; ok {
		return v
	}
	return ""
}

func (h *Handler) PreviewSmtpTemplate(w http.ResponseWriter, r *http.Request) {
	// Full implementation would go here, moved from server.go
}

func (h *Handler) ValidateEmail(w http.ResponseWriter, r *http.Request) {
	// Full implementation would go here, moved from server.go
}
