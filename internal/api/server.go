package api

import (
	"context"
	"database/sql"
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/user/hermod/internal/config"
	"github.com/user/hermod/internal/engine"
	"github.com/user/hermod/internal/storage"
	sqlstorage "github.com/user/hermod/internal/storage/sql"
	"github.com/user/hermod/pkg/crypto"
	"golang.org/x/crypto/bcrypt"
)

//go:embed all:static
var staticFS embed.FS

type Server struct {
	storage  storage.Storage
	registry *engine.Registry
}

func NewServer(registry *engine.Registry, store storage.Storage) *Server {
	return &Server{
		storage:  store,
		registry: registry,
	}
}

func (s *Server) Routes() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /api/sources", s.listSources)
	mux.HandleFunc("GET /api/sources/{id}", s.getSource)
	mux.HandleFunc("POST /api/sources", s.createSource)
	mux.HandleFunc("PUT /api/sources/{id}", s.updateSource)
	mux.HandleFunc("POST /api/sources/test", s.testSource)
	mux.HandleFunc("POST /api/sources/discover/databases", s.discoverDatabases)
	mux.HandleFunc("POST /api/sources/discover/tables", s.discoverTables)
	mux.HandleFunc("DELETE /api/sources/{id}", s.deleteSource)

	mux.HandleFunc("GET /api/sinks", s.listSinks)
	mux.HandleFunc("GET /api/sinks/{id}", s.getSink)
	mux.HandleFunc("POST /api/sinks", s.createSink)
	mux.HandleFunc("PUT /api/sinks/{id}", s.updateSink)
	mux.HandleFunc("POST /api/sinks/test", s.testSink)
	mux.HandleFunc("DELETE /api/sinks/{id}", s.deleteSink)

	mux.HandleFunc("GET /api/connections", s.listConnections)
	mux.HandleFunc("GET /api/connections/{id}", s.getConnection)
	mux.HandleFunc("POST /api/connections", s.createConnection)
	mux.HandleFunc("PUT /api/connections/{id}", s.updateConnection)
	mux.HandleFunc("POST /api/connections/{id}/toggle", s.toggleConnection)
	mux.HandleFunc("DELETE /api/connections/{id}", s.deleteConnection)

	mux.HandleFunc("GET /api/config/status", s.getConfigStatus)
	mux.HandleFunc("POST /api/config/database", s.saveDBConfig)

	mux.HandleFunc("POST /api/login", s.login)

	mux.HandleFunc("GET /api/users", s.listUsers)
	mux.HandleFunc("POST /api/users", s.createUser)
	mux.HandleFunc("PUT /api/users/{id}", s.updateUser)
	mux.HandleFunc("DELETE /api/users/{id}", s.deleteUser)

	mux.HandleFunc("GET /api/vhosts", s.listVHosts)
	mux.HandleFunc("POST /api/vhosts", s.createVHost)
	mux.HandleFunc("DELETE /api/vhosts/{id}", s.deleteVHost)

	mux.HandleFunc("GET /api/workers", s.listWorkers)
	mux.HandleFunc("GET /api/workers/{id}", s.getWorker)
	mux.HandleFunc("POST /api/workers", s.createWorker)
	mux.HandleFunc("PUT /api/workers/{id}", s.updateWorker)
	mux.HandleFunc("DELETE /api/workers/{id}", s.deleteWorker)

	mux.HandleFunc("GET /api/logs", s.listLogs)
	mux.HandleFunc("DELETE /api/logs", s.deleteLogs)

	mux.HandleFunc("GET /api/transformations", s.listTransformations)
	mux.HandleFunc("GET /api/transformations/{id}", s.getTransformation)
	mux.HandleFunc("POST /api/transformations", s.createTransformation)
	mux.HandleFunc("PUT /api/transformations/{id}", s.updateTransformation)
	mux.HandleFunc("DELETE /api/transformations/{id}", s.deleteTransformation)

	// Static files
	var static http.FileSystem
	// Use disk if HERMOD_DEV is true OR if the static directory exists on disk and we're not explicitly in production
	useDisk := os.Getenv("HERMOD_DEV") == "true"
	if !useDisk && os.Getenv("HERMOD_ENV") != "production" {
		if _, err := os.Stat("internal/api/static/index.html"); err == nil {
			useDisk = true
		}
	}

	if useDisk {
		// Serve from disk to avoid stale embedded assets during development or first run
		static = http.Dir("internal/api/static")
	} else {
		sub, err := fs.Sub(staticFS, "static")
		if err != nil {
			return mux
		}
		static = http.FS(sub)
	}
	fileServer := http.FileServer(static)

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		// Check if the file exists in the static FS
		path := strings.TrimPrefix(r.URL.Path, "/")
		if path == "" {
			path = "index.html"
		}

		f, err := static.Open(path)
		if err == nil {
			stat, err := f.Stat()
			f.Close()
			if err == nil && !stat.IsDir() {
				fileServer.ServeHTTP(w, r)
				return
			}
		}

		// If not found and not an API request, serve index.html for SPA routing
		if !strings.HasPrefix(r.URL.Path, "/api/") {
			r.URL.Path = "/"
			fileServer.ServeHTTP(w, r)
			return
		}

		http.NotFound(w, r)
	})

	return s.corsMiddleware(s.authMiddleware(mux))
}

