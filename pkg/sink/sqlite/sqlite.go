package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/user/hermod"
	"github.com/user/hermod/pkg/message"
	"github.com/user/hermod/pkg/sqlutil"
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
	if msg == nil {
		return nil
	}
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
		query := fmt.Sprintf(commonQueries[QueryUpsert], table)
		_, err := s.db.ExecContext(ctx, query, msg.ID(), msg.Payload())
		return err
	case hermod.OpDelete:
		query := fmt.Sprintf(commonQueries[QueryDelete], table)
		_, err := s.db.ExecContext(ctx, query, msg.ID())
		return err
	default:
		return fmt.Errorf("unsupported operation: %s", op)
	}
}

func (s *SQLiteSink) init(ctx context.Context) error {
	dsn := s.dbPath
	if !strings.Contains(dsn, "?") {
		dsn += "?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=synchronous(NORMAL)&_pragma=foreign_keys(ON)"
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return fmt.Errorf("failed to open sqlite db: %w", err)
	}
	db.SetMaxOpenConns(1)
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

func (s *SQLiteSink) DiscoverDatabases(ctx context.Context) ([]string, error) {
	return []string{"main"}, nil
}

func (s *SQLiteSink) DiscoverTables(ctx context.Context) ([]string, error) {
	if s.db == nil {
		if err := s.init(ctx); err != nil {
			return nil, err
		}
	}

	rows, err := s.db.QueryContext(ctx, commonQueries[QueryListTables])
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

func (s *SQLiteSink) Sample(ctx context.Context, table string) (hermod.Message, error) {
	msgs, err := s.Browse(ctx, table, 1)
	if err != nil {
		return nil, err
	}
	if len(msgs) == 0 {
		return nil, fmt.Errorf("no data found in table %s", table)
	}
	return msgs[0], nil
}

func (s *SQLiteSink) Browse(ctx context.Context, table string, limit int) ([]hermod.Message, error) {
	if s.db == nil {
		if err := s.init(ctx); err != nil {
			return nil, err
		}
	}

	quoted, err := sqlutil.QuoteIdent("sqlite", table)
	if err != nil {
		return nil, fmt.Errorf("invalid table name: %w", err)
	}
	query := fmt.Sprintf(commonQueries[QueryBrowse], quoted, limit)
	rows, err := s.db.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var msgs []hermod.Message
	for rows.Next() {
		cols, err := rows.Columns()
		if err != nil {
			return nil, fmt.Errorf("failed to get columns: %w", err)
		}

		values := make([]interface{}, len(cols))
		valuePtrs := make([]interface{}, len(cols))
		for i := range values {
			valuePtrs[i] = &values[i]
		}

		if err := rows.Scan(valuePtrs...); err != nil {
			return nil, err
		}

		record := make(map[string]interface{})
		for i, col := range cols {
			val := values[i]
			if b, ok := val.([]byte); ok {
				record[col] = string(b)
			} else {
				record[col] = val
			}
		}

		afterJSON, _ := json.Marshal(message.SanitizeMap(record))

		msg := message.AcquireMessage()
		msg.SetID(fmt.Sprintf("sample-%s-%d-%d", table, time.Now().Unix(), len(msgs)))
		msg.SetOperation(hermod.OpSnapshot)
		msg.SetTable(table)
		msg.SetAfter(afterJSON)
		msg.SetMetadata("source", "sqlite_sink")
		msg.SetMetadata("sample", "true")
		msgs = append(msgs, msg)
	}

	return msgs, nil
}
