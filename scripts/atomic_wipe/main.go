package main

import (
	"database/sql"
	"fmt"
	"log"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

func main() {
	connStr := "postgres://postgres:postgres@localhost:5432/postgres?sslmode=disable"
	db, err := sql.Open("pgx", connStr)
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	// 1. Terminate all backends
	fmt.Println("Terminating backends...")
	_, err = db.Exec("SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE pid <> pg_backend_pid() AND datname IN ('postgres', 'hermod_test_source', 'hermod_test_sink', 'hermod_metadata')")
	if err != nil {
		fmt.Printf("Warning: failed to terminate backends: %v\n", err)
	}

	// Wait for backends to actually terminate
	fmt.Println("Waiting for backends to terminate...")
	for i := 0; i < 5; i++ {
		var count int
		db.QueryRow("SELECT count(*) FROM pg_stat_activity WHERE pid <> pg_backend_pid() AND datname IN ('postgres', 'hermod_test_source', 'hermod_test_sink', 'hermod_metadata')").Scan(&count)
		if count == 0 {
			break
		}
		fmt.Printf("Still %d backends active, waiting...\n", count)
		time.Sleep(500 * time.Millisecond)
	}

	// 2. Drop all slots (global)
	fmt.Println("Dropping all replication slots...")
	rows, err := db.Query("SELECT slot_name FROM pg_replication_slots")
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var name string
			rows.Scan(&name)
			_, dropErr := db.Exec("SELECT pg_drop_replication_slot($1)", name)
			if dropErr != nil {
				fmt.Printf("Failed to drop slot %s: %v\n", name, dropErr)
			} else {
				fmt.Printf("Dropped slot %s\n", name)
			}
		}
	}

	wipeDB := func(dbname string) {
		fmt.Printf("Wiping database %s...\n", dbname)
		d, err := sql.Open("pgx", fmt.Sprintf("postgres://postgres:postgres@127.0.0.1:5432/%s?sslmode=disable", dbname))
		if err != nil {
			return
		}
		defer d.Close()

		if dbname == "hermod_metadata" {
			_, _ = d.Exec("DELETE FROM workers")
			_, _ = d.Exec("DELETE FROM workflows")
			_, _ = d.Exec("DELETE FROM sources")
			_, _ = d.Exec("DELETE FROM sinks")
			_, _ = d.Exec("DELETE FROM node_states")
			fmt.Println("[hermod_metadata] Cleared metadata tables")
		}

		// Drop publications (per-database)
		rows, err := d.Query("SELECT pubname FROM pg_publication")
		if err == nil {
			defer rows.Close()
			for rows.Next() {
				var name string
				rows.Scan(&name)
				_, dropErr := d.Exec(fmt.Sprintf("DROP PUBLICATION IF EXISTS %s", name))
				if dropErr != nil {
					fmt.Printf("[%s] Failed to drop publication %s: %v\n", dbname, name, dropErr)
				} else {
					fmt.Printf("[%s] Dropped publication %s\n", dbname, name)
				}
			}
		}
	}

	wipeDB("postgres")
	wipeDB("hermod_test_source")
	wipeDB("hermod_test_sink")
	wipeDB("hermod_metadata")

	fmt.Println("Wipe complete.")
}
