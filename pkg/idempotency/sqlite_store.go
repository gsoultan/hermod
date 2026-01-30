package idempotency

import (
	"context"
	"database/sql"
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

func (s *SQLiteStore) ensureTable() error {
	_, err := s.db.Exec(
		"CREATE TABLE IF NOT EXISTS " + s.table + " (" +
			"key TEXT PRIMARY KEY, " +
			"status INTEGER NOT NULL, " +
			"first_seen_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP, " +
			"last_update TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP" +
			")",
	)
	return err
}

// Claim attempts to insert the key; returns true if inserted (we own it), false if it already exists.
func (s *SQLiteStore) Claim(ctx context.Context, key string) (bool, error) {
	res, err := s.db.ExecContext(ctx,
		"INSERT INTO "+s.table+" (key, status) VALUES (?, 0) ON CONFLICT(key) DO NOTHING",
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
		"UPDATE "+s.table+" SET status=1, last_update=CURRENT_TIMESTAMP WHERE key=?",
		key,
	)
	return err
}

// Close closes the underlying DB.
func (s *SQLiteStore) Close() error { return s.db.Close() }
