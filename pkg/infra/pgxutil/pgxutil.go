// Package pgxutil centralizes pgx connection configuration so that every
// Postgres connection (source metadata, sink pool, pgvector pool, state store)
// is built consistently and is safe to use behind a transaction/statement
// pooling proxy such as PgBouncer.
//
// PgBouncer in transaction or statement pooling mode multiplexes client
// sessions onto a small set of backend connections. Server-side prepared
// statements and the extended query protocol (pgx's default) break in that
// environment with errors like
//
//	ERROR: prepared statement "stmtcache_xxx" already exists
//
// because a cached statement created on one backend may be executed on a
// different one. To avoid this we switch pgx to the simple/exec query mode and
// disable the statement and description caches whenever the connection targets
// a pooler.
package pgxutil

import (
	"net/url"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// poolerParams are custom, non-libpq query parameters callers may add to a
// connection string to declare that it points at a pooler. They are stripped
// before the string is handed to pgx (which would otherwise forward unknown
// keys as Postgres startup parameters and fail the handshake).
const (
	paramPgBouncer = "pgbouncer"
	paramPoolMode  = "pool_mode"
)

// IsPooledConnString reports whether connString declares that it targets a
// transaction/statement pooling proxy (e.g. PgBouncer) via the custom
// "pgbouncer=true" or "pool_mode=transaction|statement" markers.
func IsPooledConnString(connString string) bool {
	_, pooled := stripPoolerParams(connString)
	return pooled
}

// ApplyPoolerSafety configures a pgx connection config for use behind a
// transaction/statement pooling proxy by disabling the extended protocol's
// server-side prepared-statement and description caches.
func ApplyPoolerSafety(cfg *pgx.ConnConfig) {
	if cfg == nil {
		return
	}
	cfg.DefaultQueryExecMode = pgx.QueryExecModeExec
	cfg.StatementCacheCapacity = 0
	cfg.DescriptionCacheCapacity = 0
}

// ParseConfig parses connString into a pgx connection config, transparently
// stripping the custom pooler markers and applying pooler-safe settings when
// they are present. The second return value reports whether the connection was
// detected to target a pooler.
func ParseConfig(connString string) (*pgx.ConnConfig, bool, error) {
	cleaned, pooled := stripPoolerParams(connString)
	cfg, err := pgx.ParseConfig(cleaned)
	if err != nil {
		return nil, pooled, err
	}
	if pooled {
		ApplyPoolerSafety(cfg)
	}
	return cfg, pooled, nil
}

// ParsePoolConfig parses connString into a pgxpool config, applying the same
// pooler-safe settings as ParseConfig when a pooler is detected.
func ParsePoolConfig(connString string) (*pgxpool.Config, bool, error) {
	cleaned, pooled := stripPoolerParams(connString)
	cfg, err := pgxpool.ParseConfig(cleaned)
	if err != nil {
		return nil, pooled, err
	}
	if pooled {
		ApplyPoolerSafety(cfg.ConnConfig)
	}
	return cfg, pooled, nil
}

// stripPoolerParams removes the custom pooler markers from connString (in both
// URL and keyword/value DSN forms) and reports whether any of them declared a
// pooled connection.
func stripPoolerParams(connString string) (string, bool) {
	if connString == "" {
		return connString, false
	}
	if strings.Contains(connString, "://") {
		return stripURLPoolerParams(connString)
	}
	return stripDSNPoolerParams(connString)
}

func stripURLPoolerParams(connString string) (string, bool) {
	u, err := url.Parse(connString)
	if err != nil {
		return connString, false
	}
	q := u.Query()
	pooled := truthyPgBouncer(q.Get(paramPgBouncer)) || poolingMode(q.Get(paramPoolMode))
	q.Del(paramPgBouncer)
	q.Del(paramPoolMode)
	u.RawQuery = q.Encode()
	return u.String(), pooled
}

func stripDSNPoolerParams(connString string) (string, bool) {
	fields := strings.Fields(connString)
	kept := make([]string, 0, len(fields))
	pooled := false
	for _, f := range fields {
		key, val, found := strings.Cut(f, "=")
		if !found {
			kept = append(kept, f)
			continue
		}
		switch strings.ToLower(key) {
		case paramPgBouncer:
			if truthyPgBouncer(strings.Trim(val, "'\"")) {
				pooled = true
			}
		case paramPoolMode:
			if poolingMode(strings.Trim(val, "'\"")) {
				pooled = true
			}
		default:
			kept = append(kept, f)
		}
	}
	return strings.Join(kept, " "), pooled
}

func truthyPgBouncer(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "true", "1", "yes", "on":
		return true
	default:
		return false
	}
}

func poolingMode(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "transaction", "statement":
		return true
	default:
		return false
	}
}
