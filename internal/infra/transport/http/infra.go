package http

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/gsoultan/hermod/internal/api/handlers"
	"github.com/gsoultan/hermod/internal/config"
	"github.com/gsoultan/hermod/internal/mesh"
	"github.com/gsoultan/hermod/internal/notification"
	"github.com/gsoultan/hermod/internal/storage"
	storagemongo "github.com/gsoultan/hermod/internal/storage/mongodb"
	pebblestorage "github.com/gsoultan/hermod/internal/storage/pebble"
	sqlstorage "github.com/gsoultan/hermod/internal/storage/sql"
	"github.com/gsoultan/hermod/pkg/infra/filestorage"
	"github.com/gsoultan/hermod/pkg/infra/state"
	"github.com/gsoultan/hermod/pkg/security/crypto"
	"github.com/gsoultan/hermod/pkg/security/secrets"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"golang.org/x/crypto/bcrypt"
)

func (h *InfraHandler) HandleLiveness(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("OK"))
}

func (h *InfraHandler) RegisterInfrastructureRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/config/status", h.GetConfigStatus)
	mux.HandleFunc("GET /api/config/secrets", h.GetSecretConfig)
	mux.HandleFunc("PUT /api/config/secrets", h.UpdateSecretConfig)
	mux.HandleFunc("GET /api/config/state", h.GetStateStoreConfig)
	mux.HandleFunc("PUT /api/config/state", h.UpdateStateStoreConfig)
	mux.HandleFunc("GET /api/config/observability", h.GetObservabilityConfig)
	mux.HandleFunc("PUT /api/config/observability", h.UpdateObservabilityConfig)
	mux.HandleFunc("GET /api/config/storage", h.GetFileStorageConfig)
	mux.HandleFunc("PUT /api/config/storage", h.UpdateFileStorageConfig)
	mux.HandleFunc("POST /api/config/database", h.SaveDBConfig)
	mux.HandleFunc("POST /api/config/database/test", h.TestDBConfig)
	// List databases on a target server for setup wizard
	mux.HandleFunc("POST /api/config/databases", h.ListDatabases)
	// One-shot initial setup endpoint (first run only)
	mux.HandleFunc("POST /api/config/setup", h.FinalizeInitialSetup)
	mux.HandleFunc("PUT /api/config/crypto", h.UpdateCryptoMasterKey)
	mux.HandleFunc("GET /api/settings", h.GetSettings)
	mux.HandleFunc("PUT /api/settings", h.UpdateSettings)
	mux.HandleFunc("POST /api/settings/test", h.TestNotificationSettings)
	mux.HandleFunc("POST /api/settings/test-config", h.TestNotificationConfig)
	// Utilities
	mux.HandleFunc("POST /api/utils/token", h.GenerateToken)
	// Prefill DB settings & test notifications
	mux.HandleFunc("GET /api/config/database", h.GetDBConfig)

	mux.HandleFunc("GET /api/backup/export", h.ExportConfig)
	mux.HandleFunc("POST /api/backup/import", h.ImportConfig)
	// Infrastructure & Mesh
	mux.HandleFunc("GET /api/infra/mesh-health", h.GetMeshHealth)
	mux.HandleFunc("GET /api/infra/lineage", h.GetLineage)
	mux.HandleFunc("POST /api/mesh/clusters", h.RegisterMeshCluster)
	mux.HandleFunc("/api/audit-logs", h.ListAuditLogs)
}

func (h *InfraHandler) RegisterSchemaRoutes(mux *http.ServeMux) {
	// Delegated to SchemaHandler
}

