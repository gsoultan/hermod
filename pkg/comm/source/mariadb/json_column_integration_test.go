//go:build integration
// +build integration

package mariadb

import (
	"database/sql"
	"os"
	"testing"

	_ "github.com/go-sql-driver/mysql"
	"github.com/gsoultan/hermod/pkg/infra/sqlutil"
)

// MariaDB has no JSON type, and that is why this source does not decode JSON
// columns the way the MySQL and PostgreSQL sources now do.
//
// `JSON` in MariaDB is an alias for LONGTEXT with a check constraint, so the
// driver reports the column as TEXT -- byte-for-byte the same metadata a real
// LONGTEXT column produces. There is no signal to act on. Deciding from the
// *content* instead would reshape every LONGTEXT that happens to parse as JSON,
// which is a far larger and much less predictable change than the bug it would
// fix, and it would do it to columns nobody declared as JSON.
//
// This test exists so that a later reader who notices MariaDB "missing" the fix
// finds the reason here rather than adding a content sniffer. If MariaDB ever
// reports a distinct type name, this fails and the source picks the fix up for
// free -- it already routes every row through sqlutil.RecordFromValues.
//
//	container run -d --name hermod-jsonb-maria \
//	  -e MARIADB_ROOT_PASSWORD=root -e MARIADB_DATABASE=hermod_it \
//	  docker.io/arm64v8/mariadb:11.4
//
//	HERMOD_INTEGRATION=1 MARIADB_DSN='root:root@tcp(<container-ip>:3306)/hermod_it' \
//	  go test -tags=integration -run TestMariaDB ./pkg/comm/source/mariadb/
//
// Use the container's own IP: Apple's `container` drops the -p forward once the
// server starts writing, and a port that still accepts a TCP connection is not
// the same as one that reaches the database.
func TestMariaDBHasNoDistinctJSONColumnType(t *testing.T) {
	dsn := os.Getenv("MARIADB_DSN")
	if dsn == "" || os.Getenv("HERMOD_INTEGRATION") != "1" {
		t.Skip("integration: set HERMOD_INTEGRATION=1 and MARIADB_DSN to run")
	}
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()

	const table = "hermod_json_type_probe"
	if _, err := db.ExecContext(t.Context(), "DROP TABLE IF EXISTS "+table); err != nil {
		t.Fatalf("drop: %v", err)
	}
	if _, err := db.ExecContext(t.Context(),
		"CREATE TABLE "+table+" (meta JSON, big LONGTEXT)"); err != nil {
		t.Fatalf("create: %v", err)
	}
	t.Cleanup(func() { _, _ = db.Exec("DROP TABLE IF EXISTS " + table) })

	rows, err := db.QueryContext(t.Context(), "SELECT meta, big FROM "+table)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	defer rows.Close()

	names := sqlutil.ColumnTypeNames(rows)
	if len(names) != 2 {
		t.Fatalf("got %d column types, want 2: %v", len(names), names)
	}
	t.Logf("MariaDB reports JSON as %q and LONGTEXT as %q", names[0], names[1])

	if names[0] != names[1] {
		t.Fatalf("MariaDB now distinguishes a JSON column (%q) from LONGTEXT (%q) -- "+
			"delete this test; sqlutil.IsJSONColumnType will pick it up and the "+
			"source decodes JSON columns from here on", names[0], names[1])
	}
	if sqlutil.IsJSONColumnType(names[0]) {
		t.Errorf("IsJSONColumnType(%q) = true, but that name also means LONGTEXT here; "+
			"every long text column would be reshaped", names[0])
	}
}
