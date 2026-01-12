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

	// Basic implementation: insert into the table specified in the message.
	query := fmt.Sprintf("REPLACE INTO %s.%s (id, data) VALUES (?, ?)", msg.Schema(), msg.Table())
	_, err := s.db.ExecContext(ctx, query, msg.ID(), msg.After())
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
