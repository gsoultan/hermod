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
	"strconv"
	"strings"
	"syscall"
	"time"

	_ "github.com/go-sql-driver/mysql"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/user/hermod/internal/api"
	"github.com/user/hermod/internal/config"
	"github.com/user/hermod/internal/engine"
	"github.com/user/hermod/internal/storage"
	storagemongo "github.com/user/hermod/internal/storage/mongodb"
	storagesql "github.com/user/hermod/internal/storage/sql"
	"github.com/user/hermod/pkg/crypto"
	enginePkg "github.com/user/hermod/pkg/engine"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
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
	buildUI := flag.Bool("build-ui", false, "build UI before starting")
	port := flag.Int("port", 4000, "port for API server")
	grpcPort := flag.Int("grpc-port", 50051, "port for gRPC server")
	dbType := flag.String("db-type", "sqlite", "database type: sqlite, postgres, mysql, mariadb, mongodb")
	dbConn := flag.String("db-conn", "hermod.db", "database connection string")
	masterKey := flag.String("master-key", "", "Master key for encryption (32 bytes)")
	flag.Parse()

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
	}

	if *mode == "api" || *mode == "worker" || *mode == "standalone" {
		var store storage.Storage
		dbTypeVal := *dbType
		dbConnVal := *dbConn

		// Check if config file exists
		if config.IsDBConfigured() {
			cfg, err := config.LoadDBConfig()
			if err == nil {
				dbTypeVal = cfg.Type
				dbConnVal = cfg.Conn
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
				driver := ""
				switch dbTypeVal {
				case "sqlite":
					driver = "sqlite"
					if !strings.Contains(dbConnVal, "?") {
						// Use a modest default busy_timeout to avoid long startup stalls when the DB is locked.
						// Can be overridden via HERMOD_SQLITE_BUSY_TIMEOUT_MS.
						busy := os.Getenv("HERMOD_SQLITE_BUSY_TIMEOUT_MS")
						if busy == "" {
							busy = "2000"
						}
						dbConnVal += fmt.Sprintf("?_pragma=journal_mode(WAL)&_pragma=busy_timeout(%s)&_pragma=synchronous(NORMAL)&_pragma=foreign_keys(ON)", busy)
					}
				case "postgres":
					driver = "pgx"
				case "mysql", "mariadb":
					driver = "mysql"
				case "mongodb":
					// Handle MongoDB separately
					client, err := mongo.Connect(context.Background(), options.Client().ApplyURI(dbConnVal))
					if err != nil {
						log.Fatalf("Failed to connect to MongoDB: %v", err)
					}
					dbName := "hermod"
					if parts := strings.Split(dbConnVal, "/"); len(parts) > 3 {
						dbName = strings.Split(parts[3], "?")[0]
					}
					store = storagemongo.NewMongoStorage(client, dbName)
				default:
					log.Fatalf("Unsupported database type: %s", dbTypeVal)
				}

				if dbTypeVal != "mongodb" {
					startOpen := time.Now()
					fmt.Println("Opening database ...")
					db, err := sql.Open(driver, dbConnVal)
					if err != nil {
						log.Fatalf("Failed to open database: %v", err)
					}
					fmt.Printf("Database opened in %v\n", time.Since(startOpen))

					if dbTypeVal == "sqlite" {
						// Allow limited concurrency with WAL and busy timeout to reduce request queuing
						// while still being safe for SQLite.
						db.SetMaxOpenConns(4)
						db.SetMaxIdleConns(1)
					}

					store = storagesql.NewSQLStorage(db, driver)
				}

				if s, ok := store.(interface{ Init(context.Context) error }); ok {
					startInit := time.Now()
					fmt.Println("Initializing storage ...")
					// Apply a timeout to storage initialization to avoid long startup stalls.
					// Default to a 5s timeout to avoid long stalls on locked or slow SQLite init.
					// Override with HERMOD_STORAGE_INIT_TIMEOUT_MS (set to 0 to disable timeout).
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
					if err := s.Init(initCtx); err != nil {
						cancel()
						// If initialization fails, we might still want to start the API
						// so the user can configure a valid DB.
						log.Printf("Warning: Failed to initialize storage: %v", err)
					} else {
						cancel()
					}
					fmt.Printf("Storage initialized in %v\n", time.Since(startInit))
				}
			}
		}

		registry := engine.NewRegistry(store)

		// Load engine config
		if cfg, err := config.LoadConfig("config.yaml"); err == nil {
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

			worker := engine.NewWorker(workerStore, registry)
			worker.SetWorkerConfig(*workerID, *totalWorkers, *workerGUID, *workerToken)

			// Use GUID as default name
			name := *workerGUID
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
			server := api.NewServer(registry, store)
			storageName := dbTypeVal
			if firstRun {
				storageName = "unconfigured"
			}
			fmt.Printf("Starting Hermod API server on :%d using %s storage...\n", *port, storageName)

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
				return
			}
		}

		fmt.Println("Hermod shutdown complete")
		return
	}

	log.Fatalf("Invalid mode: %s. Supported modes: standalone, api, worker", *mode)
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
