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

	"github.com/user/hermod/internal/engine"
	internal_sql "github.com/user/hermod/internal/storage/sql"
	pkgengine "github.com/user/hermod/pkg/engine"
	_ "modernc.org/sqlite"
)

func main() {
	configPath := flag.String("config", "edge.yaml", "Path to edge configuration file")
	flag.Parse()

	log.Println("Starting Hermod Edge Worker Node...")

	// Initialize lightweight storage (SQLite)
	db, err := sql.Open("sqlite", "edge_state.db")
	if err != nil {
		log.Fatalf("Failed to open SQLite: %v", err)
	}
	store := internal_sql.NewSQLStorage(db, "sqlite")

	// Initialize Registry in Edge Mode
	// (This assumes Registry handles lightweight operation or we can customize it)
	reg := engine.NewRegistry(store)
	reg.SetLogger(pkgengine.NewDefaultLogger())

	log.Printf("Hermod Edge Worker Node started with config: %s", *configPath)

	// In a real scenario, the edge node would connect to a central controller
	// and pull its assigned workflows. For now, we'll start a reconciliation loop.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Handle signals
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		<-sigChan
		log.Println("Shutting down Edge Worker Node...")
		cancel()
	}()

	// Background reconciliation loop (simplified for edge)
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			reg.StopAll()
			return
		case <-ticker.C:
			// Edge nodes typically run a subset of workflows
			// For now, we'll use the standard reconciliation logic from the registry
			// if it were exposed, or just rely on manual start for this MVP.
			log.Println("Edge heartbeat...")
		}
	}
}
