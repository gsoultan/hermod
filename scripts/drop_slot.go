package main

import (
	"database/sql"
	"log"

	_ "github.com/jackc/pgx/v5/stdlib"
)

func main() {
	db, err := sql.Open("pgx", "postgres://postgres:postgres@localhost:5432/postgres?sslmode=disable")
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	_, err = db.Exec("SELECT pg_drop_replication_slot('cdc_slot_2')")
	if err != nil {
		log.Printf("Failed to drop slot (maybe already gone): %v", err)
	} else {
		log.Println("Successfully dropped cdc_slot_2 from postgres database")
	}
}