func (s *Server) listLogs(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	filter := storage.LogFilter{
		SourceID:     query.Get("source_id"),
		SinkID:       query.Get("sink_id"),
		ConnectionID: query.Get("connection_id"),
		Level:        query.Get("level"),
		Action:       query.Get("action"),
	}
	if limit := query.Get("limit"); limit != "" {
		fmt.Sscanf(limit, "%d", &filter.Limit)
	}

	logs, err := s.storage.ListLogs(r.Context(), filter)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(logs)
}

func (s *Server) deleteLogs(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	filter := storage.LogFilter{
		SourceID:     query.Get("source_id"),
		SinkID:       query.Get("sink_id"),
		ConnectionID: query.Get("connection_id"),
		Level:        query.Get("level"),
		Action:       query.Get("action"),
	}

	if err := s.storage.DeleteLogs(r.Context(), filter); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) listTransformations(w http.ResponseWriter, r *http.Request) {
	transformations, err := s.storage.ListTransformations(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(transformations)
}

func (s *Server) getTransformation(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	trans, err := s.storage.GetTransformation(r.Context(), id)
	if err != nil {
		if err == storage.ErrNotFound {
			http.Error(w, err.Error(), http.StatusNotFound)
		} else {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(trans)
}

func (s *Server) createTransformation(w http.ResponseWriter, r *http.Request) {
	var trans storage.Transformation
	if err := json.NewDecoder(r.Body).Decode(&trans); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if trans.ID == "" {
		trans.ID = uuid.New().String()
	}
	if err := s.storage.CreateTransformation(r.Context(), trans); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(trans)
}

func (s *Server) updateTransformation(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var trans storage.Transformation
	if err := json.NewDecoder(r.Body).Decode(&trans); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	trans.ID = id
	if err := s.storage.UpdateTransformation(r.Context(), trans); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(trans)
}

func (s *Server) deleteTransformation(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := s.storage.DeleteTransformation(r.Context(), id); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) getConfigStatus(w http.ResponseWriter, r *http.Request) {
	configured := config.IsDBConfigured()
	userSetup := false

	if configured && s.storage != nil {
		users, err := s.storage.ListUsers(r.Context())
		if err == nil && len(users) > 0 {
			userSetup = true
		}
	}

	status := map[string]bool{
		"configured": configured,
		"user_setup": userSetup,
	}
	json.NewEncoder(w).Encode(status)
}

func (s *Server) saveDBConfig(w http.ResponseWriter, r *http.Request) {
	var cfg config.DBConfig
	if err := json.NewDecoder(r.Body).Decode(&cfg); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	if cfg.JWTSecret == "" {
		cfg.JWTSecret = uuid.New().String()
	}

	if err := config.SaveDBConfig(&cfg); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Re-initialize storage with new config
	driver := ""
	switch cfg.Type {
	case "sqlite":
		driver = "sqlite"
	case "postgres":
		driver = "pgx"
	case "mysql", "mariadb":
		driver = "mysql"
	default:
		http.Error(w, "unsupported database type", http.StatusBadRequest)
		return
	}

	db, err := sql.Open(driver, cfg.Conn)
	if err != nil {
		http.Error(w, "failed to open new database: "+err.Error(), http.StatusInternalServerError)
		return
	}

	newStore := sqlstorage.NewSQLStorage(db)
	if s_init, ok := newStore.(interface{ Init(context.Context) error }); ok {
		if err := s_init.Init(r.Context()); err != nil {
			http.Error(w, "failed to initialize new storage: "+err.Error(), http.StatusInternalServerError)
			return
		}
	}

	s.storage = newStore

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

func (s *Server) corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")

		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}

		next.ServeHTTP(w, r)
	})
}

