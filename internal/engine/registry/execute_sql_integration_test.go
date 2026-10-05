//go:build integration
// +build integration

package registry

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/gsoultan/hermod/internal/storage"
	sqlstorage "github.com/gsoultan/hermod/internal/storage/sql"
	"github.com/gsoultan/hermod/pkg/comm/message"
	_ "github.com/jackc/pgx/v5/stdlib"
	_ "modernc.org/sqlite"

	_ "github.com/gsoultan/hermod/pkg/comm/transformer/advanced"
)

// ---------------------------------------------------------------------------
// execute_sql, from a stored source to a real PostgreSQL.
//
// The unit tests run on SQLite, which is not the driver anybody points this
// node at. What they cannot say is whether a RETURNING clause survives the path
// an operator actually configures: a source saved in storage, resolved by the
// registry into a pooled pgx connection, with a generated key coming back.
// ---------------------------------------------------------------------------

func newExecuteSQLFixture(t *testing.T) (*Registry, *sql.DB, string) {
	t.Helper()

	dsn := os.Getenv("POSTGRES_DSN")
	if os.Getenv("HERMOD_INTEGRATION") != "1" || dsn == "" {
		t.Skip("integration: set HERMOD_INTEGRATION=1 and POSTGRES_DSN to run")
	}

	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("open postgres: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.PingContext(t.Context()); err != nil {
		t.Fatalf("the configured PostgreSQL is not reachable: %v", err)
	}

	table := "execsql_" + strings.ToLower(t.Name())
	for _, q := range []string{
		"DROP TABLE IF EXISTS " + table,
		fmt.Sprintf(`CREATE TABLE %s (
			id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
			code TEXT UNIQUE,
			detail JSONB
		)`, table),
	} {
		if _, err := db.ExecContext(t.Context(), q); err != nil {
			t.Fatalf("exec %q: %v", q, err)
		}
	}
	t.Cleanup(func() {
		_, _ = db.ExecContext(context.Background(), "DROP TABLE IF EXISTS "+table)
	})

	meta, err := sql.Open("sqlite", "file:execsql_"+t.Name()+"?mode=memory&cache=shared")
	if err != nil {
		t.Fatalf("open metadata db: %v", err)
	}
	t.Cleanup(func() { _ = meta.Close() })

	store := sqlstorage.NewSQLStorage(meta, "sqlite")
	if err := store.Init(t.Context()); err != nil {
		t.Fatalf("init metadata store: %v", err)
	}
	if err := store.CreateSource(t.Context(), storage.Source{
		ID: "exec-src", Name: "exec source", Type: "postgres",
		Config: map[string]string{"connection_string": dsn, "use_cdc": "false"},
	}); err != nil {
		t.Fatalf("create source: %v", err)
	}

	return NewRegistry(store), db, table
}

// The reported case: an INSERT that returns the key it generated. The row was
// written and the key was discarded, so the message -- and the editor's Run
// Preview, which shows it -- came back unchanged.
func TestExecuteSQLReturnsTheGeneratedKeyFromPostgres(t *testing.T) {
	reg, db, table := newExecuteSQLFixture(t)

	got := transform(t, reg,
		map[string]any{"code": "C-1"},
		map[string]any{
			"transType": "execute_sql",
			"sourceId":  "exec-src",
			"queryTemplate": fmt.Sprintf(
				`INSERT INTO %s (code, detail) VALUES ({{.code}}, '{"tier":"gold"}') RETURNING id, code, detail`, table),
			"resultField":       "inserted",
			"affectedRowsField": "written",
		})

	row, ok := got["inserted"].(map[string]any)
	if !ok {
		t.Fatalf("inserted = %#v, want the returned row. Full message: %v", got["inserted"], got)
	}

	var id int64
	if err := db.QueryRowContext(t.Context(),
		"SELECT id FROM "+table+" WHERE code = 'C-1'").Scan(&id); err != nil {
		t.Fatalf("the row was not written: %v", err)
	}
	// Compared as text: whether the key reads back as int64 or float64 depends
	// on which representation transform() found the message in.
	if fmt.Sprint(row["id"]) != strconv.FormatInt(id, 10) {
		t.Errorf("inserted.id = %#v, want the generated key %d", row["id"], id)
	}
	if row["code"] != "C-1" {
		t.Errorf("inserted.code = %#v, want C-1", row["code"])
	}
	// A jsonb column comes back as the document, not as a string holding one.
	if detail, _ := row["detail"].(map[string]any); detail["tier"] != "gold" {
		t.Errorf("inserted.detail = %#v, want the jsonb document", row["detail"])
	}
	if fmt.Sprint(got["written"]) != "1" {
		t.Errorf("written = %#v, want 1", got["written"])
	}

	var n int
	if err := db.QueryRowContext(t.Context(), "SELECT count(*) FROM "+table).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 1 {
		t.Errorf("the statement wrote %d rows, want exactly 1", n)
	}
}

// pgx reports a refused write when the result is read, not when the query is
// sent. A query path that stops at QueryContext's own error would turn a
// duplicate key into a success with no rows.
func TestExecuteSQLReportsARefusedWriteFromPostgres(t *testing.T) {
	reg, db, table := newExecuteSQLFixture(t)
	if _, err := db.ExecContext(t.Context(),
		"INSERT INTO "+table+" (code) VALUES ('taken')"); err != nil {
		t.Fatalf("seed: %v", err)
	}

	msg := message.AcquireMessage()
	t.Cleanup(msg.Release)
	msg.SetData("code", "taken")

	_, err := reg.applyTransformation(t.Context(), msg, "execute_sql", map[string]any{
		"transType":     "execute_sql",
		"sourceId":      "exec-src",
		"queryTemplate": fmt.Sprintf(`INSERT INTO %s (code) VALUES ({{.code}}) RETURNING id`, table),
		"resultField":   "inserted",
	})
	if err == nil {
		t.Fatalf("a duplicate key was reported as success")
	}
	if !strings.Contains(err.Error(), "failed to execute SQL") {
		t.Errorf("the error lost the execute_sql wrapper: %v", err)
	}
}
