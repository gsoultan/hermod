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
	"syscall"

	_ "github.com/go-sql-driver/mysql"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/user/hermod/internal/api"
	"github.com/user/hermod/internal/config"
	"github.com/user/hermod/internal/engine"
	"github.com/user/hermod/internal/storage"
	storagesql "github.com/user/hermod/internal/storage/sql"
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
	workerPort := flag.Int("worker-port", 8080, "port of the worker for self-registration")
	workerDescription := flag.String("worker-description", "", "description of the worker for self-registration")
	buildUI := flag.Bool("build-ui", false, "build UI before starting")
	port := flag.Int("port", 8080, "port for API server")
	dbType := flag.String("db-type", "sqlite", "database type: sqlite, postgres, mysql, mariadb")
	dbConn := flag.String("db-conn", "hermod.db", "database connection string")
	flag.Parse()

	if *buildUI || !config.IsUIBuilt() {
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
			}
		}

		if *mode == "worker" && *platformURL != "" {
			// Worker mode with platform URL doesn't need direct DB access
			fmt.Printf("Starting Hermod worker connecting to platform at %s...\n", *platformURL)
		} else {
			driver := ""
			switch dbTypeVal {
			case "sqlite":
				driver = "sqlite"
			case "postgres":
				driver = "pgx"
			case "mysql", "mariadb":
				driver = "mysql"
			default:
				log.Fatalf("Unsupported database type: %s", dbTypeVal)
			}

			db, err := sql.Open(driver, dbConnVal)
			if err != nil {
				log.Fatalf("Failed to open database: %v", err)
			}

			store = storagesql.NewSQLStorage(db)
			if s, ok := store.(interface{ Init(context.Context) error }); ok {
				if err := s.Init(context.Background()); err != nil {
					// If initialization fails, we might still want to start the API
					// so the user can configure a valid DB.
					log.Printf("Warning: Failed to initialize storage: %v", err)
				}
			}
		}

		registry := engine.NewRegistry(store)

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

		// Start worker if not in api-only mode
		if *mode == "worker" || *mode == "standalone" {
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

		// Start API if not in worker-only mode
		if *mode == "api" || *mode == "standalone" {
			server := api.NewServer(registry, store)
			fmt.Printf("Starting Hermod API server on :%d using %s storage...\n", *port, dbTypeVal)

			httpServer := &http.Server{
				Addr:    fmt.Sprintf(":%d", *port),
				Handler: server.Routes(),
			}

			go func() {
				if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
					log.Printf("API server failed: %v", err)
				}
			}()

			<-ctx.Done()
			fmt.Println("Shutting down API server...")
			_ = httpServer.Shutdown(context.Background())
		} else {
			// Worker only mode, wait for context
			fmt.Printf("Starting Hermod worker using %s storage...\n", dbTypeVal)
			<-ctx.Done()
		}

		fmt.Println("Hermod shutdown complete")
		return
	}

	log.Fatalf("Invalid mode: %s. Supported modes: standalone, api, worker", *mode)
}
