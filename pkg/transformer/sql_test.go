package transformer

import (
	"context"
	"database/sql"
	"encoding/json"
	"github.com/user/hermod/pkg/message"
	_ "modernc.org/sqlite"
	"os"
	"testing"
)

func TestSqlTransformer(t *testing.T) {
	ctx := context.Background()
	dbFile := "test_transformer.db"
	defer os.Remove(dbFile)

	db, err := sql.Open("sqlite", dbFile)
	if err != nil {
		t.Fatalf("Failed to open DB: %v", err)
	}
	defer db.Close()

	_, err = db.Exec("CREATE TABLE users (id INTEGER, name TEXT, email TEXT)")
	if err != nil {
		t.Fatalf("Failed to create table: %v", err)
	}

	_, err = db.Exec("INSERT INTO users (id, name, email) VALUES (1, 'John Doe', 'john@example.com')")
	if err != nil {
		t.Fatalf("Failed to insert data: %v", err)
	}

	trans := &SqlTransformer{
		Driver: "sqlite",
		Conn:   dbFile,
		Query:  "SELECT name, email FROM users WHERE id = :user_id",
	}

	msg := message.AcquireMessage()
	defer message.ReleaseMessage(msg)
	msg.SetAfter([]byte(`{"user_id": 1}`))

	transformed, err := trans.Transform(ctx, msg)
	if err != nil {
		t.Fatalf("Transform failed: %v", err)
	}

	var result map[string]interface{}
	if err := json.Unmarshal(transformed.After(), &result); err != nil {
		t.Fatalf("Failed to unmarshal result: %v", err)
	}

	if result["name"] != "John Doe" {
		t.Errorf("Expected name 'John Doe', got '%v'", result["name"])
	}
	if result["email"] != "john@example.com" {
		t.Errorf("Expected email 'john@example.com', got '%v'", result["email"])
	}
}
