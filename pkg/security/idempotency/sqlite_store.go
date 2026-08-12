package idempotency

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	// modernc.org/sqlite registers the pure-Go "sqlite" database/sql driver via init().
	_ "modernc.org/sqlite"
)

// SQLiteStore is a minimal idempotency store backed by SQLite.
type SQLiteStore struct {
	db    *sql.DB
	table string
}

// NewSQLiteStore opens (or creates) a SQLite database at dsn and ensures the idempotency table exists.
// dsn can be a file path like "hermod.db" or a full SQLite DSN.
func NewSQLiteStore(dsn string) (*SQLiteStore, error) {
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	s := &SQLiteStore{db: db, table: "smtp_idempotency"}
	if err := s.ensureTable(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

// NewSQLiteStoreWithTable allows specifying a custom table name (namespace).
func NewSQLiteStoreWithTable(dsn, table string) (*SQLiteStore, error) {
	if table == "" {
		table = "smtp_idempotency"
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	s := &SQLiteStore{db: db, table: table}
	if err := s.ensureTable(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

func (s *SQLiteStore) ensureTable() error {
	_, err := s.db.Exec(fmt.Sprintf(commonQueries[QueryInitTable], s.table))
	return err
}

// Claim attempts to insert the key; returns true if inserted (we own it), false if it already exists.
func (s *SQLiteStore) Claim(ctx context.Context, key string) (bool, error) {
	res, err := s.db.ExecContext(ctx,
		fmt.Sprintf(commonQueries[QueryClaim], s.table),
		key,
	)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// MarkSent marks the key as successfully processed.
func (s *SQLiteStore) MarkSent(ctx context.Context, key string) error {
	_, err := s.db.ExecContext(ctx,
		fmt.Sprintf(commonQueries[QueryMarkSent], s.table),
		key,
	)
	return err
}

// Release gives up a claim that never completed, so a retry can take it again.
//
// A claim is taken before the work and marked sent afterwards. Without a way to
// give it up, a failed attempt leaves the key claimed for good: the retry is
// told it was already handled, and the message is dropped while the caller
// reports a suppressed duplicate. That is at-most-once wearing at-least-once's
// clothes, and it is the delivery guarantee this package underwrites.
//
// Only unsent claims are released. A key that was marked sent stays, because
// releasing it would re-admit the duplicate the whole store exists to suppress.
//
// Releasing something never claimed is not an error: retry paths call this
// speculatively, and failing them over a no-op would be its own bug.
func (s *SQLiteStore) Release(ctx context.Context, key string) error {
	_, err := s.db.ExecContext(ctx,
		fmt.Sprintf(commonQueries[QueryRelease], s.table),
		key,
	)
	return err
}

// Close closes the underlying DB.
func (s *SQLiteStore) Close() error { return s.db.Close() }

// CleanupTTL removes entries with last_update older than now-ttl.
// This is a best-effort maintenance function; errors are returned but safe to ignore by callers.
func (s *SQLiteStore) CleanupTTL(ctx context.Context, ttl time.Duration) error {
	if ttl <= 0 {
		return nil
	}
	cutoff := time.Now().Add(-ttl).UTC().Format("2006-01-02 15:04:05")
	_, err := s.db.ExecContext(ctx, fmt.Sprintf("DELETE FROM %s WHERE last_update < ?", s.table), cutoff)
	return err
}
