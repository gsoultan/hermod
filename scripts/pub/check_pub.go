package main

import (
	"database/sql"
	"fmt"
	"log"

	_ "github.com/jackc/pgx/v5/stdlib"
)

func main() {
	db, err := sql.Open("pgx", "postgres://postgres:postgres@localhost:5432/hermod_sample_db_2?sslmode=disable")
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	var pubName string
	err = db.QueryRow("SELECT pubname FROM pg_publication WHERE pubname = 'cdc_slot_2'").Scan(&pubName)
	if err != nil {
		log.Printf("Publication cdc_slot_2 not found: %v", err)
	} else {
		fmt.Printf("Publication %s exists\n", pubName)
	}

	rows, err := db.Query("SELECT schemaname, tablename FROM pg_publication_tables WHERE pubname = 'cdc_slot_2'")
	if err != nil {
		log.Fatal(err)
	}
	defer rows.Close()

	fmt.Println("Tables in publication:")
	for rows.Next() {
		var schema, table string
		if err := rows.Scan(&schema, &table); err != nil {
			log.Fatal(err)
		}
		fmt.Printf("- %s.%s\n", schema, table)
	}
}
