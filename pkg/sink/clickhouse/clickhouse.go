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
	return s.WriteBatch(ctx, []hermod.Message{msg})
}

func (s *ClickHouseSink) WriteBatch(ctx context.Context, msgs []hermod.Message) error {
	// Filter nil messages
	filtered := make([]hermod.Message, 0, len(msgs))
	for _, m := range msgs {
		if m != nil {
			filtered = append(filtered, m)
		}
	}
	msgs = filtered

	if len(msgs) == 0 {
		return nil
	}
	if s.conn == nil {
		if err := s.init(ctx); err != nil {
			return err
		}
	}

	batch, err := s.conn.PrepareBatch(ctx, fmt.Sprintf("INSERT INTO %s.%s", s.database, msgs[0].Table()))
	if err != nil {
		return err
	}

	for _, msg := range msgs {
		if err := batch.Append(msg.ID(), string(msg.Payload())); err != nil {
			return err
		}
	}

	return batch.Send()
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

func (s *ClickHouseSink) DiscoverDatabases(ctx context.Context) ([]string, error) {
	if s.conn == nil {
		if err := s.init(ctx); err != nil {
			return nil, err
		}
	}

	rows, err := s.conn.Query(ctx, "SHOW DATABASES")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var databases []string
	for rows.Next() {
		var db string
		if err := rows.Scan(&db); err != nil {
			return nil, err
		}
		databases = append(databases, db)
	}
	return databases, nil
}

func (s *ClickHouseSink) DiscoverTables(ctx context.Context) ([]string, error) {
	if s.conn == nil {
		if err := s.init(ctx); err != nil {
			return nil, err
		}
	}

	rows, err := s.conn.Query(ctx, "SHOW TABLES")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var tables []string
	for rows.Next() {
		var table string
		if err := rows.Scan(&table); err != nil {
			return nil, err
		}
		tables = append(tables, table)
	}
	return tables, nil
}
