package api

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/user/hermod/internal/config"
	"github.com/user/hermod/internal/mesh"
	"github.com/user/hermod/internal/notification"
	"github.com/user/hermod/internal/storage"
	storagemongo "github.com/user/hermod/internal/storage/mongodb"
	sqlstorage "github.com/user/hermod/internal/storage/sql"
	"github.com/user/hermod/pkg/crypto"
	"github.com/user/hermod/pkg/filestorage"
	"github.com/user/hermod/pkg/secrets"
	"github.com/user/hermod/pkg/state"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
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
		"type":     cfg.Type,
		"conn":     maskDSN(cfg.Type, cfg.Conn),
		"log_type": cfg.LogType,
		"log_conn": maskDSN(cfg.LogType, cfg.LogConn),
	})
}

func (s *Server) getSecretConfig(w http.ResponseWriter, r *http.Request) {
	role, _ := s.getRoleAndVHosts(r)
	if role != storage.RoleAdministrator {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}

	cfg, err := config.LoadConfig("config.yaml")
	if err != nil {
		s.jsonError(w, "failed to load configuration", http.StatusInternalServerError)
		return
	}

	// Mask sensitive fields
	resp := cfg.Secrets
	if resp.Vault.Token != "" {
		resp.Vault.Token = "****"
	}
	if resp.OpenBao.Token != "" {
		resp.OpenBao.Token = "****"
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

func (s *Server) updateSecretConfig(w http.ResponseWriter, r *http.Request) {
	role, _ := s.getRoleAndVHosts(r)
	if role != storage.RoleAdministrator {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}

	var secretCfg secrets.Config
	if err := json.NewDecoder(r.Body).Decode(&secretCfg); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	cfg, err := config.LoadConfig("config.yaml")
	if err != nil {
		s.jsonError(w, "failed to load configuration", http.StatusInternalServerError)
		return
	}

	// Restore tokens if masked
	if secretCfg.Vault.Token == "****" {
		secretCfg.Vault.Token = cfg.Secrets.Vault.Token
	}
	if secretCfg.OpenBao.Token == "****" {
		secretCfg.OpenBao.Token = cfg.Secrets.OpenBao.Token
	}

	cfg.Secrets = secretCfg
	if err := config.SaveConfig("config.yaml", cfg); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Re-initialize secret manager in registry
	if secretCfg.Type != "" {
		if mgr, err := secrets.NewManager(r.Context(), secretCfg); err == nil {
			s.registry.SetSecretManager(mgr)
		}
	} else {
		// Use default EnvManager if disabled
		s.registry.SetSecretManager(&secrets.EnvManager{Prefix: "HERMOD_SECRET_"})
	}

	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) getStateStoreConfig(w http.ResponseWriter, r *http.Request) {
	role, _ := s.getRoleAndVHosts(r)
	if role != storage.RoleAdministrator {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}

	cfg, err := config.LoadConfig("config.yaml")
	if err != nil {
		s.jsonError(w, "failed to load configuration", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(cfg.StateStore)
}

func (s *Server) updateStateStoreConfig(w http.ResponseWriter, r *http.Request) {
	role, _ := s.getRoleAndVHosts(r)
	if role != storage.RoleAdministrator {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}

	var stateCfg config.StateStoreConfig
	if err := json.NewDecoder(r.Body).Decode(&stateCfg); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	cfg, err := config.LoadConfig("config.yaml")
	if err != nil {
		s.jsonError(w, "failed to load configuration", http.StatusInternalServerError)
		return
	}

	cfg.StateStore = stateCfg
	if err := config.SaveConfig("config.yaml", cfg); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Re-initialize state store in registry
	stateCfgPkg := state.Config{
		Type:     stateCfg.Type,
		Path:     stateCfg.Path,
		Address:  stateCfg.Address,
		Password: stateCfg.Password,
		DB:       stateCfg.DB,
		Prefix:   stateCfg.Prefix,
	}
	if ss, err := state.NewStateStore(stateCfgPkg); err == nil {
		s.registry.SetStateStore(ss)
	}

	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) getObservabilityConfig(w http.ResponseWriter, r *http.Request) {
	role, _ := s.getRoleAndVHosts(r)
	if role != storage.RoleAdministrator {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}

	cfg, err := config.LoadConfig("config.yaml")
	if err != nil {
		s.jsonError(w, "failed to load configuration", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(cfg.Observability)
}

func (s *Server) getFileStorageConfig(w http.ResponseWriter, r *http.Request) {
	role, _ := s.getRoleAndVHosts(r)
	if role != storage.RoleAdministrator {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}

	cfg, err := config.LoadConfig("config.yaml")
	if err != nil {
		s.jsonError(w, "failed to load configuration", http.StatusInternalServerError)
		return
	}

	// Mask secrets
	resp := cfg.FileStorage
	if resp.S3.AccessKeyID != "" {
		resp.S3.AccessKeyID = "****"
	}
	if resp.S3.SecretAccessKey != "" {
		resp.S3.SecretAccessKey = "****"
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

func (s *Server) updateFileStorageConfig(w http.ResponseWriter, r *http.Request) {
	role, _ := s.getRoleAndVHosts(r)
	if role != storage.RoleAdministrator {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}

	var fsCfg config.FileStorageConfig
	if err := json.NewDecoder(r.Body).Decode(&fsCfg); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	cfg, err := config.LoadConfig("config.yaml")
	if err != nil {
		s.jsonError(w, "failed to load configuration", http.StatusInternalServerError)
		return
	}

	// Restore secrets if masked
	if fsCfg.S3.AccessKeyID == "****" {
		fsCfg.S3.AccessKeyID = cfg.FileStorage.S3.AccessKeyID
	}
	if fsCfg.S3.SecretAccessKey == "****" {
		fsCfg.S3.SecretAccessKey = cfg.FileStorage.S3.SecretAccessKey
	}

	cfg.FileStorage = fsCfg
	if err := config.SaveConfig("config.yaml", cfg); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Reinitialize server file storage with new config
	if fs, err := filestorage.NewStorage(r.Context(), cfg.FileStorage); err == nil {
		s.fileStorage = fs
	}

	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) updateObservabilityConfig(w http.ResponseWriter, r *http.Request) {
	role, _ := s.getRoleAndVHosts(r)
	if role != storage.RoleAdministrator {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}

	var obsCfg config.ObservabilityConfig
	if err := json.NewDecoder(r.Body).Decode(&obsCfg); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	cfg, err := config.LoadConfig("config.yaml")
	if err != nil {
		s.jsonError(w, "failed to load configuration", http.StatusInternalServerError)
		return
	}

	cfg.Observability = obsCfg
	if err := config.SaveConfig("config.yaml", cfg); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Note: OTLP re-initialization usually requires a restart or complex SDK management.
	// For now, we inform the user that changes will take effect after restart.

	w.WriteHeader(http.StatusNoContent)
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
			s.jsonError(w, "failed to connect to primary database: "+testErr.Error(), http.StatusBadRequest)
			return
		}

		// Test logging database if configured
		if cfg.LogType != "" && cfg.LogConn != "" {
			var logTestErr error
			switch cfg.LogType {
			case "sqlite":
				logTestErr = s.testSQLite(ctx, cfg.LogConn)
			case "postgres":
				logTestErr = s.testPostgres(ctx, cfg.LogConn)
			case "mysql", "mariadb":
				logTestErr = s.testMySQL(ctx, cfg.LogConn)
			case "mongodb":
				logTestErr = s.testMongoDB(ctx, cfg.LogConn)
			case "mssql":
				logTestErr = s.testMSSQL(ctx, cfg.LogConn)
			default:
				s.jsonError(w, "unsupported logging database type: "+cfg.LogType, http.StatusBadRequest)
				return
			}
			if logTestErr != nil {
				s.jsonError(w, "failed to connect to logging database: "+logTestErr.Error(), http.StatusBadRequest)
				return
			}
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
		http.Error(w, "failed to initialize new primary storage: "+err.Error(), http.StatusInternalServerError)
		return
	}

	var newLogStore storage.Storage
	if cfg.LogType != "" && cfg.LogConn != "" {
		if cfg.LogType == "mongodb" {
			newLogStore, err = s.initMongoStorage(r.Context(), cfg.LogConn)
		} else {
			// Create a temporary DBConfig for initSQLStorage
			logCfg := config.DBConfig{
				Type: cfg.LogType,
				Conn: cfg.LogConn,
			}
			newLogStore, err = s.initSQLStorage(r.Context(), logCfg)
		}
		if err != nil {
			log.Printf("Warning: failed to initialize new logging storage: %v", err)
		}
	}

	s.storeMu.Lock()
	s.storage = newStore
	if newLogStore != nil {
		s.logStorage = newLogStore
	} else {
		s.logStorage = newStore
	}
	s.storeMu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

func (s *Server) initMongoStorage(ctx context.Context, conn string) (storage.Storage, error) {
	client, err := mongo.Connect(options.Client().ApplyURI(conn))
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
	client, err := mongo.Connect(options.Client().ApplyURI(conn))
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
		client, err = mongo.Connect(options.Client().ApplyURI(req.Conn))
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
		SMTP   notification.NotificationSettings `json:"smtp"`
		Config struct {
			Engine struct {
				MaxRetries        int    `json:"max_retries"`
				RetryInterval     string `json:"retry_interval"`
				ReconnectInterval string `json:"reconnect_interval"`
			} `json:"engine"`
			Buffer        config.BufferConfig        `json:"buffer"`
			Secrets       secrets.Config             `json:"secrets"`
			StateStore    config.StateStoreConfig    `json:"state_store"`
			Observability config.ObservabilityConfig `json:"observability"`
			Auth          config.AuthConfig          `json:"auth"`
			FileStorage   config.FileStorageConfig   `json:"file_storage"`
		} `json:"config"`
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
	s.logStorage = newStore
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

	// 5) Save platform config
	platformCfg := config.Config{
		Engine: config.EngineConfig{
			MaxRetries: req.Config.Engine.MaxRetries,
		},
		Buffer:        req.Config.Buffer,
		Secrets:       req.Config.Secrets,
		StateStore:    req.Config.StateStore,
		Observability: req.Config.Observability,
		Auth:          req.Config.Auth,
		FileStorage:   req.Config.FileStorage,
	}

	if req.Config.Engine.RetryInterval != "" {
		if d, err := time.ParseDuration(req.Config.Engine.RetryInterval); err == nil {
			platformCfg.Engine.RetryInterval = d
		}
	}
	if req.Config.Engine.ReconnectInterval != "" {
		if d, err := time.ParseDuration(req.Config.Engine.ReconnectInterval); err == nil {
			platformCfg.Engine.ReconnectInterval = d
		}
	}

	if err := config.SaveConfig("config.yaml", &platformCfg); err != nil {
		s.jsonError(w, "failed to save platform config: "+err.Error(), http.StatusInternalServerError)
		return
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

	var settings map[string]any
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
	Sources    []storage.Source    `json:"sources"`
	Sinks      []storage.Sink      `json:"sinks"`
	Workflows  []storage.Workflow  `json:"workflows"`
	Workspaces []storage.Workspace `json:"workspaces"`
	VHosts     []storage.VHost     `json:"vhosts"`
	Settings   map[string]string   `json:"settings"`
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
	data.Workspaces, _ = s.storage.ListWorkspaces(ctx)

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

func (s *Server) getMeshHealth(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	workers, _, err := s.storage.ListWorkers(ctx, storage.CommonFilter{})
	if err != nil {
		s.jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	type ClusterHealth struct {
		ID         string    `json:"id"`
		Name       string    `json:"name"`
		Status     string    `json:"status"`
		CPU        float64   `json:"cpu"`
		Memory     float64   `json:"memory"`
		LastSeen   time.Time `json:"last_seen"`
		Workflows  int       `json:"workflows"`
		ErrorCount int       `json:"error_count"`
		Type       string    `json:"type"` // "worker" or "cluster"
		Region     string    `json:"region,omitempty"`
		Endpoint   string    `json:"endpoint,omitempty"`
	}

	var health []ClusterHealth
	now := time.Now()

	// Fetch all workflows to count them per worker
	wfs, _, _ := s.storage.ListWorkflows(ctx, storage.CommonFilter{})
	workflowCounts := make(map[string]int)
	for _, wf := range wfs {
		if wf.Active && wf.WorkerID != "" {
			workflowCounts[wf.WorkerID]++
		}
	}

	for _, wrk := range workers {
		status := "online"
		if wrk.LastSeen == nil || now.Sub(*wrk.LastSeen) > 1*time.Minute {
			status = "offline"
		} else if now.Sub(*wrk.LastSeen) > 30*time.Second {
			status = "degraded"
		}

		lastSeen := time.Time{}
		if wrk.LastSeen != nil {
			lastSeen = *wrk.LastSeen
		}

		health = append(health, ClusterHealth{
			ID:        wrk.ID,
			Name:      wrk.Name,
			Status:    status,
			CPU:       wrk.CPUUsage,
			Memory:    wrk.MemoryUsage,
			LastSeen:  lastSeen,
			Workflows: workflowCounts[wrk.ID],
			Type:      "worker",
		})
	}

	// Add Mesh Clusters
	if s.registry != nil {
		mm := s.registry.GetMeshManager()
		if mm != nil {
			clusters := mm.GetClusters()
			for _, c := range clusters {
				health = append(health, ClusterHealth{
					ID:       c.ID,
					Name:     c.ID,
					Status:   c.Status,
					Type:     "cluster",
					Region:   c.Region,
					Endpoint: c.Endpoint,
				})
			}
		}
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(health)
}

func (s *Server) registerMeshCluster(w http.ResponseWriter, r *http.Request) {
	var req mesh.Cluster
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.jsonError(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	if req.ID == "" || req.Endpoint == "" {
		s.jsonError(w, "ID and Endpoint are required", http.StatusBadRequest)
		return
	}

	if s.registry == nil {
		s.jsonError(w, "Registry not initialized", http.StatusInternalServerError)
		return
	}

	mm := s.registry.GetMeshManager()
	if mm == nil {
		s.jsonError(w, "Mesh Manager not initialized", http.StatusInternalServerError)
		return
	}

	if req.Status == "" {
		req.Status = "online"
	}

	mm.RegisterCluster(req)
	w.WriteHeader(http.StatusCreated)
}

func (s *Server) getLineage(w http.ResponseWriter, r *http.Request) {
	lineage, err := s.storage.GetLineage(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(lineage)
}

func (s *Server) getDashboardLayout(w http.ResponseWriter, r *http.Request) {
	layout, err := s.storage.GetSetting(r.Context(), "dashboard_layout")
	if err != nil {
		// Default layout if not found
		layout = `[{"i":"stats","x":0,"y":0,"w":12,"h":2},{"i":"mps","x":0,"y":2,"w":8,"h":4},{"i":"workflows","x":8,"y":2,"w":4,"h":4},{"i":"logs","x":0,"y":6,"w":12,"h":4}]`
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(layout))
}

func (s *Server) saveDashboardLayout(w http.ResponseWriter, r *http.Request) {
	var layout json.RawMessage
	if err := json.NewDecoder(r.Body).Decode(&layout); err != nil {
		s.jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := s.storage.SaveSetting(r.Context(), "dashboard_layout", string(layout)); err != nil {
		s.jsonError(w, "Failed to save layout: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) bootstrapEnterpriseScenario(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// 1. Create Workspace
	ws := storage.Workspace{
		ID:          "prod-fulfillment",
		Name:        "Production: Global Fulfillment",
		Description: "Mission-critical workspace for global order processing and regional mesh routing.",
		CreatedAt:   time.Now(),
	}
	_ = s.storage.CreateWorkspace(ctx, ws)

	// 2. Create VHost if not exists
	_ = s.storage.CreateVHost(ctx, storage.VHost{
		ID:          "fulfillment",
		Name:        "fulfillment",
		Description: "VHost for fulfillment services",
	})

	// 3. Load Template
	templatePath := "examples/templates/global_fulfillment.json"
	data, err := os.ReadFile(templatePath)
	if err != nil {
		s.jsonError(w, "Failed to read scenario template: "+err.Error(), http.StatusInternalServerError)
		return
	}

	var template struct {
		Data storage.Workflow `json:"data"`
	}
	if err := json.Unmarshal(data, &template); err != nil {
		s.jsonError(w, "Failed to parse scenario template: "+err.Error(), http.StatusInternalServerError)
		return
	}

	wf := template.Data
	wf.ID = "fulfillment-scenario-" + uuid.New().String()[:8]
	wf.VHost = "fulfillment"

	// 4. Create Workflow
	if err := s.storage.CreateWorkflow(ctx, wf); err != nil {
		s.jsonError(w, "Failed to create scenario workflow: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// 5. Record Audit Log
	s.recordAuditLog(r, "INFO", "Bootstrapped Enterprise Scenario: "+wf.Name, "BOOTSTRAP", wf.ID, "workflow", wf.ID, wf)

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{
		"status":      "success",
		"workflow_id": wf.ID,
		"workspace":   ws.Name,
		"message":     "Enterprise scenario bootstrapped successfully.",
	})
}

func (s *Server) generateSDK(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Language string `json:"language"` // "go", "typescript"
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}

	var content string
	var filename string

	switch strings.ToLower(req.Language) {
	case "go":
		filename = "hermod_client.go"
		content = `package hermod

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
)

type Client struct {
	BaseURL string
	Token   string
}

func NewClient(baseURL, token string) *Client {
	return &Client{BaseURL: baseURL, Token: token}
}

func (c *Client) Publish(path string, data any) error {
	body, _ := json.Marshal(data)
	req, _ := http.NewRequest("POST", c.BaseURL+"/api/webhooks/"+path, bytes.NewBuffer(body))
	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.Header.Set("Content-Type", "application/json")
	
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("publish failed with status: %d", resp.StatusCode)
	}
	return nil
}
`
	case "typescript":
		filename = "hermod-client.ts"
		content = "export class HermodClient {\n" +
			"  constructor(private baseURL: string, private token: string) {}\n\n" +
			"  async publish(path: string, data: any): Promise<void> {\n" +
			"    const response = await fetch(`${this.baseURL}/api/webhooks/${path}`, {\n" +
			"      method: 'POST',\n" +
			"      headers: {\n" +
			"        'Authorization': `Bearer ${this.token}`,\n" +
			"        'Content-Type': 'application/json'\n" +
			"      },\n" +
			"      body: JSON.stringify(data)\n" +
			"    });\n\n" +
			"    if (!response.ok) {\n" +
			"      throw new Error(`Publish failed with status: ${response.status}`);\n" +
			"    }\n" +
			"  }\n" +
			"}\n"
	default:
		s.jsonError(w, "unsupported language: "+req.Language, http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Disposition", "attachment; filename="+filename)
	w.Header().Set("Content-Type", "application/octet-stream")
	_, _ = w.Write([]byte(content))
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

	logs, total, err := s.logStorage.ListAuditLogs(r.Context(), filter)
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
