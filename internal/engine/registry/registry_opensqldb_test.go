package registry

import (
	"strings"
	"testing"

	"github.com/user/hermod/pkg/infra/pgxutil"
)

// TestOpenSQLDBPgBouncerMarkers verifies that the generic SQL path routes the
// pgx driver through pgxutil so the custom pgbouncer/pool_mode markers are
// stripped (they would otherwise be forwarded as Postgres startup parameters
// and fail the handshake) and a usable *sql.DB is returned.
func TestOpenSQLDBPgBouncerMarkers(t *testing.T) {
	tests := []struct {
		name    string
		driver  string
		connStr string
	}{
		{
			name:    "pgbouncer marker url",
			driver:  "pgx",
			connStr: "postgres://user:pass@localhost:6432/db?sslmode=disable&pgbouncer=true",
		},
		{
			name:    "pool_mode marker url",
			driver:  "pgx",
			connStr: "postgres://user:pass@localhost:6432/db?sslmode=disable&pool_mode=transaction",
		},
		{
			name:    "plain postgres url",
			driver:  "pgx",
			connStr: "postgres://user:pass@localhost:5432/db?sslmode=disable",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			db, err := openSQLDB(tc.driver, tc.connStr)
			if err != nil {
				t.Fatalf("openSQLDB(%q) returned error: %v", tc.connStr, err)
			}
			if db == nil {
				t.Fatal("openSQLDB returned a nil *sql.DB")
			}
			t.Cleanup(func() { _ = db.Close() })

			// The cleaned DSN must never carry the custom markers into pgx.
			cfg, pooled, err := pgxutil.ParseConfig(tc.connStr)
			if err != nil {
				t.Fatalf("pgxutil.ParseConfig: %v", err)
			}
			if strings.Contains(tc.connStr, "pgbouncer") || strings.Contains(tc.connStr, "pool_mode") {
				if !pooled {
					t.Errorf("expected pooled=true for %q", tc.connStr)
				}
				if cfg.StatementCacheCapacity != 0 || cfg.DescriptionCacheCapacity != 0 {
					t.Errorf("expected statement/description caches disabled for pooled conn")
				}
			}
			if _, ok := cfg.RuntimeParams["pgbouncer"]; ok {
				t.Error("pgbouncer marker leaked into pgx runtime params")
			}
			if _, ok := cfg.RuntimeParams["pool_mode"]; ok {
				t.Error("pool_mode marker leaked into pgx runtime params")
			}
		})
	}
}
