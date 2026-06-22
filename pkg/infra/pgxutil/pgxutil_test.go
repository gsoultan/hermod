package pgxutil

import (
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
)

func TestIsPooledConnString(t *testing.T) {
	tests := []struct {
		name       string
		connString string
		want       bool
	}{
		{"plain url", "postgres://u:p@localhost:5432/db?sslmode=disable", false},
		{"pgbouncer true", "postgres://u:p@host:6432/db?pgbouncer=true", true},
		{"pgbouncer false", "postgres://u:p@host:6432/db?pgbouncer=false", false},
		{"pool_mode transaction", "postgres://u:p@host:6432/db?pool_mode=transaction", true},
		{"pool_mode statement", "postgres://u:p@host:6432/db?pool_mode=statement", true},
		{"pool_mode session", "postgres://u:p@host:6432/db?pool_mode=session", false},
		{"dsn pgbouncer", "host=localhost port=6432 dbname=db pgbouncer=true", true},
		{"dsn pool_mode", "host=localhost port=6432 dbname=db pool_mode=transaction", true},
		{"empty", "", false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsPooledConnString(tc.connString); got != tc.want {
				t.Errorf("IsPooledConnString(%q) = %v; want %v", tc.connString, got, tc.want)
			}
		})
	}
}

func TestStripPoolerParams_RemovesMarkers(t *testing.T) {
	cleaned, pooled := stripPoolerParams("postgres://u:p@host:6432/db?sslmode=require&pgbouncer=true")
	if !pooled {
		t.Fatalf("expected pooled=true")
	}
	if strings.Contains(cleaned, "pgbouncer") {
		t.Errorf("cleaned string still contains pooler marker: %q", cleaned)
	}
	if !strings.Contains(cleaned, "sslmode=require") {
		t.Errorf("cleaned string dropped sslmode: %q", cleaned)
	}
}

func TestStripDSNPoolerParams_PreservesOtherTokens(t *testing.T) {
	cleaned, pooled := stripPoolerParams("host=localhost port=6432 dbname=db pool_mode=transaction sslmode=disable")
	if !pooled {
		t.Fatalf("expected pooled=true")
	}
	if strings.Contains(cleaned, "pool_mode") {
		t.Errorf("cleaned DSN still contains pool_mode: %q", cleaned)
	}
	for _, want := range []string{"host=localhost", "port=6432", "dbname=db", "sslmode=disable"} {
		if !strings.Contains(cleaned, want) {
			t.Errorf("cleaned DSN dropped %q: %q", want, cleaned)
		}
	}
}

func TestParseConfig_AppliesPoolerSafety(t *testing.T) {
	cfg, pooled, err := ParseConfig("postgres://u:p@host:6432/db?pgbouncer=true&sslmode=disable")
	if err != nil {
		t.Fatalf("ParseConfig error: %v", err)
	}
	if !pooled {
		t.Fatalf("expected pooled=true")
	}
	if cfg.DefaultQueryExecMode != pgx.QueryExecModeExec {
		t.Errorf("DefaultQueryExecMode = %v; want QueryExecModeExec", cfg.DefaultQueryExecMode)
	}
	if cfg.StatementCacheCapacity != 0 {
		t.Errorf("StatementCacheCapacity = %d; want 0", cfg.StatementCacheCapacity)
	}
	if cfg.DescriptionCacheCapacity != 0 {
		t.Errorf("DescriptionCacheCapacity = %d; want 0", cfg.DescriptionCacheCapacity)
	}
}

func TestParseConfig_DirectKeepsDefaults(t *testing.T) {
	cfg, pooled, err := ParseConfig("postgres://u:p@host:5432/db?sslmode=disable")
	if err != nil {
		t.Fatalf("ParseConfig error: %v", err)
	}
	if pooled {
		t.Fatalf("expected pooled=false for a direct connection")
	}
	if cfg.DefaultQueryExecMode == pgx.QueryExecModeExec {
		t.Errorf("direct connection should keep the default exec mode, got QueryExecModeExec")
	}
}

func TestParsePoolConfig_AppliesPoolerSafety(t *testing.T) {
	cfg, pooled, err := ParsePoolConfig("postgres://u:p@host:6432/db?pool_mode=transaction")
	if err != nil {
		t.Fatalf("ParsePoolConfig error: %v", err)
	}
	if !pooled {
		t.Fatalf("expected pooled=true")
	}
	if cfg.ConnConfig.DefaultQueryExecMode != pgx.QueryExecModeExec {
		t.Errorf("DefaultQueryExecMode = %v; want QueryExecModeExec", cfg.ConnConfig.DefaultQueryExecMode)
	}
}
