//go:build integration
// +build integration

package registry

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/gsoultan/hermod/internal/factory"
	sqlstorage "github.com/gsoultan/hermod/internal/storage/sql"
	_ "github.com/jackc/pgx/v5/stdlib"
	_ "modernc.org/sqlite"
)

// poolFixture stands up two databases on the same Postgres server: one holding
// recruitment.applications, one without it. That is the shape that made the
// connection-pool key collision visible — the table exists, but the preview
// query ran on the wrong database and came back 42P01.
type poolFixture struct {
	reg        *Registry
	otherCfg   factory.SourceConfig
	recruitCfg factory.SourceConfig
}

func newPoolFixture(t *testing.T) *poolFixture {
	t.Helper()

	dsn := os.Getenv("POSTGRES_DSN")
	if os.Getenv("HERMOD_INTEGRATION") != "1" || dsn == "" {
		t.Skip("integration: set HERMOD_INTEGRATION=1 and POSTGRES_DSN to run")
	}

	admin, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("open postgres: %v", err)
	}
	if err := admin.PingContext(t.Context()); err != nil {
		t.Fatalf("ping postgres: %v", err)
	}
	t.Cleanup(func() { _ = admin.Close() })

	suffix := strings.ToLower(strings.NewReplacer("/", "_", "-", "_").Replace(t.Name()))
	otherDB := "hermod_pool_other_" + suffix
	recruitDB := "hermod_pool_recruit_" + suffix

	swapDatabase := func(name string) string {
		u, err := url.Parse(dsn)
		if err != nil {
			t.Fatalf("parse POSTGRES_DSN: %v", err)
		}
		u.Path = "/" + name
		return u.String()
	}

	exec := func(db *sql.DB, q string) {
		t.Helper()
		if _, err := db.ExecContext(t.Context(), q); err != nil {
			t.Fatalf("exec %q: %v", q, err)
		}
	}

	for _, name := range []string{otherDB, recruitDB} {
		exec(admin, fmt.Sprintf("DROP DATABASE IF EXISTS %s WITH (FORCE)", name))
		exec(admin, "CREATE DATABASE "+name)
	}

	// The relation only exists in one of the two databases. A query naming it can
	// therefore only succeed on a connection actually opened against that one.
	recruitDSN := swapDatabase(recruitDB)
	seed, err := sql.Open("pgx", recruitDSN)
	if err != nil {
		t.Fatalf("open %s: %v", recruitDB, err)
	}
	exec(seed, "CREATE SCHEMA recruitment")
	exec(seed, "CREATE TABLE recruitment.applications (id INT PRIMARY KEY, candidate TEXT)")
	exec(seed, "INSERT INTO recruitment.applications VALUES (1, 'ada')")
	_ = seed.Close()

	meta, err := sql.Open("sqlite", "file:pool_"+suffix+"?mode=memory&cache=shared")
	if err != nil {
		t.Fatalf("open metadata db: %v", err)
	}
	t.Cleanup(func() { _ = meta.Close() })
	store := sqlstorage.NewSQLStorage(meta, "sqlite")
	if err := store.Init(t.Context()); err != nil {
		t.Fatalf("init metadata store: %v", err)
	}

	reg := NewRegistry(store)
	t.Cleanup(func() {
		// Close the pools before dropping, then force out anything left over.
		reg.Close()
		for _, name := range []string{otherDB, recruitDB} {
			_, _ = admin.ExecContext(context.Background(),
				fmt.Sprintf("DROP DATABASE IF EXISTS %s WITH (FORCE)", name))
		}
	})

	pgCfg := func(dsn string) factory.SourceConfig {
		return factory.SourceConfig{Type: "postgres", Config: map[string]string{"connection_string": dsn}}
	}
	return &poolFixture{
		reg:        reg,
		otherCfg:   pgCfg(swapDatabase(otherDB)),
		recruitCfg: pgCfg(recruitDSN),
	}
}

// sample is the incoming-message stand-in the query builder binds {{ }} tokens
// against. Supplying one keeps ExecuteSQL off its table-sampling fallback.
var sample = map[string]any{"after": map[string]any{"id": 1}}

// A templated query is what makes this reachable. ExecuteSQL only opens a
// dedicated source connection when the query has no {{ }} tokens; as soon as one
// binds a parameter it falls through to the shared, ID-less connection pool
// (service.go:514-531). A db_lookup template is parameterized by definition —
// that is the whole point of the node — so this branch is the normal one there
// and the plain-SELECT previews elsewhere in the UI never see the bug.
const lookupQuery = "SELECT id, candidate FROM recruitment.applications WHERE id = {{ after.id }}"

// TestExecuteSQLRunsOnTheSelectedDatabase reproduces the reported failure through
// the exact path the db_lookup SQL query builder takes: Registry.ExecuteSQL with
// an ad-hoc config and no source ID. Previewing any other source first used to
// pin a pool under the empty key, so this query ran on that database and failed
// with ERROR: relation "recruitment.applications" does not exist (SQLSTATE 42P01)
// even though the relation exists in the source the user picked.
func TestExecuteSQLRunsOnTheSelectedDatabase(t *testing.T) {
	f := newPoolFixture(t)

	if _, err := f.reg.ExecuteSQL(t.Context(), f.otherCfg, "SELECT 1 AS n WHERE 1 = {{ after.id }}", sample); err != nil {
		t.Fatalf("priming query on the unrelated source: %v", err)
	}

	rows, err := f.reg.ExecuteSQL(t.Context(), f.recruitCfg, lookupQuery, sample)
	if err != nil {
		t.Fatalf("query ran against the wrong database: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("got %d rows, want 1: %v", len(rows), rows)
	}
}

// And the reverse direction: a source that genuinely lacks the relation must
// still report 42P01 rather than being silently served the other pool.
func TestExecuteSQLStillReportsAGenuinelyMissingRelation(t *testing.T) {
	f := newPoolFixture(t)

	if _, err := f.reg.ExecuteSQL(t.Context(), f.recruitCfg, lookupQuery, sample); err != nil {
		t.Fatalf("priming query on the recruitment source: %v", err)
	}

	_, err := f.reg.ExecuteSQL(t.Context(), f.otherCfg, lookupQuery, sample)
	if err == nil {
		t.Fatal("a source without the relation reported success; it was served the other pool")
	}
	if !strings.Contains(err.Error(), "42P01") {
		t.Fatalf("want an undefined-table error, got: %v", err)
	}
}
