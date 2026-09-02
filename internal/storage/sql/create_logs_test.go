package sql

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/gsoultan/Hermod/internal/storage"
	_ "github.com/jackc/pgx/v5/stdlib"
)

// Batched log writes went to Postgres with the `?` placeholders unconverted.
//
// CreateLog (singular) goes through s.exec, which runs prepareQuery to rebind
// placeholders for the driver. CreateLogs (batched) called tx.PrepareContext
// with the raw query instead, so on Postgres every batch failed with
// `syntax error at or near ","` (SQLSTATE 42601). The error was then discarded
// by the caller, so a workflow's entire log history silently went nowhere —
// which is exactly what "the logs table shows 0 rows" looked like in production.
//
// The batched path is the only one the engine's logger uses.
func TestCreateLogsWritesThroughOnPostgres(t *testing.T) {
	dsn := os.Getenv("POSTGRES_DSN")
	if os.Getenv("HERMOD_INTEGRATION") != "1" || dsn == "" {
		t.Skip("integration: set HERMOD_INTEGRATION=1 and POSTGRES_DSN to enable")
	}

	ctx := context.Background()
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()

	st := NewSQLStorage(db, "pgx")
	if err := st.Init(ctx); err != nil {
		t.Fatalf("init: %v", err)
	}

	wfID := fmt.Sprintf("wf-createlogs-%d", time.Now().UnixNano())
	logs := []storage.Log{
		{Timestamp: time.Now(), Level: "ERROR", Message: "Pipeline stalled", WorkflowID: wfID},
		{Timestamp: time.Now(), Level: "INFO", Message: "Stalled workflow restarted", WorkflowID: wfID},
	}

	if err := st.CreateLogs(ctx, logs); err != nil {
		t.Fatalf("CreateLogs returned an error, so no workflow log ever reaches the database: %v", err)
	}
	t.Cleanup(func() {
		_, _ = db.ExecContext(context.Background(), `DELETE FROM logs WHERE workflow_id = $1`, wfID)
	})

	var count int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM logs WHERE workflow_id = $1`, wfID).Scan(&count); err != nil {
		t.Fatalf("count logs: %v", err)
	}
	if count != len(logs) {
		t.Errorf("logs table holds %d rows for the workflow, want %d", count, len(logs))
	}
}

// The same batch on SQLite, so the fix cannot regress the driver that was
// working. SQLite needs no rebinding, so a naive "always rewrite to $N" fix
// would break here.
func TestCreateLogsWritesThroughOnSQLite(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	db, err := sql.Open("sqlite", dir+"/logs.db")
	if err != nil {
		t.Skipf("sqlite driver unavailable: %v", err)
	}
	defer db.Close()

	st := NewSQLStorage(db, "sqlite")
	if err := st.Init(ctx); err != nil {
		t.Fatalf("init: %v", err)
	}

	wfID := "wf-createlogs-sqlite"
	logs := []storage.Log{
		{Timestamp: time.Now(), Level: "ERROR", Message: "Pipeline stalled", WorkflowID: wfID},
		{Timestamp: time.Now(), Level: "INFO", Message: "Stalled workflow restarted", WorkflowID: wfID},
	}

	if err := st.CreateLogs(ctx, logs); err != nil {
		t.Fatalf("CreateLogs: %v", err)
	}

	var count int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM logs WHERE workflow_id = ?`, wfID).Scan(&count); err != nil {
		t.Fatalf("count logs: %v", err)
	}
	if count != len(logs) {
		t.Errorf("logs table holds %d rows, want %d", count, len(logs))
	}
}
