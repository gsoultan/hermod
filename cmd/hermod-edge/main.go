package main

import (
	"context"
	"database/sql"
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/user/hermod/internal/config"
	"github.com/user/hermod/internal/engine/registry"
	"github.com/user/hermod/internal/runtimetune"
	"github.com/user/hermod/internal/storage"
	internal_sql "github.com/user/hermod/internal/storage/sql"
	"github.com/user/hermod/pkg/engine/telemetry"
	_ "modernc.org/sqlite"
)

func main() {
	runtimetune.Apply()
	_ = config.EnsureConfigDir()
	configPath := parseFlags()

	log.Println("Starting Hermod Edge Worker Node...")
	store := initStorage()
	reg := setupRegistry(store, *configPath)

	ctx, cancel := setupContext()
	defer cancel()
	runtimetune.StartScavenger(ctx)

	runReconciliationLoop(ctx, reg)
}

func parseFlags() *string {
	configPath := flag.String("config", config.GetConfigPath("edge.yaml"), "Path to edge configuration file")
	flag.Parse()
	return configPath
}

func initStorage() storage.Storage {
	db, err := sql.Open("sqlite", config.GetConfigPath("edge_state.db"))
	if err != nil {
		log.Fatalf("Failed to open SQLite: %v", err)
	}
	return internal_sql.NewSQLStorage(db, "sqlite")
}

func setupRegistry(store storage.Storage, configPath string) *registry.Registry {
	reg := registry.NewRegistry(store)
	reg.SetLogger(telemetry.NewDefaultLogger())
	log.Printf("Hermod Edge Worker Node started with config: %s", configPath)
	return reg
}

func setupContext() (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(context.Background())
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		<-sigChan
		log.Println("Shutting down Edge Worker Node...")
		cancel()
	}()
	return ctx, cancel
}

func runReconciliationLoop(ctx context.Context, reg *registry.Registry) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			reg.StopAll()
			return
		case <-ticker.C:
			log.Println("Edge heartbeat...")
		}
	}
}
