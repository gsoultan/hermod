package main

import (
	"database/sql"
	"fmt"
	"log"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

func main() {
	// Insert into source
	sourceDB, err := sql.Open("pgx", "postgres://postgres:postgres@localhost:5432/hermod_sample_db_2?sslmode=disable")
	if err != nil {
		log.Fatalf("Failed to connect to source: %v", err)
	}
	defer sourceDB.Close()

	username := fmt.Sprintf("user_%d", time.Now().Unix())
	_, err = sourceDB.Exec("INSERT INTO users (username, user_type_id, email) VALUES ($1, 1, $2)", username, username+"@example.com")
	if err != nil {
		log.Fatalf("Failed to insert into source: %v", err)
	}
	fmt.Printf("Inserted %s into source\n", username)

	// Wait for processing
	fmt.Println("Waiting for processing...")
	time.Sleep(5 * time.Second)

	// Check sink
	sinkDB, err := sql.Open("pgx", "postgres://postgres:postgres@localhost:5432/hermod_sample_db_3?sslmode=disable")
	if err != nil {
		log.Fatalf("Failed to connect to sink: %v", err)
	}
	defer sinkDB.Close()

	var count int
	err = sinkDB.QueryRow("SELECT count(*) FROM user_sinks WHERE username = $1", username).Scan(&count)
	if err != nil {
		log.Printf("Failed to query sink (maybe table doesn't exist yet): %v", err)
	} else if count > 0 {
		fmt.Printf("Found %d record(s) for %s in sink!\n", count, username)
	} else {
		fmt.Printf("No record found for %s in sink yet.\n", username)
	}
}