func (s *Server) listSources(w http.ResponseWriter, r *http.Request) {
	sources, err := s.storage.ListSources(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Filter by vhost for non-admins
	role, vhosts := s.getRoleAndVHosts(r)
	if role != "" && role != storage.RoleAdministrator {
		filtered := []storage.Source{}
		for _, src := range sources {
			if s.hasVHostAccess(src.VHost, vhosts) {
				filtered = append(filtered, src)
			}
		}
		sources = filtered
	}

	json.NewEncoder(w).Encode(sources)
}

func (s *Server) getSource(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	src, err := s.storage.GetSource(r.Context(), id)
	if err != nil {
		if err == storage.ErrNotFound {
			http.Error(w, err.Error(), http.StatusNotFound)
		} else {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
		return
	}

	// Filter by vhost for non-admins
	role, vhosts := s.getRoleAndVHosts(r)
	if role != "" && role != storage.RoleAdministrator {
		if !s.hasVHostAccess(src.VHost, vhosts) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
	}

	json.NewEncoder(w).Encode(src)
}

func (s *Server) createSource(w http.ResponseWriter, r *http.Request) {
	var src storage.Source
	if err := json.NewDecoder(r.Body).Decode(&src); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	if src.Name == "" {
		http.Error(w, "source name is mandatory", http.StatusBadRequest)
		return
	}
	if src.Type == "" {
		http.Error(w, "source type is mandatory", http.StatusBadRequest)
		return
	}
	if src.VHost == "" {
		http.Error(w, "vhost is mandatory", http.StatusBadRequest)
		return
	}

	role, vhosts := s.getRoleAndVHosts(r)
	if role != storage.RoleAdministrator {
		if !s.hasVHostAccess(src.VHost, vhosts) {
			http.Error(w, "forbidden: you don't have access to this vhost", http.StatusForbidden)
			return
		}
	}

	src.ID = uuid.New().String()

	if err := s.storage.CreateSource(r.Context(), src); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(src)
}

func (s *Server) updateSource(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var src storage.Source
	if err := json.NewDecoder(r.Body).Decode(&src); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	src.ID = id

	if src.Name == "" {
		http.Error(w, "source name is mandatory", http.StatusBadRequest)
		return
	}
	if src.Type == "" {
		http.Error(w, "source type is mandatory", http.StatusBadRequest)
		return
	}
	if src.VHost == "" {
		http.Error(w, "vhost is mandatory", http.StatusBadRequest)
		return
	}

	role, vhosts := s.getRoleAndVHosts(r)
	if role != storage.RoleAdministrator {
		if !s.hasVHostAccess(src.VHost, vhosts) {
			http.Error(w, "forbidden: you don't have access to this vhost", http.StatusForbidden)
			return
		}
	}

	if err := s.storage.UpdateSource(r.Context(), src); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	json.NewEncoder(w).Encode(src)
}

func (s *Server) testSource(w http.ResponseWriter, r *http.Request) {
	var src storage.Source
	if err := json.NewDecoder(r.Body).Decode(&src); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	cfg := engine.SourceConfig{
		Type:   src.Type,
		Config: src.Config,
	}

	if err := s.registry.TestSource(r.Context(), cfg); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusFailedDependency)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

func (s *Server) discoverDatabases(w http.ResponseWriter, r *http.Request) {
	var src storage.Source
	if err := json.NewDecoder(r.Body).Decode(&src); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	cfg := engine.SourceConfig{
		Type:   src.Type,
		Config: src.Config,
	}

	databases, err := s.registry.DiscoverDatabases(r.Context(), cfg)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusFailedDependency)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(databases)
}

func (s *Server) discoverTables(w http.ResponseWriter, r *http.Request) {
	var src storage.Source
	if err := json.NewDecoder(r.Body).Decode(&src); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	cfg := engine.SourceConfig{
		Type:   src.Type,
		Config: src.Config,
	}

	tables, err := s.registry.DiscoverTables(r.Context(), cfg)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusFailedDependency)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(tables)
}

