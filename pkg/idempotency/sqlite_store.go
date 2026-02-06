package idempotency

import (
	"context"
	"database/sql"
	"fmt"
	"time"

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
