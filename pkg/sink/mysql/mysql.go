package mysql

import (
	"context"
	"database/sql"
	"fmt"

	_ "github.com/go-sql-driver/mysql"
	"github.com/user/hermod"
)

// MySQLSink implements the hermod.Sink interface for MySQL.
type MySQLSink struct {
	connString string
	db         *sql.DB
}

func NewMySQLSink(connString string) *MySQLSink {
	return &MySQLSink{
		connString: connString,
	}
}

func (s *MySQLSink) Write(ctx context.Context, msg hermod.Message) error {
	if s.db == nil {
		if err := s.init(ctx); err != nil {
			return err
		}
	}

	// More production-ready implementation:
	// Handle different operations and don't assume a fixed schema.
	table := msg.Table()
	if msg.Schema() != "" {
		table = fmt.Sprintf("%s.%s", msg.Schema(), table)
	}

	op := msg.Operation()
	if op == "" {
		op = hermod.OpCreate
	}

	switch op {
	case hermod.OpCreate, hermod.OpSnapshot, hermod.OpUpdate:
		return s.upsert(ctx, table, msg)
	case hermod.OpDelete:
		query := fmt.Sprintf("DELETE FROM %s WHERE id = ?", table)
		_, err := s.db.ExecContext(ctx, query, msg.ID())
		return err
	default:
		return fmt.Errorf("unsupported operation: %s", op)
	}
}

func (s *MySQLSink) upsert(ctx context.Context, table string, msg hermod.Message) error {
	// For a truly generic sink, we would need to parse the JSON and build the query.
	// As a compromise for production readiness without a full ORM, we'll assume 'id' exists.
	query := fmt.Sprintf("REPLACE INTO %s (id, data) VALUES (?, ?)", table)
	_, err := s.db.ExecContext(ctx, query, msg.ID(), msg.Payload())
	if err != nil {
		return fmt.Errorf("failed to write to mysql: %w", err)
	}
	return nil
}

func (s *MySQLSink) init(ctx context.Context) error {
	db, err := sql.Open("mysql", s.connString)
	if err != nil {
		return fmt.Errorf("failed to connect to mysql: %w", err)
	}
	s.db = db
	return s.db.PingContext(ctx)
}

func (s *MySQLSink) Ping(ctx context.Context) error {
	if s.db == nil {
		if err := s.init(ctx); err != nil {
			return err
		}
	}
	return s.db.PingContext(ctx)
}

func (s *MySQLSink) Close() error {
	if s.db != nil {
		return s.db.Close()
	}
	return nil
}