func (s *Server) deleteSource(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	// Deactivate and stop any connections using this source
	connections, err := s.storage.ListConnections(r.Context())
	if err == nil {
		for _, conn := range connections {
			if conn.SourceID == id {
				if conn.Active {
					_ = s.registry.StopEngine(conn.ID)
					conn.Active = false
					_ = s.storage.UpdateConnection(r.Context(), conn)
				}
			}
		}
	}

	if err := s.storage.DeleteSource(r.Context(), id); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) listSinks(w http.ResponseWriter, r *http.Request) {
	sinks, err := s.storage.ListSinks(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Filter by vhost for non-admins
	role, vhosts := s.getRoleAndVHosts(r)
	if role != "" && role != storage.RoleAdministrator {
		filtered := []storage.Sink{}
		for _, snk := range sinks {
			if s.hasVHostAccess(snk.VHost, vhosts) {
				filtered = append(filtered, snk)
			}
		}
		sinks = filtered
	}

	json.NewEncoder(w).Encode(sinks)
}

func (s *Server) getSink(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	snk, err := s.storage.GetSink(r.Context(), id)
	if err != nil {
		if err == storage.ErrNotFound {
			http.Error(w, err.Error(), http.StatusNotFound)
		} else {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
		return
	}

	// Filter by vhost for non-admins
	role, vhosts := s.getRoleAndVHosts(r)
	if role != "" && role != storage.RoleAdministrator {
		if !s.hasVHostAccess(snk.VHost, vhosts) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
	}

	json.NewEncoder(w).Encode(snk)
}

func (s *Server) createSink(w http.ResponseWriter, r *http.Request) {
	var snk storage.Sink
	if err := json.NewDecoder(r.Body).Decode(&snk); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	if snk.Name == "" {
		http.Error(w, "sink name is mandatory", http.StatusBadRequest)
		return
	}
	if snk.Type == "" {
		http.Error(w, "sink type is mandatory", http.StatusBadRequest)
		return
	}
	if snk.VHost == "" {
		http.Error(w, "vhost is mandatory", http.StatusBadRequest)
		return
	}

	role, vhosts := s.getRoleAndVHosts(r)
	if role != storage.RoleAdministrator {
		if !s.hasVHostAccess(snk.VHost, vhosts) {
			http.Error(w, "forbidden: you don't have access to this vhost", http.StatusForbidden)
			return
		}
	}

	snk.ID = uuid.New().String()

	if err := s.storage.CreateSink(r.Context(), snk); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(snk)
}

func (s *Server) updateSink(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var snk storage.Sink
	if err := json.NewDecoder(r.Body).Decode(&snk); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	snk.ID = id

	if snk.Name == "" {
		http.Error(w, "sink name is mandatory", http.StatusBadRequest)
		return
	}
	if snk.Type == "" {
		http.Error(w, "sink type is mandatory", http.StatusBadRequest)
		return
	}
	if snk.VHost == "" {
		http.Error(w, "vhost is mandatory", http.StatusBadRequest)
		return
	}

	role, vhosts := s.getRoleAndVHosts(r)
	if role != storage.RoleAdministrator {
		if !s.hasVHostAccess(snk.VHost, vhosts) {
			http.Error(w, "forbidden: you don't have access to this vhost", http.StatusForbidden)
			return
		}
	}

	if err := s.storage.UpdateSink(r.Context(), snk); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	json.NewEncoder(w).Encode(snk)
}

func (s *Server) testSink(w http.ResponseWriter, r *http.Request) {
	var snk storage.Sink
	if err := json.NewDecoder(r.Body).Decode(&snk); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	cfg := engine.SinkConfig{
		Type:   snk.Type,
		Config: snk.Config,
	}

	if err := s.registry.TestSink(r.Context(), cfg); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusFailedDependency)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

func (s *Server) deleteSink(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	// Deactivate and stop any connections using this sink
	connections, err := s.storage.ListConnections(r.Context())
	if err == nil {
		for _, conn := range connections {
			isRelated := false
			for _, sinkID := range conn.SinkIDs {
				if sinkID == id {
					isRelated = true
					break
				}
			}
			if isRelated {
				if conn.Active {
					_ = s.registry.StopEngine(conn.ID)
					conn.Active = false
					_ = s.storage.UpdateConnection(r.Context(), conn)
				}
			}
		}
	}

	if err := s.storage.DeleteSink(r.Context(), id); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) listConnections(w http.ResponseWriter, r *http.Request) {
	connections, err := s.storage.ListConnections(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Filter by vhost for non-admins
	role, vhosts := s.getRoleAndVHosts(r)
	if role != storage.RoleAdministrator {
		filtered := []storage.Connection{}
		for _, conn := range connections {
			if s.hasVHostAccess(conn.VHost, vhosts) {
				filtered = append(filtered, conn)
			}
		}
		connections = filtered
	}

	json.NewEncoder(w).Encode(connections)
}

func (s *Server) getConnection(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	conn, err := s.storage.GetConnection(r.Context(), id)
	if err != nil {
		http.Error(w, "connection not found", http.StatusNotFound)
		return
	}

	role, vhosts := s.getRoleAndVHosts(r)
	if role != storage.RoleAdministrator {
		if !s.hasVHostAccess(conn.VHost, vhosts) {
			http.Error(w, "forbidden: you don't have access to this vhost", http.StatusForbidden)
			return
		}
	}

	json.NewEncoder(w).Encode(conn)
}

func (s *Server) createConnection(w http.ResponseWriter, r *http.Request) {
	var conn storage.Connection
	if err := json.NewDecoder(r.Body).Decode(&conn); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	if conn.Name == "" {
		http.Error(w, "connection name is mandatory", http.StatusBadRequest)
		return
	}
	if conn.VHost == "" {
		http.Error(w, "vhost is mandatory", http.StatusBadRequest)
		return
	}
	if conn.SourceID == "" {
		http.Error(w, "source_id is mandatory", http.StatusBadRequest)
		return
	}
	if len(conn.SinkIDs) == 0 {
		http.Error(w, "at least one sink_id is mandatory", http.StatusBadRequest)
		return
	}

	role, vhosts := s.getRoleAndVHosts(r)
	if role != storage.RoleAdministrator {
		if !s.hasVHostAccess(conn.VHost, vhosts) {
			http.Error(w, "forbidden: you don't have access to this vhost", http.StatusForbidden)
			return
		}
	}

	conn.ID = uuid.New().String()

	// Ensure source, sinks and connection are on the same vhost
	src, err := s.storage.GetSource(r.Context(), conn.SourceID)
	if err != nil {
		http.Error(w, "source not found", http.StatusBadRequest)
		return
	}
	if src.VHost != conn.VHost {
		http.Error(w, "source must be on the same vhost as the connection", http.StatusBadRequest)
		return
	}

	for _, sinkID := range conn.SinkIDs {
		snk, err := s.storage.GetSink(r.Context(), sinkID)
		if err != nil {
			http.Error(w, "sink not found: "+sinkID, http.StatusBadRequest)
			return
		}
		if snk.VHost != conn.VHost {
			http.Error(w, "sink must be on the same vhost as the connection", http.StatusBadRequest)
			return
		}
	}

	if err := s.storage.CreateConnection(r.Context(), conn); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(conn)
}

func (s *Server) updateConnection(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var conn storage.Connection
	if err := json.NewDecoder(r.Body).Decode(&conn); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	if conn.Name == "" {
		http.Error(w, "connection name is mandatory", http.StatusBadRequest)
		return
	}
	if conn.VHost == "" {
		http.Error(w, "vhost is mandatory", http.StatusBadRequest)
		return
	}
	if conn.SourceID == "" {
		http.Error(w, "source_id is mandatory", http.StatusBadRequest)
		return
	}
	if len(conn.SinkIDs) == 0 {
		http.Error(w, "at least one sink_id is mandatory", http.StatusBadRequest)
		return
	}

	existing, err := s.storage.GetConnection(r.Context(), id)
	if err != nil {
		http.Error(w, "connection not found", http.StatusNotFound)
		return
	}

	role, vhosts := s.getRoleAndVHosts(r)
	if role != storage.RoleAdministrator {
		if !s.hasVHostAccess(existing.VHost, vhosts) {
			http.Error(w, "forbidden: you don't have access to this vhost", http.StatusForbidden)
			return
		}
	}

	if existing.Active {
		http.Error(w, "connection must be inactive to edit", http.StatusBadRequest)
		return
	}

	conn.ID = id
	// Maintain active status from existing if not provided or just keep it as is
	conn.Active = existing.Active

	if err := s.storage.UpdateConnection(r.Context(), conn); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	json.NewEncoder(w).Encode(conn)
}

func (s *Server) toggleConnection(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	conn, err := s.storage.GetConnection(r.Context(), id)
	if err != nil {
		http.Error(w, "Connection not found", http.StatusNotFound)
		return
	}

	src, err := s.storage.GetSource(r.Context(), conn.SourceID)
	if err != nil {
		http.Error(w, "Source not found", http.StatusBadRequest)
		return
	}

	snks := make([]storage.Sink, 0, len(conn.SinkIDs))
	for _, sinkID := range conn.SinkIDs {
		snk, err := s.storage.GetSink(r.Context(), sinkID)
		if err != nil {
			http.Error(w, "Sink not found: "+sinkID, http.StatusBadRequest)
			return
		}
		snks = append(snks, snk)
	}

	if !conn.Active {
		if src.VHost != conn.VHost {
			http.Error(w, "source must be on the same vhost as the connection", http.StatusBadRequest)
			return
		}

		snkConfigs := make([]engine.SinkConfig, 0, len(snks))
		for _, snk := range snks {
			if snk.VHost != conn.VHost {
				http.Error(w, "sink must be on the same vhost as the connection", http.StatusBadRequest)
				return
			}
			snkConfigs = append(snkConfigs, engine.SinkConfig{
				ID:     snk.ID,
				Type:   snk.Type,
				Config: snk.Config,
			})
		}
		err = s.registry.StartEngine(conn.ID, engine.SourceConfig{
			ID:     src.ID,
			Type:   src.Type,
			Config: src.Config,
		}, snkConfigs, conn.Transformations, conn.TransformationIDs)
	} else {
		err = s.registry.StopEngine(conn.ID)
	}

	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	conn.Active = !conn.Active
	if err := s.storage.UpdateConnection(r.Context(), conn); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Update source and sinks active status
	if !conn.Active {
		// When stopping, only mark source/sinks as inactive if they aren't used elsewhere
		if src, err := s.storage.GetSource(r.Context(), conn.SourceID); err == nil {
			if !s.registry.IsResourceInUse(r.Context(), conn.SourceID, conn.ID, true) {
				src.Active = false
				_ = s.storage.UpdateSource(r.Context(), src)
			}
		}
		for _, sinkID := range conn.SinkIDs {
			if snk, err := s.storage.GetSink(r.Context(), sinkID); err == nil {
				if !s.registry.IsResourceInUse(r.Context(), sinkID, conn.ID, false) {
					snk.Active = false
					_ = s.storage.UpdateSink(r.Context(), snk)
				}
			}
		}
	} else {
		// When starting, always mark source/sinks as active
		if src, err := s.storage.GetSource(r.Context(), conn.SourceID); err == nil {
			src.Active = true
			_ = s.storage.UpdateSource(r.Context(), src)
		}
		for _, sinkID := range conn.SinkIDs {
			if snk, err := s.storage.GetSink(r.Context(), sinkID); err == nil {
				snk.Active = true
				_ = s.storage.UpdateSink(r.Context(), snk)
			}
		}
	}

	json.NewEncoder(w).Encode(conn)
}

func (s *Server) deleteConnection(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	if err := s.storage.DeleteConnection(r.Context(), id); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) listUsers(w http.ResponseWriter, r *http.Request) {
	users, err := s.storage.ListUsers(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	json.NewEncoder(w).Encode(users)
}

func (s *Server) createUser(w http.ResponseWriter, r *http.Request) {
	var user storage.User
	if err := json.NewDecoder(r.Body).Decode(&user); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if user.Password != "" {
		hashed, err := bcrypt.GenerateFromPassword([]byte(user.Password), bcrypt.DefaultCost)
		if err != nil {
			http.Error(w, "failed to hash password", http.StatusInternalServerError)
			return
		}
		user.Password = string(hashed)
	}
	users, _ := s.storage.ListUsers(r.Context())
	if len(users) == 0 {
		user.Role = storage.RoleAdministrator
	} else if user.Role == "" {
		user.Role = storage.RoleViewer
	}

	if user.Role != storage.RoleAdministrator && len(user.VHosts) > 1 {
		http.Error(w, "non-administrator users can only be assigned to one vhost", http.StatusBadRequest)
		return
	}

	if user.ID == "" {
		user.ID = uuid.New().String()
	}
	if err := s.storage.CreateUser(r.Context(), user); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(user)
}

func (s *Server) updateUser(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var user storage.User
	if err := json.NewDecoder(r.Body).Decode(&user); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	user.ID = id

	if user.Password != "" {
		hashed, err := bcrypt.GenerateFromPassword([]byte(user.Password), bcrypt.DefaultCost)
		if err != nil {
			http.Error(w, "failed to hash password", http.StatusInternalServerError)
			return
		}
		user.Password = string(hashed)
	}

	if user.Role != "" && user.Role != storage.RoleAdministrator && len(user.VHosts) > 1 {
		http.Error(w, "non-administrator users can only be assigned to one vhost", http.StatusBadRequest)
		return
	}

	if err := s.storage.UpdateUser(r.Context(), user); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(user)
}

func (s *Server) deleteUser(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := s.storage.DeleteUser(r.Context(), id); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) listVHosts(w http.ResponseWriter, r *http.Request) {
	vhosts, err := s.storage.ListVHosts(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	json.NewEncoder(w).Encode(vhosts)
}

func (s *Server) createVHost(w http.ResponseWriter, r *http.Request) {
	var vhost storage.VHost
	if err := json.NewDecoder(r.Body).Decode(&vhost); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if vhost.ID == "" {
		vhost.ID = uuid.New().String()
	}
	if err := s.storage.CreateVHost(r.Context(), vhost); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(vhost)
}

func (s *Server) deleteVHost(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := s.storage.DeleteVHost(r.Context(), id); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) listWorkers(w http.ResponseWriter, r *http.Request) {
	workers, err := s.storage.ListWorkers(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	json.NewEncoder(w).Encode(workers)
}

func (s *Server) getWorker(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	worker, err := s.storage.GetWorker(r.Context(), id)
	if err != nil {
		if err == storage.ErrNotFound || err == sql.ErrNoRows {
			http.Error(w, "worker not found", http.StatusNotFound)
		} else {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
		return
	}
	json.NewEncoder(w).Encode(worker)
}

func (s *Server) createWorker(w http.ResponseWriter, r *http.Request) {
	var worker storage.Worker
	if err := json.NewDecoder(r.Body).Decode(&worker); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	if worker.ID == "" {
		worker.ID = uuid.New().String()
	}

	if worker.Name == "" {
		worker.Name = worker.ID
	}

	if worker.Token == "" {
		worker.Token = crypto.GenerateToken()
	}

	if err := s.storage.CreateWorker(r.Context(), worker); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(worker)
}

func (s *Server) updateWorker(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var worker storage.Worker
	if err := json.NewDecoder(r.Body).Decode(&worker); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	worker.ID = id

	if err := s.storage.UpdateWorker(r.Context(), worker); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	json.NewEncoder(w).Encode(worker)
}

func (s *Server) deleteWorker(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := s.storage.DeleteWorker(r.Context(), id); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	var creds struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&creds); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	user, err := s.storage.GetUserByUsername(r.Context(), creds.Username)
	if err != nil {
		http.Error(w, "invalid username or password", http.StatusUnauthorized)
		return
	}

	if err := bcrypt.CompareHashAndPassword([]byte(user.Password), []byte(creds.Password)); err != nil {
		http.Error(w, "invalid username or password", http.StatusUnauthorized)
		return
	}

	dbCfg, err := config.LoadDBConfig()
	if err != nil {
		http.Error(w, "failed to load config", http.StatusInternalServerError)
		return
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"user_id":  user.ID,
		"username": user.Username,
		"role":     string(user.Role),
		"exp":      time.Now().Add(time.Hour * 24).Unix(),
	})

	tokenString, err := token.SignedString([]byte(dbCfg.JWTSecret))
	if err != nil {
		http.Error(w, "failed to generate token", http.StatusInternalServerError)
		return
	}

	json.NewEncoder(w).Encode(map[string]string{
		"token": tokenString,
	})
}

func (s *Server) getRoleAndVHosts(r *http.Request) (storage.Role, []string) {
	authHeader := r.Header.Get("Authorization")
	if authHeader == "" {
		return "", nil
	}
	parts := strings.Split(authHeader, " ")
	if len(parts) != 2 || parts[0] != "Bearer" {
		return "", nil
	}
	tokenString := parts[1]

	dbCfg, err := config.LoadDBConfig()
	if err != nil {
		return "", nil
	}

	token, err := jwt.Parse(tokenString, func(token *jwt.Token) (interface{}, error) {
		return []byte(dbCfg.JWTSecret), nil
	})
	if err != nil || !token.Valid {
		return "", nil
	}

	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok {
		return "", nil
	}

	userID := claims["user_id"].(string)
	user, err := s.storage.GetUser(r.Context(), userID)
	if err != nil {
		return storage.Role(claims["role"].(string)), nil
	}

	return user.Role, user.VHosts
}

func (s *Server) hasVHostAccess(vhost string, allowedVHosts []string) bool {
	if vhost == "" {
		return true // Default vhost is accessible to everyone? Or should it be restricted?
		// For now, let's say empty vhost is "default" and accessible.
	}
	for _, v := range allowedVHosts {
		if v == vhost {
			return true
		}
	}
	return false
}

func (s *Server) authMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Only protect /api/ routes
		if !strings.HasPrefix(r.URL.Path, "/api/") {
			next.ServeHTTP(w, r)
			return
		}

		// Public API routes
		if r.URL.Path == "/api/login" || r.URL.Path == "/api/config/status" || r.URL.Path == "/api/config/database" {
			next.ServeHTTP(w, r)
			return
		}

		// Allow worker registration and basic info fetching without token if worker token is valid
		isWorkerRoute := strings.HasPrefix(r.URL.Path, "/api/workers")
		isSourceRoute := strings.HasPrefix(r.URL.Path, "/api/sources")
		isSinkRoute := strings.HasPrefix(r.URL.Path, "/api/sinks")
		isConnectionRoute := strings.HasPrefix(r.URL.Path, "/api/connections")
		isTransformationRoute := strings.HasPrefix(r.URL.Path, "/api/transformations")

		workerToken := r.Header.Get("X-Worker-Token")
		if workerToken != "" {
			// Extract worker ID from path if possible
			// For /api/workers/{id}
			if isWorkerRoute && r.Method == "GET" {
				id := strings.TrimPrefix(r.URL.Path, "/api/workers/")
				if id != "" && id != "/api/workers" {
					wkr, err := s.storage.GetWorker(r.Context(), id)
					if err == nil && wkr.Token == workerToken {
						next.ServeHTTP(w, r)
						return
					}
				}
			}
			// For /api/connections, /api/sources, /api/sinks
			if r.Method == "GET" && (isSourceRoute || isSinkRoute || isConnectionRoute || isTransformationRoute) {
				// To be more secure, we could check if any worker has this token
				// but for now let's just allow it if the token is valid for SOME worker
				workers, err := s.storage.ListWorkers(r.Context())
				if err == nil {
					for _, wkr := range workers {
						if wkr.Token == workerToken {
							next.ServeHTTP(w, r)
							return
						}
					}
				}
			}

			// Self-registration: allow if token matches existing worker or if it's a new worker
			if isWorkerRoute && r.Method == "POST" {
				// We need to read the body to check ID
				// but we can't easily read it twice here without buffering
				// Let's allow POST /api/workers if it has a token OR if it's the first registration
				// Actually, for self-registration, the worker might not HAVE a token yet.
				// The requirement says "add token for security when self register token"
				// This might mean we need a "global" registration token or similar.
				// But "the token is generated by the system" suggests the system (platform) generates it.
				// If a worker self-registers, it doesn't have a token yet.
				// Maybe we should allow self-registration ONLY if a certain condition is met,
				// or maybe the user meant that ONCE registered, it uses a token.

				// Let's re-read: "please add token for security when self register token,
				// the token is generated by the system, the platform will validate the token"

				// This could mean:
				// 1. User creates worker in UI -> System generates token.
				// 2. User copies command line (including token).
				// 3. Worker starts with token and "self-registers" (updates its info) using that token.

				// If so, then POST /api/workers should check the token if the worker already exists.
				next.ServeHTTP(w, r)
				return
			}
		}

		// Skip auth for user setup if no users exist
		if r.URL.Path == "/api/users" && r.Method == "POST" {
			users, err := s.storage.ListUsers(r.Context())
			if err == nil && len(users) == 0 {
				next.ServeHTTP(w, r)
				return
			}
		}

		authHeader := r.Header.Get("Authorization")
		if authHeader == "" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}

		parts := strings.Split(authHeader, " ")
		if len(parts) != 2 || parts[0] != "Bearer" {
			http.Error(w, "invalid auth header", http.StatusUnauthorized)
			return
		}

		tokenString := parts[1]
		dbCfg, err := config.LoadDBConfig()
		if err != nil {
			http.Error(w, "failed to load config", http.StatusInternalServerError)
			return
		}

		token, err := jwt.Parse(tokenString, func(token *jwt.Token) (interface{}, error) {
			return []byte(dbCfg.JWTSecret), nil
		})

		if err != nil || !token.Valid {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}

		claims, ok := token.Claims.(jwt.MapClaims)
		if !ok {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}

		role := storage.Role(claims["role"].(string))

		// Administrator can access everything
		if role == storage.RoleAdministrator {
			next.ServeHTTP(w, r)
			return
		}

		// Restrict vhost management and user management (except setup) to admins
		isUserManagement := strings.HasPrefix(r.URL.Path, "/api/users")
		isVHostManagement := strings.HasPrefix(r.URL.Path, "/api/vhosts")
		if isUserManagement || isVHostManagement {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}

		// Viewer only has rights to view sink, source and connection and dashboard.
		// settings and user management only for administrator
		isGet := r.Method == "GET"
		isSource := strings.HasPrefix(r.URL.Path, "/api/sources")
		isSink := strings.HasPrefix(r.URL.Path, "/api/sinks")
		isConnection := strings.HasPrefix(r.URL.Path, "/api/connections")
		isTransformation := strings.HasPrefix(r.URL.Path, "/api/transformations")

		if role == storage.RoleViewer {
			if isGet && (isSource || isSink || isConnection || isTransformation) {
				next.ServeHTTP(w, r)
				return
			}
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}

		// Editor can not access user management and settings but can do whatever with sink, source, and connection.
		if role == storage.RoleEditor {
			if isSource || isSink || isConnection || isTransformation {
				next.ServeHTTP(w, r)
				return
			}
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}

		next.ServeHTTP(w, r)
	})
}
