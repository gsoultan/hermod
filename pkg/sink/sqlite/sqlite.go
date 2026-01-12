package sqlite

import (
	"context"
	"database/sql"
	"fmt"

	_ "github.com/mattn/go-sqlite3"
	"github.com/user/hermod"
)

// SQLiteSink implements the hermod.Sink interface for SQLite.
type SQLiteSink struct {
	dbPath string
	db     *sql.DB
}

func NewSQLiteSink(dbPath string) *SQLiteSink {
	return &SQLiteSink{
		dbPath: dbPath,
	}
}

func (s *SQLiteSink) Write(ctx context.Context, msg hermod.Message) error {
	if s.db == nil {
		if err := s.init(ctx); err != nil {
			return err
		}
	}

	query := fmt.Sprintf("INSERT OR REPLACE INTO %s (id, data) VALUES (?, ?)", msg.Table())
	_, err := s.db.ExecContext(ctx, query, msg.ID(), msg.After())
	if err != nil {
		return fmt.Errorf("failed to write to sqlite: %w", err)
	}

	return nil
}

func (s *SQLiteSink) init(ctx context.Context) error {
	db, err := sql.Open("sqlite3", s.dbPath)
	if err != nil {
		return fmt.Errorf("failed to open sqlite db: %w", err)
	}
	s.db = db
	return s.db.PingContext(ctx)
}

func (s *SQLiteSink) Ping(ctx context.Context) error {
	if s.db == nil {
		if err := s.init(ctx); err != nil {
			return err
		}
	}
	return s.db.PingContext(ctx)
}

func (s *SQLiteSink) Close() error {
	if s.db != nil {
		return s.db.Close()
	}
	return nil
}
