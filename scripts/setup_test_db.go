package main

import (
	"database/sql"
	"fmt"
	"log"

	_ "github.com/jackc/pgx/v5/stdlib"
)

func main() {
	// Setup Source DB
	sourceDB := "hermod_test_source"
	sinkDB := "hermod_test_sink"
	metaDB := "hermod_metadata"

	connStr := "postgres://postgres:postgres@127.0.0.1:5432/postgres?sslmode=disable"
	db, err := sql.Open("pgx", connStr)
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	// Drop and recreate databases
	for _, name := range []string{sourceDB, sinkDB, metaDB} {
		_, _ = db.Exec(fmt.Sprintf(`SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname = '%s' AND pid <> pg_backend_pid()`, name))
		_, _ = db.Exec(fmt.Sprintf("DROP DATABASE IF EXISTS %s", name))
		_, err = db.Exec(fmt.Sprintf("CREATE DATABASE %s", name))
		if err != nil {
			log.Fatalf("failed to create database %s: %v", name, err)
		}
		fmt.Printf("Database %s created\n", name)
	}

	// Connect to source and create table
	sdb, err := sql.Open("pgx", "postgres://postgres:postgres@127.0.0.1:5432/hermod_test_source?sslmode=disable")
	if err != nil {
		log.Fatal(err)
	}
	defer sdb.Close()

	_, err = sdb.Exec(`
		CREATE TABLE IF NOT EXISTS users (
			id SERIAL PRIMARY KEY,
			name TEXT,
			email TEXT,
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
		);
		ALTER TABLE users REPLICA IDENTITY FULL;

		CREATE TABLE IF NOT EXISTS audit_log (
			id SERIAL PRIMARY KEY,
			status TEXT,
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
		);
		ALTER TABLE audit_log REPLICA IDENTITY FULL;

		CREATE TABLE IF NOT EXISTS lookup_table (
			id SERIAL PRIMARY KEY,
			user_name TEXT UNIQUE,
			city TEXT,
			country TEXT
		);
		INSERT INTO lookup_table (user_name, city, country) VALUES ('John Doe', 'New York', 'USA') ON CONFLICT (user_name) DO NOTHING;

		CREATE TABLE IF NOT EXISTS orders (
			id SERIAL PRIMARY KEY,
			user_id INTEGER,
			amount DECIMAL(10,2),
			status TEXT,
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
		);
		ALTER TABLE orders REPLICA IDENTITY FULL;
	`)
	if err != nil {
		log.Fatalf("failed to setup source tables: %v", err)
	}
	fmt.Println("Source tables created")

	// Connect to sink and create tables
	kdb, err := sql.Open("pgx", "postgres://postgres:postgres@127.0.0.1:5432/hermod_test_sink?sslmode=disable")
	if err != nil {
		log.Fatal(err)
	}
	defer kdb.Close()

	_, err = kdb.Exec(`
		CREATE TABLE IF NOT EXISTS users_sink (
			id INTEGER PRIMARY KEY,
			full_name TEXT,
			email TEXT,
			synced_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
		);

		CREATE TABLE IF NOT EXISTS comp_users_sink1 (
			id INTEGER PRIMARY KEY,
			name TEXT,
			email TEXT,
			city TEXT,
			fuzzy TEXT,
			vhost TEXT,
			id_str TEXT
		);

		CREATE TABLE IF NOT EXISTS comp_users_sink2 (
			id INTEGER PRIMARY KEY,
			name TEXT,
			email TEXT,
			city TEXT
		);

		CREATE TABLE IF NOT EXISTS comp_audit_sink (
			id INTEGER PRIMARY KEY,
			status TEXT,
			tag TEXT,
			processed BOOLEAN,
			lua_ok BOOLEAN
		);

		CREATE TABLE IF NOT EXISTS orders_sink (
			id INTEGER PRIMARY KEY,
			user_id INTEGER,
			amount DECIMAL(10,2),
			status TEXT,
			synced_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
		);
		CREATE TABLE IF NOT EXISTS rmq_sink (
			id SERIAL PRIMARY KEY,
			name TEXT,
			email TEXT,
			city TEXT,
			country TEXT,
			priority TEXT,
			status TEXT
		);
	`)
	if err != nil {
		log.Fatalf("failed to setup sink table: %v", err)
	}
	fmt.Println("Sink table created")
}
