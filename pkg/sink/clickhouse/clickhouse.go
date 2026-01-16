package clickhouse

import (
	"context"
	"fmt"

	"github.com/ClickHouse/clickhouse-go/v2"
	"github.com/user/hermod"
)

type ClickHouseSink struct {
	addr     string
	database string
	conn     clickhouse.Conn
}

func NewClickHouseSink(addr, database string) *ClickHouseSink {
	return &ClickHouseSink{
		addr:     addr,
		database: database,
	}
}

func (s *ClickHouseSink) Write(ctx context.Context, msg hermod.Message) error {
	if s.conn == nil {
		if err := s.init(ctx); err != nil {
			return err
		}
	}

	query := fmt.Sprintf("INSERT INTO %s.%s (id, data) VALUES (?, ?)", s.database, msg.Table())
	err := s.conn.Exec(ctx, query, msg.ID(), string(msg.Payload()))
	if err != nil {
		return fmt.Errorf("failed to write to clickhouse: %w", err)
	}

	return nil
}

func (s *ClickHouseSink) init(ctx context.Context) error {
	conn, err := clickhouse.Open(&clickhouse.Options{
		Addr: []string{s.addr},
		Auth: clickhouse.Auth{
			Database: s.database,
		},
	})
	if err != nil {
		return fmt.Errorf("failed to connect to clickhouse: %w", err)
	}
	s.conn = conn
	return nil
}

func (s *ClickHouseSink) Ping(ctx context.Context) error {
	if s.conn == nil {
		if err := s.init(ctx); err != nil {
			return err
		}
	}
	return s.conn.Ping(ctx)
}

func (s *ClickHouseSink) Close() error {
	if s.conn != nil {
		return s.conn.Close()
	}
	return nil
}