func (h *InfraHandler) UpdateCryptoMasterKey(w http.ResponseWriter, r *http.Request) {
	role, _ := h.GetRoleAndVHosts(r)
	if role != storage.RoleAdministrator {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}

	if !config.IsDBConfigured() {
		http.Error(w, "database is not configured", http.StatusBadRequest)
		return
	}

	var req struct {
		CryptoMasterKey string `json:"crypto_master_key"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	key := strings.TrimSpace(req.CryptoMasterKey)
	if len(key) < 16 {
		http.Error(w, "crypto_master_key must be at least 16 characters", http.StatusBadRequest)
		return
	}

	cfg, err := config.LoadDBConfig()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	// Re-encrypt every stored credential under the new key *before* the key is
	// installed or persisted.
	//
	// This used to save the key and return 204. Everything already in the
	// database — every source and sink password, API key and service-account
	// document — stayed encrypted under the old key, and there was no longer a
	// key that could open it. The failure was not even loud: the storage layer
	// passed the unopened ciphertext to connectors as though it were the
	// plaintext, so the operator saw authentication errors against their own
	// databases and nothing tying them to the rotation.
	//
	// Order matters. ReEncryptSecrets writes the new ciphertext while the old
	// key is still in force, so if it fails, nothing has changed and the running
	// system is untouched.
	if h.Storage != nil {
		if err := h.Storage.ReEncryptSecrets(r.Context(), key); err != nil {
			http.Error(w, "cannot rotate the master key: "+err.Error()+
				"; no key was changed and no data was modified", http.StatusInternalServerError)
			return
		}
	}

	cfg.CryptoMasterKey = key
	if err := config.SaveDBConfig(cfg); err != nil {
		// The data is already under the new key but the key was not persisted.
		// Install it in memory anyway so the running process stays consistent
		// with its own database, and tell the operator to fix the file, because
		// the next restart will come up with the old key and fail to decrypt.
		crypto.SetMasterKey(key)
		http.Error(w, "secrets were re-encrypted with the new key but it could not be saved: "+
			err.Error()+"; set crypto_master_key in db_config.yaml before restarting",
			http.StatusInternalServerError)
		return
	}

	crypto.SetMasterKey(key)

	w.WriteHeader(http.StatusNoContent)
}

func (h *InfraHandler) GetConfigStatus(w http.ResponseWriter, r *http.Request) {
	configured := config.IsDBConfigured()
	userSetup := false

	if configured {
		if h.Storage == nil {
			userSetup = true // Assume setup done if configured but DB down
		} else {
			users, _, err := h.Storage.ListUsers(r.Context(), storage.CommonFilter{Limit: 1})
			if err == nil {
				userSetup = len(users) > 0
			} else {
				userSetup = true // DB error: assume setup done for safety
			}
		}
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]bool{
		"configured": configured,
		"user_setup": userSetup,
	})
}

func (h *InfraHandler) GetDBConfig(w http.ResponseWriter, r *http.Request) {
	role, _ := h.GetRoleAndVHosts(r)
	if role != storage.RoleAdministrator {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}

	// If not configured, return 404 to signal absence rather than 500
	if !config.IsDBConfigured() {
		h.JsonError(w, "database is not configured", http.StatusNotFound)
		return
	}

	cfg, err := config.LoadDBConfig()
	if err != nil {
		if os.IsNotExist(err) {
			h.JsonError(w, "database is not configured", http.StatusNotFound)
			return
		}
		// Avoid leaking internal details; respond with a generic message
		h.JsonError(w, "failed to load database configuration", http.StatusInternalServerError)
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

// loadConfigOrEmpty reads the configuration file, treating "it does not exist
// yet" as an empty configuration rather than an error.
//
// A fresh instance has no config.yaml — it is written the first time someone
// saves settings. Reading it with config.LoadConfig therefore failed with
// ENOENT, and all eight settings handlers turned that into a 500, so the first
// thing a new administrator saw on the Settings page was a red "failed to load
// configuration" toast. The page whose whole purpose is to create that file
// was reporting its absence as a server fault, and the save handlers could not
// write the first config either, because they load before they store.
//
// Any other read or parse error is still an error. A config.yaml that exists
// and cannot be parsed is a real problem, and silently substituting defaults
// would let the next save overwrite a file somebody meant to keep.
func loadConfigOrEmpty(path string) (*config.Config, error) {
	cfg, err := config.LoadConfig(path)
	if err == nil {
		return cfg, nil
	}
	if errors.Is(err, fs.ErrNotExist) {
		return &config.Config{}, nil
	}
	return nil, err
}

func (h *InfraHandler) GetSecretConfig(w http.ResponseWriter, r *http.Request) {
	role, _ := h.GetRoleAndVHosts(r)
	if role != storage.RoleAdministrator {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}

	cfg, err := loadConfigOrEmpty(h.ConfigPath)
	if err != nil {
		h.JsonError(w, "failed to load configuration", http.StatusInternalServerError)
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

func (h *InfraHandler) UpdateSecretConfig(w http.ResponseWriter, r *http.Request) {
	role, _ := h.GetRoleAndVHosts(r)
	if role != storage.RoleAdministrator {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}

	var secretCfg secrets.Config
	if err := json.NewDecoder(r.Body).Decode(&secretCfg); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	cfg, err := loadConfigOrEmpty(h.ConfigPath)
	if err != nil {
		h.JsonError(w, "failed to load configuration", http.StatusInternalServerError)
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
	if err := config.SaveConfig(h.ConfigPath, cfg); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Re-initialize secret manager in registry
	if secretCfg.Type != "" {
		if mgr, err := secrets.NewManager(r.Context(), secretCfg); err == nil {
			h.Registry.SetSecretManager(mgr)
		}
	} else {
		// Use default EnvManager if disabled
		h.Registry.SetSecretManager(&secrets.EnvManager{Prefix: "HERMOD_SECRET_"})
	}

	w.WriteHeader(http.StatusNoContent)
}

func (h *InfraHandler) GetStateStoreConfig(w http.ResponseWriter, r *http.Request) {
	role, _ := h.GetRoleAndVHosts(r)
	if role != storage.RoleAdministrator {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}

	cfg, err := loadConfigOrEmpty(h.ConfigPath)
	if err != nil {
		h.JsonError(w, "failed to load configuration", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(cfg.StateStore)
}

func (h *InfraHandler) UpdateStateStoreConfig(w http.ResponseWriter, r *http.Request) {
	role, _ := h.GetRoleAndVHosts(r)
	if role != storage.RoleAdministrator {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}

	var stateCfg config.StateStoreConfig
	if err := json.NewDecoder(r.Body).Decode(&stateCfg); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	cfg, err := loadConfigOrEmpty(h.ConfigPath)
	if err != nil {
		h.JsonError(w, "failed to load configuration", http.StatusInternalServerError)
		return
	}

	cfg.StateStore = stateCfg
	if err := config.SaveConfig(h.ConfigPath, cfg); err != nil {
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
		h.Registry.SetStateStore(ss)
	}

	w.WriteHeader(http.StatusNoContent)
}

func (h *InfraHandler) GetObservabilityConfig(w http.ResponseWriter, r *http.Request) {
	role, _ := h.GetRoleAndVHosts(r)
	if role != storage.RoleAdministrator {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}

	cfg, err := loadConfigOrEmpty(h.ConfigPath)
	if err != nil {
		h.JsonError(w, "failed to load configuration", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(cfg.Observability)
}

func (h *InfraHandler) GetFileStorageConfig(w http.ResponseWriter, r *http.Request) {
	role, _ := h.GetRoleAndVHosts(r)
	if role != storage.RoleAdministrator {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}

	cfg, err := loadConfigOrEmpty(h.ConfigPath)
	if err != nil {
		h.JsonError(w, "failed to load configuration", http.StatusInternalServerError)
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

func (h *InfraHandler) UpdateFileStorageConfig(w http.ResponseWriter, r *http.Request) {
	role, _ := h.GetRoleAndVHosts(r)
	if role != storage.RoleAdministrator {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}

	var fsCfg config.FileStorageConfig
	if err := json.NewDecoder(r.Body).Decode(&fsCfg); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	cfg, err := loadConfigOrEmpty(h.ConfigPath)
	if err != nil {
		h.JsonError(w, "failed to load configuration", http.StatusInternalServerError)
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
	if err := config.SaveConfig(h.ConfigPath, cfg); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Reinitialize server file storage with new config
	if fs, err := filestorage.NewStorage(r.Context(), cfg.FileStorage); err == nil {
		h.FileStorage = fs
	}

	w.WriteHeader(http.StatusNoContent)
}

func (h *InfraHandler) UpdateObservabilityConfig(w http.ResponseWriter, r *http.Request) {
	role, _ := h.GetRoleAndVHosts(r)
	if role != storage.RoleAdministrator {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}

	var obsCfg config.ObservabilityConfig
	if err := json.NewDecoder(r.Body).Decode(&obsCfg); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	cfg, err := loadConfigOrEmpty(h.ConfigPath)
	if err != nil {
		h.JsonError(w, "failed to load configuration", http.StatusInternalServerError)
		return
	}

	cfg.Observability = obsCfg
	if err := config.SaveConfig(h.ConfigPath, cfg); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Note: OTLP re-initialization usually requires a restart or complex SDK management.
	// For now, we inform the user that changes will take effect after restart.

	w.WriteHeader(http.StatusNoContent)
}

func (h *InfraHandler) SaveDBConfig(w http.ResponseWriter, r *http.Request) {
	if !h.IsFirstRun(r.Context()) {
		role, _ := h.GetRoleAndVHosts(r)
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
		h.JsonError(w, "database type is required", http.StatusBadRequest)
		return
	}
	if cfg.Conn == "" {
		h.JsonError(w, "connection string is required", http.StatusBadRequest)
		return
	}

	if cfg.JWTSecret == "" {
		if existing, err := config.LoadDBConfig(); err == nil && strings.TrimSpace(existing.JWTSecret) != "" {
			cfg.JWTSecret = existing.JWTSecret
		} else {
			secret, gerr := generateJWTSecret()
			if gerr != nil {
				h.JsonError(w, "failed to generate JWT secret", http.StatusInternalServerError)
				return
			}
			cfg.JWTSecret = secret
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
			testErr = h.TestSQLite(ctx, cfg.Conn)
		case "postgres":
			testErr = h.TestPostgres(ctx, cfg.Conn)
		case "mysql", "mariadb":
			testErr = h.TestMySQL(ctx, cfg.Conn)
		case "mongodb":
			testErr = h.TestMongoDB(ctx, cfg.Conn)
		case "pebble":
			testErr = errors.New("pebble is only supported for logging database")
		case "mssql":
			testErr = h.TestMSSQL(ctx, cfg.Conn)
		default:
			h.JsonError(w, "unsupported database type", http.StatusBadRequest)
			return
		}
		if testErr != nil {
			// Return 400 with a clear message so the UI can inform the user.
			// Sanitize the error to avoid leaking the database host/IP and port.
			h.JsonError(w, "failed to connect to primary database: "+handlers.SanitizeDBError(testErr), http.StatusBadRequest)
			return
		}

		// Test logging database if configured
		if cfg.LogType != "" && cfg.LogConn != "" {
			var logTestErr error
			switch cfg.LogType {
			case "sqlite":
				logTestErr = h.TestSQLite(ctx, cfg.LogConn)
			case "postgres":
				logTestErr = h.TestPostgres(ctx, cfg.LogConn)
			case "mysql", "mariadb":
				logTestErr = h.TestMySQL(ctx, cfg.LogConn)
			case "mongodb":
				logTestErr = h.TestMongoDB(ctx, cfg.LogConn)
			case "pebble":
				logTestErr = h.TestPebble(ctx, cfg.LogConn)
			case "mssql":
				logTestErr = h.TestMSSQL(ctx, cfg.LogConn)
			default:
				h.JsonError(w, "unsupported logging database type: "+cfg.LogType, http.StatusBadRequest)
				return
			}
			if logTestErr != nil {
				h.JsonError(w, "failed to connect to logging database: "+handlers.SanitizeDBError(logTestErr), http.StatusBadRequest)
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
		newStore, err = h.InitMongoStorage(r.Context(), cfg.Conn)
	} else {
		newStore, err = h.InitSQLStorage(r.Context(), cfg)
	}

	if err != nil {
		// Extremely unlikely after a successful connectivity test, but handle gracefully.
		// Sanitize to avoid leaking the database host/IP and port.
		h.JsonError(w, "failed to initialize new primary storage: "+handlers.SanitizeDBError(err), http.StatusInternalServerError)
		return
	}

	var newLogStore storage.Storage
	if cfg.LogType != "" && cfg.LogConn != "" {
		switch cfg.LogType {
		case "mongodb":
			newLogStore, err = h.InitMongoStorage(r.Context(), cfg.LogConn)
		case "pebble":
			newLogStore, err = h.InitPebbleStorage(r.Context(), cfg.LogConn)
		default:
			// Create a temporary DBConfig for initSQLStorage
			logCfg := config.DBConfig{
				Type: cfg.LogType,
				Conn: cfg.LogConn,
			}
			newLogStore, err = h.InitSQLStorage(r.Context(), logCfg)
		}
		if err != nil {
			h.Registry.GetLogger().Warn("Failed to initialize new logging storage", "error", err)
		}
	}

	h.StoreMu.Lock()
	oldStore := h.Storage
	oldLogStore := h.LogStorage
	h.Storage = newStore
	if newLogStore != nil {
		h.LogStorage = newLogStore
	} else {
		h.LogStorage = newStore
	}
	h.StoreMu.Unlock()

	// Close old storages after a small delay to ensure no in-flight requests are using them
	go func() {
		time.Sleep(1 * time.Second)
		if oldStore != nil {
			if closer, ok := oldStore.(interface{ Close() error }); ok {
				_ = closer.Close()
			}
		}
		// Only close oldLogStore if it's different from oldStore
		if oldLogStore != nil && oldLogStore != oldStore {
			if closer, ok := oldLogStore.(interface{ Close() error }); ok {
				_ = closer.Close()
			}
		}
	}()

	// Update Registry to use new storage
	if h.Registry != nil {
		h.Registry.SetStorage(newStore)
		if newLogStore != nil {
			h.Registry.SetLogStorage(newLogStore)
		} else {
			h.Registry.SetLogStorage(newStore)
		}
	}

	// Update Worker to use new storage
	if h.Worker != nil {
		h.Worker.SetStorage(newStore)
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

func (h *InfraHandler) InitMongoStorage(ctx context.Context, conn string) (storage.Storage, error) {
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

func (h *InfraHandler) InitPebbleStorage(ctx context.Context, path string) (storage.Storage, error) {
	newStore, err := pebblestorage.NewPebbleStorage(path)
	if err != nil {
		return nil, err
	}
	if s_init, ok := newStore.(interface{ Init(context.Context) error }); ok {
		if err := s_init.Init(ctx); err != nil {
			return nil, err
		}
	}
	return newStore, nil
}

func (h *InfraHandler) InitSQLStorage(ctx context.Context, cfg config.DBConfig) (storage.Storage, error) {
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

func (h *InfraHandler) TestDBConfig(w http.ResponseWriter, r *http.Request) {
	if !h.IsFirstRun(r.Context()) {
		role, _ := h.GetRoleAndVHosts(r)
		if role != storage.RoleAdministrator {
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}
	}
	var cfg config.DBConfig
	if err := json.NewDecoder(r.Body).Decode(&cfg); err != nil {
		h.JsonError(w, "Failed to decode request body: "+err.Error(), http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	var err error
	switch cfg.Type {
	case "sqlite":
		err = h.TestSQLite(ctx, cfg.Conn)
	case "postgres":
		err = h.TestPostgres(ctx, cfg.Conn)
	case "mysql", "mariadb":
		err = h.TestMySQL(ctx, cfg.Conn)
	case "mongodb":
		err = h.TestMongoDB(ctx, cfg.Conn)
	case "pebble":
		err = h.TestPebble(ctx, cfg.Conn)
	case "mssql":
		err = h.TestMSSQL(ctx, cfg.Conn)
	default:
		h.JsonError(w, "unsupported database type", http.StatusBadRequest)
		return
	}

	ok := (err == nil)
	errStr := ""
	if err != nil {
		// Strip host/IP and port from the error so connection details are not leaked.
		errStr = handlers.SanitizeDBError(err)
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"ok":    ok,
		"error": errStr,
	})
}

func (h *InfraHandler) TestSQLite(ctx context.Context, conn string) error {
	db, err := sql.Open("sqlite", conn)
	if err != nil {
		return err
	}
	defer db.Close()
	return db.PingContext(ctx)
}

func (h *InfraHandler) TestPostgres(ctx context.Context, conn string) error {
	db, err := sql.Open("pgx", conn)
	if err != nil {
		return err
	}
	defer db.Close()
	return db.PingContext(ctx)
}

func (h *InfraHandler) TestMySQL(ctx context.Context, conn string) error {
	db, err := sql.Open("mysql", conn)
	if err != nil {
		return err
	}
	defer db.Close()
	return db.PingContext(ctx)
}

func (h *InfraHandler) TestMSSQL(ctx context.Context, conn string) error {
	db, err := sql.Open("sqlserver", conn)
	if err != nil {
		return err
	}
	defer db.Close()
	return db.PingContext(ctx)
}

func (h *InfraHandler) TestMongoDB(ctx context.Context, conn string) error {
	client, err := mongo.Connect(options.Client().ApplyURI(conn))
	if err != nil {
		return err
	}
	return client.Ping(ctx, nil)
}

func (h *InfraHandler) TestPebble(ctx context.Context, path string) error {
	// We check if Pebble can be opened (it will create the directory if it doesn't exist).
	// Since we don't want to leave it open, we use a temporary storage instance.
	store, err := pebblestorage.NewPebbleStorage(path)
	if err != nil {
		return err
	}
	if closer, ok := store.(interface{ Close() error }); ok {
		defer closer.Close()
	}
	return nil
}

// listDatabases connects to the target server and returns available database names for supported types.
// Supported: postgres (pgx), mysql/mariadb, mongodb. For sqlite it returns an empty list.
func (h *InfraHandler) ListDatabases(w http.ResponseWriter, r *http.Request) {
	if !h.IsFirstRun(r.Context()) {
		role, _ := h.GetRoleAndVHosts(r)
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
		h.JsonError(w, "invalid request body", http.StatusBadRequest)
		return
	}
	req.Type = strings.TrimSpace(req.Type)
	req.Conn = strings.TrimSpace(req.Conn)
	if req.Type == "" {
		h.JsonError(w, "database type is required", http.StatusBadRequest)
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
		h.JsonError(w, "unsupported database type", http.StatusBadRequest)
		return
	}

	if err != nil {
		h.JsonError(w, "failed to fetch databases: "+handlers.SanitizeDBError(err), http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"databases": dbs,
	})
}

// finalizeInitialSetup performs the one-shot initial configuration.
// It is only allowed during first run (no users). If already configured, returns 401 Unauthorized.
func (h *InfraHandler) FinalizeInitialSetup(w http.ResponseWriter, r *http.Request) {
	// Only allowed during first run
	if !h.IsFirstRun(r.Context()) {
		h.JsonError(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	var req struct {
		DB struct {
			Type            string `json:"type"`
			Conn            string `json:"conn"`
			LogType         string `json:"log_type"`
			LogConn         string `json:"log_conn"`
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
				MaxInflight       int    `json:"max_inflight"`
				DrainTimeout      string `json:"drain_timeout"`
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
		h.JsonError(w, "invalid request body", http.StatusBadRequest)
		return
	}

	// Basic validation
	dbType := strings.TrimSpace(req.DB.Type)
	dbConn := strings.TrimSpace(req.DB.Conn)
	if dbType == "" || dbConn == "" {
		h.JsonError(w, "database type and connection are required", http.StatusBadRequest)
		return
	}
	if len(strings.TrimSpace(req.DB.CryptoMasterKey)) < 16 {
		h.JsonError(w, "crypto_master_key must be at least 16 characters", http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(req.Admin.Username) == "" || strings.TrimSpace(req.Admin.Password) == "" {
		h.JsonError(w, "admin username and password are required", http.StatusBadRequest)
		return
	}

	// 2) Persist DB config and initialize storage
	cfg := config.DBConfig{
		Type:            dbType,
		Conn:            dbConn,
		LogType:         req.DB.LogType,
		LogConn:         req.DB.LogConn,
		CryptoMasterKey: req.DB.CryptoMasterKey,
	}
	if cfg.JWTSecret == "" {
		secret, gerr := generateJWTSecret()
		if gerr != nil {
			h.JsonError(w, "failed to generate JWT secret", http.StatusInternalServerError)
			return
		}
		cfg.JWTSecret = secret
	}
	if err := config.SaveDBConfig(&cfg); err != nil {
		h.JsonError(w, "failed to save database config: "+err.Error(), http.StatusInternalServerError)
		return
	}
	crypto.SetMasterKey(cfg.CryptoMasterKey)

	var newStore storage.Storage
	var err error
	if cfg.Type == "mongodb" {
		newStore, err = h.InitMongoStorage(r.Context(), cfg.Conn)
	} else {
		newStore, err = h.InitSQLStorage(r.Context(), cfg)
	}
	if err != nil {
		h.JsonError(w, "failed to initialize storage: "+handlers.SanitizeDBError(err), http.StatusInternalServerError)
		return
	}
	h.StoreMu.Lock()
	h.Storage = newStore
	h.LogStorage = newStore
	h.StoreMu.Unlock()

	// Hand the new storage to the engine as well as to the handler.
	//
	// A first run has no database, so main.go builds the registry with nil
	// storage and does not start a worker (shouldStartWorker requires the
	// install to be complete at process start). Swapping only h.Storage above
	// left the registry holding nil: the install looked finished, the UI listed
	// workflows, and every attempt to start one failed with "registry storage is
	// not initialized" until somebody restarted the process. Nothing said so.
	//
	// The worker is nil on a first run — it is set here for the case where setup
	// is re-run against an instance that already has one, and so that a future
	// in-process worker start needs no change here.
	if h.Registry != nil {
		h.Registry.SetStorage(newStore)
	}
	if h.Worker != nil {
		h.Worker.SetStorage(newStore)
	}

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
		if err := h.Storage.CreateUser(uctx, user); err != nil {
			h.JsonError(w, "failed to create admin user: "+err.Error(), http.StatusInternalServerError)
			return
		}
	}

	// 4) Optionally save SMTP settings (if provided)
	if (req.SMTP != notification.NotificationSettings{}) {
		bytes, merr := json.Marshal(req.SMTP)
		if merr != nil {
			h.JsonError(w, "failed to serialize SMTP settings: "+merr.Error(), http.StatusInternalServerError)
			return
		}
		sctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		if err := h.Storage.SaveSetting(sctx, "notification_settings", string(bytes)); err != nil {
			h.JsonError(w, "failed to save SMTP settings: "+err.Error(), http.StatusInternalServerError)
			return
		}
	}

	// 5) Save platform config
	platformCfg := config.Config{
		Engine: config.EngineConfig{
			MaxRetries:  req.Config.Engine.MaxRetries,
			MaxInflight: req.Config.Engine.MaxInflight,
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
	if req.Config.Engine.DrainTimeout != "" {
		if d, err := time.ParseDuration(req.Config.Engine.DrainTimeout); err == nil {
			platformCfg.Engine.DrainTimeout = d
		}
	}

	if err := config.SaveConfig(h.ConfigPath, &platformCfg); err != nil {
		h.JsonError(w, "failed to save platform config: "+err.Error(), http.StatusInternalServerError)
		return
	}
	log.Printf("Platform configuration saved to %s", h.ConfigPath)

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

// generateJWTSecret returns a cryptographically secure 256-bit secret encoded
// as hex, suitable for signing HS256 session tokens.
func generateJWTSecret() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

func (h *InfraHandler) GetSettings(w http.ResponseWriter, r *http.Request) {
	role, _ := h.GetRoleAndVHosts(r)
	if role != storage.RoleAdministrator {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}

	val, err := h.Storage.GetSetting(r.Context(), "notification_settings")
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

func (h *InfraHandler) UpdateSettings(w http.ResponseWriter, r *http.Request) {
	role, _ := h.GetRoleAndVHosts(r)
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

	if err := h.Storage.SaveSetting(r.Context(), "notification_settings", string(bytes)); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (h *InfraHandler) TestNotificationSettings(w http.ResponseWriter, r *http.Request) {
	role, _ := h.GetRoleAndVHosts(r)
	if role != storage.RoleAdministrator {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}

	val, err := h.Storage.GetSetting(r.Context(), "notification_settings")
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

func (h *InfraHandler) TestNotificationConfig(w http.ResponseWriter, r *http.Request) {
	if !h.IsFirstRun(r.Context()) {
		role, _ := h.GetRoleAndVHosts(r)
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

func (h *InfraHandler) GenerateToken(w http.ResponseWriter, r *http.Request) {
	role, _ := h.GetRoleAndVHosts(r)
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
		h.JsonError(w, "Failed to generate random bytes: "+err.Error(), http.StatusInternalServerError)
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

// ErrExportTooLarge reports that a deployment holds more objects of one kind
// than an export carries. The export refuses rather than truncating: a backup
// missing its overflow is not partially useful, and the operator finds out
// during a restore, which is the one moment they cannot afford to.
type ErrExportTooLarge struct {
	Kind  string
	Total int
	Limit int
}

func (e *ErrExportTooLarge) Error() string {
	return fmt.Sprintf("this deployment has %d %s, more than the %d an export carries; "+
		"the backup was refused rather than silently truncated", e.Total, e.Kind, e.Limit)
}

// CollectBackup gathers everything a backup carries.
//
// It is shared by the download endpoint and the scheduled writer, so the two
// cannot drift: whatever the endpoint refuses to produce, the scheduler refuses
// to write. Every list surfaces its error rather than discarding it — an
// unreachable database used to produce a plausible-looking file full of empty
// arrays.
func (h *InfraHandler) CollectBackup(ctx context.Context) (BackupData, error) {
	data := BackupData{Settings: make(map[string]string)}
	filter := storage.CommonFilter{Limit: exportLimit}

	var total int
	var err error
	if data.Sources, total, err = h.Storage.ListSources(ctx, filter); err != nil {
		return data, fmt.Errorf("cannot export sources, so this backup would be incomplete: %w", err)
	} else if total > exportLimit {
		return data, &ErrExportTooLarge{Kind: "sources", Total: total, Limit: exportLimit}
	}
	if data.Sinks, total, err = h.Storage.ListSinks(ctx, filter); err != nil {
		return data, fmt.Errorf("cannot export sinks, so this backup would be incomplete: %w", err)
	} else if total > exportLimit {
		return data, &ErrExportTooLarge{Kind: "sinks", Total: total, Limit: exportLimit}
	}
	if data.Workflows, total, err = h.Storage.ListWorkflows(ctx, filter); err != nil {
		return data, fmt.Errorf("cannot export workflows, so this backup would be incomplete: %w", err)
	} else if total > exportLimit {
		return data, &ErrExportTooLarge{Kind: "workflows", Total: total, Limit: exportLimit}
	}
	if data.VHosts, total, err = h.Storage.ListVHosts(ctx, filter); err != nil {
		return data, fmt.Errorf("cannot export vhosts, so this backup would be incomplete: %w", err)
	} else if total > exportLimit {
		return data, &ErrExportTooLarge{Kind: "vhosts", Total: total, Limit: exportLimit}
	}
	if data.Workspaces, err = h.Storage.ListWorkspaces(ctx); err != nil {
		return data, fmt.Errorf("cannot export workspaces, so this backup would be incomplete: %w", err)
	}

	// A missing setting is not a failure; anything else is.
	if val, err := h.Storage.GetSetting(ctx, "notification_settings"); err == nil {
		data.Settings["notification_settings"] = val
	}
	return data, nil
}

func (h *InfraHandler) ExportConfig(w http.ResponseWriter, r *http.Request) {
	role, _ := h.GetRoleAndVHosts(r)
	if role != storage.RoleAdministrator {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}

	data, err := h.CollectBackup(r.Context())
	if err != nil {
		if tooLarge, ok := errors.AsType[*ErrExportTooLarge](err); ok {
			h.exportTruncated(w, tooLarge.Kind, tooLarge.Total)
			return
		}
		h.JsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Serialise before committing to a 200, so an encoding failure is not
	// written into the middle of a file the browser has already started saving.
	body, err := json.Marshal(data)
	if err != nil {
		h.JsonError(w, "cannot serialise the backup: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", "attachment; filename=hermod-config-backup.json")
	_, _ = w.Write(body)
}

// exportLimit bounds a single export. It is a real bound rather than a silent
// one: past it the export fails instead of truncating.
const exportLimit = 1000

func (h *InfraHandler) exportTruncated(w http.ResponseWriter, kind string, total int) {
	h.JsonError(w, fmt.Sprintf(
		"this deployment has %d %s but an export carries at most %d; the backup would be "+
			"silently incomplete, so it was refused", total, kind, exportLimit),
		http.StatusInternalServerError)
}

func (h *InfraHandler) ImportConfig(w http.ResponseWriter, r *http.Request) {
	role, _ := h.GetRoleAndVHosts(r)
	if role != storage.RoleAdministrator {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}

	var data BackupData
	if err := json.NewDecoder(r.Body).Decode(&data); err != nil {
		h.JsonError(w, "Invalid backup data: "+err.Error(), http.StatusBadRequest)
		return
	}

	ctx := r.Context()

	// Every write here used to be `_ = h.Storage.Create...(...)`, and the
	// handler finished with an unconditional 204. A restore into a database
	// that rejected every row reported success, and the operator — who is
	// running this because something has already gone wrong — would believe
	// their configuration was back.
	//
	// The restore still runs to completion rather than stopping at the first
	// failure: recovering most of a configuration is more useful than
	// recovering the prefix before the first bad row. What changes is that the
	// response says exactly what did not make it.
	var failures []string
	record := func(kind, id string, err error) {
		if err != nil {
			failures = append(failures, fmt.Sprintf("%s %s: %v", kind, id, err))
		}
	}

	for _, v := range data.VHosts {
		if _, err := h.Storage.GetVHost(ctx, v.ID); err != nil {
			record("vhost", v.ID, h.Storage.CreateVHost(ctx, v))
		}
	}
	for _, src := range data.Sources {
		if _, err := h.Storage.GetSource(ctx, src.ID); err != nil {
			record("source", src.ID, h.Storage.CreateSource(ctx, src))
		} else {
			record("source", src.ID, h.Storage.UpdateSource(ctx, src))
		}
	}
	for _, snk := range data.Sinks {
		if _, err := h.Storage.GetSink(ctx, snk.ID); err != nil {
			record("sink", snk.ID, h.Storage.CreateSink(ctx, snk))
		} else {
			record("sink", snk.ID, h.Storage.UpdateSink(ctx, snk))
		}
	}
	for _, wf := range data.Workflows {
		if _, err := h.Storage.GetWorkflow(ctx, wf.ID); err != nil {
			record("workflow", wf.ID, h.Storage.CreateWorkflow(ctx, wf))
		} else {
			record("workflow", wf.ID, h.Storage.UpdateWorkflow(ctx, wf))
		}
	}
	for k, v := range data.Settings {
		record("setting", k, h.Storage.SaveSetting(ctx, k, v))
	}

	if len(failures) > 0 {
		const shown = 10
		listed := failures
		suffix := ""
		if len(listed) > shown {
			listed = listed[:shown]
			suffix = fmt.Sprintf(" (and %d more)", len(failures)-shown)
		}
		h.JsonError(w, fmt.Sprintf("restore is incomplete: %d object(s) could not be written: %s%s",
			len(failures), strings.Join(listed, "; "), suffix), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (h *InfraHandler) GetMeshHealth(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	workers, _, err := h.Storage.ListWorkers(ctx, storage.CommonFilter{})
	if err != nil {
		h.JsonError(w, err.Error(), http.StatusInternalServerError)
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

	// Fetch all workflows to count them per worker
	wfs, _, _ := h.Storage.ListWorkflows(ctx, storage.CommonFilter{})
	workflowCounts := make(map[string]int)
	for _, wf := range wfs {
		if wf.Active && wf.WorkerID != "" {
			workflowCounts[wf.WorkerID]++
		}
	}

	for _, wrk := range workers {
		status := "online"
		if wrk.LastSeen == nil || time.Since(*wrk.LastSeen) > 1*time.Minute {
			status = "offline"
		} else if time.Since(*wrk.LastSeen) > 30*time.Second {
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
	if h.Registry != nil {
		mm := h.Registry.GetMeshManager()
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

func (h *InfraHandler) RegisterMeshCluster(w http.ResponseWriter, r *http.Request) {
	var req mesh.Cluster
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.JsonError(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	if req.ID == "" || req.Endpoint == "" {
		h.JsonError(w, "ID and Endpoint are required", http.StatusBadRequest)
		return
	}

	if h.Registry == nil {
		h.JsonError(w, "Registry not initialized", http.StatusInternalServerError)
		return
	}

	mm := h.Registry.GetMeshManager()
	if mm == nil {
		h.JsonError(w, "Mesh Manager not initialized", http.StatusInternalServerError)
		return
	}

	if req.Status == "" {
		req.Status = "online"
	}

	if err := mm.RegisterCluster(r.Context(), req); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusCreated)
}

func (h *InfraHandler) GetLineage(w http.ResponseWriter, r *http.Request) {
	lineage, err := h.Storage.GetLineage(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(lineage)
}

func (h *InfraHandler) GetDashboardLayout(w http.ResponseWriter, r *http.Request) {
	layout, err := h.Storage.GetSetting(r.Context(), "dashboard_layout")
	if err != nil {
		// Default layout if not found
		layout = `[{"i":"stats","x":0,"y":0,"w":12,"h":2},{"i":"mps","x":0,"y":2,"w":8,"h":4},{"i":"workflows","x":8,"y":2,"w":4,"h":4},{"i":"logs","x":0,"y":6,"w":12,"h":4}]`
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(layout))
}

func (h *InfraHandler) SaveDashboardLayout(w http.ResponseWriter, r *http.Request) {
	var layout json.RawMessage
	if err := json.NewDecoder(r.Body).Decode(&layout); err != nil {
		h.JsonError(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := h.Storage.SaveSetting(r.Context(), "dashboard_layout", string(layout)); err != nil {
		h.JsonError(w, "Failed to save layout: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *InfraHandler) BootstrapEnterpriseScenario(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// 1. Create Workspace
	ws := storage.Workspace{
		ID:          "prod-fulfillment",
		Name:        "Production: Global Fulfillment",
		Description: "Mission-critical workspace for global order processing and regional mesh routing.",
		CreatedAt:   time.Now(),
	}
	_ = h.Storage.CreateWorkspace(ctx, ws)

	// 2. Create VHost if not exists
	_ = h.Storage.CreateVHost(ctx, storage.VHost{
		ID:          "fulfillment",
		Name:        "fulfillment",
		Description: "VHost for fulfillment services",
	})

	// 3. Load Template
	templatePath := "examples/templates/global_fulfillment.json"
	data, err := os.ReadFile(templatePath)
	if err != nil {
		h.JsonError(w, "Failed to read scenario template: "+err.Error(), http.StatusInternalServerError)
		return
	}

	var template struct {
		Data storage.Workflow `json:"data"`
	}
	if err := json.Unmarshal(data, &template); err != nil {
		h.JsonError(w, "Failed to parse scenario template: "+err.Error(), http.StatusInternalServerError)
		return
	}

	wf := template.Data
	wf.ID = "fulfillment-scenario-" + uuid.New().String()[:8]
	wf.VHost = "fulfillment"

	// 4. Create Workflow
	if err := h.Storage.CreateWorkflow(ctx, wf); err != nil {
		h.JsonError(w, "Failed to create scenario workflow: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// 5. Record Audit Log
	h.RecordAuditLog(r, "INFO", "Bootstrapped Enterprise Scenario: "+wf.Name, "BOOTSTRAP", wf.ID, "workflow", wf.ID, wf)

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{
		"status":      "success",
		"workflow_id": wf.ID,
		"workspace":   ws.Name,
		"message":     "Enterprise scenario bootstrapped successfully.",
	})
}

func (h *InfraHandler) GenerateSDK(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Language string `json:"language"` // "go", "typescript"
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.JsonError(w, err.Error(), http.StatusBadRequest)
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
		h.JsonError(w, "unsupported language: "+req.Language, http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Disposition", "attachment; filename="+filename)
	w.Header().Set("Content-Type", "application/octet-stream")
	_, _ = w.Write([]byte(content))
}

func (h *InfraHandler) ListAuditLogs(w http.ResponseWriter, r *http.Request) {
	role, _ := h.GetRoleAndVHosts(r)
	if role != storage.RoleAdministrator {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}

	query := r.URL.Query()
	filter := storage.AuditFilter{
		Limit:      50,
		Page:       1,
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

	logs, total, err := h.LogStorage.ListAuditLogs(r.Context(), filter)
	if err != nil {
		h.JsonError(w, err.Error(), http.StatusInternalServerError)
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
