package sqlite

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/user/hermod"
	"github.com/user/hermod/pkg/message"
	_ "modernc.org/sqlite"
)

// SQLiteSource implements the hermod.Source interface for SQLite.
// Since SQLite doesn't have native CDC like Postgres, this implementation
// might rely on triggers or polling. For now, it's a placeholder consistent with other sources.
type SQLiteSource struct {
	dbPath string
	tables []string
	db     *sql.DB
}

func NewSQLiteSource(dbPath string, tables []string) *SQLiteSource {
	return &SQLiteSource{
		dbPath: dbPath,
		tables: tables,
	}
}

func (s *SQLiteSource) Read(ctx context.Context) (hermod.Message, error) {
	if s.db == nil {
		if err := s.init(ctx); err != nil {
			return nil, err
		}
	}

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
		// Placeholder logic for SQLite "CDC"
		msg := message.AcquireMessage()
		msg.SetID("sqlite-1")
		msg.SetOperation(hermod.OpCreate)
		msg.SetTable(s.tables[0]) // Assume at least one table for placeholder
		msg.SetMetadata("source", "sqlite")
		return msg, nil
	}
}

func (s *SQLiteSource) init(ctx context.Context) error {
	db, err := sql.Open("sqlite", s.dbPath)
	if err != nil {
		return fmt.Errorf("failed to open sqlite database: %w", err)
	}
	if err := db.PingContext(ctx); err != nil {
		return fmt.Errorf("failed to ping sqlite database: %w", err)
	}
	s.db = db
	return nil
}

func (s *SQLiteSource) Ack(ctx context.Context, msg hermod.Message) error {
	// Acknowledgement logic for SQLite if needed (e.g. updating a watermark table)
	return nil
}

func (s *SQLiteSource) Ping(ctx context.Context) error {
	if s.db == nil {
		if err := s.init(ctx); err != nil {
			return err
		}
	}
	return s.db.PingContext(ctx)
}

func (s *SQLiteSource) Close() error {
	if s.db != nil {
		return s.db.Close()
	}
	return nil
}

func (s *SQLiteSource) DiscoverDatabases(ctx context.Context) ([]string, error) {
	return []string{"main"}, nil
}

func (s *SQLiteSource) DiscoverTables(ctx context.Context) ([]string, error) {
	if s.db == nil {
		if err := s.init(ctx); err != nil {
			return nil, err
		}
	}

	rows, err := s.db.QueryContext(ctx, "SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%'")
	if err != nil {
		return nil, fmt.Errorf("failed to query tables: %w", err)
	}
	defer rows.Close()

	var tables []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		tables = append(tables, name)
	}
	return tables, nil
}
