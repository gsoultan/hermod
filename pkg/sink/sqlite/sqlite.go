package sqlite

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/user/hermod"
	_ "modernc.org/sqlite"
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

	table := msg.Table()
	if msg.Schema() != "" {
		table = fmt.Sprintf("%s_%s", msg.Schema(), table)
	}

	op := msg.Operation()
	if op == "" {
		op = hermod.OpCreate
	}

	switch op {
	case hermod.OpCreate, hermod.OpSnapshot, hermod.OpUpdate:
		query := fmt.Sprintf("INSERT OR REPLACE INTO %s (id, data) VALUES (?, ?)", table)
		_, err := s.db.ExecContext(ctx, query, msg.ID(), msg.Payload())
		return err
	case hermod.OpDelete:
		query := fmt.Sprintf("DELETE FROM %s WHERE id = ?", table)
		_, err := s.db.ExecContext(ctx, query, msg.ID())
		return err
	default:
		return fmt.Errorf("unsupported operation: %s", op)
	}
}

func (s *SQLiteSink) init(ctx context.Context) error {
	db, err := sql.Open("sqlite", s.dbPath)
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
