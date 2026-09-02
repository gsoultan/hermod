package registry

import (
	"context"
	"database/sql"
	"fmt"
	"runtime"
	"strings"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/gsoultan/Hermod/internal/factory"
	"github.com/gsoultan/Hermod/internal/storage"
	sqlstorage "github.com/gsoultan/Hermod/internal/storage/sql"
)

func TestE2E_PgBouncer_Real(t *testing.T) {
	requireIntegrationInfra(t)
	ctx := context.Background()
	// 1. Setup Postgres Storage for Metadata
	metadataDSN := "postgres://postgres:postgres@localhost:5432/hermod_metadata?sslmode=disable"
	db := requireIntegrationDB(t, metadataDSN)
	defer db.Close()

	ms := sqlstorage.NewSQLStorage(db, "pgx")
	if err := ms.Init(ctx); err != nil {
		t.Fatalf("Failed to initialize metadata storage: %v", err)
	}
	reg := NewRegistry(ms)

	// 2. Define Source and Sink with PgBouncer markers
	sourceDSN := "postgres://postgres:postgres@localhost:5432/hermod_test_source?sslmode=disable&pgbouncer=true"
	sinkDSN := "postgres://postgres:postgres@localhost:5432/hermod_test_sink?sslmode=disable&pgbouncer=true"

	// Create Source and Sink in storage
	sourceID := "pg-source-pooled"
	src := storage.Source{
		ID:   sourceID,
		Name: "Pooled Source",
		Type: "postgres",
		Config: map[string]string{
			"connection_string": sourceDSN,
			"query":             "SELECT id, name, value FROM test_data WHERE id > $1 ORDER BY id ASC",
			"use_cdc":           "false",
			"poll_interval":     "1s",
		},
	}
	ms.CreateSource(ctx, src)
	defer ms.DeleteSource(ctx, sourceID)

	sinkID := "pg-sink-pooled"
	snk := storage.Sink{
		ID:   sinkID,
		Name: "Pooled Sink",
		Type: "postgres",
		Config: map[string]string{
			"connection_string": sinkDSN,
			"table":             "test_sink",
			"column_mappings":   `[{"source_field": "id", "target_column": "source_id"}, {"source_field": "name", "target_column": "name"}, {"source_field": "value", "target_column": "value"}]`,
		},
	}
	ms.CreateSink(ctx, snk)
	defer ms.DeleteSink(ctx, sinkID)

	// 3. Define Workflow
	wfID := "wf-pgbouncer-e2e"
	wf := storage.Workflow{
		ID:     wfID,
		Name:   "PgBouncer E2E Workflow",
		Active: true,
		Nodes: []storage.WorkflowNode{
			{
				ID:    "src-1",
				Type:  "source",
				RefID: sourceID,
			},
			{
				ID:    "snk-1",
				Type:  "sink",
				RefID: sinkID,
			},
		},
		Edges: []storage.WorkflowEdge{
			{SourceID: "src-1", TargetID: "snk-1"},
		},
	}
	ms.CreateWorkflow(ctx, wf)
	defer ms.DeleteWorkflow(ctx, wfID)

	// 4. Start Workflow and monitor
	var memBefore, memAfter runtime.MemStats
	runtime.ReadMemStats(&memBefore)

	// The workflow's source query reads test_data, so it has to exist before the
	// engine starts rather than being assumed to be lying around from an earlier
	// run on someone's laptop.
	sourceDB := requireIntegrationDB(t, strings.ReplaceAll(sourceDSN, "&pgbouncer=true", ""))
	defer sourceDB.Close()
	seedTestData(t, sourceDB)

	// Three rows that exist before the engine starts. The assertion below wants
	// 3 + 1000, and those three used to be whatever a previous run had left in
	// the table -- so the count was only ever right by luck.
	for i := range 3 {
		if _, err := sourceDB.ExecContext(ctx,
			"INSERT INTO test_data (name, value) VALUES ($1, $2)",
			fmt.Sprintf("baseline_%d", i), i); err != nil {
			t.Fatalf("seeding baseline row %d: %v", i, err)
		}
	}

	err := reg.StartWorkflow(wfID, wf)
	if err != nil {
		t.Fatalf("Failed to start workflow: %v", err)
	}
	defer reg.StopEngine(ctx, wfID)

	// 4.5 High Traffic: Insert 1000 more rows
	for i := range 1000 {
		_, err = sourceDB.Exec("INSERT INTO test_data (name, value) VALUES ($1, $2)", fmt.Sprintf("item_%d", i), i)
		if err != nil {
			t.Fatalf("Failed to insert high traffic data: %v", err)
		}
	}

	// 5. Verify data flow (should process 3 + 1000 items)
	time.Sleep(10 * time.Second)

	// Verify sink table
	// Strip pgbouncer=true for sql.Open which doesn't know about it
	cleanedSinkDSN := strings.ReplaceAll(sinkDSN, "&pgbouncer=true", "")
	cleanedSinkDSN = strings.ReplaceAll(cleanedSinkDSN, "?pgbouncer=true", "")
	sinkDB, err := sql.Open("pgx", cleanedSinkDSN)
	if err != nil {
		t.Fatalf("Failed to open sink DB for verification: %v", err)
	}
	defer sinkDB.Close()

	var count int
	err = sinkDB.QueryRow("SELECT count(*) FROM test_sink").Scan(&count)
	if err != nil {
		t.Fatalf("Failed to query sink table: %v", err)
	}
	if count < 1003 {
		t.Errorf("Expected at least 1003 rows in sink, got %d", count)
	}

	// 6. Performance & Resource Check
	runtime.ReadMemStats(&memAfter)
	var diff int64
	if memAfter.Alloc > memBefore.Alloc {
		diff = int64(memAfter.Alloc - memBefore.Alloc)
	} else {
		diff = -int64(memBefore.Alloc - memAfter.Alloc)
	}
	t.Logf("Memory usage: before=%d KB, after=%d KB, diff=%d KB",
		memBefore.Alloc/1024, memAfter.Alloc/1024, diff/1024)

	// Memory diff should be reasonable (e.g. < 50MB for this small test)
	if diff > 50*1024*1024 {
		t.Errorf("Potential memory leak: memory usage increased by %d bytes", diff)
	}
}

func TestE2E_CDC_PgBouncer_Failure(t *testing.T) {
	requireIntegrationInfra(t)
	// Verify that CDC fails fast when pgbouncer=true is used
	sourceDSN := "postgres://postgres:postgres@localhost:5432/hermod_test_source?sslmode=disable&pgbouncer=true"

	reg := NewRegistry(nil)
	cfg := factory.SourceConfig{
		Type: "postgres",
		Config: map[string]string{
			"connection_string": sourceDSN,
			"use_cdc":           "true",
		},
	}

	err := reg.TestSource(context.Background(), cfg)
	if err == nil {
		t.Fatal("Expected error when using CDC behind PgBouncer, got nil")
	}

	if !strings.Contains(err.Error(), "PgBouncer") {
		t.Errorf("Expected error to mention PgBouncer, got: %v", err)
	}
}
