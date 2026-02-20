package postgres

import (
	"context"
	"encoding/json"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/user/hermod"
	"github.com/user/hermod/pkg/message"
	"github.com/user/hermod/pkg/sqlutil"
)

func TestPostgresSink_ColumnMapping(t *testing.T) {
	dsn := os.Getenv("POSTGRES_DSN")
	if os.Getenv("HERMOD_INTEGRATION") != "1" || dsn == "" {
		t.Skip("integration: set HERMOD_INTEGRATION=1 and POSTGRES_DSN to run")
	}

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("pgxpool: %v", err)
	}
	defer pool.Close()

	table := "hermod_mapping_test"
	_, _ = pool.Exec(ctx, "DROP TABLE IF EXISTS "+table)

	mappings := []sqlutil.ColumnMapping{
		{SourceField: "id", TargetColumn: "user_id", DataType: "TEXT", IsPrimaryKey: true},
		{SourceField: "name", TargetColumn: "full_name", DataType: "TEXT"},
		{SourceField: "age", TargetColumn: "user_age", DataType: "INTEGER"},
	}

	snk := NewPostgresSink(dsn, table, mappings, false, "hard_delete", "", "", false, false)

	// Test table creation with mappings
	msg := message.AcquireMessage()
	defer message.ReleaseMessage(msg)
	msg.SetID("u1")
	msg.SetOperation(hermod.OpCreate)
	data := map[string]any{
		"id":   "u1",
		"name": "John Doe",
		"age":  30,
	}
	payload, _ := json.Marshal(data)
	msg.SetAfter(payload)
	msg.SetData("id", "u1")
	msg.SetData("name", "John Doe")
	msg.SetData("age", 30)

	if err := snk.Write(ctx, msg); err != nil {
		t.Fatalf("Write: %v", err)
	}

	// Verify columns and data
	var fullName string
	var age int
	err = pool.QueryRow(ctx, "SELECT full_name, user_age FROM "+table+" WHERE user_id = $1", "u1").Scan(&fullName, &age)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if fullName != "John Doe" || age != 30 {
		t.Errorf("Unexpected data: %s, %d", fullName, age)
	}

	// Test UPSERT
	msg2 := message.AcquireMessage()
	defer message.ReleaseMessage(msg2)
	msg2.SetID("u1")
	msg2.SetOperation(hermod.OpUpdate)
	msg2.SetData("id", "u1")
	msg2.SetData("name", "John Smith")
	msg2.SetData("age", 31)

	if err := snk.Write(ctx, msg2); err != nil {
		t.Fatalf("Update: %v", err)
	}

	err = pool.QueryRow(ctx, "SELECT full_name, user_age FROM "+table+" WHERE user_id = $1", "u1").Scan(&fullName, &age)
	if err != nil {
		t.Fatalf("Scan after update: %v", err)
	}
	if fullName != "John Smith" || age != 31 {
		t.Errorf("Unexpected data after update: %s, %d", fullName, age)
	}

	// Test "after." field mapping
	mappings3 := []sqlutil.ColumnMapping{
		{SourceField: "after.id", TargetColumn: "user_id", DataType: "TEXT", IsPrimaryKey: true},
		{SourceField: "after.name", TargetColumn: "full_name", DataType: "TEXT"},
	}
	snk3 := NewPostgresSink(dsn, table, mappings3, true, "hard_delete", "", "", false, false)

	msg3 := message.AcquireMessage()
	defer message.ReleaseMessage(msg3)
	msg3.SetID("u3")
	msg3.SetOperation(hermod.OpCreate)
	// Important: we DON'T set msg.Data() here, only payload (which simulate CDC event)
	msg3.SetAfter([]byte(`{"id":"u3", "name":"After Name"}`))

	if err := snk3.Write(ctx, msg3); err != nil {
		t.Fatalf("Write msg3: %v", err)
	}

	err = pool.QueryRow(ctx, "SELECT full_name FROM "+table+" WHERE user_id = $1", "u3").Scan(&fullName)
	if err != nil {
		t.Fatalf("Scan msg3: %v", err)
	}
	if fullName != "After Name" {
		t.Errorf("Expected After Name, got %s", fullName)
	}

	_, _ = pool.Exec(ctx, "DROP TABLE IF EXISTS "+table)
}
