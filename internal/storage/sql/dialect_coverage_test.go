package sql

import (
	"strings"
	"testing"
)

// The driver strings that actually reach NewSQLStorage. InitSQLStorage maps the
// operator's configured database type onto exactly these four
// (internal/infra/transport/http/infra.go:701-714): sqlite stays sqlite,
// postgres becomes pgx, mysql *and* mariadb both become mysql, and mssql
// becomes sqlserver.
//
// Note what that means for driverOverrides["mariadb"]: nothing ever selects it.
// The key is dead, and its entries duplicate the mysql ones.
var selectableDrivers = []string{"sqlite", "pgx", "mysql", "sqlserver"}

// Dialects that cannot parse SQLite's upsert. PostgreSQL shares the
// `ON CONFLICT ... DO UPDATE ... excluded.` form, so the common query is
// already correct for it; MySQL spells it `ON DUPLICATE KEY UPDATE` and SQL
// Server has no upsert at all and needs a MERGE.
var cannotParseSQLiteUpsert = []string{"mysql", "sqlserver"}

// A query in commonQueries is the SQLite spelling and the fallback for every
// driver with no override. When that spelling is dialect-specific, a driver
// that cannot parse it does not degrade — the statement is rejected outright
// and whatever the query was for silently stops working.
//
// QueryUpdateNodeState was in exactly that state: overridden for mysql and pgx,
// missing for sqlserver, so it fell through to `ON CONFLICT ... DO UPDATE` and
// every attempt to persist a workflow's node state failed on SQL Server. It was
// invisible because this package's tests all run on SQLite, where the common
// query is the right one by construction, and because the one place a live SQL
// Server is exercised in CI is a sink test that never touches node state.
//
// This is the sibling of TestNoQueryBypassesPlaceholderPreparation: that one
// covers the placeholders, this one covers the syntax around them.
func TestEveryDialectCanParseTheQueriesItWillBeGiven(t *testing.T) {
	// Markers of a statement written in a dialect not everyone shares.
	sqliteOnlyUpsert := []string{"ON CONFLICT", "excluded."}

	checked := 0
	for key, query := range commonQueries {
		upper := strings.ToUpper(query)
		isUpsert := false
		for _, marker := range sqliteOnlyUpsert {
			if strings.Contains(upper, strings.ToUpper(marker)) {
				isUpsert = true
				break
			}
		}
		if !isUpsert {
			continue
		}
		checked++

		for _, driver := range cannotParseSQLiteUpsert {
			override, ok := driverOverrides[driver][key]
			if !ok {
				t.Errorf("%s falls through to the SQLite spelling on %q, which cannot parse it:\n  %s\n"+
					"add a %s entry to driverOverrides[%q]",
					key, driver, query, key, driver)
				continue
			}
			if strings.Contains(strings.ToUpper(override), "ON CONFLICT") {
				t.Errorf("driverOverrides[%q][%s] still uses ON CONFLICT, which %q cannot parse:\n  %s",
					driver, key, driver, override)
			}
		}
	}

	if checked == 0 {
		t.Fatal("no upsert-shaped query was examined, so this guard proves nothing")
	}
}

// Every override has to be reachable. A key nothing selects is not a safety
// net — it reads like coverage while the driver it names keeps taking the
// common query.
func TestEveryDriverOverrideIsSelectable(t *testing.T) {
	selectable := map[string]bool{}
	for _, d := range selectableDrivers {
		selectable[d] = true
	}
	for driver := range driverOverrides {
		if !selectable[driver] {
			t.Errorf("driverOverrides[%q] is never selected: InitSQLStorage maps every configured "+
				"database type onto one of %v, so these entries are dead and the driver they "+
				"name keeps taking the common query", driver, selectableDrivers)
		}
	}
}

// The placeholder rewrite runs over whatever `get` returns, so an override that
// hardcodes another dialect's placeholders gets them mangled: preparePlaceholders
// only rewrites `?`, so `$1` reaches SQL Server unchanged and `@p1` reaches pgx
// unchanged.
func TestOverridesUseThePlaceholdersTheirDriverExpects(t *testing.T) {
	for driver, queries := range driverOverrides {
		s := &sqlStorage{driver: driver}
		for key, query := range queries {
			got := s.prepareQuery(query)
			switch driver {
			case "pgx", "postgres":
				if strings.Contains(got, "@p") || strings.Contains(got, "?") {
					t.Errorf("driverOverrides[%q][%s] does not end up with $n placeholders:\n  %s", driver, key, got)
				}
			case "sqlserver":
				if strings.Contains(got, "$1") || strings.Contains(got, "?") {
					t.Errorf("driverOverrides[%q][%s] does not end up with @pn placeholders:\n  %s", driver, key, got)
				}
			case "mysql", "mariadb", "sqlite":
				if strings.Contains(got, "$1") || strings.Contains(got, "@p1") {
					t.Errorf("driverOverrides[%q][%s] does not end up with ? placeholders:\n  %s", driver, key, got)
				}
			}
		}
	}
}
