package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/jackc/pgx/v5"
)

func main() {
	conn, err := pgx.Connect(context.Background(), "postgres://postgres:postgres@localhost:5432/postgres?sslmode=disable")
	if err != nil {
		log.Fatal(err)
	}
	defer conn.Close(context.Background())

	rows, err := conn.Query(context.Background(), "SELECT slot_name FROM pg_replication_slots")
	if err != nil {
		log.Fatal(err)
	}
	defer rows.Close()

	var slots []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err == nil {
			slots = append(slots, name)
		}
	}

	for _, slot := range slots {
		fmt.Printf("Dropping slot: %s\n", slot)
		// Try to terminate backend if active
		_, _ = conn.Exec(context.Background(), "SELECT pg_terminate_backend(active_pid) FROM pg_replication_slots WHERE slot_name = $1 AND active_pid IS NOT NULL", slot)

		// Wait a bit for termination
		for range 5 {
			var active bool
			err := conn.QueryRow(context.Background(), "SELECT active FROM pg_replication_slots WHERE slot_name = $1", slot).Scan(&active)
			if err != nil || !active {
				break
			}
			fmt.Printf("Slot %s still active, waiting...\n", slot)
			time.Sleep(500 * time.Millisecond)
		}

		_, err = conn.Exec(context.Background(), "SELECT pg_drop_replication_slot($1)", slot)
		if err != nil {
			fmt.Printf("Error dropping slot %s: %v\n", slot, err)
		}
	}

	// Also terminate all walsenders to be sure
	fmt.Println("Terminating all walsender backends...")
	_, _ = conn.Exec(context.Background(), "SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE backend_type = 'walsender'")

	rows2, err := conn.Query(context.Background(), "SELECT pubname FROM pg_publication")
	if err == nil {
		var pubs []string
		for rows2.Next() {
			var name string
			if err := rows2.Scan(&name); err == nil {
				pubs = append(pubs, name)
			}
		}
		rows2.Close()
		for _, pub := range pubs {
			fmt.Printf("Dropping publication: %s\n", pub)
			_, _ = conn.Exec(context.Background(), fmt.Sprintf("DROP PUBLICATION IF EXISTS %s", pub))
		}
	}
}
