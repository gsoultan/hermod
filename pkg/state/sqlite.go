package state

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/user/hermod"
	_ "modernc.org/sqlite"
)

type SQLiteStateStore struct {
	db *sql.DB
}

func NewSQLiteStateStore(path string) (hermod.StateStore, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("failed to open sqlite state store: %w", err)
	}

	_, err = db.Exec(commonQueries[QueryInitTable])
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to create states table: %w", err)
	}

	return &SQLiteStateStore{db: db}, nil
}

func (s *SQLiteStateStore) Get(ctx context.Context, key string) ([]byte, error) {
	var val []byte
	err := s.db.QueryRowContext(ctx, commonQueries[QueryGet], key).Scan(&val)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return val, nil
}

func (s *SQLiteStateStore) Set(ctx context.Context, key string, value []byte) error {
	_, err := s.db.ExecContext(ctx, commonQueries[QuerySet], key, value)
	return err
}

func (s *SQLiteStateStore) Delete(ctx context.Context, key string) error {
	_, err := s.db.ExecContext(ctx, commonQueries[QueryDelete], key)
	return err
}

func (s *SQLiteStateStore) Close() error {
	return s.db.Close()
}
