package main

import (
	"context"
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	_ "github.com/go-sql-driver/mysql"
	"github.com/google/uuid"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/user/hermod/internal/ai"
	"github.com/user/hermod/internal/api"
	"github.com/user/hermod/internal/autoscaler"
	"github.com/user/hermod/internal/config"
	"github.com/user/hermod/internal/engine"
	"github.com/user/hermod/internal/observability"
	"github.com/user/hermod/internal/service"
	"github.com/user/hermod/internal/storage"
	storagemongo "github.com/user/hermod/internal/storage/mongodb"
	storagesql "github.com/user/hermod/internal/storage/sql"
	"github.com/user/hermod/internal/version"
	"github.com/user/hermod/pkg/crypto"
	enginePkg "github.com/user/hermod/pkg/engine"
	"github.com/user/hermod/pkg/secrets"
	"github.com/user/hermod/pkg/state"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"gopkg.in/yaml.v3"
	_ "modernc.org/sqlite"
)

func main() {
	mode := flag.String("mode", "standalone", "running mode: standalone, api, worker")
	workerID := flag.Int("worker-id", 0, "ID of the worker (0 to total-workers-1)")
	totalWorkers := flag.Int("total-workers", 1, "total number of workers for sharding")
	workerGUID := flag.String("worker-guid", "", "GUID of the worker for explicit assignment")
	workerToken := flag.String("worker-token", "", "Security token for the worker")
	platformURL := flag.String("platform-url", "", "URL of the Hermod platform API (e.g., http://localhost:8080)")
	workerHost := flag.String("worker-host", "localhost", "host of the worker for self-registration")
	workerPort := flag.Int("worker-port", 3000, "port of the worker for self-registration")
	workerDescription := flag.String("worker-description", "", "description of the worker for self-registration")
	workerIdentityPath := flag.String("worker-identity", "worker.yaml", "path to persist worker identity (id/token)")
	initWorker := flag.Bool("init-worker", false, "initialize/register a worker locally (DB mode) and save identity, then exit")
	buildUI := flag.Bool("build-ui", false, "build UI before starting")
	port := flag.Int("port", 4000, "port for API server")
	grpcPort := flag.Int("grpc-port", 50051, "port for gRPC server")
	dbType := flag.String("db-type", "sqlite", "database type: sqlite, postgres, mysql, mariadb, mongodb")
	dbConn := flag.String("db-conn", "hermod.db", "database connection string")
	masterKey := flag.String("master-key", "", "Master key for encryption (32 bytes)")
	versionFlag := flag.Bool("version", false, "Print the version and exit")
	serviceAction := flag.String("service", "", "Service action: install, uninstall, start, stop, restart, status")
	flag.Parse()
	if *versionFlag {
		fmt.Printf("hermod %s\n", version.Version)
		return
	}

	runFunc := func() {
		// Environment fallbacks to simplify production configuration
		// Only apply when corresponding flag keeps its default value.
		if v := os.Getenv("HERMOD_MODE"); v != "" && *mode == "standalone" {
			*mode = v
		}
		if v := os.Getenv("HERMOD_WORKER_GUID"); v != "" && *workerGUID == "" {
			*workerGUID = v
		}
		if v := os.Getenv("HERMOD_WORKER_TOKEN"); v != "" && *workerToken == "" {
			*workerToken = v
		}
		if v := os.Getenv("HERMOD_PLATFORM_URL"); v != "" && *platformURL == "" {
			*platformURL = v
		}
		if v := os.Getenv("HERMOD_WORKER_HOST"); v != "" && *workerHost == "localhost" {
			*workerHost = v
		}
		if v := os.Getenv("HERMOD_WORKER_PORT"); v != "" && *workerPort == 3000 {
			if p, err := strconv.Atoi(v); err == nil {
				*workerPort = p
			}
		}
		if v := os.Getenv("HERMOD_WORKER_DESCRIPTION"); v != "" && *workerDescription == "" {
			*workerDescription = v
		}
		if v := os.Getenv("HERMOD_WORKER_IDENTITY"); v != "" && *workerIdentityPath == "worker.yaml" {
			*workerIdentityPath = v
		}
		if v := os.Getenv("HERMOD_WORKER_ID"); v != "" && *workerID == 0 {
			if id, err := strconv.Atoi(v); err == nil {
				*workerID = id
			}
		}
		if v := os.Getenv("HERMOD_TOTAL_WORKERS"); v != "" && *totalWorkers == 1 {
			if tw, err := strconv.Atoi(v); err == nil {
				*totalWorkers = tw
			}
		}

		// Set master key from environment or flag
		if *masterKey != "" {
			crypto.SetMasterKey(*masterKey)
		} else if envKey := os.Getenv("HERMOD_MASTER_KEY"); envKey != "" {
			crypto.SetMasterKey(envKey)
		}

		// Only build the UI when explicitly requested. Avoid implicit builds on startup
		// to prevent long startup times due to npm install/build.
		if *buildUI {
			if err := config.BuildUI(); err != nil {
				log.Fatalf("Failed to build UI: %v", err)
			}
			if *mode == "build-only" {
				fmt.Println("UI build complete. Exiting due to build-only mode.")
				os.Exit(0)
			}
		}

		if *mode == "api" || *mode == "worker" || *mode == "standalone" {
			var store storage.Storage
			var logStore storage.Storage
			dbTypeVal := *dbType
			dbConnVal := *dbConn
			logTypeVal := ""
			logConnVal := ""

			// Check if config file exists
			if config.IsDBConfigured() {
				cfg, err := config.LoadDBConfig()
				if err == nil {
					dbTypeVal = cfg.Type
					dbConnVal = cfg.Conn
					logTypeVal = cfg.LogType
					logConnVal = cfg.LogConn
					if cfg.CryptoMasterKey != "" && *masterKey == "" && os.Getenv("HERMOD_MASTER_KEY") == "" {
						crypto.SetMasterKey(cfg.CryptoMasterKey)
					}
				}
			}

			firstRun := !config.IsDBConfigured()

			if *mode == "worker" && *platformURL != "" {
				// Worker mode with platform URL doesn't need direct DB access
				fmt.Printf("Starting Hermod worker connecting to platform at %s...\n", *platformURL)
			} else {
				if firstRun {
					// Do not open any database on first run to avoid creating hermod.db implicitly.
					fmt.Println("First run detected: starting API without database connection. Proceed to /setup to configure.")
				} else {
					var err error
					fmt.Println("Opening primary database ...")
					store, err = initStorage(dbTypeVal, dbConnVal)
					if err != nil {
						log.Printf("Warning: Failed to initialize primary storage: %v", err)
					}

					if logTypeVal != "" && logConnVal != "" {
						fmt.Println("Opening logging database ...")
						logStore, err = initStorage(logTypeVal, logConnVal)
						if err != nil {
							log.Printf("Warning: Failed to initialize logging storage: %v", err)
						}
					}
				}
			}

			registry := engine.NewRegistry(store, logStore)
			logger := enginePkg.NewDefaultLogger()
			registry.SetLogger(logger)

			// Load engine config
			cfg, err := config.LoadConfig("config.yaml")
			if err != nil {
				// Provide a default config if file is missing
				cfg = &config.Config{}
				log.Printf("Warning: Using default config because config.yaml could not be loaded: %v", err)
			}

			if cfg != nil {
				// Initialize OTLP if configured
				if cfg.Observability.OTLP.Endpoint != "" {
					if cfg.Observability.OTLP.ServiceName == "" {
						cfg.Observability.OTLP.ServiceName = "hermod"
					}
					if shutdown, err := observability.InitOTLP(context.Background(), cfg.Observability.OTLP); err == nil {
						defer shutdown(context.Background())
						fmt.Printf("OTLP observability initialized: %s (service: %s)\n", cfg.Observability.OTLP.Endpoint, cfg.Observability.OTLP.ServiceName)
					} else {
						log.Printf("Warning: Failed to initialize OTLP: %v", err)
					}
				}

				// Initialize secret manager if configured
				if cfg.Secrets.Type != "" {
					if mgr, err := secrets.NewManager(context.Background(), cfg.Secrets); err == nil {
						registry.SetSecretManager(mgr)
						fmt.Printf("Secret manager initialized: %s\n", cfg.Secrets.Type)
					} else {
						log.Printf("Warning: Failed to initialize secret manager: %v", err)
					}
				}

				// Initialize state store if configured
				if cfg.StateStore.Type != "" {
					stateCfg := state.Config{
						Type:     cfg.StateStore.Type,
						Path:     cfg.StateStore.Path,
						Address:  cfg.StateStore.Address,
						Password: cfg.StateStore.Password,
						DB:       cfg.StateStore.DB,
						Prefix:   cfg.StateStore.Prefix,
					}
					if ss, err := state.NewStateStore(stateCfg); err == nil {
						registry.SetStateStore(ss)
						fmt.Printf("State store initialized: %s\n", cfg.StateStore.Type)
					} else {
						log.Printf("Warning: Failed to initialize state store: %v", err)
					}
				}

				engCfg := enginePkg.DefaultConfig()
				if cfg.Engine.MaxRetries > 0 {
					engCfg.MaxRetries = cfg.Engine.MaxRetries
				}
				if cfg.Engine.RetryInterval > 0 {
					engCfg.RetryInterval = cfg.Engine.RetryInterval
				}
				if cfg.Engine.ReconnectInterval > 0 {
					engCfg.ReconnectInterval = cfg.Engine.ReconnectInterval
				}
				if cfg.Engine.MaxInflight > 0 {
					engCfg.MaxInflight = cfg.Engine.MaxInflight
				}
				if cfg.Engine.DrainTimeout > 0 {
					engCfg.DrainTimeout = cfg.Engine.DrainTimeout
				}
				registry.SetConfig(engCfg)
			}

			// Graceful shutdown
			ctx, cancel := context.WithCancel(context.Background())
			sigChan := make(chan os.Signal, 1)
			signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

			go func() {
				sig := <-sigChan
				fmt.Printf("\nReceived signal %v, shutting down gracefully...\n", sig)
				cancel()
				registry.StopAll()
			}()

			// Determine setup status (first run vs already configured)
			configured, userSetup := computeSetupStatus(ctx, store, config.IsDBConfigured())

			if !configured || !userSetup {
				// First-time setup: always start API, but avoid starting worker to prevent unintended processing.
				fmt.Println("First-time setup detected. Starting API only. Open http://localhost:" + strconv.Itoa(*port) + "/setup to configure Hermod.")
			}

			// Start worker if not in api-only mode and setup is completed
			if (*mode == "worker" || *mode == "standalone") && configured && userSetup {
				var workerStore engine.WorkerStorage = store
				if *mode == "worker" && *platformURL != "" {
					workerStore = engine.NewWorkerAPIClient(*platformURL, *workerToken)
				}

				// Auto-load or create worker identity if missing and not using remote platform URL
				if (*workerGUID == "" || *workerToken == "") && *platformURL == "" {
					// Try to load from identity file first
					type workerIdentity struct {
						ID          string `yaml:"id"`
						Token       string `yaml:"token"`
						Name        string `yaml:"name"`
						Host        string `yaml:"host"`
						Port        int    `yaml:"port"`
						Description string `yaml:"description"`
					}

					loaded := false
					if b, err := os.ReadFile(*workerIdentityPath); err == nil {
						var wi workerIdentity
						if yamlErr := yaml.Unmarshal(b, &wi); yamlErr == nil && wi.ID != "" && wi.Token != "" {
							*workerGUID = wi.ID
							*workerToken = wi.Token
							if wi.Name != "" {
								// Set as default registration values if provided
								*workerDescription = wi.Description
								*workerHost = wi.Host
								if wi.Port != 0 {
									*workerPort = wi.Port
								}
							}
							loaded = true
							fmt.Printf("Loaded worker identity from %s (id=%s)\n", *workerIdentityPath, wi.ID)
						}
					}

					// If not loaded, create in local storage
					if !loaded && store != nil {
						id := uuid.New().String()
						token := uuid.New().String()
						hostname := id
						if hn, err := os.Hostname(); err == nil && hn != "" {
							hostname = hn
						}
						// Persist in DB first
						if err := store.CreateWorker(ctx, storage.Worker{
							ID:          id,
							Name:        hostname,
							Host:        *workerHost,
							Port:        *workerPort,
							Description: *workerDescription,
							Token:       token,
						}); err == nil {
							*workerGUID = id
							*workerToken = token
							wi := workerIdentity{ID: id, Token: token, Name: hostname, Host: *workerHost, Port: *workerPort, Description: *workerDescription}
							if data, err := yaml.Marshal(&wi); err == nil {
								// Ensure directory exists if provided in path
								_ = os.MkdirAll(filepath.Dir(*workerIdentityPath), 0o755)
								_ = os.WriteFile(*workerIdentityPath, data, 0o600)
								fmt.Printf("Saved worker identity to %s\n", *workerIdentityPath)
							}
						} else {
							log.Printf("Warning: failed to auto-create worker identity: %v", err)
						}
					}
				}

				// Optional one-shot init and exit (local DB only)
				if *initWorker {
					if *platformURL != "" {
						log.Fatal("-init-worker is only supported in local DB mode (no platform-url). Please create the worker via the Hermod UI and copy its token.")
					}
					if *workerGUID == "" || *workerToken == "" {
						log.Fatal("Failed to initialize worker: missing GUID or token. Ensure database is configured.")
					}
					fmt.Printf("Worker initialized.\nID: %s\nToken: %s\nIdentity file: %s\n", *workerGUID, *workerToken, *workerIdentityPath)
					fmt.Printf("Set environment:\n  HERMOD_WORKER_GUID=%s\n  HERMOD_WORKER_TOKEN=%s\n", *workerGUID, *workerToken)
					os.Exit(0)
				}

				worker := engine.NewWorker(workerStore, registry)
				worker.SetWorkerConfig(*workerID, *totalWorkers, *workerGUID, *workerToken)

				// Default registration info: prefer hostname if name is empty
				name := *workerGUID
				if hn, err := os.Hostname(); err == nil && hn != "" {
					name = hn
				}
				worker.SetRegistrationInfo(name, *workerHost, *workerPort, *workerDescription)

				go func() {
					if err := worker.Start(ctx); err != nil {
						if !errors.Is(err, context.Canceled) {
							log.Printf("Worker failed: %v", err)
						}
					}
				}()
			}

			// Start API if not in worker-only mode (always start API in first-time setup)
			if *mode == "api" || *mode == "standalone" {
				aiSvc := ai.NewSelfHealingService(logger)

				server := api.NewServer(registry, store, cfg, aiSvc, logStore)
				storageName := dbTypeVal
				if firstRun {
					storageName = "unconfigured"
				}
				fmt.Printf("Starting Hermod API server on :%d using %s storage...\n", *port, storageName)

				// Start Autoscaler in control-plane mode
				if (*mode == "api" || *mode == "standalone") && configured && userSetup {
					manager := &autoscaler.KubernetesWorkerManager{
						Namespace:  "hermod",
						Deployment: "hermod-worker",
						Storage:    store,
					}
					as := autoscaler.NewAutoscaler(store, manager)
					as.Start()
					fmt.Println("Autoscaler service started")
					defer as.Stop()
				}

				httpServer := &http.Server{
					Addr:    fmt.Sprintf(":%d", *port),
					Handler: server.Routes(),
				}

				go func() {
					if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
						log.Printf("API server failed: %v", err)
					}
				}()

				go func() {
					if err := server.StartGRPC(fmt.Sprintf(":%d", *grpcPort)); err != nil {
						log.Printf("gRPC server failed: %v", err)
					}
				}()

				<-ctx.Done()
				fmt.Println("Shutting down API server...")
				_ = httpServer.Shutdown(context.Background())
			} else {
				// Worker only mode
				if configured && userSetup {
					fmt.Printf("Starting Hermod worker using %s storage...\n", dbTypeVal)
					<-ctx.Done()
				} else {
					// In worker-only mode without setup, we cannot proceed. Keep process alive to allow external configuration.
					fmt.Println("Hermod is not configured yet. Please run API mode to complete setup. Exiting.")
					os.Exit(1)
				}
			}

			fmt.Println("Hermod shutdown complete")
			return
		}

		log.Fatalf("Invalid mode: %s. Supported modes: standalone, api, worker", *mode)
	}

	// Service management
	svcCfg := service.Config{
		Name:        "hermod",
		DisplayName: "Hermod Messaging System",
		Description: "Hermod is a high-performance messaging and workflow automation platform.",
	}

	// Filter out -service flag from arguments for the service
	var filteredArgs []string
	for i := 1; i < len(os.Args); i++ {
		arg := os.Args[i]
		if strings.HasPrefix(arg, "-service=") || arg == "-service" || arg == "--service" {
			if arg == "-service" || arg == "--service" {
				i++ // Skip value
			}
			continue
		}
		filteredArgs = append(filteredArgs, arg)
	}
	svcCfg.Arguments = filteredArgs

	if err := service.Manage(svcCfg, *serviceAction, runFunc); err != nil {
		log.Fatal(err)
	}
}

