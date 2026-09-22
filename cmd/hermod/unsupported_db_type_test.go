package main

import (
	"strings"
	"testing"
)

// An unsupported metadata store must be refused by name.
//
// getSQLDriverAndConn knows sqlite, postgres and mysql/mariadb. Anything else
// falls off the end of its switch and returns an empty driver string, which
// initSQLStorage hands straight to sql.Open — so the server dies with
//
//	failed to open database: sql: unknown driver "" (forgotten import?)
//
// which names neither the setting that is wrong nor the value that was in it.
// "forgotten import" actively misdirects: nothing is missing from the build,
// the configured type simply is not one Hermod stores its catalogue in.
//
// mssql is the case that makes this reachable rather than theoretical. The two
// places that map a configured type onto a driver disagree about it:
// InitSQLStorage (internal/infra/transport/http/infra.go:701) accepts it and
// returns "sqlserver", while this path does not accept it at all. So the same
// value is a working configuration through the settings API and an obscure
// crash at start-up. README documents MSSQL as a source and a sink, never as
// the metadata store, and the UI's database picker offers only SQLite,
// PostgreSQL and MySQL — so refusing it here is the behaviour that matches what
// Hermod actually claims, and the disagreement is worth naming rather than
// silently resolving in favour of either side.
//
// Same shape as TestPebbleIsRefusedAsAMetadataStore: a backend that cannot hold
// the catalogue should be turned away with a message, not accepted and then
// discovered.
func TestUnsupportedMetadataStoreIsRefusedByName(t *testing.T) {
	for _, dbType := range []string{"mssql", "oracle", "cockroach", ""} {
		t.Run(dbType, func(t *testing.T) {
			_, err := initStorage(dbType, "whatever://connection")
			if err == nil {
				t.Fatalf("%q was accepted as a metadata store", dbType)
			}
			msg := err.Error()
			if strings.Contains(msg, "unknown driver") || strings.Contains(msg, "forgotten import") {
				t.Errorf("%q fails with the driver layer's error rather than a refusal that names "+
					"the setting:\n  %s", dbType, msg)
			}
			if dbType != "" && !strings.Contains(msg, dbType) {
				t.Errorf("the error for %q does not name the value that was configured:\n  %s", dbType, msg)
			}
			// The operator's next question is "then what may I use?".
			for _, supported := range []string{"sqlite", "postgres", "mysql"} {
				if !strings.Contains(msg, supported) {
					t.Errorf("the error for %q does not mention %q as a supported type:\n  %s",
						dbType, supported, msg)
				}
			}
		})
	}
}

// The types that do work must keep working — a guard that only ever refuses is
// easy to get right and useless.
func TestSupportedMetadataStoresStillResolve(t *testing.T) {
	for _, tc := range []struct{ dbType, wantDriver string }{
		{"sqlite", "sqlite"},
		{"postgres", "pgx"},
		{"mysql", "mysql"},
		{"mariadb", "mysql"},
	} {
		driver, _ := getSQLDriverAndConn(tc.dbType, "conn")
		if driver != tc.wantDriver {
			t.Errorf("%s: got driver %q, want %q", tc.dbType, driver, tc.wantDriver)
		}
	}
}
