//go:build integration

package sql

import (
	"database/sql"
	"os"
	"testing"

	_ "github.com/microsoft/go-mssqldb"
)

// Node state on a real SQL Server.
//
// QueryUpdateNodeState had overrides for mysql and pgx and none for sqlserver,
// so it fell through to the SQLite spelling — `INSERT ... ON CONFLICT ... DO
// UPDATE SET state = excluded.state` — which SQL Server cannot parse. Every
// attempt to persist a workflow's node state was rejected outright.
//
// Nothing caught it. Every other test in this package runs on SQLite, where the
// common query is correct by construction, and the only place CI starts a live
// SQL Server is a sink test that never touches node state. A unit test cannot
// find this class at all: the statement is well-formed Go and a valid string,
// and only a server that speaks T-SQL will refuse it.
//
//	HERMOD_INTEGRATION=1 \
//	MSSQL_DSN='sqlserver://sa:Hermod%21Passw0rd@127.0.0.1:1433?database=master&encrypt=disable' \
//	go test -tags integration ./internal/storage/sql/ -run MSSQLNodeState
func TestMSSQLNodeStateRoundTrip(t *testing.T) {
	dsn := os.Getenv("MSSQL_DSN")
	if os.Getenv("HERMOD_INTEGRATION") != "1" || dsn == "" {
		t.Skip("integration: set HERMOD_INTEGRATION=1 and MSSQL_DSN to run")
	}

	db, err := sql.Open("sqlserver", dsn)
	if err != nil {
		t.Fatalf("open sqlserver: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.PingContext(t.Context()); err != nil {
		t.Fatalf("ping sqlserver: %v", err)
	}

	// A scratch table rather than Init(): this is about one statement's
	// dialect, and standing up the whole catalogue would fail for unrelated
	// reasons and hide what is under test. The shape matches
	// QueryInitWorkflowNodeStatesTable, in the types SQL Server actually has.
	const table = "hermod_it_workflow_node_states"
	if _, err := db.ExecContext(t.Context(),
		"IF OBJECT_ID('"+table+"', 'U') IS NOT NULL DROP TABLE "+table); err != nil {
		t.Fatalf("dropping scratch table: %v", err)
	}
	if _, err := db.ExecContext(t.Context(),
		"CREATE TABLE "+table+" (workflow_id NVARCHAR(256), node_id NVARCHAR(256), state NVARCHAR(MAX), "+
			"PRIMARY KEY (workflow_id, node_id))"); err != nil {
		t.Fatalf("creating scratch table: %v", err)
	}
	t.Cleanup(func() {
		_, _ = db.ExecContext(t.Context(), "DROP TABLE IF EXISTS "+table)
	})

	s := &sqlStorage{db: db, driver: "sqlserver", queries: newQueryRegistry("sqlserver")}
	query := s.queries.get(QueryUpdateNodeState)

	// The statement the storage layer would run, against the scratch table.
	// Before the sqlserver override existed this was the SQLite upsert and the
	// server rejected it outright.
	stmt := replaceTable(query, "workflow_node_states", table)

	if _, err := s.exec(t.Context(), stmt, "wf-1", "n-1", `{"offset":1}`); err != nil {
		t.Fatalf("first write (insert path) failed: %v\n  %s", err, stmt)
	}
	// The same key again has to update rather than collide with the primary key.
	if _, err := s.exec(t.Context(), stmt, "wf-1", "n-1", `{"offset":2}`); err != nil {
		t.Fatalf("second write (update path) failed: %v\n  %s", err, stmt)
	}
	if _, err := s.exec(t.Context(), stmt, "wf-1", "n-2", `{"offset":9}`); err != nil {
		t.Fatalf("write for a second node failed: %v\n  %s", err, stmt)
	}

	var rows int
	if err := db.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM "+table).Scan(&rows); err != nil {
		t.Fatalf("counting rows: %v", err)
	}
	if rows != 2 {
		t.Errorf("want 2 rows (one per node), got %d — the second write should have updated, not inserted", rows)
	}

	var state string
	if err := db.QueryRowContext(t.Context(),
		"SELECT state FROM "+table+" WHERE workflow_id = @p1 AND node_id = @p2", "wf-1", "n-1").Scan(&state); err != nil {
		t.Fatalf("reading state back: %v", err)
	}
	if state != `{"offset":2}` {
		t.Errorf("state was not updated: got %q, want %q", state, `{"offset":2}`)
	}
}

func replaceTable(query, from, to string) string {
	out := ""
	for {
		i := indexOf(query, from)
		if i < 0 {
			return out + query
		}
		out += query[:i] + to
		query = query[i+len(from):]
	}
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
