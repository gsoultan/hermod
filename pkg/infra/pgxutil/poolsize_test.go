package pgxutil

import (
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// Pool sizing.
//
// ParsePoolConfig raised the pool size when it looked small:
//
//	if cfg.MaxConns <= 10 { cfg.MaxConns = 50 }
//
// The intent was a sensible default, but the condition cannot tell "pgx picked
// a small default because nobody said otherwise" from "the operator asked for
// five". So an explicit pool_max_conns of 10 or less was silently replaced with
// 50 — per DSN, and Hermod opens a pool per distinct DSN.
//
// That is not a tuning nicety. Managed Postgres instances sell connection
// limits, PgBouncer pools are sized deliberately, and a default max_connections
// is 100: three DSNs at 50 each exhausts it. The failure mode is the server
// dropping connections mid-query, which surfaces as unrelated-looking errors
// somewhere else entirely.
// ---------------------------------------------------------------------------

func TestExplicitPoolSizeIsRespected(t *testing.T) {
	for _, want := range []int32{1, 2, 5, 8, 10} {
		t.Run(strings.ReplaceAll(dsnFor(want), "postgres://u:p@h/db?", ""), func(t *testing.T) {
			cfg, _, err := ParsePoolConfig(dsnFor(want))
			if err != nil {
				t.Fatalf("ParsePoolConfig: %v", err)
			}
			if cfg.MaxConns != want {
				t.Errorf("pool_max_conns=%d became MaxConns=%d; an operator capping "+
					"connections against a managed or pooled Postgres does not get the cap "+
					"they asked for, and enough DSNs at the inflated size exhaust the server",
					want, cfg.MaxConns)
			}
		})
	}
}

// TestLargeExplicitPoolSizeIsRespected is the case that already worked, kept so
// the fix cannot regress it in the other direction.
func TestLargeExplicitPoolSizeIsRespected(t *testing.T) {
	cfg, _, err := ParsePoolConfig(dsnFor(75))
	if err != nil {
		t.Fatalf("ParsePoolConfig: %v", err)
	}
	if cfg.MaxConns != 75 {
		t.Errorf("MaxConns = %d, want 75", cfg.MaxConns)
	}
}

// TestDefaultPoolSizeAppliesWhenUnspecified keeps the behaviour the raise was
// added for: without an explicit setting, pgx defaults to a handful of
// connections, which is too few for the concurrency Hermod runs at.
func TestDefaultPoolSizeAppliesWhenUnspecified(t *testing.T) {
	cfg, _, err := ParsePoolConfig("postgres://u:p@h/db?sslmode=disable")
	if err != nil {
		t.Fatalf("ParsePoolConfig: %v", err)
	}
	if cfg.MaxConns != defaultMaxConns {
		t.Errorf("MaxConns = %d with nothing specified, want the %d default",
			cfg.MaxConns, defaultMaxConns)
	}
}

// TestExplicitPoolSizeIsRespectedInKeywordDSN covers the other DSN syntax; both
// forms reach this code and only one of them was ever exercised.
func TestExplicitPoolSizeIsRespectedInKeywordDSN(t *testing.T) {
	cfg, _, err := ParsePoolConfig("host=h user=u password=p dbname=db pool_max_conns=4")
	if err != nil {
		t.Fatalf("ParsePoolConfig: %v", err)
	}
	if cfg.MaxConns != 4 {
		t.Errorf("keyword-form pool_max_conns=4 became MaxConns=%d", cfg.MaxConns)
	}
}

// TestPoolSizeSurvivesPoolerMarkerStripping guards an interaction: Hermod
// strips its own pgbouncer=true marker out of the DSN before handing it to pgx,
// and that rewriting must not take pool_max_conns with it.
func TestPoolSizeSurvivesPoolerMarkerStripping(t *testing.T) {
	cfg, pooled, err := ParsePoolConfig("postgres://u:p@h/db?pool_max_conns=6&pgbouncer=true")
	if err != nil {
		t.Fatalf("ParsePoolConfig: %v", err)
	}
	if !pooled {
		t.Error("pgbouncer=true was not detected")
	}
	if cfg.MaxConns != 6 {
		t.Errorf("MaxConns = %d after the pooler marker was stripped, want 6", cfg.MaxConns)
	}
}

func dsnFor(n int32) string {
	return "postgres://u:p@h/db?pool_max_conns=" + itoa(n)
}

func itoa(n int32) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}
