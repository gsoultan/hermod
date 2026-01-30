package api

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/user/hermod/internal/config"
	"github.com/user/hermod/internal/notification"
	"github.com/user/hermod/internal/storage"
	storagemongo "github.com/user/hermod/internal/storage/mongodb"
	sqlstorage "github.com/user/hermod/internal/storage/sql"
	"github.com/user/hermod/pkg/crypto"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"golang.org/x/crypto/bcrypt"
)

func (s *Server) handleLiveness(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"status": "ok",
		"time":   time.Now().UTC().Format(time.RFC3339Nano),
	})
}

func (s *Server) getConfigStatus(w http.ResponseWriter, r *http.Request) {
	configured := config.IsDBConfigured()
	userSetup := false

	if configured && s.storage != nil {
		users, _, err := s.storage.ListUsers(r.Context(), storage.CommonFilter{})
		if err == nil && len(users) > 0 {
			userSetup = true
		}
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]bool{
		"configured": configured,
		"user_setup": userSetup,
	})
}

func (s *Server) getDBConfig(w http.ResponseWriter, r *http.Request) {
	role, _ := s.getRoleAndVHosts(r)
	if role != storage.RoleAdministrator {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}

	// If not configured, return 404 to signal absence rather than 500
	if !config.IsDBConfigured() {
		s.jsonError(w, "database is not configured", http.StatusNotFound)
		return
	}

	cfg, err := config.LoadDBConfig()
	if err != nil {
		if os.IsNotExist(err) {
			s.jsonError(w, "database is not configured", http.StatusNotFound)
			return
		}
		// Avoid leaking internal details; respond with a generic message
		s.jsonError(w, "failed to load database configuration", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{
		"type": cfg.Type,
		"conn": maskDSN(cfg.Type, cfg.Conn),
	})
}

func (s *Server) saveDBConfig(w http.ResponseWriter, r *http.Request) {
	if !s.isFirstRun(r.Context()) {
		role, _ := s.getRoleAndVHosts(r)
		if role != storage.RoleAdministrator {
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}
	}
	var cfg config.DBConfig
	if err := json.NewDecoder(r.Body).Decode(&cfg); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	// Basic validation
	cfg.Type = strings.TrimSpace(cfg.Type)
	cfg.Conn = strings.TrimSpace(cfg.Conn)
	if cfg.Type == "" {
		s.jsonError(w, "database type is required", http.StatusBadRequest)
		return
	}
	if cfg.Conn == "" {
		s.jsonError(w, "connection string is required", http.StatusBadRequest)
		return
	}

	if cfg.JWTSecret == "" {
		if existing, err := config.LoadDBConfig(); err == nil {
			cfg.JWTSecret = existing.JWTSecret
		} else {
			cfg.JWTSecret = uuid.New().String()
		}
	}

	if len(strings.TrimSpace(cfg.CryptoMasterKey)) < 16 {
		http.Error(w, "crypto_master_key must be at least 16 characters", http.StatusBadRequest)
		return
	}

	// Proactively test connectivity first to avoid 500s on common misconfigurations
	{
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		var testErr error
		switch cfg.Type {
		case "sqlite":
			testErr = s.testSQLite(ctx, cfg.Conn)
		case "postgres":
			testErr = s.testPostgres(ctx, cfg.Conn)
		case "mysql", "mariadb":
			testErr = s.testMySQL(ctx, cfg.Conn)
		case "mongodb":
			testErr = s.testMongoDB(ctx, cfg.Conn)
		case "mssql":
			testErr = s.testMSSQL(ctx, cfg.Conn)
		default:
			s.jsonError(w, "unsupported database type", http.StatusBadRequest)
			return
		}
		if testErr != nil {
			// Return 400 with a clear message so the UI can inform the user
			s.jsonError(w, "failed to connect to database: "+testErr.Error(), http.StatusBadRequest)
			return
		}
	}

	// Persist configuration only after successful connectivity test
	if err := config.SaveDBConfig(&cfg); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	crypto.SetMasterKey(cfg.CryptoMasterKey)

	var newStore storage.Storage
	var err error
	if cfg.Type == "mongodb" {
		newStore, err = s.initMongoStorage(r.Context(), cfg.Conn)
	} else {
		newStore, err = s.initSQLStorage(r.Context(), cfg)
	}

	if err != nil {
		// Extremely unlikely after a successful connectivity test, but handle gracefully
		http.Error(w, "failed to initialize new storage: "+err.Error(), http.StatusInternalServerError)
		return
	}

	s.storeMu.Lock()
	s.storage = newStore
	s.storeMu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

func (s *Server) initMongoStorage(ctx context.Context, conn string) (storage.Storage, error) {
	client, err := mongo.Connect(ctx, options.Client().ApplyURI(conn))
	if err != nil {
		return nil, err
	}
	dbName := "hermod"
	if parts := strings.Split(conn, "/"); len(parts) > 3 {
		dbName = strings.Split(parts[3], "?")[0]
	}
	newStore := storagemongo.NewMongoStorage(client, dbName)
	if s_init, ok := newStore.(interface{ Init(context.Context) error }); ok {
		if err := s_init.Init(ctx); err != nil {
			return nil, err
		}
	}
	return newStore, nil
}

func (s *Server) initSQLStorage(ctx context.Context, cfg config.DBConfig) (storage.Storage, error) {
	driver := ""
	switch cfg.Type {
	case "sqlite":
		driver = "sqlite"
	case "postgres":
		driver = "pgx"
	case "mysql", "mariadb":
		driver = "mysql"
	case "mssql":
		driver = "sqlserver"
	default:
		return nil, fmt.Errorf("unsupported database type: %s", cfg.Type)
	}

	db, err := sql.Open(driver, cfg.Conn)
	if err != nil {
		return nil, err
	}
	if cfg.Type == "sqlite" {
		db.SetMaxOpenConns(1)
	}
	newStore := sqlstorage.NewSQLStorage(db, driver)
	if s_init, ok := newStore.(interface{ Init(context.Context) error }); ok {
		if err := s_init.Init(ctx); err != nil {
			return nil, err
		}
	}
	return newStore, nil
}

func (s *Server) testDBConfig(w http.ResponseWriter, r *http.Request) {
	if !s.isFirstRun(r.Context()) {
		role, _ := s.getRoleAndVHosts(r)
		if role != storage.RoleAdministrator {
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}
	}
	var cfg config.DBConfig
	if err := json.NewDecoder(r.Body).Decode(&cfg); err != nil {
		s.jsonError(w, "Failed to decode request body: "+err.Error(), http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	var err error
	switch cfg.Type {
	case "sqlite":
		err = s.testSQLite(ctx, cfg.Conn)
	case "postgres":
		err = s.testPostgres(ctx, cfg.Conn)
	case "mysql", "mariadb":
		err = s.testMySQL(ctx, cfg.Conn)
	case "mongodb":
		err = s.testMongoDB(ctx, cfg.Conn)
	case "mssql":
		err = s.testMSSQL(ctx, cfg.Conn)
	default:
		s.jsonError(w, "unsupported database type", http.StatusBadRequest)
		return
	}

	ok := (err == nil)
	errStr := ""
	if err != nil {
		errStr = err.Error()
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"ok":    ok,
		"error": errStr,
	})
}

func (s *Server) testSQLite(ctx context.Context, conn string) error {
	db, err := sql.Open("sqlite", conn)
	if err != nil {
		return err
	}
	defer db.Close()
	return db.PingContext(ctx)
}

func (s *Server) testPostgres(ctx context.Context, conn string) error {
	db, err := sql.Open("pgx", conn)
	if err != nil {
		return err
	}
	defer db.Close()
	return db.PingContext(ctx)
}

func (s *Server) testMySQL(ctx context.Context, conn string) error {
	db, err := sql.Open("mysql", conn)
	if err != nil {
		return err
	}
	defer db.Close()
	return db.PingContext(ctx)
}

func (s *Server) testMSSQL(ctx context.Context, conn string) error {
	db, err := sql.Open("sqlserver", conn)
	if err != nil {
		return err
	}
	defer db.Close()
	return db.PingContext(ctx)
}

func (s *Server) testMongoDB(ctx context.Context, conn string) error {
	client, err := mongo.Connect(ctx, options.Client().ApplyURI(conn))
	if err != nil {
		return err
	}
	defer func() { _ = client.Disconnect(context.Background()) }()
	return client.Ping(ctx, nil)
}

// listDatabases connects to the target server and returns available database names for supported types.
// Supported: postgres (pgx), mysql/mariadb, mongodb. For sqlite it returns an empty list.
func (s *Server) listDatabases(w http.ResponseWriter, r *http.Request) {
	if !s.isFirstRun(r.Context()) {
		role, _ := s.getRoleAndVHosts(r)
		if role != storage.RoleAdministrator {
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}
	}
	// List databases on a target server for setup wizard
	var req struct {
		Type string `json:"type"`
		Conn string `json:"conn"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.jsonError(w, "invalid request body", http.StatusBadRequest)
		return
	}
	req.Type = strings.TrimSpace(req.Type)
	req.Conn = strings.TrimSpace(req.Conn)
	if req.Type == "" {
		s.jsonError(w, "database type is required", http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	var dbs []string
	var err error

	switch req.Type {
	case "sqlite":
		// No concept of multiple databases
		dbs = []string{}
	case "postgres":
		var db *sql.DB
		db, err = sql.Open("pgx", req.Conn)
		if err == nil {
			defer db.Close()
			// Ensure connection works quickly
			if err = db.PingContext(ctx); err == nil {
				rows, qerr := db.QueryContext(ctx, "SELECT datname FROM pg_database WHERE datistemplate = false ORDER BY datname")
				if qerr != nil {
					err = qerr
				} else {
					defer rows.Close()
					for rows.Next() {
						var name string
						if scanErr := rows.Scan(&name); scanErr == nil {
							dbs = append(dbs, name)
						}
					}
					_ = rows.Err()
				}
			}
		}
	case "mysql", "mariadb":
		var db *sql.DB
		db, err = sql.Open("mysql", req.Conn)
		if err == nil {
			defer db.Close()
			if err = db.PingContext(ctx); err == nil {
				rows, qerr := db.QueryContext(ctx, "SHOW DATABASES")
				if qerr != nil {
					err = qerr
				} else {
					defer rows.Close()
					for rows.Next() {
						var name string
						if scanErr := rows.Scan(&name); scanErr == nil {
							// filter common system databases
							switch strings.ToLower(name) {
							case "information_schema", "performance_schema", "mysql", "sys":
								// skip
							default:
								dbs = append(dbs, name)
							}
						}
					}
					_ = rows.Err()
				}
			}
		}
	case "mongodb":
		var client *mongo.Client
		client, err = mongo.Connect(ctx, options.Client().ApplyURI(req.Conn))
		if err == nil {
			defer func() { _ = client.Disconnect(context.Background()) }()
			var names []string
			names, err = client.ListDatabaseNames(ctx, bson.D{})
			if err == nil {
				dbs = names
			}
		}
	default:
		s.jsonError(w, "unsupported database type", http.StatusBadRequest)
		return
	}

	if err != nil {
		s.jsonError(w, "failed to fetch databases: "+err.Error(), http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"databases": dbs,
	})
}

// finalizeInitialSetup performs the one-shot initial configuration.
// It is only allowed during first run (no users). If already configured, returns 401 Unauthorized.
func (s *Server) finalizeInitialSetup(w http.ResponseWriter, r *http.Request) {
	// Only allowed during first run
	if !s.isFirstRun(r.Context()) {
		s.jsonError(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	var req struct {
		DB struct {
			Type            string `json:"type"`
			Conn            string `json:"conn"`
			CryptoMasterKey string `json:"crypto_master_key"`
		} `json:"db"`
		Admin struct {
			Username string `json:"username"`
			Password string `json:"password"`
			FullName string `json:"full_name"`
			Email    string `json:"email"`
		} `json:"admin"`
		SMTP notification.NotificationSettings `json:"smtp"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.jsonError(w, "invalid request body", http.StatusBadRequest)
		return
	}

	// Basic validation
	dbType := strings.TrimSpace(req.DB.Type)
	dbConn := strings.TrimSpace(req.DB.Conn)
	if dbType == "" || dbConn == "" {
		s.jsonError(w, "database type and connection are required", http.StatusBadRequest)
		return
	}
	if len(strings.TrimSpace(req.DB.CryptoMasterKey)) < 16 {
		s.jsonError(w, "crypto_master_key must be at least 16 characters", http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(req.Admin.Username) == "" || strings.TrimSpace(req.Admin.Password) == "" {
		s.jsonError(w, "admin username and password are required", http.StatusBadRequest)
		return
	}

	// 2) Persist DB config and initialize storage
	cfg := config.DBConfig{Type: dbType, Conn: dbConn, CryptoMasterKey: req.DB.CryptoMasterKey}
	if cfg.JWTSecret == "" {
		cfg.JWTSecret = uuid.New().String()
	}
	if err := config.SaveDBConfig(&cfg); err != nil {
		s.jsonError(w, "failed to save database config: "+err.Error(), http.StatusInternalServerError)
		return
	}
	crypto.SetMasterKey(cfg.CryptoMasterKey)

	var newStore storage.Storage
	var err error
	if cfg.Type == "mongodb" {
		newStore, err = s.initMongoStorage(r.Context(), cfg.Conn)
	} else {
		newStore, err = s.initSQLStorage(r.Context(), cfg)
	}
	if err != nil {
		s.jsonError(w, "failed to initialize storage: "+err.Error(), http.StatusInternalServerError)
		return
	}
	s.storeMu.Lock()
	s.storage = newStore
	s.storeMu.Unlock()

	// 3) Create first admin user
	{
		hashed, _ := bcrypt.GenerateFromPassword([]byte(req.Admin.Password), bcrypt.DefaultCost)
		user := storage.User{
			ID:       uuid.New().String(),
			Username: strings.TrimSpace(req.Admin.Username),
			Password: string(hashed),
			FullName: strings.TrimSpace(req.Admin.FullName),
			Email:    strings.TrimSpace(req.Admin.Email),
			Role:     storage.RoleAdministrator,
		}
		uctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		if err := s.storage.CreateUser(uctx, user); err != nil {
			s.jsonError(w, "failed to create admin user: "+err.Error(), http.StatusInternalServerError)
			return
		}
	}

	// 4) Optionally save SMTP settings (if provided)
	if (req.SMTP != notification.NotificationSettings{}) {
		bytes, merr := json.Marshal(req.SMTP)
		if merr != nil {
			s.jsonError(w, "failed to serialize SMTP settings: "+merr.Error(), http.StatusInternalServerError)
			return
		}
		sctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		if err := s.storage.SaveSetting(sctx, "notification_settings", string(bytes)); err != nil {
			s.jsonError(w, "failed to save SMTP settings: "+err.Error(), http.StatusInternalServerError)
			return
		}
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

func (s *Server) getSettings(w http.ResponseWriter, r *http.Request) {
	role, _ := s.getRoleAndVHosts(r)
	if role != storage.RoleAdministrator {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}

	val, err := s.storage.GetSetting(r.Context(), "notification_settings")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if val == "" {
		val = "{}"
	}

	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(val))
}

func (s *Server) updateSettings(w http.ResponseWriter, r *http.Request) {
	role, _ := s.getRoleAndVHosts(r)
	if role != storage.RoleAdministrator {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}

	var settings map[string]interface{}
	if err := json.NewDecoder(r.Body).Decode(&settings); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	bytes, err := json.Marshal(settings)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if err := s.storage.SaveSetting(r.Context(), "notification_settings", string(bytes)); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) testNotificationSettings(w http.ResponseWriter, r *http.Request) {
	role, _ := s.getRoleAndVHosts(r)
	if role != storage.RoleAdministrator {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}

	val, err := s.storage.GetSetting(r.Context(), "notification_settings")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	var ns notification.NotificationSettings
	if val != "" {
		_ = json.Unmarshal([]byte(val), &ns)
	}

	results := ns.Test(r.Context())
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(results)
}

func (s *Server) testNotificationConfig(w http.ResponseWriter, r *http.Request) {
	if !s.isFirstRun(r.Context()) {
		role, _ := s.getRoleAndVHosts(r)
		if role != storage.RoleAdministrator {
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}
	}
	var ns notification.NotificationSettings
	if err := json.NewDecoder(r.Body).Decode(&ns); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	results := ns.Test(r.Context())
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(results)
}

func (s *Server) generateToken(w http.ResponseWriter, r *http.Request) {
	role, _ := s.getRoleAndVHosts(r)
	if role != storage.RoleAdministrator && role != storage.RoleEditor {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}

	var req struct {
		Length   *int   `json:"length"`
		Encoding string `json:"encoding"`
	}
	if r.Body != nil && r.ContentLength != 0 {
		_ = json.NewDecoder(r.Body).Decode(&req)
	}

	length := 32
	if req.Length != nil {
		length = *req.Length
	}
	if length < 8 {
		length = 8
	} else if length > 64 {
		length = 64
	}

	bytes := make([]byte, length)
	if _, err := rand.Read(bytes); err != nil {
		s.jsonError(w, "Failed to generate random bytes: "+err.Error(), http.StatusInternalServerError)
		return
	}

	token := ""
	switch strings.ToLower(req.Encoding) {
	case "hex":
		token = hex.EncodeToString(bytes)
	default:
		token = base64.RawURLEncoding.EncodeToString(bytes)
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"token": token,
	})
}

type BackupData struct {
	Sources   []storage.Source   `json:"sources"`
	Sinks     []storage.Sink     `json:"sinks"`
	Workflows []storage.Workflow `json:"workflows"`
	VHosts    []storage.VHost    `json:"vhosts"`
	Settings  map[string]string  `json:"settings"`
}

func (s *Server) exportConfig(w http.ResponseWriter, r *http.Request) {
	role, _ := s.getRoleAndVHosts(r)
	if role != storage.RoleAdministrator {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}

	ctx := r.Context()
	data := BackupData{
		Settings: make(map[string]string),
	}

	filter := storage.CommonFilter{Limit: 1000}
	data.Sources, _, _ = s.storage.ListSources(ctx, filter)
	data.Sinks, _, _ = s.storage.ListSinks(ctx, filter)
	data.Workflows, _, _ = s.storage.ListWorkflows(ctx, filter)
	data.VHosts, _, _ = s.storage.ListVHosts(ctx, filter)

	if val, err := s.storage.GetSetting(ctx, "notification_settings"); err == nil {
		data.Settings["notification_settings"] = val
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", "attachment; filename=hermod-config-backup.json")
	_ = json.NewEncoder(w).Encode(data)
}

func (s *Server) importConfig(w http.ResponseWriter, r *http.Request) {
	role, _ := s.getRoleAndVHosts(r)
	if role != storage.RoleAdministrator {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}

	var data BackupData
	if err := json.NewDecoder(r.Body).Decode(&data); err != nil {
		s.jsonError(w, "Invalid backup data: "+err.Error(), http.StatusBadRequest)
		return
	}

	ctx := r.Context()
	for _, v := range data.VHosts {
		if _, err := s.storage.GetVHost(ctx, v.ID); err != nil {
			_ = s.storage.CreateVHost(ctx, v)
		}
	}
	for _, src := range data.Sources {
		if _, err := s.storage.GetSource(ctx, src.ID); err != nil {
			_ = s.storage.CreateSource(ctx, src)
		} else {
			_ = s.storage.UpdateSource(ctx, src)
		}
	}
	for _, snk := range data.Sinks {
		if _, err := s.storage.GetSink(ctx, snk.ID); err != nil {
			_ = s.storage.CreateSink(ctx, snk)
		} else {
			_ = s.storage.UpdateSink(ctx, snk)
		}
	}
	for _, wf := range data.Workflows {
		if _, err := s.storage.GetWorkflow(ctx, wf.ID); err != nil {
			_ = s.storage.CreateWorkflow(ctx, wf)
		} else {
			_ = s.storage.UpdateWorkflow(ctx, wf)
		}
	}
	for k, v := range data.Settings {
		_ = s.storage.SaveSetting(ctx, k, v)
	}

	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) listAuditLogs(w http.ResponseWriter, r *http.Request) {
	role, _ := s.getRoleAndVHosts(r)
	if role != storage.RoleAdministrator {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}

	query := r.URL.Query()
	filter := storage.AuditFilter{
		CommonFilter: storage.CommonFilter{
			Limit: 50,
			Page:  1,
		},
		Action:     query.Get("action"),
		EntityType: query.Get("entity_type"),
		EntityID:   query.Get("entity_id"),
		UserID:     query.Get("user_id"),
	}

	if limitStr := query.Get("limit"); limitStr != "" {
		if l, err := strconv.Atoi(limitStr); err == nil {
			filter.Limit = l
		}
	}
	if pageStr := query.Get("page"); pageStr != "" {
		if p, err := strconv.Atoi(pageStr); err == nil {
			filter.Page = p
		}
	}
	if fromStr := query.Get("from"); fromStr != "" {
		if t, err := time.Parse(time.RFC3339, fromStr); err == nil {
			filter.From = &t
		}
	}
	if toStr := query.Get("to"); toStr != "" {
		if t, err := time.Parse(time.RFC3339, toStr); err == nil {
			filter.To = &t
		}
	}

	logs, total, err := s.storage.ListAuditLogs(r.Context(), filter)
	if err != nil {
		s.jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"items": logs,
		"total": total,
	})
}

func maskDSN(dbType string, conn string) string {
	if dbType == "sqlite" {
		return conn
	}

	if dbType == "mysql" || dbType == "mariadb" {
		// [username[:password]@][protocol[(address)]]/dbname[?param1=value1&...&paramN=valueN]
		if strings.Contains(conn, "@") {
			parts := strings.SplitN(conn, "@", 2)
			if strings.Contains(parts[0], ":") {
				sub := strings.SplitN(parts[0], ":", 2)
				return sub[0] + ":****@" + parts[1]
			}
		}
		return conn
	}

	u, err := url.Parse(conn)
	if err != nil {
		return conn
	}
	if u.User != nil {
		_, hasPass := u.User.Password()
		if hasPass {
			u.User = url.UserPassword(u.User.Username(), "****")
		}
	}
	return u.String()
}
