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

	var relreplident string
	err = db.QueryRow("SELECT relreplident FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace WHERE n.nspname = 'public' AND c.relname = 'users'").Scan(&relreplident)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("Replica Identity for users: %s (d=default, n=nothing, f=full, i=index)\n", relreplident)
}
