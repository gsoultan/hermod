package main

import (
	"database/sql"
	"fmt"
	"log"

	_ "github.com/jackc/pgx/v5/stdlib"
)

func main() {
	db, err := sql.Open("pgx", "postgres://postgres:postgres@localhost:5432/postgres?sslmode=disable")
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	rows, err := db.Query("SELECT slot_name, database, active FROM pg_replication_slots")
	if err != nil {
		log.Fatal(err)
	}
	defer rows.Close()

	fmt.Println("Replication Slots:")
	for rows.Next() {
		var name, database string
		var active bool
		if err := rows.Scan(&name, &database, &active); err != nil {
			log.Fatal(err)
		}
		fmt.Printf("Slot: %s, Database: %s, Active: %v\n", name, database, active)
	}
}
