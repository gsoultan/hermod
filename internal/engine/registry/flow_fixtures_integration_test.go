//go:build integration
// +build integration

package registry

import (
	"database/sql"
	"os"
	"testing"
)

// flowDSNs resolves where the node-by-node pipeline tests should run.
//
// FLOW_SOURCE_DSN/FLOW_SINK_DSN point the source and the sink at separate
// databases, which is how it is usually run locally. CI has one PostgreSQL
// service and exports POSTGRES_DSN, so that is the fallback for both: the
// publication names only the source table, so sink writes into the same
// database are not captured and the two do not interfere.
func flowDSNs(t *testing.T) (srcDSN, sinkDSN string) {
	t.Helper()
	if os.Getenv("HERMOD_INTEGRATION") != "1" {
		t.Skip("integration: set HERMOD_INTEGRATION=1 to run")
	}
	shared := os.Getenv("POSTGRES_DSN")
	srcDSN = firstNonEmpty(os.Getenv("FLOW_SOURCE_DSN"), shared)
	sinkDSN = firstNonEmpty(os.Getenv("FLOW_SINK_DSN"), shared)
	if srcDSN == "" || sinkDSN == "" {
		t.Skip("integration: set POSTGRES_DSN, or FLOW_SOURCE_DSN and FLOW_SINK_DSN")
	}
	return srcDSN, sinkDSN
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// openFlowDB opens a DSN and fails — rather than skips — if it is unreachable.
// A DSN naming a server is the statement that it should be there.
func openFlowDB(t *testing.T, dsn string) *sql.DB {
	t.Helper()
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("open %s: %v", redactDSN(dsn), err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.PingContext(t.Context()); err != nil {
		t.Fatalf("the configured PostgreSQL is not reachable: %v", err)
	}
	return db
}

// provisionFlowFixtures creates everything these tests read and write.
//
// They used to assume a schema someone had built by hand, which meant they
// skipped everywhere except the machine they were written on — including CI,
// where the defects they guard would otherwise go unnoticed until a person ran
// them locally again.
func provisionFlowFixtures(t *testing.T, srcDB, sinkDB *sql.DB) {
	t.Helper()

	// Logical decoding is the whole subject; without it there is nothing to test
	// and a failure here would name the wrong thing.
	var walLevel string
	if err := srcDB.QueryRowContext(t.Context(), "SHOW wal_level").Scan(&walLevel); err != nil {
		t.Fatalf("read wal_level: %v", err)
	}
	if walLevel != "logical" {
		t.Skipf("integration: source PostgreSQL has wal_level=%q, CDC needs \"logical\"", walLevel)
	}

	mustExec(t, srcDB, `CREATE TABLE IF NOT EXISTS flow_orders (
		id          TEXT PRIMARY KEY,
		customer_id TEXT NOT NULL,
		amount      TEXT NOT NULL,
		qty         TEXT NOT NULL)`)
	// FULL so an UPDATE carries a before-image; the after-image is repaired from
	// it when a TOASTed column is unchanged.
	mustExec(t, srcDB, `ALTER TABLE flow_orders REPLICA IDENTITY FULL`)

	mustExec(t, srcDB, `CREATE TABLE IF NOT EXISTS flow_customers (
		code TEXT PRIMARY KEY,
		name TEXT NOT NULL,
		tier TEXT NOT NULL)`)
	mustExec(t, srcDB, `INSERT INTO flow_customers (code,name,tier)
		VALUES ('C-1','ACME Corp','gold'), ('C-2','Globex','silver')
		ON CONFLICT (code) DO UPDATE SET name = EXCLUDED.name, tier = EXCLUDED.tier`)

	// CREATE PUBLICATION has no IF NOT EXISTS form.
	var havePub bool
	if err := srcDB.QueryRowContext(t.Context(),
		`SELECT EXISTS (SELECT 1 FROM pg_publication WHERE pubname = 'flow_pub')`).Scan(&havePub); err != nil {
		t.Fatalf("check publication: %v", err)
	}
	if !havePub {
		mustExec(t, srcDB, `CREATE PUBLICATION flow_pub FOR TABLE flow_orders`)
	}

	mustExec(t, sinkDB, `CREATE TABLE IF NOT EXISTS flow_orders_enriched (
		order_id      TEXT PRIMARY KEY,
		customer_id   TEXT,
		customer_name TEXT,
		amount        NUMERIC,
		qty           INTEGER)`)
	// No column mappings on the sink that writes here: it stores msg.Payload().
	mustExec(t, sinkDB, `CREATE TABLE IF NOT EXISTS flow_orders_raw (
		id   TEXT PRIMARY KEY,
		data JSONB)`)
}
