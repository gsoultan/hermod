package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/user/hermod"
)

// PostgresSink implements the hermod.Sink interface for PostgreSQL.
type PostgresSink struct {
	connString string
	conn       *pgx.Conn
}

func NewPostgresSink(connString string) *PostgresSink {
	return &PostgresSink{
		connString: connString,
	}
}

func (s *PostgresSink) Write(ctx context.Context, msg hermod.Message) error {
	if s.conn == nil {
		if err := s.init(ctx); err != nil {
			return err
		}
	}

	// Basic implementation: insert into the table specified in the message.
	// This assumes the table exists and the payload/after can be mapped to it.
	// For a more robust implementation, we might need a mapping strategy.

	query := fmt.Sprintf("INSERT INTO %s.%s (id, data) VALUES ($1, $2) ON CONFLICT (id) DO UPDATE SET data = $2", msg.Schema(), msg.Table())
	_, err := s.conn.Exec(ctx, query, msg.ID(), msg.After())
	if err != nil {
		return fmt.Errorf("failed to write to postgres: %w", err)
	}

	return nil
}

func (s *PostgresSink) init(ctx context.Context) error {
	conn, err := pgx.Connect(ctx, s.connString)
	if err != nil {
		return fmt.Errorf("failed to connect to postgres: %w", err)
	}
	s.conn = conn
	return nil
}

func (s *PostgresSink) Ping(ctx context.Context) error {
	if s.conn == nil {
		if err := s.init(ctx); err != nil {
			return err
		}
	}
	return s.conn.Ping(ctx)
}

func (s *PostgresSink) Close() error {
	if s.conn != nil {
		return s.conn.Close(context.Background())
	}
	return nil
}
