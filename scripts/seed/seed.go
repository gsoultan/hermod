package main

import (
	"database/sql"
	"fmt"
	"log"

	_ "github.com/jackc/pgx/v5/stdlib"
	"golang.org/x/crypto/bcrypt"
)

func main() {
	connStr := "postgres://postgres:postgres@localhost:5432/postgres?sslmode=disable"
	db, err := sql.Open("pgx", connStr)
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	databases := []string{"hermod_sample_db_2", "hermod_sample_db_3", "hermod_db"}
	for _, dbName := range databases {
		var exists bool
		err = db.QueryRow("SELECT EXISTS(SELECT 1 FROM pg_database WHERE datname = $1)", dbName).Scan(&exists)
		if err != nil {
			log.Fatal(err)
		}
		if !exists {
			_, err = db.Exec(fmt.Sprintf("CREATE DATABASE %s", dbName))
			if err != nil {
				log.Fatal(err)
			}
			fmt.Printf("Database %s created\n", dbName)
		} else {
			fmt.Printf("Database %s already exists\n", dbName)
		}
	}

	setupDB2()
	setupDB3()
	setupHermodDB()
}

func setupDB2() {
	connStr := "postgres://postgres:postgres@localhost:5432/hermod_sample_db_2?sslmode=disable"
	db, err := sql.Open("pgx", connStr)
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	_, err = db.Exec(`
		CREATE TABLE IF NOT EXISTS user_types (
			id SERIAL PRIMARY KEY,
			name TEXT NOT NULL
		);
		CREATE TABLE IF NOT EXISTS users (
			id SERIAL PRIMARY KEY,
			username TEXT NOT NULL,
			user_type_id INTEGER REFERENCES user_types(id),
			email TEXT,
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
		);
	`)
	if err != nil {
		log.Fatal(err)
	}

	var count int
	err = db.QueryRow("SELECT COUNT(*) FROM user_types").Scan(&count)
	if err == nil && count == 0 {
		_, err = db.Exec("INSERT INTO user_types (name) VALUES ('Admin'), ('User'), ('Guest')")
		if err != nil {
			log.Printf("Failed to seed user_types: %v", err)
		}
	}

	// Enable logical replication
	_, _ = db.Exec("ALTER TABLE users REPLICA IDENTITY FULL")

	fmt.Println("hermod_sample_db_2 setup complete")
}

func setupDB3() {
	connStr := "postgres://postgres:postgres@localhost:5432/hermod_sample_db_3?sslmode=disable"
	db, err := sql.Open("pgx", connStr)
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	_, err = db.Exec(`
		CREATE TABLE IF NOT EXISTS user_sinks (
			id INTEGER PRIMARY KEY,
			username TEXT NOT NULL,
			user_type_name TEXT,
			email TEXT,
			transformed_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
		);
	`)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("hermod_sample_db_3 setup complete")
}

func setupHermodDB() {
	connStr := "postgres://postgres:postgres@localhost:5432/hermod_db?sslmode=disable"
	db, err := sql.Open("pgx", connStr)
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

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

	username := "gsoultan"
	password := "23oktober*_01234"

	var exists bool
	err = db.QueryRow("SELECT EXISTS(SELECT 1 FROM users WHERE username = $1)", username).Scan(&exists)
	if err != nil {
		log.Fatal(err)
	}
	if !exists {
		hash, _ := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
		_, err = db.Exec("INSERT INTO users (id, username, password, role) VALUES ($1, $2, $3, $4)",
			"user-1", username, string(hash), "admin")
		if err != nil {
			log.Fatal(err)
		}
		fmt.Println("User gsoultan created in hermod_db")
	} else {
		fmt.Println("User gsoultan already exists")
	}
}