// computeSetupStatus determines whether Hermod is configured and whether at least one user exists.
// configuredFlag should reflect persistent configuration status (db_config.yaml presence).
// When configuredFlag is false, userSetup is always false.
// userLister is the minimal subset needed from storage for setup detection.
type userLister interface {
	ListUsers(ctx context.Context, filter storage.CommonFilter) ([]storage.User, int, error)
}

func computeSetupStatus(ctx context.Context, store userLister, configuredFlag bool) (configured bool, userSetup bool) {
	configured = configuredFlag
	if !configured || store == nil {
		return configured, false
	}
	users, _, err := store.ListUsers(ctx, storage.CommonFilter{Limit: 1})
	if err != nil {
		return configured, false
	}
	return configured, len(users) > 0
}

func initStorage(dbType, dbConn string) (storage.Storage, error) {
	driver := ""
	var store storage.Storage

	switch dbType {
	case "sqlite":
		driver = "sqlite"
		if !strings.Contains(dbConn, "?") {
			busy := os.Getenv("HERMOD_SQLITE_BUSY_TIMEOUT_MS")
			if busy == "" {
				busy = "2000"
			}
			dbConn += fmt.Sprintf("?_pragma=journal_mode(WAL)&_pragma=busy_timeout(%s)&_pragma=synchronous(NORMAL)&_pragma=foreign_keys(ON)", busy)
		}
	case "postgres":
		driver = "pgx"
	case "mysql", "mariadb":
		driver = "mysql"
	case "mongodb":
		client, err := mongo.Connect(options.Client().ApplyURI(dbConn))
		if err != nil {
			return nil, fmt.Errorf("failed to connect to MongoDB: %v", err)
		}
		dbName := "hermod"
		if parts := strings.Split(dbConn, "/"); len(parts) > 3 {
			dbName = strings.Split(parts[3], "?")[0]
		}
		store = storagemongo.NewMongoStorage(client, dbName)
	default:
		return nil, fmt.Errorf("unsupported database type: %s", dbType)
	}

	if dbType != "mongodb" {
		db, err := sql.Open(driver, dbConn)
		if err != nil {
			return nil, fmt.Errorf("failed to open database: %v", err)
		}

		if dbType == "sqlite" {
			db.SetMaxOpenConns(4)
			db.SetMaxIdleConns(1)
		} else {
			// Conservative pool defaults for API/storage DB
			db.SetMaxOpenConns(20)
			db.SetMaxIdleConns(10)
			db.SetConnMaxIdleTime(60 * time.Second)
		}

		store = storagesql.NewSQLStorage(db, driver)
	}

	if s, ok := store.(interface{ Init(context.Context) error }); ok {
		initTimeoutMs := 5000
		if v := os.Getenv("HERMOD_STORAGE_INIT_TIMEOUT_MS"); v != "" {
			if n, err := strconv.Atoi(v); err == nil && n >= 0 {
				initTimeoutMs = n
			}
		}
		var initCtx context.Context = context.Background()
		var cancel context.CancelFunc = func() {}
		if initTimeoutMs > 0 {
			initCtx, cancel = context.WithTimeout(context.Background(), time.Duration(initTimeoutMs)*time.Millisecond)
		}
		defer cancel()
		if err := s.Init(initCtx); err != nil {
			return store, fmt.Errorf("failed to initialize storage: %v", err)
		}
	}

	return store, nil
}
