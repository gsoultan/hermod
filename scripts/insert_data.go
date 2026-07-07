package main

import (
	"database/sql"
	_ "github.com/jackc/pgx/v5/stdlib"
	"log"
)

func main() {
	db, err := sql.Open("pgx", "postgres://postgres:postgres@localhost:5432/hermod_sample_db_2?sslmode=disable")
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()
	_, err = db.Exec("INSERT INTO users (username, user_type_id, email) VALUES ('gsoultan', 1, 'gsoultan@example.com')")
	if err != nil {
		log.Fatal(err)
	}
	log.Println("Data inserted")
}
