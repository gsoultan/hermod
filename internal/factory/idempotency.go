package factory

import (
	"context"
	"fmt"
	"time"

	"github.com/gsoultan/hermod/internal/config"
	"github.com/gsoultan/hermod/pkg/security/idempotency"
)

// sinkIdemAdapter adapts the SQLite idempotency store to the minimal interface
// the SMTP and panmail sinks declare. Both declare the same three methods, so
// one adapter satisfies both.
type sinkIdemAdapter struct{ s *idempotency.SQLiteStore }

func (a sinkIdemAdapter) Claim(ctx context.Context, key string) (bool, error) {
	return a.s.Claim(ctx, key)
}
func (a sinkIdemAdapter) Release(ctx context.Context, key string) error {
	return a.s.Release(ctx, key)
}
func (a sinkIdemAdapter) MarkSent(ctx context.Context, key string) error {
	return a.s.MarkSent(ctx, key)
}

// newSinkIdempotencyStore opens the duplicate-suppression store for a sink.
//
// `defaultTable` names the sink, so two sinks sharing the database do not share
// a keyspace: the same message rendered as mail through SMTP and through
// panmail is two different sends, and one suppressing the other would be a
// message silently not delivered.
func newSinkIdempotencyStore(cfg map[string]string, defaultTable string) (*idempotency.SQLiteStore, error) {
	dsn := cfg["idempotency_dsn"]
	if dsn == "" {
		dsn = config.GetConfigPath("hermod.db")
	}

	table := defaultTable
	if suffix := idempotencyTableSuffix(cfg["idempotency_namespace"]); suffix != "" {
		table = defaultTable + "_" + suffix
	}

	store, err := idempotency.NewSQLiteStoreWithTable(dsn, table)
	if err != nil {
		return nil, fmt.Errorf("init idempotency store: %w", err)
	}
	return store, nil
}

// idempotencyTableSuffix reduces an operator-supplied namespace to the
// characters that may appear in a table name.
//
// The namespace reaches SQL as an identifier, so anything outside this set is
// dropped rather than quoted or rejected — a namespace that sanitises to
// nothing simply leaves the default table name alone.
func idempotencyTableSuffix(namespace string) string {
	sanitized := make([]rune, 0, len(namespace))
	for _, r := range namespace {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == '-' {
			sanitized = append(sanitized, r)
		}
	}
	return string(sanitized)
}

// startIdempotencyTTLSweep drops claim records older than the configured TTL,
// hourly, for as long as the process runs.
//
// A blank or unparseable TTL means no sweep, which is the safe default: the
// store only grows, and a claim that is deleted early is a duplicate that can
// be sent again.
func startIdempotencyTTLSweep(store *idempotency.SQLiteStore, ttlStr string) {
	if ttlStr == "" {
		return
	}
	ttl, err := time.ParseDuration(ttlStr)
	if err != nil || ttl <= 0 {
		return
	}

	go func() {
		ticker := time.NewTicker(1 * time.Hour)
		defer ticker.Stop()
		ctx := context.Background()
		_ = store.CleanupTTL(ctx, ttl)
		for range ticker.C {
			_ = store.CleanupTTL(ctx, ttl)
		}
	}()
}
