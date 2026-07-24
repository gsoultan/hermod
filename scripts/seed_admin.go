package main

import (
	"database/sql"
	"fmt"
	"log"
	"os"

	_ "github.com/jackc/pgx/v5/stdlib"
	"golang.org/x/crypto/bcrypt"
	_ "modernc.org/sqlite"
)

func main() {
	dbType := os.Getenv("HERMOD_DB_TYPE")
	if dbType == "" || dbType == "sqlite" {
		dbType = "sqlite"
	} else if dbType == "postgres" {
		dbType = "pgx"
	}

	dbConn := os.Getenv("HERMOD_DB_CONN")
	if dbConn == "" {
		dbConn = "hermod_e2e.db"
	}

	db, err := sql.Open(dbType, dbConn)
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	// Use generic CREATE TABLE and INSERT
	_, err = db.Exec(`
		CREATE TABLE IF NOT EXISTS users (
			id TEXT PRIMARY KEY,
			username TEXT UNIQUE,
			password TEXT,
			full_name TEXT,
			email TEXT,
			role TEXT,
			vhosts TEXT,
			two_factor_enabled BOOLEAN DEFAULT FALSE,
			two_factor_secret TEXT
		)
	`)
	if err != nil {
		log.Fatal(err)
	}

	hash, _ := bcrypt.GenerateFromPassword([]byte("admin123"), bcrypt.DefaultCost)

	// Try DELETE + INSERT for portability across SQLite (INSERT OR REPLACE) and Postgres (ON CONFLICT)
	_, _ = db.Exec("DELETE FROM users WHERE username = 'admin'")
	_, err = db.Exec(`
		INSERT INTO users (id, username, password, role)
		VALUES ('admin-id', 'admin', ?, 'Administrator')
	`, string(hash))

	if err != nil {
		// Try $1 for postgres if ? failed
		_, err = db.Exec(`
			INSERT INTO users (id, username, password, role)
			VALUES ('admin-id', 'admin', $1, 'Administrator')
		`, string(hash))
	}

	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("Admin user seeded in %s\n", dbConn)
}
