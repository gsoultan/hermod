package main

import (
	"database/sql"
	"fmt"
	"log"

	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
	_ "modernc.org/sqlite"
)

func main() {
	db, err := sql.Open("sqlite", "hermod_e2e.db")
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	hash, _ := bcrypt.GenerateFromPassword([]byte("admin123"), bcrypt.DefaultCost)

	_, err = db.Exec("INSERT OR IGNORE INTO users (id, username, password, role) VALUES (?, ?, ?, ?)",
		uuid.New().String(), "admin", string(hash), "Administrator")
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("Admin user ensured: admin/admin123")
}
