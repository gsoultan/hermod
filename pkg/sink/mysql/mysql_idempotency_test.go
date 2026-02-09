//go:build integration
// +build integration

package mysql

import (
	"context"
	"database/sql"
	"os"
	"testing"

	_ "github.com/go-sql-driver/mysql"
	"github.com/user/hermod"
	"github.com/user/hermod/pkg/message"
)

func TestMySQLSink_IdempotentUpsert(t *testing.T) {
	dsn := os.Getenv("MYSQL_DSN")
	if os.Getenv("HERMOD_INTEGRATION") != "1" || dsn == "" {
		t.Skip("integration: set HERMOD_INTEGRATION=1 and MYSQL_DSN to run")
	}

	db, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatalf("open mysql: %v", err)
	}
	defer db.Close()

	table := "hermod_idemp_test"
	_, _ = db.Exec("DROP TABLE IF EXISTS " + table)
	// Use TEXT to maximize compatibility; JSON is fine on 5.7+ but TEXT works for test
	_, err = db.Exec("CREATE TABLE " + table + " (id VARCHAR(255) PRIMARY KEY, data TEXT NOT NULL)")
	if err != nil {
		t.Fatalf("create table: %v", err)
	}
	defer db.Exec("DROP TABLE IF EXISTS " + table)

	snk := NewMySQLSink(dsn, "", nil, true, "hard_delete", "", "", false, false)
	ctx := context.Background()

	// First write
	m1 := message.AcquireMessage()
	defer message.ReleaseMessage(m1)
	m1.SetID("id-1")
	m1.SetOperation(hermod.OpCreate)
	m1.SetTable(table)
	m1.SetAfter([]byte(`{"v":1}`))
	if err := snk.Write(ctx, m1); err != nil {
		t.Fatalf("first write: %v", err)
	}

	// Duplicate (same id) with new payload should upsert/update
	m2 := message.AcquireMessage()
	defer message.ReleaseMessage(m2)
	m2.SetID("id-1")
	m2.SetOperation(hermod.OpUpdate)
	m2.SetTable(table)
	m2.SetAfter([]byte(`{"v":2}`))
	if err := snk.Write(ctx, m2); err != nil {
		t.Fatalf("second write: %v", err)
	}

	// Verify row equals latest payload
	var got string
	if err := db.QueryRow("SELECT data FROM "+table+" WHERE id = ?", "id-1").Scan(&got); err != nil {
		t.Fatalf("select: %v", err)
	}
	if got != "{\"v\":2}" && got != "{\"v\": 2}" { // allow whitespace variants
		t.Fatalf("unexpected data: %s", got)
	}
}
