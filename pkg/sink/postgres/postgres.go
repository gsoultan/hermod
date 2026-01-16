package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/user/hermod"
)

// PostgresSink implements the hermod.Sink interface for PostgreSQL.
type PostgresSink struct {
	connString string
	pool       *pgxpool.Pool
}

func NewPostgresSink(connString string) *PostgresSink {
	return &PostgresSink{
		connString: connString,
	}
}

func (s *PostgresSink) Write(ctx context.Context, msg hermod.Message) error {
	if s.pool == nil {
		if err := s.init(ctx); err != nil {
			return err
		}
	}

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
		query := fmt.Sprintf("INSERT INTO %s (id, data) VALUES ($1, $2) ON CONFLICT (id) DO UPDATE SET data = $2", table)
		_, err := s.pool.Exec(ctx, query, msg.ID(), msg.Payload())
		return err
	case hermod.OpDelete:
		query := fmt.Sprintf("DELETE FROM %s WHERE id = $1", table)
		_, err := s.pool.Exec(ctx, query, msg.ID())
		return err
	default:
		return fmt.Errorf("unsupported operation: %s", op)
	}
}

func (s *PostgresSink) init(ctx context.Context) error {
	pool, err := pgxpool.New(ctx, s.connString)
	if err != nil {
		return fmt.Errorf("failed to create postgres pool: %w", err)
	}
	s.pool = pool
	return s.pool.Ping(ctx)
}

func (s *PostgresSink) Ping(ctx context.Context) error {
	if s.pool == nil {
		if err := s.init(ctx); err != nil {
			return err
		}
	}
	return s.pool.Ping(ctx)
}

func (s *PostgresSink) Close() error {
	if s.pool != nil {
		s.pool.Close()
	}
	return nil
}
