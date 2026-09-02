//go:build integration
// +build integration

package registry

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/gsoultan/hermod/internal/factory"
	sqlstorage "github.com/gsoultan/hermod/internal/storage/sql"
	_ "github.com/jackc/pgx/v5/stdlib"
	_ "modernc.org/sqlite"
)

// ---------------------------------------------------------------------------
// Source discovery and ad-hoc query, against a real Postgres.
//
// This is what the UI calls while someone builds a pipeline: list the tables in
// a source, list a table's columns, and run a sample query to see what comes
// back. It is also the surface sql_e2e.spec.ts was really testing, through a
// browser and against a hardcoded 127.0.0.1:5432 with a `users` table it never
// created — so on any machine without that exact fixture it failed on a missing
// relation rather than skipping, which reads like a product defect and is not.
//
// Run with:
//
//	HERMOD_INTEGRATION=1 \
//	POSTGRES_DSN='postgres://postgres:postgres@localhost:5432/hermod_it?sslmode=disable' \
//	go test -tags=integration ./internal/discovery/service/
// ---------------------------------------------------------------------------

type discoveryFixture struct {
	reg   *Registry
	db    *sql.DB
	table string
	cfg   factory.SourceConfig
}

func newDiscoveryFixture(t *testing.T) *discoveryFixture {
	t.Helper()

	dsn := os.Getenv("POSTGRES_DSN")
	if os.Getenv("HERMOD_INTEGRATION") != "1" || dsn == "" {
		t.Skip("integration: set HERMOD_INTEGRATION=1 and POSTGRES_DSN to run")
	}

	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("open postgres: %v", err)
	}
	if err := db.PingContext(t.Context()); err != nil {
		t.Fatalf("ping postgres: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	table := "disc_" + strings.ToLower(strings.NewReplacer("/", "_", "-", "_").Replace(t.Name()))

	// Create the fixture rather than assume it. A test that cannot tell a
	// broken feature from a missing table is worse than no test.
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.ExecContext(t.Context(), q, args...); err != nil {
			t.Fatalf("exec %q: %v", q, err)
		}
	}
	exec("DROP TABLE IF EXISTS " + table)
	exec(fmt.Sprintf(`CREATE TABLE %s (
		id SERIAL PRIMARY KEY,
		name TEXT NOT NULL,
		email TEXT,
		age INTEGER
	)`, table))
	exec(fmt.Sprintf("INSERT INTO %s (name, email, age) VALUES ($1,$2,$3)", table), "Ada", "ada@example.com", 36)
	exec(fmt.Sprintf("INSERT INTO %s (name, email, age) VALUES ($1,$2,$3)", table), "Grace", "grace@example.com", 45)
	t.Cleanup(func() { _, _ = db.ExecContext(context.Background(), "DROP TABLE IF EXISTS "+table) })

	// Through the Registry, which is what supplies the DiscoveryService its
	// ComponentCreator in production. Testing the service with a hand-written
	// creator would skip exactly the wiring most likely to be wrong.
	meta, err := sql.Open("sqlite", "file:disc_"+table+"?mode=memory&cache=shared")
	if err != nil {
		t.Fatalf("open metadata db: %v", err)
	}
	t.Cleanup(func() { _ = meta.Close() })
	store := sqlstorage.NewSQLStorage(meta, "sqlite")
	if err := store.Init(t.Context()); err != nil {
		t.Fatalf("init metadata store: %v", err)
	}

	return &discoveryFixture{
		reg:   NewRegistry(store),
		db:    db,
		table: table,
		cfg: factory.SourceConfig{
			Type:   "postgres",
			Config: map[string]string{"connection_string": dsn},
		},
	}
}

// TestDiscoverTablesFindsARealTable is the first call the source wizard makes.
func TestDiscoverTablesFindsARealTable(t *testing.T) {
	f := newDiscoveryFixture(t)

	tables, err := f.reg.DiscoverTables(t.Context(), f.cfg)
	if err != nil {
		t.Fatalf("DiscoverTables: %v", err)
	}
	if len(tables) == 0 {
		t.Fatal("no tables discovered; the source wizard would offer an empty list")
	}

	found := slices.ContainsFunc(tables, func(s string) bool {
		return s == f.table || strings.HasSuffix(s, "."+f.table)
	})
	if !found {
		t.Errorf("table %q not among the %d discovered; a user cannot select a table "+
			"that discovery does not return", f.table, len(tables))
	}
}

// TestDiscoverColumnsReturnsNamesAndTypes covers the step that drives column
// mapping. A wrong or empty type here silently produces a bad sink schema.
func TestDiscoverColumnsReturnsNamesAndTypes(t *testing.T) {
	f := newDiscoveryFixture(t)

	cols, err := f.reg.DiscoverSourceColumns(t.Context(), f.cfg, f.table)
	if err != nil {
		t.Fatalf("DiscoverColumns: %v", err)
	}

	byName := map[string]string{}
	for _, c := range cols {
		byName[strings.ToLower(c.Name)] = strings.ToLower(c.Type)
	}
	for _, want := range []string{"id", "name", "email", "age"} {
		if _, ok := byName[want]; !ok {
			t.Errorf("column %q missing from discovery; got %v", want, byName)
		}
	}
	if typ := byName["age"]; typ != "" && !strings.Contains(typ, "int") {
		t.Errorf("age discovered as %q, want an integer type: the sink schema is "+
			"generated from this, so a wrong type produces a column that rejects its data", typ)
	}
}

// TestExecuteSQLReturnsRows is the "preview my query" button.
func TestExecuteSQLReturnsRows(t *testing.T) {
	f := newDiscoveryFixture(t)

	rows, err := f.reg.ExecuteSQL(t.Context(), f.cfg,
		fmt.Sprintf("SELECT name, email FROM %s ORDER BY id", f.table), nil)
	if err != nil {
		t.Fatalf("ExecuteSQL: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("got %d rows, want 2", len(rows))
	}
	if got := fmt.Sprint(rows[0]["name"]); got != "Ada" {
		t.Errorf("first row name is %q, want Ada", got)
	}
	if _, ok := rows[0]["email"]; !ok {
		t.Errorf("row is missing the email column: %v", rows[0])
	}
}

// TestExecuteSQLReportsAnErrorRatherThanEmptyResults pins the failure mode.
//
// A preview that returns nothing looks identical to a query that matched no
// rows. The user then builds a pipeline on a query that does not run.
func TestExecuteSQLReportsAnErrorRatherThanEmptyResults(t *testing.T) {
	f := newDiscoveryFixture(t)

	rows, err := f.reg.ExecuteSQL(t.Context(), f.cfg, "SELECT * FROM table_that_does_not_exist", nil)
	if err == nil {
		t.Errorf("querying a missing table returned %d rows and no error; the preview "+
			"would look like an empty result set rather than a broken query", len(rows))
	}
}
